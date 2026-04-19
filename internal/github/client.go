// Package github provides a GitHub App client for private repo access,
// SARIF upload, and webhook handling.
package github

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
)

// parseGitHubRepo extracts owner/repo from common GitHub URL formats.
// Supported forms:
// - https://github.com/owner/repo(.git)
// - ssh://git@github.com/owner/repo(.git)
// - git@github.com:owner/repo(.git)
func parseGitHubRepo(gitURL string) (owner, repo string) {
	u := strings.TrimSpace(gitURL)
	if u == "" {
		return "", ""
	}

	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "git@github.com:") {
		rest := strings.TrimPrefix(u, "git@github.com:")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			return "", ""
		}
		owner = strings.TrimSpace(parts[0])
		repo = strings.TrimSuffix(strings.TrimSpace(parts[1]), ".git")
		repo = strings.TrimSuffix(repo, "/")
		return owner, repo
	}

	for _, prefix := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/"} {
		if strings.HasPrefix(lower, prefix) {
			rest := u[len(prefix):]
			parts := strings.SplitN(rest, "/", 3)
			if len(parts) < 2 {
				return "", ""
			}
			owner = strings.TrimSpace(parts[0])
			repo = strings.TrimSpace(parts[1])
			repo = strings.TrimSuffix(repo, ".git")
			repo = strings.TrimSuffix(repo, "/")
			return owner, repo
		}
	}

	return "", ""
}

// Client provides GitHub App API operations.
type Client struct {
	cfg     *config.Config
	queries *db.Queries
	apiURL  string
	appID   int64
	privKey *rsa.PrivateKey

	// Cache installation tokens (they last 1 hour, we expire at 50 min).
	mu     sync.Mutex
	tokens map[int64]*cachedToken

	// Cached app metadata from GET /app.
	appSlug    string // e.g. "my-swamp-app"
	appHTMLURL string // e.g. "https://github.com/apps/my-swamp-app"
	appInfoErr error
	appInfoOnce sync.Once
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// NewClient creates a GitHub App client. Returns nil if GitHub App is not configured.
func NewClient(cfg *config.Config, queries *db.Queries) *Client {
	if !cfg.GitHubAppConfigured() {
		return nil
	}
	pemData := cfg.GitHubAppPrivateKeyPEM()
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		log.Error().Msg("GitHub App: failed to decode PEM private key")
		return nil
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8
		parsed, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			log.Error().Err(err).Msg("GitHub App: failed to parse private key")
			return nil
		}
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			log.Error().Msg("GitHub App: private key is not RSA")
			return nil
		}
	}

	apiURL := cfg.GitHubAPIURL
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}

	log.Info().Int64("app_id", cfg.GitHubAppID).Str("api_url", apiURL).Msg("GitHub App client initialized")

	return &Client{
		cfg:     cfg,
		queries: queries,
		apiURL:  strings.TrimRight(apiURL, "/"),
		appID:   cfg.GitHubAppID,
		privKey: key,
		tokens:  make(map[int64]*cachedToken),
	}
}

// Configured returns true if this client is usable.
func (c *Client) Configured() bool {
	return c != nil && c.privKey != nil
}

// fetchAppInfo fetches app metadata (slug, html_url) from GET /app (cached).
func (c *Client) fetchAppInfo(ctx context.Context) {
	c.appInfoOnce.Do(func() {
		jwt, err := c.generateJWT()
		if err != nil {
			c.appInfoErr = err
			return
		}
		url := fmt.Sprintf("%s/app", c.apiURL)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			c.appInfoErr = err
			return
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.appInfoErr = fmt.Errorf("fetching app info: %w", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			c.appInfoErr = fmt.Errorf("GET /app returned %d: %s", resp.StatusCode, string(body))
			return
		}

		var result struct {
			Slug    string `json:"slug"`
			HTMLURL string `json:"html_url"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			c.appInfoErr = err
			return
		}
		c.appSlug = result.Slug
		c.appHTMLURL = result.HTMLURL
		log.Info().Str("slug", c.appSlug).Str("html_url", c.appHTMLURL).Msg("GitHub App info fetched")
	})
}

// InstallURL returns the URL where users can install the GitHub App.
// Returns empty string if the app info hasn't been fetched or failed.
func (c *Client) InstallURL(ctx context.Context) string {
	if c == nil || !c.Configured() {
		return ""
	}
	c.fetchAppInfo(ctx)
	if c.appHTMLURL != "" {
		return c.appHTMLURL + "/installations/new"
	}
	return ""
}

// generateJWT creates a signed JWT for GitHub App authentication.
// GitHub App JWTs are valid for up to 10 minutes.
func (c *Client) generateJWT() (string, error) {
	now := time.Now()
	// JWT header
	header := base64URLEncode([]byte(`{"alg":"RS256","typ":"JWT"}`))
	// JWT payload
	payload := fmt.Sprintf(`{"iat":%d,"exp":%d,"iss":"%d"}`,
		now.Add(-60*time.Second).Unix(), // 1 minute in the past (clock drift)
		now.Add(9*time.Minute).Unix(),   // 9 minutes (max 10)
		c.appID)
	payloadEnc := base64URLEncode([]byte(payload))

	signingInput := header + "." + payloadEnc
	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(nil, c.privKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}
	return signingInput + "." + base64URLEncode(sig), nil
}

func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// GetInstallationToken returns an installation access token, using cache when possible.
func (c *Client) GetInstallationToken(ctx context.Context, installationID int64) (string, error) {
	c.mu.Lock()
	if cached, ok := c.tokens[installationID]; ok && time.Now().Before(cached.expiresAt) {
		token := cached.token
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	jwt, err := c.generateJWT()
	if err != nil {
		return "", fmt.Errorf("generating JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiURL, installationID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting installation token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		return "", fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}

	// Cache with 10 minute buffer before expiry.
	c.mu.Lock()
	c.tokens[installationID] = &cachedToken{
		token:     result.Token,
		expiresAt: result.ExpiresAt.Add(-10 * time.Minute),
	}
	c.mu.Unlock()

	return result.Token, nil
}

// CloneURL returns an authenticated HTTPS URL for cloning a repository.
// For private repos, this injects the installation token into the URL.
func (c *Client) CloneURL(ctx context.Context, installationID int64, owner, repo string) (string, error) {
	token, err := c.GetInstallationToken(ctx, installationID)
	if err != nil {
		return "", fmt.Errorf("getting installation token for clone: %w", err)
	}
	// Use x-access-token as the username (GitHub convention for installation tokens).
	return fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repo), nil
}

// UploadSARIF uploads a SARIF file to GitHub Code Scanning API.
// The SARIF data is gzipped and base64-encoded as required by the API.
// Returns the URL of the uploaded SARIF analysis on success.
func (c *Client) UploadSARIF(ctx context.Context, installationID int64, owner, repo, commitSHA, ref string, sarifData []byte) (string, error) {
	token, err := c.GetInstallationToken(ctx, installationID)
	if err != nil {
		return "", fmt.Errorf("getting installation token for SARIF upload: %w", err)
	}

	// Gzip compress the SARIF data.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(sarifData); err != nil {
		return "", fmt.Errorf("gzip SARIF: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", fmt.Errorf("gzip close: %w", err)
	}

	// Base64 encode.
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	// Ensure ref has proper prefix.
	if !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + ref
	}

	payload := map[string]interface{}{
		"commit_sha": commitSHA,
		"ref":        ref,
		"sarif":      encoded,
		"tool_name":  "SWAMP",
	}
	payloadBytes, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/repos/%s/%s/code-scanning/sarifs", c.apiURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payloadBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("SARIF upload request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 202 {
		return "", fmt.Errorf("SARIF upload returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &result); err == nil {
		log.Info().Str("sarif_id", result.ID).Str("owner", owner).Str("repo", repo).
			Msg("SARIF uploaded to GitHub Code Scanning")
	}

	// Build the human-friendly Code Scanning alerts URL.
	alertsURL := fmt.Sprintf("https://github.com/%s/%s/security/code-scanning", owner, repo)
	return alertsURL, nil
}

// ValidateWebhookSignature validates the HMAC-SHA256 signature of a webhook payload.
func (c *Client) ValidateWebhookSignature(payload []byte, signature string) bool {
	secret := c.cfg.GitHubWebhookSecret
	if secret == "" {
		// No secret configured — reject all webhooks.
		return false
	}
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	sigHex := strings.TrimPrefix(signature, "sha256=")
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)
	return hmac.Equal(sigBytes, expected)
}

// SyncInstallations fetches all installations from GitHub and syncs them to the database.
func (c *Client) SyncInstallations(ctx context.Context) error {
	log.Info().Msg("Starting GitHub App installation sync")
	jwt, err := c.generateJWT()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/app/installations", c.apiURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("listing installations: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("listing installations returned %d: %s", resp.StatusCode, string(body))
	}

	var installations []struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		Permissions json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(body, &installations); err != nil {
		return fmt.Errorf("parsing installations: %w", err)
	}

	log.Info().Int("count", len(installations)).Msg("Fetched installations from GitHub API")
	for _, inst := range installations {
		permJSON, _ := json.Marshal(inst.Permissions)
		if err := c.queries.UpsertGitHubInstallation(ctx, inst.ID, inst.Account.Login, inst.Account.Type, permJSON); err != nil {
			log.Error().Err(err).Int64("installation_id", inst.ID).Msg("Failed to upsert installation")
		} else {
			log.Info().Int64("installation_id", inst.ID).Str("account", inst.Account.Login).Str("type", inst.Account.Type).Msg("Upserted installation")
		}
	}

	log.Info().Int("count", len(installations)).Msg("GitHub App installation sync finished")
	return nil
}

// GetRepositoryDefaultBranch fetches the default branch for a repository.
func (c *Client) GetRepositoryDefaultBranch(ctx context.Context, installationID int64, owner, repo string) (string, error) {
	token, err := c.GetInstallationToken(ctx, installationID)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/repos/%s/%s", c.apiURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("get repo returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	return result.DefaultBranch, nil
}

// ListBranches lists branches for a repository using installation auth.
func (c *Client) ListBranches(ctx context.Context, installationID int64, owner, repo string) ([]string, error) {
	token, err := c.GetInstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	apiURL := fmt.Sprintf("%s/repos/%s/%s/branches?per_page=100", c.apiURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("list branches returned %d: %s", resp.StatusCode, string(body))
	}

	var branches []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &branches); err != nil {
		return nil, err
	}
	names := make([]string, len(branches))
	for i, b := range branches {
		names[i] = b.Name
	}
	return names, nil
}

// Status returns the GitHub integration status for the admin dashboard.
func (c *Client) Status(ctx context.Context) *models.GitHubStatus {
	if c == nil || !c.Configured() {
		return &models.GitHubStatus{Configured: false}
	}
	status := &models.GitHubStatus{
		Configured: true,
		AppID:      c.appID,
		APIURL:     c.apiURL,
		WebhookURL: c.cfg.BaseURL + "/api/v1/github/webhook",
	}
	if installations, err := c.queries.ListGitHubInstallations(ctx); err == nil {
		status.Installations = installations
	}
	return status
}

// CloneCredential implements agent.GitHubIntegration.
// It returns a short-lived clone credential for a project's GitHub repo
// without performing the actual clone. Used by remote executors (K8s, process)
// that pass the credential to a worker for pre-cloning.
func (c *Client) CloneCredential(ctx context.Context, projectID string) (*models.GitCloneCredential, error) {
	if c == nil || !c.Configured() {
		return nil, nil
	}
	ghCfg, err := c.queries.GetProjectGitHubConfig(ctx, projectID)
	if err != nil || ghCfg.InstallationID == 0 {
		return nil, nil
	}
	token, err := c.GetInstallationToken(ctx, ghCfg.InstallationID)
	if err != nil {
		return nil, fmt.Errorf("getting installation token: %w", err)
	}
	return &models.GitCloneCredential{
		CloneURL: fmt.Sprintf("https://github.com/%s/%s.git", ghCfg.GitHubOwner, ghCfg.GitHubRepo),
		Token:    token,
		Branch:   ghCfg.DefaultBranch,
	}, nil
}

// CloneCredentialForPackage returns a clone credential using a package's
// own GitHub config fields. Falls back to project-level config.
func (c *Client) CloneCredentialForPackage(ctx context.Context, pkg *models.SoftwarePackage) (*models.GitCloneCredential, error) {
	if c == nil || !c.Configured() {
		if pkg != nil {
			log.Debug().
				Str("package_id", pkg.ID).
				Str("package", pkg.Name).
				Msg("Skipping GitHub clone credential resolution: integration not configured")
		}
		return nil, nil
	}

	ownerSource := "package"
	owner := pkg.GitHubOwner
	repo := pkg.GitHubRepo
	if owner == "" || repo == "" {
		ownerSource = "git_url"
		owner, repo = parseGitHubRepo(pkg.GitURL)
	}
	installSource := "package"
	installationID := pkg.InstallationID
	if installationID == 0 && owner != "" {
		installSource = "owner_lookup"
		if inst, err := c.queries.GetInstallationByOwner(ctx, owner); err == nil {
			installationID = inst.InstallationID
		}
	}

	// Use package-level GitHub config if present.
	if owner != "" && repo != "" && installationID != 0 {
		token, err := c.GetInstallationToken(ctx, installationID)
		if err != nil {
			return nil, fmt.Errorf("getting installation token for package: %w", err)
		}
		log.Debug().
			Str("package_id", pkg.ID).
			Str("package", pkg.Name).
			Str("owner", owner).
			Str("repo", repo).
			Str("owner_source", ownerSource).
			Str("installation_source", installSource).
			Int64("installation_id", installationID).
			Msg("Resolved GitHub clone credential for package")
		return &models.GitCloneCredential{
			CloneURL: fmt.Sprintf("https://github.com/%s/%s.git", owner, repo),
			Token:    token,
			Branch:   pkg.GitBranch,
		}, nil
	}
	// Fall back to project-level config.
	log.Debug().
		Str("package_id", pkg.ID).
		Str("package", pkg.Name).
		Str("owner", owner).
		Str("repo", repo).
		Str("owner_source", ownerSource).
		Int64("installation_id", installationID).
		Msg("Package-level clone credential unavailable; trying project-level GitHub config")

	cred, err := c.CloneCredential(ctx, pkg.ProjectID)
	if err != nil {
		return nil, err
	}
	if cred != nil {
		log.Debug().
			Str("package_id", pkg.ID).
			Str("package", pkg.Name).
			Msg("Resolved GitHub clone credential via project-level config")
	} else {
		log.Debug().
			Str("package_id", pkg.ID).
			Str("package", pkg.Name).
			Msg("No GitHub clone credential resolved for package")
	}
	return cred, nil
}

// UploadSARIFForProject implements agent.GitHubIntegration.
// It checks the project's GitHub config and uploads SARIF if enabled.
// Returns the Code Scanning URL if the upload succeeded, or "" if skipped.
func (c *Client) UploadSARIFForProject(ctx context.Context, projectID string, sarifData []byte) (string, error) {
	if c == nil || !c.Configured() {
		return "", nil
	}
	ghCfg, err := c.queries.GetProjectGitHubConfig(ctx, projectID)
	if err != nil || !ghCfg.SARIFUploadEnabled || ghCfg.InstallationID == 0 {
		return "", nil
	}

	// Try to extract the commit SHA from the SARIF file.
	commitSHA := extractCommitSHA(sarifData)
	if commitSHA == "" {
		// Use "HEAD" as fallback — GitHub will resolve it.
		commitSHA = "HEAD"
	}

	return c.UploadSARIF(ctx, ghCfg.InstallationID, ghCfg.GitHubOwner, ghCfg.GitHubRepo,
		commitSHA, ghCfg.DefaultBranch, sarifData)
}

// UploadSARIFForPackage uploads SARIF to GitHub Code Scanning using a
// package's own GitHub config. Falls back to project-level config.
func (c *Client) UploadSARIFForPackage(ctx context.Context, pkg *models.SoftwarePackage, sarifData []byte) (string, error) {
	if c == nil || !c.Configured() {
		return "", nil
	}
	// Use package-level GitHub config if present.
	if pkg.GitHubOwner != "" && pkg.GitHubRepo != "" && pkg.InstallationID != 0 && pkg.SARIFUploadEnabled {
		commitSHA := extractCommitSHA(sarifData)
		if commitSHA == "" {
			commitSHA = "HEAD"
		}
		return c.UploadSARIF(ctx, pkg.InstallationID, pkg.GitHubOwner, pkg.GitHubRepo,
			commitSHA, pkg.GitBranch, sarifData)
	}
	// Fall back to project-level.
	return c.UploadSARIFForProject(ctx, pkg.ProjectID, sarifData)
}

// RepoAccessResult describes whether the GitHub App can access a specific repo.
type RepoAccessResult struct {
	// HasInstallation is true if an installation exists for the repo owner.
	HasInstallation bool
	// Accessible is true if the installation can actually read the repo.
	Accessible bool
	// DefaultBranch is set when the repo is accessible.
	DefaultBranch string
	// Error is a user-safe message when access check fails.
	Error string
}

// CheckRepoAccess verifies whether any known installation can access the given
// repository. This does NOT filter by user — installations are org-scoped and
// usable by anyone who can reach the repo through the app.
func (c *Client) CheckRepoAccess(ctx context.Context, owner, repo string) *RepoAccessResult {
	if c == nil || !c.Configured() {
		return &RepoAccessResult{Error: "GitHub App is not configured"}
	}

	inst, err := c.queries.GetInstallationByOwner(ctx, owner)
	if err != nil || inst == nil {
		return &RepoAccessResult{
			HasInstallation: false,
			Error:           fmt.Sprintf("No GitHub App installation found for %q", owner),
		}
	}

	token, err := c.GetInstallationToken(ctx, inst.InstallationID)
	if err != nil {
		log.Warn().Err(err).Str("owner", owner).Int64("installation_id", inst.InstallationID).
			Msg("Failed to get installation token for repo access check")
		return &RepoAccessResult{
			HasInstallation: true,
			Error:           "GitHub App is installed but could not authenticate. The installation may need to be re-authorized.",
		}
	}

	url := fmt.Sprintf("%s/repos/%s/%s", c.apiURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &RepoAccessResult{HasInstallation: true, Error: "internal error"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &RepoAccessResult{HasInstallation: true, Error: "Failed to reach GitHub API"}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 200 {
		var repoInfo struct {
			DefaultBranch string `json:"default_branch"`
		}
		_ = json.Unmarshal(body, &repoInfo)
		return &RepoAccessResult{
			HasInstallation: true,
			Accessible:      true,
			DefaultBranch:   repoInfo.DefaultBranch,
		}
	}
	if resp.StatusCode == 404 || resp.StatusCode == 403 {
		return &RepoAccessResult{
			HasInstallation: true,
			Accessible:      false,
			Error:           "GitHub App is installed but does not have access to this repository. Ask the organization admin to grant the app access.",
		}
	}
	return &RepoAccessResult{
		HasInstallation: true,
		Error:           fmt.Sprintf("GitHub API returned unexpected status %d", resp.StatusCode),
	}
}

// extractCommitSHA attempts to find a git commit SHA in the SARIF data.
func extractCommitSHA(sarifData []byte) string {
	var sarif struct {
		Runs []struct {
			VersionControlProvenance []struct {
				RevisionID string `json:"revisionId"`
			} `json:"versionControlProvenance"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(sarifData, &sarif); err != nil {
		return ""
	}
	for _, run := range sarif.Runs {
		for _, vcp := range run.VersionControlProvenance {
			if vcp.RevisionID != "" {
				return vcp.RevisionID
			}
		}
	}
	return ""
}
