package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/github"
	"github.com/bbockelm/swamp/internal/models"
)

// SetGitHubClient sets the GitHub App client on the handler.
func (h *Handler) SetGitHubClient(ghClient *github.Client) {
	h.ghClient = ghClient
}

// userCanUseInstallation checks whether the given user is authorized to use
// the specified GitHub App installation. Admins can use any installation;
// non-admins can only use installations they own or that are linked to
// projects they admin.
func (h *Handler) userCanUseInstallation(ctx context.Context, userID string, installationID int64) bool {
	if UserHasRole(ctx, RoleAdmin) {
		return true
	}
	installations, err := h.queries.ListInstallationsForUser(ctx, userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list installations for authorization check")
		return false
	}
	for _, inst := range installations {
		if inst.InstallationID == installationID {
			return true
		}
	}
	return false
}

// GetGitHubStatus returns the GitHub App integration status (admin only).
func (h *Handler) GetGitHubStatus(w http.ResponseWriter, r *http.Request) {
	status := h.ghClient.Status(r.Context())
	respondJSON(w, http.StatusOK, status)
}

// ListGitHubInstallations returns GitHub App installations the current user
// is authorized to see:
//   - Admins: all installations (optionally filtered by ?owner=)
//   - Others: only installations they created, or installations linked to
//     projects where they have admin access.
func (h *Handler) ListGitHubInstallations(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Installations []models.GitHubAppInstallation `json:"installations"`
		InstallURL    string                         `json:"install_url,omitempty"`
	}

	if h.ghClient == nil || !h.ghClient.Configured() {
		respondJSON(w, http.StatusOK, response{Installations: []models.GitHubAppInstallation{}})
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var installations []models.GitHubAppInstallation
	var err error

	if UserHasRole(r.Context(), RoleAdmin) {
		installations, err = h.queries.ListGitHubInstallations(r.Context())
	} else {
		installations, err = h.queries.ListInstallationsForUser(r.Context(), user.ID)
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to list installations")
		respondError(w, http.StatusInternalServerError, "Failed to list installations")
		return
	}
	if installations == nil {
		installations = []models.GitHubAppInstallation{}
	}

	// Filter by owner if specified (case-insensitive).
	owner := r.URL.Query().Get("owner")
	if owner != "" {
		filtered := make([]models.GitHubAppInstallation, 0, 1)
		for _, inst := range installations {
			if strings.EqualFold(inst.AccountLogin, owner) {
				filtered = append(filtered, inst)
			}
		}
		installations = filtered
	}

	respondJSON(w, http.StatusOK, response{
		Installations: installations,
		InstallURL:    h.ghClient.InstallURL(r.Context()),
	})
}

// ClaimInstallation lets an authenticated user claim ownership of an
// installation (sets installed_by_user_id if not already set). This is
// called after the user returns from installing the GitHub App.
func (h *Handler) ClaimInstallation(w http.ResponseWriter, r *http.Request) {
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusBadRequest, "GitHub App is not configured")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	installationIDStr := chi.URLParam(r, "installationID")
	installationID, err := strconv.ParseInt(installationIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid installation ID")
		return
	}

	// Sync installations from GitHub first to ensure this one exists.
	if err := h.ghClient.SyncInstallations(r.Context()); err != nil {
		log.Error().Err(err).Msg("Failed to sync installations before claim")
	}

	// Verify the installation exists and is not already claimed.
	inst, err := h.queries.GetInstallationByID(r.Context(), installationID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Installation not found")
		return
	}
	if inst.InstalledByUserID != nil && *inst.InstalledByUserID != "" {
		// Already claimed — only allow if the claimer is the current owner.
		if *inst.InstalledByUserID != user.ID {
			respondError(w, http.StatusForbidden, "Installation is already claimed by another user")
			return
		}
		respondJSON(w, http.StatusOK, inst)
		return
	}

	// Try to claim (only sets if not already claimed).
	if err := h.queries.SetInstallationInstalledBy(r.Context(), installationID, user.ID); err != nil {
		log.Error().Err(err).Int64("installation_id", installationID).Msg("Failed to claim installation")
		respondError(w, http.StatusInternalServerError, "Failed to claim installation")
		return
	}

	// Return the installation.
	inst, err = h.queries.GetInstallationByID(r.Context(), installationID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Installation not found")
		return
	}
	respondJSON(w, http.StatusOK, inst)
}

// GetGitHubAppInfo returns non-sensitive GitHub App info (configured status
// and install URL). Available to any authenticated user.
func (h *Handler) GetGitHubAppInfo(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Configured bool   `json:"configured"`
		InstallURL string `json:"install_url,omitempty"`
	}
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondJSON(w, http.StatusOK, response{Configured: false})
		return
	}
	respondJSON(w, http.StatusOK, response{
		Configured: true,
		InstallURL: h.ghClient.InstallURL(r.Context()),
	})
}

// SyncGitHubInstallations fetches installations from GitHub and syncs to DB (admin only).
func (h *Handler) SyncGitHubInstallations(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusBadRequest, "GitHub App is not configured")
		return
	}
	log.Info().Str("user_id", user.ID).Str("email", user.Email).Msg("Admin triggered GitHub installation sync")
	if err := h.ghClient.SyncInstallations(r.Context()); err != nil {
		log.Error().Err(err).Msg("Failed to sync GitHub installations")
		respondError(w, http.StatusInternalServerError, "Failed to sync installations")
		return
	}
	status := h.ghClient.Status(r.Context())
	log.Info().Int("installations", len(status.Installations)).Msg("GitHub installation sync completed")
	respondJSON(w, http.StatusOK, status)
}

// GetProjectGitHubConfig returns the GitHub integration settings for a project.
func (h *Handler) GetProjectGitHubConfig(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	cfg, err := h.queries.GetProjectGitHubConfig(r.Context(), projectID)
	if err != nil {
		// Return empty config if not set up.
		respondJSON(w, http.StatusOK, &models.ProjectGitHubConfig{
			ProjectID: projectID,
		})
		return
	}
	respondJSON(w, http.StatusOK, cfg)
}

// UpdateProjectGitHubConfig creates or updates the GitHub config for a project.
func (h *Handler) UpdateProjectGitHubConfig(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")

	var req struct {
		GitHubOwner        string   `json:"github_owner"`
		GitHubRepo         string   `json:"github_repo"`
		DefaultBranch      string   `json:"default_branch"`
		InstallationID     int64    `json:"installation_id"`
		SARIFUploadEnabled bool     `json:"sarif_upload_enabled"`
		WebhookEnabled     bool     `json:"webhook_enabled"`
		WebhookEvents      []string `json:"webhook_events"`
		WebhookAgentModel  string   `json:"webhook_agent_model"`
		WebhookProviderID  *string  `json:"webhook_provider_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.GitHubOwner == "" || req.GitHubRepo == "" {
		respondError(w, http.StatusBadRequest, "github_owner and github_repo are required")
		return
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	if req.WebhookEvents == nil {
		req.WebhookEvents = []string{}
	}

	// Verify the user is authorized to use this installation.
	if req.InstallationID != 0 {
		user := GetUserFromContext(r.Context())
		if user == nil {
			respondError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		if !h.userCanUseInstallation(r.Context(), user.ID, req.InstallationID) {
			respondError(w, http.StatusForbidden, "You are not authorized to use this GitHub App installation")
			return
		}
	}

	if err := h.queries.UpsertProjectGitHubConfig(r.Context(), projectID, req.GitHubOwner, req.GitHubRepo, req.DefaultBranch, req.InstallationID, req.SARIFUploadEnabled, req.WebhookEnabled, req.WebhookEvents, req.WebhookAgentModel, req.WebhookProviderID); err != nil {
		log.Error().Err(err).Str("project_id", projectID).Msg("Failed to save project GitHub config")
		respondError(w, http.StatusInternalServerError, "Failed to save GitHub config")
		return
	}

	cfg, err := h.queries.GetProjectGitHubConfig(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Saved but failed to retrieve config")
		return
	}
	respondJSON(w, http.StatusOK, cfg)
}

// DeleteProjectGitHubConfig removes the GitHub integration for a project.
func (h *Handler) DeleteProjectGitHubConfig(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	if err := h.queries.DeleteProjectGitHubConfig(r.Context(), projectID); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to delete GitHub config")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ListPackageBranches lists branches for a package's GitHub repository,
// using the GitHub App installation token for private repo access.
func (h *Handler) ListPackageBranches(w http.ResponseWriter, r *http.Request) {
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusBadRequest, "GitHub App is not configured")
		return
	}
	pkgID := chi.URLParam(r, "packageID")
	pkg, err := h.queries.GetPackage(r.Context(), pkgID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Package not found")
		return
	}
	if pkg.GitHubOwner == "" || pkg.GitHubRepo == "" || pkg.InstallationID == 0 {
		respondError(w, http.StatusBadRequest, "Package has no GitHub App integration configured")
		return
	}
	branches, err := h.ghClient.ListBranches(r.Context(), pkg.InstallationID, pkg.GitHubOwner, pkg.GitHubRepo)
	if err != nil {
		log.Error().Err(err).Str("package_id", pkgID).Msg("Failed to list branches")
		respondError(w, http.StatusBadGateway, "Failed to list branches from GitHub")
		return
	}
	respondJSON(w, http.StatusOK, branches)
}

// ListRepoBranches lists branches for a GitHub repo by owner/repo.
// It finds the appropriate installation automatically, scoped to
// installations the current user is authorized to use.
// GET /api/v1/github/branches?owner=X&repo=Y
func (h *Handler) ListRepoBranches(w http.ResponseWriter, r *http.Request) {
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusBadRequest, "GitHub App is not configured")
		return
	}

	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")
	if owner == "" || repo == "" {
		respondError(w, http.StatusBadRequest, "owner and repo query parameters are required")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	// Look up installations the user is authorized to access, filtered by owner.
	var installations []models.GitHubAppInstallation
	var err error
	if UserHasRole(r.Context(), RoleAdmin) {
		installations, err = h.queries.ListGitHubInstallations(r.Context())
	} else {
		installations, err = h.queries.ListInstallationsForUser(r.Context(), user.ID)
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to list installations for branch lookup")
		respondError(w, http.StatusInternalServerError, "Failed to look up installations")
		return
	}

	// Find the installation matching this owner.
	var matchedInstallation *models.GitHubAppInstallation
	for i := range installations {
		if strings.EqualFold(installations[i].AccountLogin, owner) {
			matchedInstallation = &installations[i]
			break
		}
	}
	if matchedInstallation == nil {
		respondError(w, http.StatusNotFound, "No GitHub App installation found for this repository owner")
		return
	}

	branches, err := h.ghClient.ListBranches(r.Context(), matchedInstallation.InstallationID, owner, repo)
	if err != nil {
		log.Error().Err(err).Str("owner", owner).Str("repo", repo).Msg("Failed to list branches via installation")
		respondError(w, http.StatusBadGateway, "Failed to list branches from GitHub")
		return
	}
	respondJSON(w, http.StatusOK, branches)
}

// ListWebhookDeliveries returns webhook delivery logs for a project.
func (h *Handler) ListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	deliveries, err := h.queries.ListWebhookDeliveries(r.Context(), projectID, 100)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to list webhook deliveries")
		return
	}
	if deliveries == nil {
		deliveries = []models.GitHubWebhookDelivery{}
	}
	respondJSON(w, http.StatusOK, deliveries)
}

// HandleGitHubWebhook processes incoming GitHub webhook events.
// This endpoint is public (no auth) but validates HMAC-SHA256 signatures.
func (h *Handler) HandleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusServiceUnavailable, "GitHub App not configured")
		return
	}

	// Read body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 5*1024*1024)) // 5MB limit
	if err != nil {
		respondError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	// Validate signature.
	signature := r.Header.Get("X-Hub-Signature-256")
	if !h.ghClient.ValidateWebhookSignature(body, signature) {
		respondError(w, http.StatusUnauthorized, "Invalid webhook signature")
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")

	// Handle installation lifecycle events (created/deleted) before parsing
	// repo-specific payload fields, since these events don't have a repository.
	if eventType == "installation" {
		h.handleInstallationEvent(r.Context(), body, deliveryID)
		respondJSON(w, http.StatusOK, map[string]string{"status": "processed", "event": "installation"})
		return
	}

	// Parse common payload fields.
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
			Name string `json:"name"`
		} `json:"repository"`
		Ref    string `json:"ref"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
		// pull_request event fields
		PullRequest *struct {
			Number int    `json:"number"`
			State  string `json:"state"`
			Head   struct {
				Ref string `json:"ref"` // branch name
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		} `json:"pull_request,omitempty"`
		// release event fields
		Release *struct {
			TagName    string `json:"tag_name"`
			Name       string `json:"name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		} `json:"release,omitempty"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	log.Info().
		Str("event", eventType).
		Str("delivery_id", deliveryID).
		Str("repo", payload.Repository.FullName).
		Str("action", payload.Action).
		Msg("Received GitHub webhook")

	// Find matching project by repo.
	parts := strings.SplitN(payload.Repository.FullName, "/", 2)
	if len(parts) != 2 {
		respondError(w, http.StatusBadRequest, "Invalid repository name")
		return
	}
	owner, repo := parts[0], parts[1]

	ghCfg, findErr := h.queries.FindProjectByGitHubRepo(r.Context(), owner, repo)
	var projectIDPtr *string
	if findErr == nil {
		projectIDPtr = &ghCfg.ProjectID
	}

	// Record the delivery.
	delivery := &models.GitHubWebhookDelivery{
		DeliveryID:   deliveryID,
		EventType:    eventType,
		Action:       payload.Action,
		RepoFullName: payload.Repository.FullName,
		Ref:          payload.Ref,
		SenderLogin:  payload.Sender.Login,
		ProjectID:    projectIDPtr,
		Status:       "received",
		PayloadJSON:  json.RawMessage(body),
	}
	_ = h.queries.InsertWebhookDelivery(r.Context(), delivery)

	updateStatus := func(status, detail string, analysisID *string) {
		if delivery.ID != "" {
			_ = h.queries.UpdateWebhookDeliveryStatus(r.Context(), delivery.ID, status, detail, analysisID)
		}
	}

	// No matching project?
	if ghCfg == nil {
		updateStatus("ignored", "No matching project found", nil)
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no matching project"})
		return
	}

	if !ghCfg.WebhookEnabled {
		updateStatus("ignored", "Webhooks not enabled for project", nil)
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "webhooks not enabled"})
		return
	}

	// Check if this event type is in the allowed list.
	eventAllowed := false
	for _, e := range ghCfg.WebhookEvents {
		if e == eventType {
			eventAllowed = true
			break
		}
	}
	if !eventAllowed {
		updateStatus("ignored", "Event type not enabled: "+eventType, nil)
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "event type not configured"})
		return
	}

	// Trigger analysis based on event type.
	switch eventType {
	case "push":
		expectedRef := "refs/heads/" + ghCfg.DefaultBranch
		if payload.Ref != expectedRef {
			updateStatus("ignored", "Push to non-default branch: "+payload.Ref, nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "non-default branch"})
			return
		}
		// Extract branch name from refs/heads/<branch>.
		branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
		info := webhookTriggerInfo{
			Event:  "push",
			Branch: branch,
			Meta: map[string]interface{}{
				"ref":   payload.Ref,
				"repo":  payload.Repository.FullName,
				"push_sender": payload.Sender.Login,
			},
		}
		analysisID, triggerErr := h.triggerWebhookAnalysis(r.Context(), ghCfg, payload.Sender.Login, info)
		if triggerErr != nil {
			log.Error().Err(triggerErr).Str("project_id", ghCfg.ProjectID).Msg("Failed to trigger webhook analysis")
			updateStatus("error", triggerErr.Error(), nil)
			respondError(w, http.StatusInternalServerError, "Failed to trigger analysis")
			return
		}
		updateStatus("processed", "Triggered analysis: "+analysisID, &analysisID)
		respondJSON(w, http.StatusOK, map[string]string{"status": "processed", "analysis_id": analysisID})

	case "pull_request":
		if payload.PullRequest == nil {
			updateStatus("ignored", "Missing pull_request payload", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "missing pull_request payload"})
			return
		}
		// Only trigger on opened or synchronized (new commits pushed).
		if payload.Action != "opened" && payload.Action != "synchronize" {
			updateStatus("ignored", "Ignored pull_request action: "+payload.Action, nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "action not relevant"})
			return
		}
		prURL := fmt.Sprintf("https://github.com/%s/pull/%d", payload.Repository.FullName, payload.PullRequest.Number)
		info := webhookTriggerInfo{
			Event:  "pull_request",
			Branch: payload.PullRequest.Head.Ref,
			Commit: payload.PullRequest.Head.SHA,
			Meta: map[string]interface{}{
				"pr_number":  payload.PullRequest.Number,
				"pr_url":     prURL,
				"pr_action":  payload.Action,
				"head_ref":   payload.PullRequest.Head.Ref,
				"head_sha":   payload.PullRequest.Head.SHA,
				"base_ref":   payload.PullRequest.Base.Ref,
				"repo":       payload.Repository.FullName,
			},
		}
		analysisID, triggerErr := h.triggerWebhookAnalysis(r.Context(), ghCfg, payload.Sender.Login, info)
		if triggerErr != nil {
			log.Error().Err(triggerErr).Str("project_id", ghCfg.ProjectID).Msg("Failed to trigger webhook analysis for PR")
			updateStatus("error", triggerErr.Error(), nil)
			respondError(w, http.StatusInternalServerError, "Failed to trigger analysis")
			return
		}
		log.Info().
			Int("pr_number", payload.PullRequest.Number).
			Str("branch", payload.PullRequest.Head.Ref).
			Str("analysis_id", analysisID).
			Msg("Triggered analysis for pull request")
		updateStatus("processed", "Triggered analysis for PR #"+strconv.Itoa(payload.PullRequest.Number)+": "+analysisID, &analysisID)
		respondJSON(w, http.StatusOK, map[string]string{"status": "processed", "analysis_id": analysisID})

	case "release":
		if payload.Release == nil {
			updateStatus("ignored", "Missing release payload", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "missing release payload"})
			return
		}
		// Only trigger on published (not drafts, edits, or deletes).
		if payload.Action != "published" {
			updateStatus("ignored", "Ignored release action: "+payload.Action, nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "action not relevant"})
			return
		}
		if payload.Release.Draft {
			updateStatus("ignored", "Ignored draft release", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "draft release"})
			return
		}
		releaseURL := fmt.Sprintf("https://github.com/%s/releases/tag/%s", payload.Repository.FullName, payload.Release.TagName)
		info := webhookTriggerInfo{
			Event:  "release",
			Branch: payload.Release.TagName,
			Meta: map[string]interface{}{
				"tag":         payload.Release.TagName,
				"release_name": payload.Release.Name,
				"release_url": releaseURL,
				"prerelease":  payload.Release.Prerelease,
				"repo":        payload.Repository.FullName,
			},
		}
		analysisID, triggerErr := h.triggerWebhookAnalysis(r.Context(), ghCfg, payload.Sender.Login, info)
		if triggerErr != nil {
			log.Error().Err(triggerErr).Str("project_id", ghCfg.ProjectID).Msg("Failed to trigger webhook analysis for release")
			updateStatus("error", triggerErr.Error(), nil)
			respondError(w, http.StatusInternalServerError, "Failed to trigger analysis")
			return
		}
		log.Info().
			Str("tag", payload.Release.TagName).
			Str("analysis_id", analysisID).
			Msg("Triggered analysis for release")
		updateStatus("processed", "Triggered analysis for release "+payload.Release.TagName+": "+analysisID, &analysisID)
		respondJSON(w, http.StatusOK, map[string]string{"status": "processed", "analysis_id": analysisID})

	default:
		updateStatus("ignored", "Unhandled event type: "+eventType, nil)
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "unhandled event type"})
	}
}

// webhookTriggerInfo carries metadata about the triggering event.
type webhookTriggerInfo struct {
	Event    string                 // "push", "pull_request", "release"
	Branch   string                 // branch name or tag
	Commit   string                 // commit SHA if known
	Meta     map[string]interface{} // additional fields (pr_number, pr_url, tag, etc.)
}

// triggerWebhookAnalysis creates and starts an analysis triggered by a webhook.
func (h *Handler) triggerWebhookAnalysis(ctx context.Context, ghCfg *models.ProjectGitHubConfig, senderLogin string, info webhookTriggerInfo) (string, error) {
	// Get packages for this project.
	packages, err := h.queries.ListProjectPackages(ctx, ghCfg.ProjectID)
	if err != nil {
		return "", err
	}
	if len(packages) == 0 {
		return "", nil
	}

	metaBytes, _ := json.Marshal(info.Meta)

	// Build agent_config with provider info if configured.
	agentConfig := map[string]interface{}{}
	if ghCfg.WebhookProviderID != nil && *ghCfg.WebhookProviderID != "" {
		agentConfig["llm_provider_id"] = *ghCfg.WebhookProviderID
		agentConfig["provider_source"] = "global"
	}
	configBytes, _ := json.Marshal(agentConfig)

	analysis := &models.Analysis{
		ProjectID:    ghCfg.ProjectID,
		Status:       "pending",
		TriggeredBy:  "webhook:" + senderLogin,
		AgentModel:   ghCfg.WebhookAgentModel,
		AgentConfig:  json.RawMessage(configBytes),
		GitBranch:    info.Branch,
		GitCommit:    info.Commit,
		TriggerEvent: info.Event,
		TriggerMeta:  json.RawMessage(metaBytes),
	}

	// Generate a per-analysis DEK for encrypting output artifacts.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		return "", err
	}
	encDEK, nonce, err := h.encryptor.WrapDEK(dek)
	if err != nil {
		return "", err
	}
	analysis.EncryptedDEK = encDEK
	analysis.DEKNonce = nonce

	if err := h.queries.CreateAnalysis(ctx, analysis); err != nil {
		return "", err
	}

	// Link all packages.
	for _, pkg := range packages {
		if err := h.queries.AddAnalysisPackage(ctx, analysis.ID, pkg.ID); err != nil {
			log.Error().Err(err).Str("analysis_id", analysis.ID).Str("package_id", pkg.ID).Msg("Failed to link package")
		}
	}

	// Submit to executor.
	if h.executor != nil {
		h.executor.Submit(analysis, packages)
	}

	return analysis.ID, nil
}

// handleInstallationEvent processes GitHub App installation/uninstallation events.
func (h *Handler) handleInstallationEvent(ctx context.Context, body []byte, deliveryID string) {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID      int64 `json:"id"`
			Account struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"account"`
		} `json:"installation"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Error().Err(err).Str("delivery_id", deliveryID).Msg("Failed to parse installation event")
		return
	}

	installationID := payload.Installation.ID
	accountLogin := payload.Installation.Account.Login
	accountType := payload.Installation.Account.Type

	log.Info().
		Str("action", payload.Action).
		Int64("installation_id", installationID).
		Str("account", accountLogin).
		Str("sender", payload.Sender.Login).
		Msg("Processing installation event")

	switch payload.Action {
	case "created":
		if err := h.queries.UpsertGitHubInstallation(ctx, installationID, accountLogin, accountType, []byte("{}")); err != nil {
			log.Error().Err(err).Int64("installation_id", installationID).Msg("Failed to upsert installation")
			return
		}
	case "deleted":
		if err := h.queries.DeleteGitHubInstallation(ctx, installationID); err != nil {
			log.Error().Err(err).Int64("installation_id", installationID).Msg("Failed to delete installation")
		}
	default:
		log.Debug().Str("action", payload.Action).Int64("installation_id", installationID).Msg("Ignored installation action")
	}
}
