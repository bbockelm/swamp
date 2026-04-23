package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/internal/storage"
	"github.com/bbockelm/swamp/internal/ws"
)

// ProcessExecutor launches analyses as detached child processes of the same
// binary (in worker mode). Unlike the local Executor, these processes survive
// server/air restarts because they are fully detached (using SysProcAttr.Setsid).
// The real Anthropic API key is proxied through the server, never in the child env.
//
// Liveness detection uses flock(2) rather than kill(pid, 0) to avoid PID-reuse
// races. Each child holds an exclusive lock on its state file for its entire
// lifetime. The parent/reconciler probes that lock with LOCK_NB to determine
// whether the child is still alive.
type ProcessExecutor struct {
	cfg        *config.Config
	queries    *db.Queries
	store      *storage.Store
	hub        *ws.Hub
	encryptor  *crypto.Encryptor
	tokenStore *WorkerTokenStore
	ghInteg    GitHubIntegration // optional GitHub App integration

	mu       sync.Mutex
	running  map[string]*processState // analysisID → state
	countsem chan struct{}

	stateDir string // directory for lock/state files
	stopSync context.CancelFunc
}

type processState struct {
	PID       int       `json:"pid"`
	Analysis  string    `json:"analysis_id"`
	StartedAt time.Time `json:"started_at"`
	cancel    context.CancelFunc
}

// NewProcessExecutor creates a new process-based executor.
func NewProcessExecutor(
	cfg *config.Config,
	queries *db.Queries,
	store *storage.Store,
	hub *ws.Hub,
	enc *crypto.Encryptor,
	tokenStore *WorkerTokenStore,
) (*ProcessExecutor, error) {
	stateDir := cfg.ProcessStateDir
	// Resolve to absolute so child processes (which run in a different
	// working directory) can open the lock file by the same path.
	if !filepath.IsAbs(stateDir) {
		abs, err := filepath.Abs(stateDir)
		if err != nil {
			return nil, fmt.Errorf("resolving process state dir: %w", err)
		}
		stateDir = abs
	}
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return nil, fmt.Errorf("creating process state dir %s: %w", stateDir, err)
	}

	return &ProcessExecutor{
		cfg:        cfg,
		queries:    queries,
		store:      store,
		hub:        hub,
		encryptor:  enc,
		tokenStore: tokenStore,
		running:    make(map[string]*processState),
		countsem:   make(chan struct{}, cfg.MaxConcurrentAnalyses),
		stateDir:   stateDir,
	}, nil
}

// CanPersist returns true — detached processes survive server restarts.
func (e *ProcessExecutor) CanPersist() bool {
	return true
}

// AgentReady returns true if the executor is properly configured.
func (e *ProcessExecutor) AgentReady() bool {
	return true
}

// SetGitHubIntegration injects the optional GitHub App integration.
func (e *ProcessExecutor) SetGitHubIntegration(gh GitHubIntegration) {
	e.ghInteg = gh
}

// Start reconciles running processes from the state directory and begins
// the sync loop.
func (e *ProcessExecutor) Start(ctx context.Context) {
	e.reconcileExisting(ctx)

	syncCtx, cancel := context.WithCancel(ctx)
	e.stopSync = cancel
	go e.syncLoop(syncCtx)
}

// Shutdown stops the sync loop but does NOT kill child processes — they
// persist and will be reconciled on the next startup.
func (e *ProcessExecutor) Shutdown(ctx context.Context) {
	if e.stopSync != nil {
		e.stopSync()
	}
	log.Info().Msg("Process executor shutdown (child processes will persist)")
}

// Submit launches a new analysis as a detached process.
func (e *ProcessExecutor) Submit(analysis *models.Analysis, packages []models.SoftwarePackage) {
	go e.launchProcess(analysis, packages)
}

// Cancel terminates a running analysis process.
func (e *ProcessExecutor) Cancel(analysisID string) {
	e.mu.Lock()
	state, ok := e.running[analysisID]
	e.mu.Unlock()

	if !ok {
		return
	}

	if state.cancel != nil {
		state.cancel()
	}

	// Send SIGTERM to the process group.
	if state.PID > 0 {
		if err := syscall.Kill(-state.PID, syscall.SIGTERM); err != nil {
			log.Debug().Err(err).Int("pid", state.PID).Msg("Failed to send SIGTERM to process group")
		}
	}

	e.tokenStore.RevokeAnalysis(analysisID)
	e.removeState(analysisID)

	e.mu.Lock()
	delete(e.running, analysisID)
	e.mu.Unlock()
}

// IsRunning reports whether the executor is tracking a given analysis.
func (e *ProcessExecutor) IsRunning(analysisID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.running[analysisID]
	return ok
}

// TokenStore returns the worker token store.
func (e *ProcessExecutor) TokenStore() *WorkerTokenStore {
	return e.tokenStore
}

// Hub returns the WebSocket hub.
func (e *ProcessExecutor) Hub() *ws.Hub {
	return e.hub
}

// statePath returns the lock/state file path for an analysis.
func (e *ProcessExecutor) statePath(analysisID string) string {
	return filepath.Join(e.stateDir, analysisID+".lock")
}

// launchProcess forks the current binary as a detached worker daemon.
func (e *ProcessExecutor) launchProcess(analysis *models.Analysis, packages []models.SoftwarePackage) {
	e.hub.Broadcast(analysis.ID, []byte("[system] Analysis queued, waiting for available slot..."))

	// Acquire semaphore.
	e.countsem <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.MaxAnalysisDuration)

	// Build the Anthropic API proxy URL. Use the Go backend directly
	// (localhost:AppPort) rather than BaseURL which may point to Next.js
	// dev server — its rewrite proxy has a ~30s timeout that kills long
	// Claude API calls.
	internalBase := fmt.Sprintf("http://localhost:%s", e.cfg.AppPort)
	proxyURL := internalBase + "/api/v1/internal/worker/anthropic"

	// Build the generic LLM proxy URL for non-Anthropic providers.
	llmProxyURL := internalBase + "/api/v1/internal/worker/llm"

	// Gather analysis context (prior findings + notes) for the worker.
	analysisCtx := gatherAnalysisContext(ctx, e.queries, e.encryptor, e.store, analysis.ProjectID, packages)

	// Determine effective model: per-analysis overrides global config.
	effectiveModel := analysis.AgentModel
	if effectiveModel == "" {
		effectiveModel = e.cfg.AgentModel
	}

	// Try new provider-based resolution first (from analysis agent_config).
	resolvedProvider, resolveErr := ResolveAnalysisProvider(ctx, e.queries, e.encryptor, e.cfg, analysis)
	if resolveErr != nil {
		log.Warn().Err(resolveErr).Str("analysis_id", analysis.ID).Msg("Failed to resolve analysis provider, falling back to legacy")
	}

	var agentProvider string
	var extLLMProxyURL string
	var extLLMAnalysisModel string
	var extLLMPoCModel string

	if resolvedProvider != nil {
		// New provider-based path: use the resolved provider info.
		if resolvedProvider.Model != "" {
			effectiveModel = resolvedProvider.Model
		}
		if resolvedProvider.APISchema == "openai" {
			agentProvider = "external"
			extLLMProxyURL = llmProxyURL
			extLLMAnalysisModel = resolvedProvider.Model
			extLLMPoCModel = resolvedProvider.Model
		} else {
			// Anthropic schema — use the anthropic proxy.
			agentProvider = "anthropic"
		}
	} else {
		// Legacy: resolve from global + per-project overrides.
		var project *models.Project
		if e.queries != nil && analysis.ProjectID != "" {
			project, _ = e.queries.GetProject(ctx, analysis.ProjectID)
		}
		llmConfig := ResolveEffectiveLLMConfig(e.cfg, project)
		agentProvider = llmConfig.Provider
		extLLMAnalysisModel = llmConfig.AnalysisModel
		extLLMPoCModel = llmConfig.PoCModel
		if llmConfig.Provider == "external" {
			extLLMProxyURL = llmProxyURL
		}
	}

	// Resolve GitHub clone credentials for all packages.
	gitCloneCreds := make([]*models.GitCloneCredential, 0, len(packages))
	if e.ghInteg != nil {
		for i := range packages {
			pkg := &packages[i]
			cred, err := e.ghInteg.CloneCredentialForPackage(ctx, pkg)
			if err != nil {
				log.Warn().Err(err).
					Str("analysis_id", analysis.ID).
					Str("package_id", pkg.ID).
					Str("package", pkg.Name).
					Msg("Failed to resolve GitHub clone credential for package")
				continue
			}
			if cred != nil {
				gitCloneCreds = append(gitCloneCreds, cred)
			}
		}
	}
	log.Info().
		Str("analysis_id", analysis.ID).
		Int("package_count", len(packages)).
		Int("clone_credential_count", len(gitCloneCreds)).
		Msg("Prepared clone credentials for worker")

	// Issue one-time token.
	token, err := e.tokenStore.IssueToken(
		analysis.ID,
		packages,
		effectiveModel,
		proxyURL,
		analysis.CustomPrompt,
		analysisCtx,
		10*time.Minute,
		agentProvider,
		extLLMProxyURL,
		"", // no direct key in process mode — worker reaches SWAMP proxy on localhost
		extLLMAnalysisModel,
		extLLMPoCModel,
		gitCloneCreds,
	)
	if err != nil {
		e.failAnalysis(analysis.ID, "Failed to issue worker token", err)
		cancel()
		<-e.countsem
		return
	}

	// Resolve the binary path. Use the current executable so it works with
	// air-rebuilt binaries.
	binary, err := os.Executable()
	if err != nil {
		e.failAnalysis(analysis.ID, "Failed to resolve executable path", err)
		cancel()
		<-e.countsem
		return
	}

	// Create a work directory for this analysis.
	workDir := filepath.Join(os.TempDir(), fmt.Sprintf("swamp-worker-%s", analysis.ID[:8]))
	if err := os.MkdirAll(workDir, 0750); err != nil {
		e.failAnalysis(analysis.ID, "Failed to create work directory", err)
		cancel()
		<-e.countsem
		return
	}

	// Create the lock/state file. The child will flock(LOCK_EX) it on startup.
	lockPath := e.statePath(analysis.ID)
	stateData, _ := json.Marshal(&processState{
		Analysis:  analysis.ID,
		StartedAt: time.Now(),
	})
	if err := os.WriteFile(lockPath, stateData, 0600); err != nil {
		e.failAnalysis(analysis.ID, "Failed to create lock file", err)
		cancel()
		<-e.countsem
		return
	}

	// Build worker environment. Only pass the minimum required vars.
	// SWAMP_WORKER_LOCK_FILE tells the child which file to flock.
	// Use internalBase (localhost:AppPort) so the worker talks directly to
	// the Go backend. In dev this bypasses the Next.js rewrite proxy
	// (which has a ~30s timeout). In production the embedded frontend is
	// served by the same Go process, so localhost:AppPort is still correct.
	env := []string{
		"SWAMP_WORKER_MODE=true",
		fmt.Sprintf("SWAMP_WORKER_TOKEN=%s", token),
		fmt.Sprintf("SWAMP_WORKER_SERVER=%s", internalBase),
		fmt.Sprintf("SWAMP_WORKER_ANALYSIS=%s", analysis.ID),
		fmt.Sprintf("SWAMP_WORKER_LOCK_FILE=%s", lockPath),
		fmt.Sprintf("AGENT_BINARY=%s", e.cfg.AgentBinary),
		fmt.Sprintf("OPENCODE_BINARY=%s", e.cfg.OpenCodeBinary),
		fmt.Sprintf("MAX_ANALYSIS_DURATION=%s", e.cfg.MaxAnalysisDuration.String()),
		fmt.Sprintf("HOME=%s", workDir),
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
		fmt.Sprintf("SHELL=%s", resolveShell()),
		"TERM=xterm-256color",
	}

	cmd := exec.Command(binary)
	cmd.Dir = workDir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create a new session — survives parent death.
	}

	// Inherit the server's stdout/stderr so worker output is interleaved
	// with server logs, matching the behaviour of local mode.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		e.failAnalysis(analysis.ID, "Failed to start worker process", err)
		cancel()
		<-e.countsem
		return
	}

	pid := cmd.Process.Pid

	// Update the lock file with the actual PID.
	stateData, _ = json.Marshal(&processState{
		PID:       pid,
		Analysis:  analysis.ID,
		StartedAt: time.Now(),
	})
	// Best-effort write; the child already has the lock.
	_ = os.WriteFile(lockPath, stateData, 0600)

	if err := e.queries.SetAnalysisStarted(ctx, analysis.ID); err != nil {
		log.Error().Err(err).Str("analysis_id", analysis.ID).Msg("Failed to mark analysis started")
	}

	state := &processState{
		PID:       pid,
		Analysis:  analysis.ID,
		StartedAt: time.Now(),
		cancel:    cancel,
	}

	e.mu.Lock()
	e.running[analysis.ID] = state
	e.mu.Unlock()

	e.hub.Broadcast(analysis.ID, []byte(fmt.Sprintf("[system] Worker process started (PID %d)", pid)))
	log.Info().
		Str("analysis_id", analysis.ID).
		Int("pid", pid).
		Str("work_dir", workDir).
		Msg("Launched detached worker process")

	// Wait for the child in background to avoid zombies.
	go e.waitProcess(ctx, cancel, cmd, analysis.ID, pid)
}

// waitProcess waits for the child to exit and cleans up.
func (e *ProcessExecutor) waitProcess(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, analysisID string, pid int) {
	defer func() {
		cancel()
		<-e.countsem
		e.removeState(analysisID)
		e.mu.Lock()
		delete(e.running, analysisID)
		e.mu.Unlock()
		e.tokenStore.RevokeAnalysis(analysisID)
	}()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			log.Warn().Err(err).Int("pid", pid).Str("analysis_id", analysisID).Msg("Worker process exited with error")
			// If the analysis is still running/pending in the DB, the worker
			// crashed before it could report its own status.
			checkCtx, checkCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer checkCancel()
			a, getErr := e.queries.GetAnalysis(checkCtx, analysisID)
			if getErr == nil && (a.Status == "running" || a.Status == "pending") {
				e.failAnalysis(analysisID, fmt.Sprintf("Worker process crashed (exit status: %v)", err), nil)
			}
		} else {
			log.Info().Int("pid", pid).Str("analysis_id", analysisID).Msg("Worker process completed")
		}
	case <-ctx.Done():
		log.Info().Int("pid", pid).Str("analysis_id", analysisID).Msg("Analysis timed out, sending SIGTERM for graceful shutdown")
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		// Give the worker up to 120s to save progress and upload partial results.
		select {
		case <-done:
			log.Info().Int("pid", pid).Str("analysis_id", analysisID).Msg("Worker exited gracefully after timeout")
		case <-time.After(120 * time.Second):
			log.Warn().Int("pid", pid).Str("analysis_id", analysisID).Msg("Worker did not exit within grace period, sending SIGKILL")
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			<-done
		}
		// Only mark failed if the worker didn't already report its own status.
		e.failAnalysis(analysisID, "Analysis timed out", nil)
	}
}

// removeState deletes the lock/state file for an analysis.
func (e *ProcessExecutor) removeState(analysisID string) {
	_ = os.Remove(e.statePath(analysisID))
}

// lockFileHeld attempts a non-blocking flock on the given path.
// Returns true if the file is locked by another process (child is alive).
func lockFileHeld(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	// Try a non-blocking exclusive lock.
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// EWOULDBLOCK (or EAGAIN) means the file is locked by the child.
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return true
		}
		return false
	}
	// We acquired the lock — child is dead. Release it.
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// reconcileExisting scans the state directory for worker lock files and
// re-tracks processes that are still alive. Uses flock to detect liveness
// without PID-reuse races.
func (e *ProcessExecutor) reconcileExisting(ctx context.Context) {
	entries, err := os.ReadDir(e.stateDir)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read process state dir")
		return
	}

	tracked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}

		path := filepath.Join(e.stateDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var state processState
		if err := json.Unmarshal(data, &state); err != nil {
			log.Warn().Str("file", entry.Name()).Msg("Invalid process state file, removing")
			_ = os.Remove(path)
			continue
		}

		// Check if the child still holds the lock.
		if !lockFileHeld(path) {
			log.Info().
				Int("pid", state.PID).
				Str("analysis_id", state.Analysis).
				Msg("Worker lock file not held, cleaning up")
			_ = os.Remove(path)

			// Only fail analyses that were already running. Pending analyses may
			// still be queued behind concurrency limits and should remain pending.
			a, getErr := e.queries.GetAnalysis(ctx, state.Analysis)
			if getErr == nil && a.Status == "running" {
				_ = e.queries.SetAnalysisCompleted(ctx, state.Analysis, "failed", "Worker process exited unexpectedly")
			}
			continue
		}

		log.Info().
			Int("pid", state.PID).
			Str("analysis_id", state.Analysis).
			Msg("Reconciling running worker process (lock held)")

		_, cancelFunc := context.WithTimeout(ctx, e.cfg.MaxAnalysisDuration)
		stateCopy := state
		stateCopy.cancel = cancelFunc

		e.mu.Lock()
		e.running[state.Analysis] = &stateCopy
		e.mu.Unlock()

		// Monitor this process by polling the lock.
		go e.monitorProcess(ctx, cancelFunc, state.Analysis, state.PID)
		tracked++
	}

	log.Info().Int("tracked", tracked).Msg("Process executor reconciliation complete")
}

// monitorProcess polls a process (found during reconciliation) until its
// lock file is released. Uses flock rather than kill(pid,0) for safety.
func (e *ProcessExecutor) monitorProcess(ctx context.Context, cancel context.CancelFunc, analysisID string, pid int) {
	defer func() {
		cancel()
		e.removeState(analysisID)
		e.mu.Lock()
		delete(e.running, analysisID)
		e.mu.Unlock()
		e.tokenStore.RevokeAnalysis(analysisID)
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Int("pid", pid).Str("analysis_id", analysisID).Msg("Analysis timed out, sending SIGTERM for graceful shutdown")
			if pid > 0 {
				_ = syscall.Kill(-pid, syscall.SIGTERM)
				// Give the worker up to 120s to save progress.
				for i := 0; i < 24; i++ {
					time.Sleep(5 * time.Second)
					if !lockFileHeld(e.statePath(analysisID)) {
						log.Info().Int("pid", pid).Str("analysis_id", analysisID).Msg("Worker exited gracefully after timeout")
						break
					}
				}
				if lockFileHeld(e.statePath(analysisID)) {
					log.Warn().Int("pid", pid).Str("analysis_id", analysisID).Msg("Worker did not exit within grace period, sending SIGKILL")
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
			}
			// Only mark failed if the worker didn't already report its own status.
			e.failAnalysis(analysisID, "Analysis timed out", nil)
			return
		case <-ticker.C:
			if !lockFileHeld(e.statePath(analysisID)) {
				log.Info().Int("pid", pid).Str("analysis_id", analysisID).Msg("Monitored worker lock released")
				return
			}
		}
	}
}

// syncLoop periodically cleans up expired tokens.
func (e *ProcessExecutor) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.tokenStore.CleanupExpired()
		}
	}
}

// failAnalysis marks an analysis as failed.
func (e *ProcessExecutor) failAnalysis(analysisID, detail string, err error) {
	log.Error().Err(err).Str("analysis_id", analysisID).Str("detail", detail).Msg("Analysis failed")
	dbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, getErr := e.queries.GetAnalysis(dbCtx, analysisID)
	if getErr == nil && (a.Status == "cancelled" || a.Status == "completed" || a.Status == "timed_out") {
		// Worker already reported a terminal status; don't overwrite it.
		return
	}
	_ = e.queries.SetAnalysisCompleted(dbCtx, analysisID, "failed", detail)
	e.hub.Broadcast(analysisID, []byte("[system] Analysis failed: "+detail))
}

// AcquireWorkerLock is called by the worker process (child) to hold an
// exclusive flock on its lock file for its entire lifetime. The returned
// file must be kept open (not closed) until the worker exits. The OS
// releases the lock automatically when the process dies.
func AcquireWorkerLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock: %w", err)
	}
	return f, nil
}
