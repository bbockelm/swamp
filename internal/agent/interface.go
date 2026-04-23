package agent

import (
	"context"

	"github.com/bbockelm/swamp/internal/models"
)

// AnalysisExecutor is the interface for running security analyses.
// Implementations include the local fork/exec Executor and the
// Kubernetes-based K8sExecutor.
type AnalysisExecutor interface {
	// AgentReady returns true if the executor can accept new analyses.
	AgentReady() bool

	// Submit queues an analysis for execution. It runs asynchronously.
	Submit(analysis *models.Analysis, packages []models.SoftwarePackage)

	// Cancel stops a running analysis.
	Cancel(analysisID string)

	// IsRunning reports whether the executor is currently tracking the analysis.
	IsRunning(analysisID string) bool

	// CanPersist reports whether the executor survives server restarts.
	CanPersist() bool

	// Start performs startup reconciliation and begins background loops.
	Start(ctx context.Context)

	// Shutdown cancels running analyses and waits for cleanup.
	Shutdown(ctx context.Context)
}

// GitHubIntegration provides optional GitHub App integration for the executor.
// This is implemented by the github.Client and injected to avoid circular imports.
type GitHubIntegration interface {
	// CloneCredential returns a short-lived clone credential for a project's
	// GitHub repo, without performing the actual clone. The caller uses
	// SecureGitClone() to perform the clone so that the credential is never
	// exposed via command-line arguments, environment variables, or files.
	// Returns nil if the project has no GitHub config.
	CloneCredential(ctx context.Context, projectID string) (*models.GitCloneCredential, error)

	// CloneCredentialForPackage returns a clone credential using a package's
	// own GitHub config, falling back to project-level config.
	CloneCredentialForPackage(ctx context.Context, pkg *models.SoftwarePackage) (*models.GitCloneCredential, error)

	// UploadSARIFForProject uploads SARIF results to GitHub Code Scanning
	// if the project has SARIF upload enabled. Returns the Code Scanning
	// alerts URL if upload succeeded, or "" if skipped.
	// commitSHA is the known 40-char git SHA for the analysis; pass "" to
	// auto-detect from SARIF or from the GitHub API.
	UploadSARIFForProject(ctx context.Context, projectID string, sarifData []byte, commitSHA string) (string, error)

	// UploadSARIFForPackage uploads SARIF results using a package's own
	// GitHub config, falling back to project-level config.
	// commitSHA is the known 40-char git SHA for the analysis; pass "" to
	// auto-detect from SARIF or from the GitHub API.
	UploadSARIFForPackage(ctx context.Context, pkg *models.SoftwarePackage, sarifData []byte, commitSHA string) (string, error)
}
