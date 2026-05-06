package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/agent"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/models"
)

// --- Analyses ---

// ListAllAnalyses returns analyses visible to the current user (jobs table).
func (h *Handler) ListAllAnalyses(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var analyses []models.Analysis
	var err error
	if UserHasRole(r.Context(), RoleAdmin) {
		analyses, err = h.queries.ListAllAnalysesAdmin(r.Context())
	} else {
		analyses, err = h.queries.ListAllAnalyses(r.Context(), user.ID)
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to list all analyses")
		respondError(w, http.StatusInternalServerError, "Failed to list analyses")
		return
	}
	if analyses == nil {
		analyses = []models.Analysis{}
	}
	respondJSON(w, http.StatusOK, analyses)
}

// ListAnalyses returns analyses for a project.
func (h *Handler) ListAnalyses(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")

	analyses, err := h.queries.ListProjectAnalyses(r.Context(), projectID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list analyses")
		respondError(w, http.StatusInternalServerError, "Failed to list analyses")
		return
	}
	respondJSON(w, http.StatusOK, analyses)
}

// CheckAnalysisLiveness reports whether the executor is actively tracking a
// given analysis. The frontend polls this to detect stale "running" states.
// If the executor says the analysis is not running but the DB still shows
// running, we auto-correct the DB to "failed".
func (h *Handler) CheckAnalysisLiveness(w http.ResponseWriter, r *http.Request) {
	analysisID := chi.URLParam(r, "analysisID")
	alive := h.executor != nil && h.executor.IsRunning(analysisID)

	if !alive {
		// Auto-fix only for analyses that were already running. Pending
		// analyses may be queued behind the concurrency limiter and should
		// not be marked failed just because no worker is active yet.
		a, err := h.queries.GetAnalysis(r.Context(), analysisID)
		if err == nil && a.Status == "running" {
			_ = h.queries.SetAnalysisCompleted(r.Context(), analysisID, "failed", "Worker process is no longer running")
		}
	}

	respondJSON(w, http.StatusOK, map[string]bool{"alive": alive})
}

// CreateAnalysis triggers a new analysis for a project.
func (h *Handler) CreateAnalysis(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	user := GetUserFromContext(r.Context())

	// Reject if the agent is not configured.
	if h.executor == nil || !h.executor.AgentReady() {
		respondError(w, http.StatusServiceUnavailable, "Analysis agent is not configured (missing API key or binary)")
		return
	}

	var req struct {
		PackageIDs     []string        `json:"package_ids"`
		AgentModel     string          `json:"agent_model"`
		AgentConfig    json.RawMessage `json:"agent_config,omitempty"`
		CustomPrompt   string          `json:"custom_prompt"`
		ProviderID     string          `json:"provider_id"`
		ProviderSource string          `json:"provider_source"` // "global" or "project"
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(req.PackageIDs) == 0 {
		respondError(w, http.StatusBadRequest, "At least one package_id is required")
		return
	}

	selectedPackages := make([]models.SoftwarePackage, 0, len(req.PackageIDs))

	// A provider must always be specified — "server default" is not allowed
	// because it bypasses the per-project provider ACL.
	if req.ProviderID == "" {
		respondError(w, http.StatusBadRequest, "A provider must be selected")
		return
	}

	// Verify all packages belong to the project
	for _, pkgID := range req.PackageIDs {
		pkg, err := h.queries.GetPackage(r.Context(), pkgID)
		if err != nil || pkg.ProjectID != projectID {
			respondError(w, http.StatusBadRequest, "Invalid package ID: "+pkgID)
			return
		}
		selectedPackages = append(selectedPackages, *pkg)
	}

	// Merge provider info into agent_config.
	agentConfig := map[string]interface{}{}
	if len(req.AgentConfig) > 0 {
		_ = json.Unmarshal(req.AgentConfig, &agentConfig)
	}
	if req.ProviderID != "" {
		agentConfig["llm_provider_id"] = req.ProviderID
		if req.ProviderSource == "" {
			req.ProviderSource = "global"
		}
		agentConfig["provider_source"] = req.ProviderSource

		// Look up human-readable label for display.
		if label := h.resolveProviderLabel(r.Context(), projectID, req.ProviderID, req.ProviderSource); label != "" {
			agentConfig["provider_label"] = label
		}
	}
	configBytes, _ := json.Marshal(agentConfig)

	analysis := &models.Analysis{
		ProjectID:    projectID,
		Status:       "pending",
		TriggeredBy:  user.ID,
		AgentModel:   req.AgentModel,
		AgentConfig:  json.RawMessage(configBytes),
		CustomPrompt: req.CustomPrompt,
	}

	// Generate a per-analysis DEK for encrypting output artifacts.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate analysis DEK")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}
	encDEK, nonce, err := h.encryptor.WrapDEK(dek)
	if err != nil {
		log.Error().Err(err).Msg("Failed to wrap analysis DEK")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}
	analysis.EncryptedDEK = encDEK
	analysis.DEKNonce = nonce

	if err := h.queries.CreateAnalysis(r.Context(), analysis); err != nil {
		log.Error().Err(err).Msg("Failed to create analysis")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}

	githubConfigured := 0
	packageMeta := make([]string, 0, len(selectedPackages))
	for _, p := range selectedPackages {
		if p.GitHubOwner != "" && p.GitHubRepo != "" {
			githubConfigured++
		}
		packageMeta = append(packageMeta, p.Name+"("+p.GitBranch+")")
	}
	log.Info().
		Str("analysis_id", analysis.ID).
		Str("project_id", projectID).
		Str("triggered_by", user.ID).
		Int("package_count", len(selectedPackages)).
		Int("github_clone_capable_packages", githubConfigured).
		Strs("packages", packageMeta).
		Str("provider_id", req.ProviderID).
		Str("provider_source", req.ProviderSource).
		Msg("Created analysis")

	// Link packages to analysis
	for _, pkgID := range req.PackageIDs {
		if err := h.queries.AddAnalysisPackage(r.Context(), analysis.ID, pkgID); err != nil {
			log.Error().Err(err).Str("package_id", pkgID).Msg("Failed to link package to analysis")
		}
	}

	// Submit to the agent executor for async processing.
	if h.executor != nil {
		packages, err := h.queries.ListAnalysisPackages(r.Context(), analysis.ID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to fetch packages for analysis submission")
		} else {
			h.executor.Submit(analysis, packages)
		}
	}

	respondJSON(w, http.StatusCreated, analysis)
}

// GetAnalysis returns details of a specific analysis.
func (h *Handler) GetAnalysis(w http.ResponseWriter, r *http.Request) {
	analysisID := chi.URLParam(r, "analysisID")

	analysis, err := h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Analysis not found")
		return
	}

	// Enrich agent_config with provider label if needed.
	if len(analysis.AgentConfig) > 0 {
		agentConfig := make(map[string]interface{})
		if err := json.Unmarshal(analysis.AgentConfig, &agentConfig); err == nil {
			providerID, _ := agentConfig["llm_provider_id"].(string)
			providerSource, _ := agentConfig["provider_source"].(string)
			if label := h.resolveProviderLabel(r.Context(), analysis.ProjectID, providerID, providerSource); label != "" {
				agentConfig["provider_label"] = label
				if configBytes, err := json.Marshal(agentConfig); err == nil {
					analysis.AgentConfig = json.RawMessage(configBytes)
				}
			}
		}
	}

	// Also fetch linked packages
	packages, _ := h.queries.ListAnalysisPackages(r.Context(), analysisID)

	// Fetch token usage and enrich with provider labels
	tokenUsage, _ := h.queries.GetAnalysisTokenUsage(r.Context(), analysisID)
	providerLabel := ""
	providerID := ""
	providerSource := ""
	if len(analysis.AgentConfig) > 0 {
		agentConfig := make(map[string]interface{})
		if err := json.Unmarshal(analysis.AgentConfig, &agentConfig); err == nil {
			providerLabel, _ = agentConfig["provider_label"].(string)
			providerID, _ = agentConfig["llm_provider_id"].(string)
			providerSource, _ = agentConfig["provider_source"].(string)
		}
	}

	// Enrich token usage with provider labels if they are UUIDs
	if len(tokenUsage) > 0 {
		seenProviders := make(map[string]string) // uuid -> label cache
		for i := range tokenUsage {
			u := &tokenUsage[i]
			if providerLabel != "" && (u.Provider == "" || len(u.Provider) == 36 || u.Provider == providerID) {
				u.Provider = providerLabel
				continue
			}
			// If provider is empty or looks like a UUID, try to look it up
			if u.Provider == "" || len(u.Provider) == 36 { // UUID length
				if label, ok := seenProviders[u.Provider]; ok {
					u.Provider = label
				} else if u.Provider != "" {
					if label := h.resolveProviderLabel(r.Context(), analysis.ProjectID, u.Provider, providerSource); label != "" {
						seenProviders[u.Provider] = label
						u.Provider = label
					}
				} else if label := h.resolveProviderLabel(r.Context(), analysis.ProjectID, providerID, providerSource); label != "" {
					u.Provider = label
				}
			}
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"analysis":    analysis,
		"packages":    packages,
		"token_usage": tokenUsage,
	})
}

// CancelAnalysis cancels a running or pending analysis.
func (h *Handler) CancelAnalysis(w http.ResponseWriter, r *http.Request) {
	analysisID := chi.URLParam(r, "analysisID")

	analysis, err := h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Analysis not found")
		return
	}

	if analysis.Status != "pending" && analysis.Status != "running" {
		respondError(w, http.StatusBadRequest, "Analysis cannot be cancelled in its current state")
		return
	}

	if err := h.queries.SetAnalysisCompleted(r.Context(), analysisID, "cancelled", "Cancelled by user"); err != nil {
		log.Error().Err(err).Msg("Failed to cancel analysis")
		respondError(w, http.StatusInternalServerError, "Failed to cancel analysis")
		return
	}

	// Signal the agent executor to kill the process.
	if h.executor != nil {
		h.executor.Cancel(analysisID)
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// ResubmitAnalysis creates a new analysis with the same packages as an existing one.
func (h *Handler) ResubmitAnalysis(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	analysisID := chi.URLParam(r, "analysisID")
	user := GetUserFromContext(r.Context())

	// Fetch original analysis's packages
	packages, err := h.queries.ListAnalysisPackages(r.Context(), analysisID)
	if err != nil || len(packages) == 0 {
		respondError(w, http.StatusBadRequest, "Original analysis has no packages")
		return
	}

	// Get original analysis for config
	orig, err := h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Analysis not found")
		return
	}

	agentConfig := orig.AgentConfig
	if len(agentConfig) == 0 {
		agentConfig = json.RawMessage(`{}`)
	}

	analysis := &models.Analysis{
		ProjectID:    projectID,
		Status:       "pending",
		TriggeredBy:  user.ID,
		AgentModel:   orig.AgentModel,
		AgentConfig:  agentConfig,
		CustomPrompt: orig.CustomPrompt,
	}

	// Generate a per-analysis DEK for encrypting output artifacts.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate analysis DEK")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}
	encDEK, nonce, err := h.encryptor.WrapDEK(dek)
	if err != nil {
		log.Error().Err(err).Msg("Failed to wrap analysis DEK")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}
	analysis.EncryptedDEK = encDEK
	analysis.DEKNonce = nonce

	if err := h.queries.CreateAnalysis(r.Context(), analysis); err != nil {
		log.Error().Err(err).Msg("Failed to create resubmitted analysis")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}

	for _, pkg := range packages {
		if err := h.queries.AddAnalysisPackage(r.Context(), analysis.ID, pkg.ID); err != nil {
			log.Error().Err(err).Str("package_id", pkg.ID).Msg("Failed to link package to resubmitted analysis")
		}
	}

	if h.executor != nil {
		pkgs, err := h.queries.ListAnalysisPackages(r.Context(), analysis.ID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to fetch packages for resubmitted analysis")
		} else {
			h.executor.Submit(analysis, pkgs)
		}
	}

	respondJSON(w, http.StatusCreated, analysis)
}

// --- External Upload ---

// CreateExternalAnalysis creates an analysis record in "importing" status so
// that an external tool can upload its own results. It does not launch any
// agent.
//
// POST /api/v1/projects/{projectID}/analyses/external
// Requires write project access.
func (h *Handler) CreateExternalAnalysis(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	user := GetUserFromContext(r.Context())

	if h.encryptor == nil {
		respondError(w, http.StatusServiceUnavailable, "Encryption not configured")
		return
	}

	var req struct {
		GitCommit    string          `json:"git_commit"`
		Environment  string          `json:"environment"`
		TriggerMeta  json.RawMessage `json:"trigger_meta"`
		StatusDetail string          `json:"status_detail"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// trigger_meta must be a JSON object when provided.
	meta := map[string]json.RawMessage{}
	if len(req.TriggerMeta) > 0 && string(req.TriggerMeta) != "null" {
		if err := json.Unmarshal(req.TriggerMeta, &meta); err != nil {
			respondError(w, http.StatusBadRequest, "trigger_meta must be a JSON object")
			return
		}
	}
	// Always stamp the origin of the upload.
	meta["workflow"] = json.RawMessage(`"external_upload"`)
	mergedMeta, _ := json.Marshal(meta)

	env := req.Environment
	if env == "" {
		env = "import"
	}

	dek, err := crypto.GenerateDEK()
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate DEK for external analysis")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}
	encDEK, nonce, err := h.encryptor.WrapDEK(dek)
	if err != nil {
		log.Error().Err(err).Msg("Failed to wrap DEK for external analysis")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}

	analysis := &models.Analysis{
		ProjectID:    projectID,
		TriggeredBy:  user.ID,
		Status:       "importing",
		Environment:  env,
		GitCommit:    req.GitCommit,
		StatusDetail: req.StatusDetail,
		TriggerEvent: "manual",
		TriggerMeta:  json.RawMessage(mergedMeta),
		EncryptedDEK: encDEK,
		DEKNonce:     nonce,
	}

	if err := h.queries.CreateExternalAnalysis(r.Context(), analysis); err != nil {
		log.Error().Err(err).Str("project_id", projectID).Msg("Failed to create external analysis")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}

	log.Info().
		Str("analysis_id", analysis.ID).
		Str("project_id", projectID).
		Str("triggered_by", user.ID).
		Msg("Created external analysis")

	respondJSON(w, http.StatusCreated, analysis)
}

// UploadAnalysisResult uploads a single result file (SARIF or other) to an
// analysis that is in "importing" status. Duplicate filenames are rejected
// with HTTP 409.
//
// POST /api/v1/projects/{projectID}/analyses/{analysisID}/results
// Requires read project access.
func (h *Handler) UploadAnalysisResult(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	analysisID := chi.URLParam(r, "analysisID")

	// Load and validate the analysis.
	analysis, err := h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Analysis not found")
		return
	}
	if analysis.ProjectID != projectID {
		respondError(w, http.StatusForbidden, "Analysis does not belong to this project")
		return
	}
	if analysis.Status != "importing" {
		respondError(w, http.StatusConflict,
			"Analysis is not in importing status (current: "+analysis.Status+")")
		return
	}
	if len(analysis.EncryptedDEK) == 0 || h.encryptor == nil {
		respondError(w, http.StatusInternalServerError, "Encryption not available")
		return
	}

	// Parse the multipart upload (limit 100 MB).
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid multipart form: "+err.Error())
		return
	}

	filename := r.FormValue("filename")
	if filename == "" {
		// Fall back to the filename from the multipart part header.
		_, fh, ferr := r.FormFile("file")
		if ferr == nil {
			filename = fh.Filename
		}
	}
	filename = sanitizeFilename(filename)
	if filename == "" {
		respondError(w, http.StatusBadRequest, "filename is required")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer func() { _ = file.Close() }()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, file); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}
	plaintext := buf.Bytes()

	// Classify before the duplicate check so we can validate SARIF early.
	resultType := classifyResultType(filename)

	if resultType == "sarif" {
		if err := agent.ValidateSARIFBytes(plaintext); err != nil {
			respondError(w, http.StatusBadRequest, "Invalid SARIF: "+err.Error())
			return
		}
	}

	// Reject duplicate filenames (idempotency check).
	if existing, err := h.queries.GetAnalysisResultByFilename(r.Context(), analysisID, filename); err == nil {
		respondError(w, http.StatusConflict,
			"A result with filename '"+existing.Filename+"' already exists for this analysis")
		return
	}

	// Encrypt and upload.
	dek, err := h.encryptor.UnwrapDEK(analysis.EncryptedDEK, analysis.DEKNonce)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to unwrap DEK")
		return
	}
	ciphertext, err := crypto.Encrypt(dek, plaintext)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to encrypt result")
		return
	}

	// Generate a per-upload UUID key so that concurrent uploads of the same
	// filename to the same analysis do not race on S3 content. If the DB
	// insert loses to a unique violation, the temp key is ours alone and we
	// delete it before returning 409.
	s3Key := fmt.Sprintf("analyses/%s/%s-%s", analysisID, uuid.New().String(), filename)
	if err := h.store.Upload(r.Context(), s3Key, bytes.NewReader(ciphertext),
		int64(len(ciphertext)), "application/octet-stream"); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to upload to storage")
		return
	}

	result := &models.AnalysisResult{
		AnalysisID:  analysisID,
		Filename:    filename,
		S3Key:       s3Key,
		FileSize:    int64(len(plaintext)),
		ResultType:  resultType,
		ContentType: "application/octet-stream",
	}
	if resultType == "sarif" {
		result.ContentType = "application/json"
	}

	if err := h.queries.CreateAnalysisResultIfImporting(r.Context(), result); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Lost a race with a concurrent upload. Our temp S3 key is not
			// claimed by any DB row, so delete it before returning 409.
			_ = h.store.Delete(r.Context(), s3Key)
			respondError(w, http.StatusConflict,
				"A result with filename '"+filename+"' already exists for this analysis")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// /complete raced ahead: the analysis is no longer 'importing'.
			_ = h.store.Delete(r.Context(), s3Key)
			respondError(w, http.StatusBadRequest,
				"Analysis is not in 'importing' status")
			return
		}
		// Genuine DB failure: clean up the orphaned S3 object.
		_ = h.store.Delete(r.Context(), s3Key)
		log.Error().Err(err).Str("filename", filename).Msg("Failed to insert analysis result")
		respondError(w, http.StatusInternalServerError, "Failed to record result")
		return
	}

	var findings []models.Finding
	if resultType == "sarif" {
		summary, findingCount, severityCounts := agent.ParseSARIFBytes(plaintext)
		result.Summary = summary
		result.FindingCount = findingCount
		result.SeverityCounts = severityCounts
		if err := h.queries.UpdateAnalysisResultMetadata(r.Context(), result.ID,
			summary, findingCount, severityCounts); err != nil {
			log.Error().Err(err).Str("result_id", result.ID).Msg("Failed to update SARIF metadata")
		}

		extracted := agent.ExtractFindingsFromBytes(plaintext, analysisID, analysis.ProjectID)
		if len(extracted) > 0 {
			for i := range extracted {
				extracted[i].ResultID = result.ID
				extracted[i].GitCommit = analysis.GitCommit
			}
			if err := h.queries.CreateFindingsBatch(r.Context(), extracted); err != nil {
				log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to save findings")
			} else {
				findings = extracted
			}
		}
	}

	log.Info().
		Str("analysis_id", analysisID).
		Str("filename", filename).
		Str("result_type", resultType).
		Int("size", len(plaintext)).
		Msg("External upload: stored result file")

	respondJSON(w, http.StatusCreated, map[string]any{
		"result":   result,
		"findings": findings,
	})
}

// CompleteAnalysis transitions an "importing" analysis to "completed". It
// requires at least one result to have been uploaded first.
//
// POST /api/v1/projects/{projectID}/analyses/{analysisID}/complete
// Requires read project access.
func (h *Handler) CompleteAnalysis(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	analysisID := chi.URLParam(r, "analysisID")

	// Optional status_detail in the request body.
	var req struct {
		StatusDetail string `json:"status_detail"`
	}
	// Ignore decode errors — body is optional.
	_ = decodeJSON(r, &req)

	analysis, err := h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Analysis not found")
		return
	}
	if analysis.ProjectID != projectID {
		respondError(w, http.StatusForbidden, "Analysis does not belong to this project")
		return
	}
	if analysis.Status != "importing" {
		respondError(w, http.StatusConflict,
			"Analysis is not in importing status (current: "+analysis.Status+")")
		return
	}

	count, err := h.queries.CountAnalysisResults(r.Context(), analysisID)
	if err != nil {
		log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to count results")
		respondError(w, http.StatusInternalServerError, "Failed to check results")
		return
	}
	if count == 0 {
		respondError(w, http.StatusBadRequest,
			"Cannot complete analysis: no results have been uploaded")
		return
	}

	if err := h.queries.CompleteExternalAnalysis(r.Context(), analysisID, req.StatusDetail); err != nil {
		if err == pgx.ErrNoRows {
			// Race: status changed between our check and the UPDATE.
			respondError(w, http.StatusConflict,
				"Analysis is no longer in importing status")
			return
		}
		log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to complete analysis")
		respondError(w, http.StatusInternalServerError, "Failed to complete analysis")
		return
	}

	log.Info().
		Str("analysis_id", analysisID).
		Str("project_id", projectID).
		Int("result_count", count).
		Msg("External analysis completed")

	respondJSON(w, http.StatusOK, map[string]string{
		"analysis_id": analysisID,
		"status":      "completed",
	})
}
