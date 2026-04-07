package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/agent"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/internal/ws"
)

// WorkerHandler handles API requests from worker pods.
// It is separate from the main Handler to keep the worker auth path clean.
type WorkerHandler struct {
	tokenStore     *agent.WorkerTokenStore
	hub            *ws.Hub
	h              *Handler // parent handler for DB/storage access
	anthropicProxy *httputil.ReverseProxy
}

// NewWorkerHandler creates a new handler for worker pod API endpoints.
func NewWorkerHandler(tokenStore *agent.WorkerTokenStore, hub *ws.Hub, h *Handler) *WorkerHandler {
	target, _ := url.Parse("https://api.anthropic.com")
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			// x-api-key is replaced per-request in ProxyAnthropic before calling ServeHTTP.
		},
		// FlushInterval -1 enables immediate flushing for streaming SSE responses.
		FlushInterval: -1,
	}
	return &WorkerHandler{
		tokenStore:     tokenStore,
		hub:            hub,
		h:              h,
		anthropicProxy: proxy,
	}
}

// ExchangeToken handles POST /api/v1/internal/worker/exchange.
// The worker pod sends its one-time token and receives a session credential.
func (wh *WorkerHandler) ExchangeToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Token == "" {
		respondError(w, http.StatusBadRequest, "Token is required")
		return
	}

	resp, err := wh.tokenStore.ExchangeToken(req.Token)
	if err != nil {
		log.Warn().Err(err).Msg("Worker token exchange failed")
		respondError(w, http.StatusUnauthorized, "Invalid or expired token")
		return
	}

	log.Info().
		Str("analysis_id", resp.AnalysisID).
		Msg("Worker token exchanged successfully")

	respondJSON(w, http.StatusOK, resp)
}

// ExchangeSidecarToken handles POST /api/v1/internal/worker/exchange-sidecar.
// The LLM proxy sidecar container sends its one-time token and receives the
// real external LLM API key and endpoint URL. The token is consumed on use.
// Credentials are never stored in the pod spec — only the one-time token is.
func (wh *WorkerHandler) ExchangeSidecarToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Token == "" {
		respondError(w, http.StatusBadRequest, "Token is required")
		return
	}

	apiKey, endpointURL, err := wh.tokenStore.ExchangeSidecarToken(req.Token)
	if err != nil {
		log.Warn().Err(err).Msg("Sidecar token exchange failed")
		respondError(w, http.StatusUnauthorized, "Invalid or expired sidecar token")
		return
	}

	log.Info().Msg("Sidecar token exchanged successfully")
	respondJSON(w, http.StatusOK, map[string]string{
		"api_key":      apiKey,
		"endpoint_url": endpointURL,
	})
}

// ProxyAnthropic reverse-proxies Anthropic API requests from worker pods.
// The worker sends a dedicated proxy token as the x-api-key header (set via
// ANTHROPIC_API_KEY env var). This token is separate from the session token
// and can only be used for proxy authentication — even if Claude reads it,
// it cannot call other worker endpoints (stream, status, results).
//
// Route: /api/v1/internal/worker/anthropic/*
// The worker sets ANTHROPIC_BASE_URL to point here, so the Anthropic SDK
// appends /v1/messages (etc.) which becomes the wildcard suffix.
func (wh *WorkerHandler) ProxyAnthropic(w http.ResponseWriter, r *http.Request) {
	// The Anthropic SDK sends the API key via x-api-key header.
	// In our case, the worker sets ANTHROPIC_API_KEY = proxy token.
	proxyToken := r.Header.Get("x-api-key")
	if proxyToken == "" {
		// Fall back to Authorization header.
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			proxyToken = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if proxyToken == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	analysisID, err := wh.tokenStore.ValidateProxyToken(proxyToken)
	if err != nil {
		log.Warn().Err(err).Msg("Anthropic proxy: invalid proxy token")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	apiKey := ""
	usesGlobalKey := false
	if wh.h.encryptor != nil {
		analysis, err := wh.h.queries.GetAnalysis(r.Context(), analysisID)
		if err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Anthropic proxy: failed to load analysis")
			http.Error(w, "Proxy not configured", http.StatusServiceUnavailable)
			return
		}

		// Try to get a project-specific provider key.
		if k, err := wh.h.queries.GetActiveProviderKey(r.Context(), analysis.ProjectID, "anthropic"); err == nil {
			dek, err := wh.h.encryptor.UnwrapDEK(k.EncryptedDEK, k.DEKNonce)
			if err == nil {
				if pt, err := crypto.Decrypt(dek, k.EncryptedKey); err == nil {
					apiKey = strings.TrimSpace(string(pt))
				}
			}
		}

		// If no project key, check if the project is allowed to use the global key.
		if apiKey == "" {
			project, err := wh.h.queries.GetProject(r.Context(), analysis.ProjectID)
			if err != nil {
				log.Error().Err(err).Str("project_id", analysis.ProjectID).Msg("Anthropic proxy: failed to load project")
				http.Error(w, "Proxy not configured", http.StatusServiceUnavailable)
				return
			}
			usesGlobalKey = project.UsesGlobalKey
		}
	} else {
		// No encryptor means no project keys; allow global key fallback.
		usesGlobalKey = true
	}

	// Only fall back to global key if the project allows it.
	if apiKey == "" && usesGlobalKey {
		if wh.h.cfg.AgentAPIKeyFile != "" {
			if keyData, err := os.ReadFile(wh.h.cfg.AgentAPIKeyFile); err == nil {
				apiKey = strings.TrimSpace(string(keyData))
			}
		}
		if apiKey == "" {
			apiKey = strings.TrimSpace(wh.h.cfg.AgentAPIKey)
		}
	}

	if apiKey == "" {
		log.Error().Str("analysis_id", analysisID).Msg("Anthropic proxy: no API key available for project")
		http.Error(w, "No API key configured for this project", http.StatusServiceUnavailable)
		return
	}

	// Strip the proxy prefix so /api/v1/internal/worker/anthropic/v1/messages
	// becomes /v1/messages when forwarded to api.anthropic.com.
	const proxyPrefix = "/api/v1/internal/worker/anthropic"
	if strings.HasPrefix(r.URL.Path, proxyPrefix) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, proxyPrefix)
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
	}

	// Replace the worker session token with the real Anthropic API key.
	r.Header.Set("x-api-key", apiKey)
	r.Header.Del("Authorization")

	wh.anthropicProxy.ServeHTTP(w, r)
}

// StreamOutput handles POST /api/v1/internal/worker/stream.
// The worker sends batches of output lines to be broadcast via WebSocket.
func (wh *WorkerHandler) StreamOutput(w http.ResponseWriter, r *http.Request) {
	session, err := wh.authenticateWorker(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Invalid session")
		return
	}

	var req struct {
		AnalysisID string   `json:"analysis_id"`
		Lines      []string `json:"lines"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.AnalysisID != session.AnalysisID {
		respondError(w, http.StatusForbidden, "Session does not match analysis")
		return
	}

	for _, line := range req.Lines {
		wh.hub.Broadcast(session.AnalysisID, []byte(line))
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// UpdateStatus handles POST /api/v1/internal/worker/status.
// The worker reports status changes (running, completed, failed).
func (wh *WorkerHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	session, err := wh.authenticateWorker(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Invalid session")
		return
	}

	var req struct {
		AnalysisID string `json:"analysis_id"`
		Status     string `json:"status"`
		Detail     string `json:"detail"`
		GitCommit  string `json:"git_commit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.AnalysisID != session.AnalysisID {
		respondError(w, http.StatusForbidden, "Session does not match analysis")
		return
	}

	switch req.Status {
	case "running", "completed", "failed", "timed_out":
	default:
		respondError(w, http.StatusBadRequest, "Invalid status: "+req.Status)
		return
	}

	log.Info().
		Str("analysis_id", req.AnalysisID).
		Str("status", req.Status).
		Str("detail", req.Detail).
		Msg("Worker status update")

	switch req.Status {
	case "completed":
		if req.GitCommit != "" {
			if err := wh.h.queries.SetAnalysisGitCommit(r.Context(), req.AnalysisID, req.GitCommit); err != nil {
				log.Error().Err(err).Msg("Failed to store git commit from worker")
			}
		}
		if err := wh.h.queries.SetAnalysisCompleted(r.Context(), req.AnalysisID, "completed", ""); err != nil {
			log.Error().Err(err).Msg("Failed to mark analysis completed")
		}
		wh.hub.CloseRoom(req.AnalysisID)
	case "failed":
		if err := wh.h.queries.SetAnalysisCompleted(r.Context(), req.AnalysisID, "failed", req.Detail); err != nil {
			log.Error().Err(err).Msg("Failed to mark analysis failed")
		}
		wh.hub.CloseRoom(req.AnalysisID)
	case "timed_out":
		if err := wh.h.queries.SetAnalysisCompleted(r.Context(), req.AnalysisID, "timed_out", req.Detail); err != nil {
			log.Error().Err(err).Msg("Failed to mark analysis timed out")
		}
		wh.hub.CloseRoom(req.AnalysisID)
	default:
		if err := wh.h.queries.UpdateAnalysisStatus(r.Context(), req.AnalysisID, req.Status, req.Detail, ""); err != nil {
			log.Error().Err(err).Msg("Failed to update analysis status")
		}
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// UploadResult handles POST /api/v1/internal/worker/results.
// The worker uploads output files (SARIF, reports, exploits) which are
// encrypted with the analysis DEK and stored in S3.
func (wh *WorkerHandler) UploadResult(w http.ResponseWriter, r *http.Request) {
	session, err := wh.authenticateWorker(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Invalid session")
		return
	}

	// Limit upload size to 100MB.
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid multipart form: "+err.Error())
		return
	}

	analysisID := r.FormValue("analysis_id")
	filename := r.FormValue("filename")

	if analysisID != session.AnalysisID {
		respondError(w, http.StatusForbidden, "Session does not match analysis")
		return
	}
	if filename == "" {
		respondError(w, http.StatusBadRequest, "Filename is required")
		return
	}

	filename = sanitizeFilename(filename)

	file, _, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "File is required")
		return
	}
	defer func() { _ = file.Close() }()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, file); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}

	plaintext := buf.Bytes()

	// Get the analysis DEK for encryption.
	analysis, err := wh.h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to load analysis")
		return
	}
	if len(analysis.EncryptedDEK) == 0 || wh.h.encryptor == nil {
		respondError(w, http.StatusInternalServerError, "Encryption not available")
		return
	}

	dek, err := wh.h.encryptor.UnwrapDEK(analysis.EncryptedDEK, analysis.DEKNonce)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to unwrap DEK")
		return
	}

	ciphertext, err := crypto.Encrypt(dek, plaintext)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to encrypt result")
		return
	}

	s3Key := fmt.Sprintf("analyses/%s/%s", analysisID, filename)
	if err := wh.h.store.Upload(r.Context(), s3Key, bytes.NewReader(ciphertext), int64(len(ciphertext)), "application/octet-stream"); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to upload to storage")
		return
	}

	// Determine result type from filename.
	resultType := classifyResultType(filename)

	result := &models.AnalysisResult{
		AnalysisID:  analysisID,
		Filename:    filename,
		S3Key:       s3Key,
		FileSize:    int64(len(plaintext)),
		ResultType:  resultType,
		ContentType: "application/octet-stream",
	}

	if err := wh.h.queries.CreateAnalysisResult(r.Context(), result); err != nil {
		log.Error().Err(err).Str("filename", filename).Msg("Failed to insert analysis result row")
		respondError(w, http.StatusInternalServerError, "Failed to record result")
		return
	}

	// Extract and store findings from SARIF files; also set metadata counts.
	if resultType == "sarif" {
		// Parse SARIF to get summary, finding count, and severity breakdown.
		summary, findingCount, severityCounts := agent.ParseSARIFBytes(plaintext)
		result.Summary = summary
		result.FindingCount = findingCount
		result.SeverityCounts = severityCounts
		if err := wh.h.queries.UpdateAnalysisResultMetadata(r.Context(), result.ID, summary, findingCount, severityCounts); err != nil {
			log.Error().Err(err).Str("result_id", result.ID).Msg("Failed to update SARIF metadata")
		}

		// Extract individual findings.
		findings := agent.ExtractFindingsFromBytes(plaintext, analysisID, analysis.ProjectID)
		if len(findings) > 0 {
			// Link all findings to this result.
			for i := range findings {
				findings[i].ResultID = result.ID
				findings[i].GitCommit = analysis.GitCommit
			}
			if err := wh.h.queries.CreateFindingsBatch(r.Context(), findings); err != nil {
				log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to save findings")
			} else {
				log.Info().Int("count", len(findings)).Str("analysis_id", analysisID).Msg("Saved individual findings from uploaded SARIF")
			}
		}
	}

	log.Info().
		Str("analysis_id", analysisID).
		Str("filename", filename).
		Str("s3_key", s3Key).
		Int("size", len(plaintext)).
		Msg("Worker uploaded result file")

	respondJSON(w, http.StatusCreated, result)
}

// authenticateWorker validates the Bearer token in the request.
func (wh *WorkerHandler) authenticateWorker(r *http.Request) (*agent.WorkerSession, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil, fmt.Errorf("missing Bearer token")
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	return wh.tokenStore.ValidateSession(token)
}

// sanitizeFilename prevents path traversal in uploaded filenames.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	if name == "" {
		name = "unknown"
	}
	return name
}

// classifyResultType determines the result type from a filename.
// This must match the classification logic in agent/parser.go.
func classifyResultType(filename string) string {
	switch {
	case strings.HasSuffix(filename, ".sarif"):
		return "sarif"
	case filename == "notes.md":
		return "analysis_notes"
	case filename == "prompt.md":
		return "analysis_prompt"
	case filename == "context.md":
		return "analysis_context"
	case strings.HasSuffix(filename, ".md"):
		return "markdown_report"
	case strings.HasSuffix(filename, ".log"):
		return "agent_log"
	case strings.HasSuffix(filename, ".tar.gz") || strings.HasSuffix(filename, ".tgz"):
		return "exploit_tarball"
	case strings.Contains(filename, "exploit"):
		return "exploit_tarball"
	default:
		return "other"
	}
}
