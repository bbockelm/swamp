package agent

import (
	"context"
	"fmt"

	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/internal/storage"
	"github.com/bbockelm/swamp/internal/ws"
)

// NRPExecutor composes the Kubernetes executor and adds NRP-specific policy
// checks before delegating actual execution to Kubernetes jobs.
type NRPExecutor struct {
	base    *K8sExecutor
	queries *db.Queries
}

// NewNRPExecutor creates the NRP executor wrapper around the Kubernetes executor.
func NewNRPExecutor(
	cfg *config.Config,
	queries *db.Queries,
	store *storage.Store,
	hub *ws.Hub,
	enc *crypto.Encryptor,
	tokenStore *WorkerTokenStore,
) (*NRPExecutor, error) {
	base, err := NewK8sExecutor(cfg, queries, store, hub, enc, tokenStore)
	if err != nil {
		return nil, fmt.Errorf("creating base K8s executor for NRP mode: %w", err)
	}
	return &NRPExecutor{base: base, queries: queries}, nil
}

func (e *NRPExecutor) AgentReady() bool {
	return e.base.AgentReady()
}

func (e *NRPExecutor) Submit(analysis *models.Analysis, packages []models.SoftwarePackage) {
	if analysis == nil {
		return
	}
	if e.queries != nil {
		project, err := e.queries.GetProject(context.Background(), analysis.ProjectID)
		if err != nil {
			e.base.failAnalysis(analysis.ID, "Failed to load project for NRP execution", err)
			return
		}
		if project == nil || !project.NRPExecutionEnabled {
			e.base.failAnalysis(analysis.ID, "Project is not enabled for NRP execution", nil)
			return
		}
	}
	e.base.Submit(analysis, packages)
}

func (e *NRPExecutor) Cancel(analysisID string) {
	e.base.Cancel(analysisID)
}

func (e *NRPExecutor) IsRunning(analysisID string) bool {
	return e.base.IsRunning(analysisID)
}

func (e *NRPExecutor) CanPersist() bool {
	return e.base.CanPersist()
}

func (e *NRPExecutor) Start(ctx context.Context) {
	e.base.Start(ctx)
}

func (e *NRPExecutor) Shutdown(ctx context.Context) {
	e.base.Shutdown(ctx)
}

// SetGitHubIntegration forwards GitHub integration to the base executor.
func (e *NRPExecutor) SetGitHubIntegration(gh GitHubIntegration) {
	e.base.SetGitHubIntegration(gh)
}

// TokenStore exposes the underlying worker token store for router wiring.
func (e *NRPExecutor) TokenStore() *WorkerTokenStore {
	return e.base.TokenStore()
}

// Hub exposes the underlying websocket hub for router wiring.
func (e *NRPExecutor) Hub() *ws.Hub {
	return e.base.Hub()
}