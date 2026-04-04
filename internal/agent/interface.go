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
