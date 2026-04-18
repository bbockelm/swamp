package models

import (
	"encoding/json"
	"time"
)

// User represents a registered SWAMP user.
type User struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"display_name"`
	Email       string     `json:"email"`
	Status      string     `json:"status"`
	LastLogin   *time.Time `json:"last_login,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// UserIdentity is a federated identity link (OIDC issuer + subject).
type UserIdentity struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Issuer      string    `json:"issuer"`
	Subject     string    `json:"subject"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	IDPName     string    `json:"idp_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// UserRole is a role assignment for a user.
type UserRole struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// Session represents an active authentication session.
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	TokenHash []byte    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// UserInvite is a platform-level invite token.
type UserInvite struct {
	ID        string    `json:"id"`
	TokenHash []byte    `json:"-"`
	Token     string    `json:"token,omitempty"`
	CreatedBy string    `json:"created_by"`
	Email     string    `json:"email"`
	Used      bool      `json:"used"`
	UsedBy    *string   `json:"used_by,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// AUPAgreement tracks a user's acceptance of the Acceptable Use Policy.
type AUPAgreement struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	AUPVersion string    `json:"aup_version"`
	AgreedAt   time.Time `json:"agreed_at"`
	IPAddress  string    `json:"ip_address"`
}

// Group represents a group of users for access control.
type Group struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	OwnerID      string    `json:"owner_id"`
	AdminGroupID *string   `json:"admin_group_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// GroupMember is a membership record linking a user to a group.
type GroupMember struct {
	ID          string    `json:"id"`
	GroupID     string    `json:"group_id"`
	UserID      string    `json:"user_id"`
	Role        string    `json:"role"`
	AddedBy     string    `json:"added_by"`
	CreatedAt   time.Time `json:"created_at"`
	DisplayName string    `json:"display_name,omitempty"`
	Email       string    `json:"email,omitempty"`
}

// GroupInvite is an invite to join a group.
type GroupInvite struct {
	ID                 string    `json:"id"`
	GroupID            string    `json:"group_id"`
	TokenHash          []byte    `json:"-"`
	Token              string    `json:"token,omitempty"`
	InvitedBy          string    `json:"invited_by"`
	Email              string    `json:"email"`
	Role               string    `json:"role"`
	AllowsRegistration bool      `json:"allows_registration"`
	Used               bool      `json:"used"`
	ExpiresAt          time.Time `json:"expires_at"`
	CreatedAt          time.Time `json:"created_at"`
}

// Project is a SWAMP analysis project.
type Project struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	OwnerID       string    `json:"owner_id"`
	ReadGroupID   *string   `json:"read_group_id,omitempty"`
	WriteGroupID  *string   `json:"write_group_id,omitempty"`
	AdminGroupID  *string   `json:"admin_group_id,omitempty"`
	UsesGlobalKey bool      `json:"uses_global_key"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	// Per-project LLM overrides. NULL means "use global config".
	// AgentProvider: "anthropic" or "external". NULL → use AGENT_PROVIDER env var.
	AgentProvider *string `json:"agent_provider,omitempty"`
	// ExternalLLMAnalysisModel overrides EXTERNAL_LLM_ANALYSIS_MODEL for Phase 1.
	ExternalLLMAnalysisModel *string `json:"ext_llm_analysis_model,omitempty"`
	// ExternalLLMPoCModel overrides EXTERNAL_LLM_POC_MODEL for Phase 2.
	// NULL → falls back to ExternalLLMAnalysisModel.
	ExternalLLMPoCModel *string `json:"ext_llm_poc_model,omitempty"`
	// ExternalLLMFallback overrides EXTERNAL_LLM_FALLBACK.
	// "anthropic" = retry Phase with Anthropic on failure. "" = no fallback.
	ExternalLLMFallback *string `json:"ext_llm_fallback,omitempty"`

	// MyRole is the caller's effective role for this project (set by handlers, not stored).
	MyRole string `json:"my_role,omitempty"`
}

// SoftwarePackage is a Git repository registered for analysis.
type SoftwarePackage struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	Name           string    `json:"name"`
	GitURL         string    `json:"git_url"`
	GitBranch      string    `json:"git_branch"`
	GitCommit      string    `json:"git_commit"`
	AnalysisPrompt string    `json:"analysis_prompt"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Analysis represents a security analysis run.
type Analysis struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"project_id"`
	ProjectName     string          `json:"project_name,omitempty"`
	TriggeredBy     string          `json:"triggered_by"`
	TriggeredByName string          `json:"triggered_by_name,omitempty"`
	Status          string          `json:"status"`
	StatusDetail    string          `json:"status_detail"`
	AgentModel      string          `json:"agent_model"`
	AgentConfig     json.RawMessage `json:"agent_config"`
	Environment     string          `json:"environment"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	ErrorMessage    string          `json:"error_message"`
	CustomPrompt    string          `json:"custom_prompt"`
	GitCommit       string          `json:"git_commit"`
	GitBranch       string          `json:"git_branch"`
	TriggerEvent    string          `json:"trigger_event"`
	TriggerMeta     json.RawMessage `json:"trigger_meta,omitempty"`
	SARIFUploadURL  string          `json:"sarif_upload_url,omitempty"`
	EncryptedDEK    []byte          `json:"-"`
	DEKNonce        []byte          `json:"-"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// AnalysisPackage links an analysis to a software package.
type AnalysisPackage struct {
	ID         string `json:"id"`
	AnalysisID string `json:"analysis_id"`
	PackageID  string `json:"package_id"`
}

// AnalysisResult is an output artifact from an analysis.
type AnalysisResult struct {
	ID             string          `json:"id"`
	AnalysisID     string          `json:"analysis_id"`
	PackageID      *string         `json:"package_id,omitempty"`
	ResultType     string          `json:"result_type"`
	S3Key          string          `json:"s3_key"`
	Filename       string          `json:"filename"`
	ContentType    string          `json:"content_type"`
	FileSize       int64           `json:"file_size"`
	Summary        string          `json:"summary"`
	FindingCount   int             `json:"finding_count"`
	SeverityCounts json.RawMessage `json:"severity_counts"`
	CreatedAt      time.Time       `json:"created_at"`
}

// APIKey is a long-lived API authentication key.
type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"-"`
	KeyPrefix  string     `json:"key_prefix"`
	UserID     string     `json:"user_id"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Revoked    bool       `json:"revoked"`
}

// AppConfig is a key-value configuration entry.
type AppConfig struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Backup represents a system backup record.
type Backup struct {
	ID           string     `json:"id"`
	Filename     string     `json:"filename"`
	S3Key        string     `json:"s3_key"`
	S3Bucket     string     `json:"s3_bucket"`
	SizeBytes    int64      `json:"size_bytes"`
	Status       string     `json:"status"`
	StatusDetail string     `json:"status_detail"`
	ErrorMsg     string     `json:"error_msg"`
	InitiatedBy  string     `json:"initiated_by"`
	Encrypted    bool       `json:"encrypted"`
	Checksum     string     `json:"checksum"`
	DurationSecs int        `json:"duration_secs,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// ObjectHash tracks SHA-256 hashes of S3 objects.
type ObjectHash struct {
	ID        string    `json:"id"`
	S3Key     string    `json:"s3_key"`
	SHA256    string    `json:"sha256"`
	SizeBytes int64     `json:"size_bytes"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BackupSettings holds backup configuration from app_config.
type BackupSettings struct {
	BackupFrequencyHours int    `json:"backup_frequency_hours"`
	BackupBucket         string `json:"backup_bucket"`
	BackupEndpoint       string `json:"backup_endpoint"`
	BackupAccessKey      string `json:"backup_access_key,omitempty"`
	BackupSecretKey      string `json:"backup_secret_key,omitempty"`
	BackupUseSSL         bool   `json:"backup_use_ssl"`
}

// Finding is an individual SARIF finding extracted from an analysis result.
type Finding struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	AnalysisID  string          `json:"analysis_id"`
	ResultID    string          `json:"result_id"`
	RuleID      string          `json:"rule_id"`
	Level       string          `json:"level"`
	Message     string          `json:"message"`
	FilePath    string          `json:"file_path"`
	StartLine   int             `json:"start_line"`
	EndLine     int             `json:"end_line"`
	Snippet     string          `json:"snippet"`
	Fingerprint string          `json:"fingerprint"`
	RawJSON     json.RawMessage `json:"raw_json"`
	GitCommit   string          `json:"git_commit"`
	CreatedAt   time.Time       `json:"created_at"`
	// Joined fields (not always populated)
	LatestStatus string `json:"latest_status,omitempty"`
	LatestNote   string `json:"latest_note,omitempty"`
	AnnotationBy string `json:"annotation_by,omitempty"`
	GitURL       string `json:"git_url,omitempty"`
}

// FindingAnnotation is a user's annotation/triage on a finding.
type FindingAnnotation struct {
	ID              string    `json:"id"`
	FindingID       string    `json:"finding_id"`
	UserID          string    `json:"user_id"`
	UserDisplayName string    `json:"user_display_name,omitempty"`
	Status          string    `json:"status"`
	Note            string    `json:"note"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// FindingSummary is a compact representation of a finding for prompt injection.
type FindingSummary struct {
	RuleID    string `json:"rule_id"`
	Level     string `json:"level"`
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	Message   string `json:"message"`
	Status    string `json:"status"`
	Note      string `json:"note"`
}

// AnalysisNoteRef holds metadata needed to retrieve a notes file from S3.
type AnalysisNoteRef struct {
	AnalysisID   string     `json:"analysis_id"`
	CompletedAt  *time.Time `json:"completed_at"`
	S3Key        string     `json:"s3_key"`
	EncryptedDEK []byte     `json:"-"`
	DEKNonce     []byte     `json:"-"`
}

// AnalysisContext holds prior-run context injected into analysis prompts.
type AnalysisContext struct {
	OpenFindings []FindingSummary    `json:"open_findings,omitempty"`
	PriorNotes   []string            `json:"prior_notes,omitempty"` // decrypted notes content from recent runs
	Annotations  []FindingAnnotation `json:"annotations,omitempty"`
}

// GitCloneCredential holds a short-lived credential for cloning a private repo.
// The worker uses this in its own Go code to pre-clone before the AI agent starts,
// so the credential is never visible to the agent.
type GitCloneCredential struct {
	CloneURL string `json:"clone_url"` // HTTPS URL without embedded credentials
	Token    string `json:"token"`     // Installation access token (Bearer)
	Branch   string `json:"branch"`    // Branch to clone (may be empty for default)
}

// ProjectProviderKey is an encrypted API key for an external provider (e.g. Anthropic).
type ProjectProviderKey struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"project_id"`
	Provider     string     `json:"provider"`
	Label        string     `json:"label"`
	KeyHint      string     `json:"key_hint"`
	EndpointURL  string     `json:"endpoint_url,omitempty"` // endpoint URL for custom, nrp, or external_llm providers
	APISchema    string     `json:"api_schema"`             // "anthropic" or "openai"
	EncryptedKey []byte     `json:"-"`
	EncryptedDEK []byte     `json:"-"`
	DEKNonce     []byte     `json:"-"`
	IsActive     bool       `json:"is_active"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

// LLMProvider is a global LLM provider configured by an admin.
type LLMProvider struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	APISchema    string    `json:"api_schema"` // "anthropic" or "openai"
	BaseURL      string    `json:"base_url"`
	DefaultModel string    `json:"default_model"`
	KeyHint      string    `json:"key_hint"`
	EncryptedKey []byte    `json:"-"`
	EncryptedDEK []byte    `json:"-"`
	DEKNonce     []byte    `json:"-"`
	Enabled      bool      `json:"enabled"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AvailableProvider is a unified view of a provider available for analysis.
// It can come from either a global LLM provider or a project provider key.
type AvailableProvider struct {
	ID           string `json:"id"`
	Source       string `json:"source"` // "global", "project", or "env"
	Label        string `json:"label"`
	APISchema    string `json:"api_schema"` // "anthropic" or "openai"
	BaseURL      string `json:"base_url"`
	DefaultModel string `json:"default_model"`
}

// ProjectAllowedProvider tracks which global/env providers a project can use.
type ProjectAllowedProvider struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	ProviderID     string    `json:"provider_id"`
	ProviderSource string    `json:"provider_source"` // "global" or "env"
	CreatedAt      time.Time `json:"created_at"`
	CreatedBy      string    `json:"created_by"`
}

// GitHubAppInstallation represents a GitHub App installation on an account.
type GitHubAppInstallation struct {
	ID             string          `json:"id"`
	InstallationID int64           `json:"installation_id"`
	AccountLogin   string          `json:"account_login"`
	AccountType    string          `json:"account_type"` // "User" or "Organization"
	Permissions    json.RawMessage `json:"permissions"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// ProjectGitHubConfig holds per-project GitHub integration settings.
type ProjectGitHubConfig struct {
	ID                 string    `json:"id"`
	ProjectID          string    `json:"project_id"`
	GitHubOwner        string    `json:"github_owner"`
	GitHubRepo         string    `json:"github_repo"`
	DefaultBranch      string    `json:"default_branch"`
	InstallationID     int64     `json:"installation_id"`
	SARIFUploadEnabled bool      `json:"sarif_upload_enabled"`
	WebhookEnabled     bool      `json:"webhook_enabled"`
	WebhookEvents      []string  `json:"webhook_events"`
	WebhookAgentModel  string    `json:"webhook_agent_model"`
	WebhookProviderID  *string   `json:"webhook_provider_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// GitHubWebhookDelivery is a log entry for a received webhook.
type GitHubWebhookDelivery struct {
	ID            string          `json:"id"`
	DeliveryID    string          `json:"delivery_id"`
	EventType     string          `json:"event_type"`
	Action        string          `json:"action"`
	RepoFullName  string          `json:"repo_full_name"`
	Ref           string          `json:"ref"`
	SenderLogin   string          `json:"sender_login"`
	ProjectID     *string         `json:"project_id,omitempty"`
	AnalysisID    *string         `json:"analysis_id,omitempty"`
	Status        string          `json:"status"`
	StatusDetail  string          `json:"status_detail"`
	PayloadJSON   json.RawMessage `json:"payload_json,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// GitHubStatus is the summary returned by the GitHub status endpoint.
type GitHubStatus struct {
	Configured    bool                     `json:"configured"`
	AppID         int64                    `json:"app_id,omitempty"`
	APIURL        string                   `json:"api_url,omitempty"`
	WebhookURL    string                   `json:"webhook_url,omitempty"`
	Installations []GitHubAppInstallation   `json:"installations,omitempty"`
}
