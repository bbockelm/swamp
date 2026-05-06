package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

	analysis, err := wh.h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		log.Error().Err(err).Str("analysis_id", analysisID).Msg("Anthropic proxy: failed to load analysis")
		http.Error(w, "Proxy not configured", http.StatusServiceUnavailable)
		return
	}

	// Try new provider resolution from analysis agent_config first.
	resolvedProvider, resolveErr := agent.ResolveAnalysisProvider(r.Context(), wh.h.queries, wh.h.encryptor, wh.h.cfg, analysis)
	if resolveErr != nil {
		log.Warn().Err(resolveErr).Str("analysis_id", analysisID).Msg("Anthropic proxy: failed to resolve analysis provider")
	}

	apiKey := ""
	if resolvedProvider != nil {
		apiKey = resolvedProvider.APIKey
	}

	if apiKey == "" {
		// Legacy fallback: project-level provider keys, then global key.
		usesGlobalKey := false
		if wh.h.encryptor != nil {
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

// ProxyLLM reverse-proxies LLM API requests from worker pods to the
// appropriate upstream provider. It supports anthropic, nrp, and custom
// providers by resolving the correct API key and endpoint from the project's
// provider key configuration.
//
// Route: /api/v1/internal/worker/llm/*
// The worker sets its base URL to point here. The proxy token authenticates
// the request and maps it to an analysis + project for key resolution.
func (wh *WorkerHandler) ProxyLLM(w http.ResponseWriter, r *http.Request) {
	// Extract proxy token from Authorization header (Bearer) or x-api-key.
	proxyToken := ""
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		proxyToken = strings.TrimPrefix(auth, "Bearer ")
	}
	if proxyToken == "" {
		proxyToken = r.Header.Get("x-api-key")
	}
	if proxyToken == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	analysisID, err := wh.tokenStore.ValidateProxyToken(proxyToken)
	if err != nil {
		log.Warn().Err(err).Msg("LLM proxy: invalid proxy token")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	analysis, err := wh.h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		log.Error().Err(err).Str("analysis_id", analysisID).Msg("LLM proxy: failed to load analysis")
		http.Error(w, "Proxy not configured", http.StatusServiceUnavailable)
		return
	}

	// Resolve which provider key to use for this project.
	apiKey, endpointURL, err := wh.resolveLLMCredentials(r, analysis)
	if err != nil {
		log.Error().Err(err).Str("analysis_id", analysisID).Msg("LLM proxy: failed to resolve credentials")
		http.Error(w, "No API key configured for this project", http.StatusServiceUnavailable)
		return
	}

	// Strip the proxy prefix from the path.
	const proxyPrefix = "/api/v1/internal/worker/llm"
	if strings.HasPrefix(r.URL.Path, proxyPrefix) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, proxyPrefix)
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
	}

	// Parse the upstream endpoint and proxy the request.
	target, err := url.Parse(endpointURL)
	if err != nil {
		log.Error().Err(err).Str("endpoint", endpointURL).Msg("LLM proxy: invalid endpoint URL")
		http.Error(w, "Invalid upstream endpoint", http.StatusInternalServerError)
		return
	}

	// SSRF protection: require https and reject private/loopback addresses.
	if target.Scheme != "https" {
		log.Error().Str("endpoint", endpointURL).Msg("LLM proxy: endpoint must use https")
		http.Error(w, "Upstream endpoint must use https", http.StatusBadRequest)
		return
	}
	if err := validateNotPrivateHost(target.Hostname()); err != nil {
		log.Error().Err(err).Str("endpoint", endpointURL).Msg("LLM proxy: SSRF blocked")
		http.Error(w, "Upstream endpoint not allowed", http.StatusBadRequest)
		return
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			// Prepend the base path from the target (e.g. /v1).
			if target.Path != "" && target.Path != "/" {
				req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
			}
			// Set the real API key.
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Del("x-api-key")
		},
		FlushInterval: -1,
	}

	proxy.ServeHTTP(w, r)
}

// resolveLLMCredentials resolves the API key and endpoint URL for an analysis.
// First tries the new provider resolution from the analysis's agent_config,
// then falls back to legacy project-level and global key lookup.
func (wh *WorkerHandler) resolveLLMCredentials(r *http.Request, analysis *models.Analysis) (apiKey, endpointURL string, err error) {
	ctx := r.Context()

	// Try new provider resolution from analysis agent_config first.
	resolvedProvider, resolveErr := agent.ResolveAnalysisProvider(ctx, wh.h.queries, wh.h.encryptor, wh.h.cfg, analysis)
	if resolveErr != nil {
		log.Warn().Err(resolveErr).Str("analysis_id", analysis.ID).Msg("LLM proxy: failed to resolve analysis provider")
	}
	if resolvedProvider != nil && resolvedProvider.APIKey != "" && resolvedProvider.BaseURL != "" {
		return resolvedProvider.APIKey, resolvedProvider.BaseURL, nil
	}

	// Legacy fallback: project-level provider keys, then global config.
	if wh.h.encryptor == nil {
		return "", "", fmt.Errorf("encryption not configured")
	}

	// Try provider keys in order: nrp, custom, external_llm
	for _, provider := range []string{"nrp", "custom", "external_llm"} {
		k, err := wh.h.queries.GetActiveProviderKey(ctx, analysis.ProjectID, provider)
		if err != nil {
			continue
		}
		dek, err := wh.h.encryptor.UnwrapDEK(k.EncryptedDEK, k.DEKNonce)
		if err != nil {
			continue
		}
		pt, err := crypto.Decrypt(dek, k.EncryptedKey)
		if err != nil {
			continue
		}
		key := strings.TrimSpace(string(pt))
		if key == "" {
			continue
		}
		ep := k.EndpointURL
		if ep == "" {
			// Use global external LLM endpoint as default.
			ep = wh.h.cfg.ExternalLLMEndpoint
		}
		if ep == "" {
			continue
		}
		return key, ep, nil
	}

	// Fall back to global external LLM config if project allows global key.
	project, err := wh.h.queries.GetProject(ctx, analysis.ProjectID)
	if err != nil {
		return "", "", fmt.Errorf("lookup project: %w", err)
	}
	if !project.UsesGlobalKey {
		return "", "", fmt.Errorf("project does not have an API key configured")
	}

	// Try global external LLM key.
	globalKey := strings.TrimSpace(wh.h.cfg.ExternalLLMAPIKey)
	if globalKey == "" && wh.h.cfg.ExternalLLMAPIKeyFile != "" {
		if keyData, err := os.ReadFile(wh.h.cfg.ExternalLLMAPIKeyFile); err == nil {
			globalKey = strings.TrimSpace(string(keyData))
		}
	}
	ep := wh.h.cfg.ExternalLLMEndpoint
	if globalKey != "" && ep != "" {
		return globalKey, ep, nil
	}

	return "", "", fmt.Errorf("no LLM API key configured for this project")
}

// singleJoiningSlash joins two URL paths with exactly one slash between them.
func singleJoiningSlash(a, b string) string {
	aSlash := strings.HasSuffix(a, "/")
	bSlash := strings.HasPrefix(b, "/")
	switch {
	case aSlash && bSlash:
		return a + b[1:]
	case !aSlash && !bSlash:
		return a + "/" + b
	}
	return a + b
}

// validateNotPrivateHost resolves hostname to IP addresses and rejects
// loopback, private, and link-local addresses to prevent SSRF.
func validateNotPrivateHost(hostname string) error {
	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("DNS lookup failed for %q: %w", hostname, err)
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return fmt.Errorf("invalid IP %q for host %q", ipStr, hostname)
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("host %q resolves to private/loopback address %s", hostname, ipStr)
		}
	}
	return nil
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

	// Idempotency pre-check: if this (analysis_id, filename) was already
	// uploaded, return the existing row so the caller can safely retry.
	if existing, err := wh.h.queries.GetAnalysisResultByFilename(r.Context(), analysisID, filename); err == nil {
		respondJSON(w, http.StatusOK, existing)
		return
	}

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
	if resultType == "sarif" {
		base := strings.TrimSuffix(filename, ".sarif")
		for i := range session.Packages {
			if strings.EqualFold(session.Packages[i].Name, base) {
				pkgID := session.Packages[i].ID
				result.PackageID = &pkgID
				break
			}
		}
		// Single-package analyses always produce "results.sarif" which won't
		// match the package name. Fall back to the only package available.
		if result.PackageID == nil && len(session.Packages) == 1 {
			pkgID := session.Packages[0].ID
			result.PackageID = &pkgID
		}
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

		wh.trackSARIFUploadAttempt(r.Context(), analysis, session, result, plaintext)
	}

	// Extract token usage from agent stdout logs.
	if resultType == "agent_log" && filename == "agent_stdout.log" {
		lines := strings.Split(string(plaintext), "\n")
		usages := agent.ExtractTokenUsage(lines)
		if normalized, changed := agent.ApplyAnalysisTokenUsageIdentity(usages, analysis); changed {
			usages = normalized
		}
		if len(usages) > 0 {
			if err := wh.h.queries.ReplaceAnalysisTokenUsage(r.Context(), analysisID, usages); err != nil {
				log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to save token usage")
			} else {
				totalIn, totalOut := int64(0), int64(0)
				for _, u := range usages {
					totalIn += u.InputTokens
					totalOut += u.OutputTokens
				}
				log.Info().
					Int("models", len(usages)).
					Int64("input_tokens", totalIn).
					Int64("output_tokens", totalOut).
					Str("analysis_id", analysisID).
					Msg("Saved token usage from agent stdout")
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

func (wh *WorkerHandler) trackSARIFUploadAttempt(ctx context.Context, analysis *models.Analysis, session *agent.WorkerSession, result *models.AnalysisResult, sarifData []byte) {
	if result == nil || result.ID == "" {
		return
	}
	if wh.h == nil || wh.h.ghClient == nil || !wh.h.ghClient.Configured() {
		log.Info().Str("analysis_id", analysis.ID).Str("result_id", result.ID).Msg("Skipping GitHub SARIF upload: GitHub integration not configured")
		return
	}

	attempted := false
	uploadURL := ""
	uploadErrMsg := ""

	var pkg *models.SoftwarePackage
	if result.PackageID != nil {
		for i := range session.Packages {
			if session.Packages[i].ID == *result.PackageID {
				pkg = &session.Packages[i]
				break
			}
		}
	}

	if pkg != nil && pkg.SARIFUploadEnabled && pkg.GitHubOwner != "" && pkg.GitHubRepo != "" {
		attempted = true
		log.Info().
			Str("analysis_id", analysis.ID).
			Str("result_id", result.ID).
			Str("package_id", pkg.ID).
			Str("package", pkg.Name).
			Msg("Attempting GitHub SARIF upload for package result")
		url, err := wh.h.ghClient.UploadSARIFForPackage(ctx, pkg, sarifData, analysis.GitCommit)
		if err != nil {
			uploadErrMsg = err.Error()
			log.Warn().Err(err).Str("analysis_id", analysis.ID).Str("result_id", result.ID).Str("package_id", pkg.ID).Msg("GitHub SARIF upload failed for package result")
		} else {
			uploadURL = url
		}
	}

	if !attempted {
		if ghCfg, err := wh.h.queries.GetProjectGitHubConfig(ctx, analysis.ProjectID); err == nil && ghCfg.SARIFUploadEnabled && ghCfg.InstallationID != 0 {
			attempted = true
			log.Info().
				Str("analysis_id", analysis.ID).
				Str("result_id", result.ID).
				Msg("Attempting GitHub SARIF upload via project-level config")
			url, upErr := wh.h.ghClient.UploadSARIFForProject(ctx, analysis.ProjectID, sarifData, analysis.GitCommit)
			if upErr != nil {
				uploadErrMsg = upErr.Error()
				log.Warn().Err(upErr).Str("analysis_id", analysis.ID).Str("result_id", result.ID).Msg("GitHub SARIF upload failed for project result")
			} else {
				uploadURL = url
			}
		}
	}

	if !attempted {
		log.Info().Str("analysis_id", analysis.ID).Str("result_id", result.ID).Msg("Skipping GitHub SARIF upload: no eligible package/project SARIF config")
		return
	}

	if uploadURL == "" && uploadErrMsg == "" {
		uploadErrMsg = "Upload attempted but no GitHub alerts URL was returned"
	}
	if err := wh.h.queries.SetResultSARIFUploadStatus(ctx, result.ID, true, uploadURL, uploadErrMsg); err != nil {
		log.Warn().Err(err).Str("result_id", result.ID).Msg("Failed to persist SARIF upload tracking status")
	}
	if uploadURL != "" {
		if err := wh.h.queries.SetAnalysisSARIFUploadURL(ctx, analysis.ID, uploadURL); err != nil {
			log.Warn().Err(err).Str("analysis_id", analysis.ID).Msg("Failed to record analysis SARIF upload URL")
		}
		wh.hub.Broadcast(analysis.ID, []byte("[system] SARIF results uploaded to GitHub"))
		return
	}
	wh.hub.Broadcast(analysis.ID, []byte("[error] GitHub SARIF upload failed: "+uploadErrMsg))
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
