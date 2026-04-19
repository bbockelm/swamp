package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/internal/storage"
	"github.com/bbockelm/swamp/internal/ws"
)

// Executor manages running analysis agents.
type Executor struct {
	cfg       *config.Config
	queries   *db.Queries
	store     *storage.Store
	hub       *ws.Hub
	encryptor *crypto.Encryptor

	mu       sync.Mutex
	running  map[string]context.CancelFunc // analysisID → cancel func
	countsem chan struct{}                 // semaphore for max concurrent analyses
	wg       sync.WaitGroup                // tracks in-flight run() goroutines

	stopSync context.CancelFunc // stops the periodic sync loop
	ghInteg  GitHubIntegration  // optional GitHub App integration
}

// NewExecutor creates a new Executor.
func NewExecutor(cfg *config.Config, queries *db.Queries, store *storage.Store, hub *ws.Hub, enc *crypto.Encryptor) *Executor {
	return &Executor{
		cfg:       cfg,
		queries:   queries,
		store:     store,
		hub:       hub,
		encryptor: enc,
		running:   make(map[string]context.CancelFunc),
		countsem:  make(chan struct{}, cfg.MaxConcurrentAnalyses),
	}
}

// CanPersist reports whether the executor can persist running jobs across
// server restarts. The basic fork/exec executor cannot; future HTCondor-based
// executors may return true.
func (e *Executor) CanPersist() bool {
	return false
}

// SetGitHubIntegration sets the optional GitHub App integration.
func (e *Executor) SetGitHubIntegration(gh GitHubIntegration) {
	e.ghInteg = gh
}

// Start performs startup reconciliation and begins periodic sync.
// For non-persistent executors, this marks any pending/running analyses as
// failed since the processes were lost on restart.
func (e *Executor) Start(ctx context.Context) {
	if !e.CanPersist() {
		n, err := e.queries.MarkStaleAnalyses(ctx)
		if err != nil {
			log.Error().Err(err).Msg("Failed to mark stale analyses on startup")
		} else if n > 0 {
			log.Warn().Int64("count", n).Msg("Marked stale analyses as failed after restart")
		}
	}

	syncCtx, cancel := context.WithCancel(ctx)
	e.stopSync = cancel
	go e.syncLoop(syncCtx)
}

// Shutdown cancels all running analyses and waits for their goroutines to
// finish uploading results. It blocks until all in-flight goroutines exit
// or the supplied context expires.
func (e *Executor) Shutdown(ctx context.Context) {
	// Stop the periodic sync goroutine.
	if e.stopSync != nil {
		e.stopSync()
	}

	if !e.CanPersist() {
		// Cancel the agent processes so they stop quickly.
		e.mu.Lock()
		for id, cancel := range e.running {
			log.Info().Str("analysis_id", id).Msg("Shutdown: cancelling running analysis")
			cancel()
		}
		e.mu.Unlock()

		// Wait for goroutines to finish their deferred uploads.
		done := make(chan struct{})
		go func() {
			e.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			log.Info().Msg("Shutdown: all analysis goroutines finished")
		case <-ctx.Done():
			log.Warn().Msg("Shutdown: timed out waiting for analysis goroutines")
		}
	}
}

// IsRunning reports whether the executor is currently tracking the given
// analysis as an active process. This lets the frontend distinguish between
// "DB says running" and "actually running right now in this process".
func (e *Executor) IsRunning(analysisID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.running[analysisID]
	return ok
}

// syncLoop periodically checks for analyses that the DB thinks are running
// but this executor is not actually tracking, and marks them as failed.
func (e *Executor) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.syncOnce(ctx)
		}
	}
}

// syncOnce checks for analyses that are running/pending in the DB but not
// tracked by this executor and marks them failed.
func (e *Executor) syncOnce(ctx context.Context) {
	if e.CanPersist() {
		return
	}
	analyses, err := e.queries.ListActiveAnalyses(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Sync: failed to list active analyses")
		return
	}
	e.mu.Lock()
	runningSet := make(map[string]bool, len(e.running))
	for id := range e.running {
		runningSet[id] = true
	}
	e.mu.Unlock()

	for _, a := range analyses {
		if !runningSet[a.ID] {
			log.Warn().Str("analysis_id", a.ID).Str("status", a.Status).
				Msg("Sync: marking orphaned analysis as failed")
			_ = e.queries.SetAnalysisCompleted(ctx, a.ID, "failed",
				"Analysis process not found (server may have restarted)")
		}
	}
}

// Submit queues an analysis for execution. It runs asynchronously.
func (e *Executor) Submit(analysis *models.Analysis, packages []models.SoftwarePackage) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.run(analysis, packages)
	}()
}

// Cancel stops a running analysis.
func (e *Executor) Cancel(analysisID string) {
	e.mu.Lock()
	cancel, ok := e.running[analysisID]
	e.mu.Unlock()
	if ok {
		cancel()
	}
}

// AgentReady returns true if the agent binary is available and an API key is configured.
func (e *Executor) AgentReady() bool {
	// Check that the agent binary exists on PATH (or is an absolute path).
	if _, err := exec.LookPath(e.cfg.AgentBinary); err != nil {
		return false
	}
	return true
}

// run executes the analysis workflow.
func (e *Executor) run(analysis *models.Analysis, packages []models.SoftwarePackage) {
	// Broadcast queued status while waiting for semaphore.
	e.hub.Broadcast(analysis.ID, []byte("[system] Analysis queued, waiting for available slot..."))

	// Acquire semaphore slot.
	e.countsem <- struct{}{}
	defer func() { <-e.countsem }()

	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.MaxAnalysisDuration)
	defer cancel()

	e.mu.Lock()
	e.running[analysis.ID] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, analysis.ID)
		e.mu.Unlock()
	}()

	// Mark analysis as running.
	if err := e.queries.SetAnalysisStarted(ctx, analysis.ID); err != nil {
		log.Error().Err(err).Str("analysis_id", analysis.ID).Msg("Failed to mark analysis started")
		return
	}

	// Create working directory.
	workDir := filepath.Join(os.TempDir(), fmt.Sprintf("swamp-analysis-%s", analysis.ID))
	outputDir := filepath.Join(workDir, "output")
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		e.failAnalysis(ctx, analysis.ID, "Failed to create working directory", err)
		return
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	// Always upload log files from the output directory, even on failure.
	// Use a separate context so uploads survive analysis cancellation / shutdown.
	defer func() {
		uploadCtx, uploadCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer uploadCancel()
		e.uploadOutputDir(uploadCtx, outputDir, analysis.ID)
		e.hub.CloseRoom(analysis.ID)
	}()

	// Gather context from prior analyses for this project.
	analysisCtx := e.gatherAnalysisContext(ctx, analysis.ProjectID, packages)

	// Resolve effective LLM configuration (global defaults + per-project overrides).
	var project *models.Project
	if e.queries != nil && analysis.ProjectID != "" {
		project, _ = e.queries.GetProject(ctx, analysis.ProjectID)
	}
	llmConfig := ResolveEffectiveLLMConfig(e.cfg, project)

	// If GitHub integration is configured, pre-clone repos so that
	// credentials are never exposed in the agent prompt.
	// For single-package analyses with package-level GitHub config, use that;
	// otherwise fall back to project-level config.
	var preClonedPath string
	if e.ghInteg != nil && len(packages) == 1 {
		pkg := &packages[0]
		cred, err := e.ghInteg.CloneCredentialForPackage(ctx, pkg)
		if err != nil {
			log.Warn().Err(err).Str("analysis_id", analysis.ID).Msg("GitHub clone credential failed, falling back to agent clone")
		} else if cred != nil {
			localPath, cloneErr := SecureGitClone(ctx, cred, workDir)
			// Zero the token in memory immediately after use.
			cred.Token = ""
			if cloneErr != nil {
				log.Warn().Err(cloneErr).Str("analysis_id", analysis.ID).Msg("GitHub pre-clone failed, falling back to agent clone")
			} else {
				log.Info().Str("analysis_id", analysis.ID).Str("path", localPath).Msg("Pre-cloned repo for private access")
				preClonedPath = localPath
			}
		}
	}

	// Build prompt.
	var prompt string
	if len(packages) == 1 {
		prompt = BuildPrompt(&packages[0], "phase1", analysis.CustomPrompt, analysisCtx, preClonedPath)
	} else {
		prompt = BuildMultiPackagePrompt(packages, analysis.CustomPrompt, analysisCtx, nil)
	}

	// Save the prompt and context as output artifacts so they can be viewed
	// in the analysis detail page after completion.
	_ = os.WriteFile(filepath.Join(outputDir, "prompt.md"), []byte(prompt), 0640)
	if ctxText := formatAnalysisContext(analysisCtx); ctxText != "" {
		_ = os.WriteFile(filepath.Join(outputDir, "context.md"), []byte(ctxText), 0640)
	}

	sarifPath := filepath.Join(outputDir, "results.sarif")

	// Try new provider-based resolution first (from analysis agent_config).
	resolvedProvider, err := ResolveAnalysisProvider(ctx, e.queries, e.encryptor, e.cfg, analysis)
	if err != nil {
		e.failAnalysis(ctx, analysis.ID, "Provider resolution failed", err)
		return
	}

	if resolvedProvider != nil {
		// New provider-based execution path.
		switch resolvedProvider.APISchema {
		case "openai":
			if err := e.updateStatus(ctx, analysis.ID, "running", "Phase 1: Security analysis"); err != nil {
				return
			}
			e.hub.Broadcast(analysis.ID, []byte("[system] Starting Phase 1: Security analysis ("+resolvedProvider.APISchema+")"))
			phase1Err := e.runOpenCodeAgent(ctx, workDir, prompt, analysis.ID, resolvedProvider.BaseURL, resolvedProvider.APIKey, resolvedProvider.Model)
			if phase1Err != nil {
				e.failAnalysis(ctx, analysis.ID, "Agent execution failed (Phase 1)", phase1Err)
				return
			}
			// Detect error-only output (e.g. API routing error, invalid model).
			stdoutLog := filepath.Join(workDir, "output", "agent_stdout.log")
			if fatalErr := checkOpenCodeFatalError(stdoutLog); fatalErr != "" {
				e.failAnalysis(ctx, analysis.ID, "Agent failed: "+fatalErr, nil)
				return
			}
			if _, statErr := os.Stat(sarifPath); statErr == nil {
				if err := e.updateStatus(ctx, analysis.ID, "running", "Phase 2: Exploit validation"); err != nil {
					return
				}
				e.hub.Broadcast(analysis.ID, []byte("[system] Starting Phase 2: Exploit validation"))
				phase2Prompt := BuildPrompt(&packages[0], "phase2", "", nil, "")
				if err := e.runOpenCodeAgent(ctx, workDir, phase2Prompt, analysis.ID, resolvedProvider.BaseURL, resolvedProvider.APIKey, resolvedProvider.Model); err != nil {
					log.Warn().Err(err).Str("analysis_id", analysis.ID).Msg("Phase 2 (exploit validation) failed")
				}
			}
		default: // "anthropic"
			if err := e.runAnthropicPhasesWithKey(ctx, workDir, prompt, sarifPath, packages, analysis, resolvedProvider.APIKey, resolvedProvider.Model); err != nil {
				e.failAnalysis(ctx, analysis.ID, "Agent execution failed", err)
				return
			}
		}
		// Mark completed for new provider-based path.
		completeCtx, completeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer completeCancel()
		if err := e.queries.SetAnalysisCompleted(completeCtx, analysis.ID, "completed", ""); err != nil {
			log.Error().Err(err).Str("analysis_id", analysis.ID).Msg("Failed to mark analysis completed")
		}
		e.hub.Broadcast(analysis.ID, []byte("[system] Analysis complete"))
		return
	}

	// Legacy provider resolution path.
	switch llmConfig.Provider {
	case "external":
		extCreds, err := resolveExternalLLMDirect(ctx, e.queries, e.encryptor, e.cfg, analysis)
		if err != nil {
			if llmConfig.Fallback == "anthropic" {
				log.Warn().Err(err).Str("analysis_id", analysis.ID).Msg("External LLM credential resolution failed, falling back to Anthropic")
				if err := e.runAnthropicPhases(ctx, workDir, prompt, sarifPath, packages, analysis); err != nil {
					e.failAnalysis(ctx, analysis.ID, "Agent execution failed (Anthropic fallback)", err)
				}
				return
			}
			e.failAnalysis(ctx, analysis.ID, "External LLM credential resolution failed", err)
			return
		}

		// Phase 1: Security analysis.
		if err := e.updateStatus(ctx, analysis.ID, "running", "Phase 1: Security analysis"); err != nil {
			return
		}
		e.hub.Broadcast(analysis.ID, []byte("[system] Starting Phase 1: Security analysis (external LLM)"))
		phase1Err := e.runOpenCodeAgent(ctx, workDir, prompt, analysis.ID, extCreds.EndpointURL, extCreds.APIKey, llmConfig.AnalysisModel)
		if phase1Err != nil {
			if llmConfig.Fallback == "anthropic" {
				log.Warn().Err(phase1Err).Str("analysis_id", analysis.ID).Msg("External LLM Phase 1 failed, falling back to Anthropic")
				if err := e.runAnthropicPhases(ctx, workDir, prompt, sarifPath, packages, analysis); err != nil {
					e.failAnalysis(ctx, analysis.ID, "Agent execution failed (Anthropic fallback)", err)
				}
				return
			}
			e.failAnalysis(ctx, analysis.ID, "Agent execution failed (Phase 1)", phase1Err)
			return
		}
		// Detect error-only output (e.g. API routing error, invalid model).
		legacyStdoutLog := filepath.Join(workDir, "output", "agent_stdout.log")
		if fatalErr := checkOpenCodeFatalError(legacyStdoutLog); fatalErr != "" {
			if llmConfig.Fallback == "anthropic" {
				log.Warn().Str("analysis_id", analysis.ID).Str("error", fatalErr).Msg("External LLM emitted only errors, falling back to Anthropic")
				if err := e.runAnthropicPhases(ctx, workDir, prompt, sarifPath, packages, analysis); err != nil {
					e.failAnalysis(ctx, analysis.ID, "Agent execution failed (Anthropic fallback)", err)
				}
				return
			}
			e.failAnalysis(ctx, analysis.ID, "Agent failed: "+fatalErr, nil)
			return
		}

		// Phase 2: Exploit validation.
		if _, err := os.Stat(sarifPath); err == nil {
			if err := e.updateStatus(ctx, analysis.ID, "running", "Phase 2: Exploit validation"); err != nil {
				return
			}
			e.hub.Broadcast(analysis.ID, []byte("[system] Starting Phase 2: Exploit validation (external LLM)"))
			phase2Prompt := BuildPrompt(&packages[0], "phase2", "", nil, "")
			if err := e.runOpenCodeAgent(ctx, workDir, phase2Prompt, analysis.ID, extCreds.EndpointURL, extCreds.APIKey, llmConfig.PoCModel); err != nil {
				log.Warn().Err(err).Str("analysis_id", analysis.ID).Msg("Phase 2 (exploit validation) failed")
			}
		}

	default: // "anthropic"
		if err := e.runAnthropicPhases(ctx, workDir, prompt, sarifPath, packages, analysis); err != nil {
			e.failAnalysis(ctx, analysis.ID, "Agent execution failed", err)
			return
		}
	}

	// Mark completed (use a fresh context in case the original expired during Phase 2).
	completeCtx, completeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer completeCancel()
	if err := e.queries.SetAnalysisCompleted(completeCtx, analysis.ID, "completed", ""); err != nil {
		log.Error().Err(err).Str("analysis_id", analysis.ID).Msg("Failed to mark analysis completed")
	}
	e.hub.Broadcast(analysis.ID, []byte("[system] Analysis complete"))
	// Log upload and room cleanup handled by deferred uploadOutputDir + CloseRoom.
}

// runAnthropicPhases runs both analysis phases using the Anthropic/Claude backend.
// Used directly when provider is "anthropic" and also as a fallback when the
// external LLM fails and Fallback == "anthropic".
func (e *Executor) runAnthropicPhases(ctx context.Context, workDir, prompt, sarifPath string, packages []models.SoftwarePackage, analysis *models.Analysis) error {
	anthropicKey, err := resolveAnthropicAPIKey(ctx, e.queries, e.encryptor, e.cfg, analysis)
	if err != nil {
		return err
	}
	_ = e.updateStatus(ctx, analysis.ID, "running", "Phase 1: Security analysis (Anthropic)")
	e.hub.Broadcast(analysis.ID, []byte("[system] Starting Phase 1: Security analysis (Anthropic)"))
	if err := e.runAgent(ctx, workDir, prompt, analysis.ID, anthropicKey, analysis.AgentModel); err != nil {
		return err
	}
	if _, statErr := os.Stat(sarifPath); statErr == nil {
		_ = e.updateStatus(ctx, analysis.ID, "running", "Phase 2: Exploit validation (Anthropic)")
		e.hub.Broadcast(analysis.ID, []byte("[system] Starting Phase 2: Exploit validation (Anthropic)"))
		phase2Prompt := BuildPrompt(&packages[0], "phase2", "", nil, "")
		if err := e.runAgent(ctx, workDir, phase2Prompt, analysis.ID, anthropicKey, analysis.AgentModel); err != nil {
			log.Warn().Err(err).Str("analysis_id", analysis.ID).Msg("Phase 2 (exploit validation) failed")
		}
	}
	return nil
}

// runAnthropicPhasesWithKey runs both analysis phases using the Anthropic CLI
// with an explicitly provided API key, skipping the legacy key resolution.
func (e *Executor) runAnthropicPhasesWithKey(ctx context.Context, workDir, prompt, sarifPath string, packages []models.SoftwarePackage, analysis *models.Analysis, apiKey, model string) error {
	if model == "" {
		model = analysis.AgentModel
	}
	_ = e.updateStatus(ctx, analysis.ID, "running", "Phase 1: Security analysis (Anthropic)")
	e.hub.Broadcast(analysis.ID, []byte("[system] Starting Phase 1: Security analysis (Anthropic)"))
	if err := e.runAgent(ctx, workDir, prompt, analysis.ID, apiKey, model); err != nil {
		return err
	}
	if _, statErr := os.Stat(sarifPath); statErr == nil {
		_ = e.updateStatus(ctx, analysis.ID, "running", "Phase 2: Exploit validation (Anthropic)")
		e.hub.Broadcast(analysis.ID, []byte("[system] Starting Phase 2: Exploit validation (Anthropic)"))
		phase2Prompt := BuildPrompt(&packages[0], "phase2", "", nil, "")
		if err := e.runAgent(ctx, workDir, phase2Prompt, analysis.ID, apiKey, model); err != nil {
			log.Warn().Err(err).Str("analysis_id", analysis.ID).Msg("Phase 2 (exploit validation) failed")
		}
	}
	return nil
}

// runAgent executes the claude CLI with the given prompt.
func (e *Executor) runAgent(ctx context.Context, workDir, prompt string, analysisID string, anthropicKey string, agentModel string) error {
	// Write prompt to temp file.
	promptFile := filepath.Join(workDir, "prompt.txt")
	if err := os.WriteFile(promptFile, []byte(prompt), 0640); err != nil {
		return fmt.Errorf("write prompt file: %w", err)
	}

	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--max-turns", "50",
		"--verbose",
		"--dangerously-skip-permissions",
	}
	// Use per-analysis model if set, otherwise fall back to global config.
	model := agentModel
	if model == "" {
		model = e.cfg.AgentModel
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, e.cfg.AgentBinary, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("HOME=%s", workDir), // Isolate agent home
		"SHELL=/bin/bash",               // Claude CLI requires a POSIX shell
	)

	cmd.Env = append(cmd.Env, fmt.Sprintf("ANTHROPIC_API_KEY=%s", anthropicKey))

	// Capture stdout/stderr to log files and broadcast via WebSocket.
	stdoutFile, err := os.OpenFile(filepath.Join(workDir, "output", "agent_stdout.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return fmt.Errorf("create stdout log: %w", err)
	}
	defer func() { _ = stdoutFile.Close() }()

	stderrFile, err := os.OpenFile(filepath.Join(workDir, "output", "agent_stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return fmt.Errorf("create stderr log: %w", err)
	}
	defer func() { _ = stderrFile.Close() }()

	// Pipe stdout through a broadcaster so WS clients see output live.
	stdoutPR, stdoutPW := io.Pipe()
	stderrPR, stderrPW := io.Pipe()
	cmd.Stdout = io.MultiWriter(stdoutFile, stdoutPW)
	cmd.Stderr = io.MultiWriter(stderrFile, stderrPW)

	// Stream lines to WebSocket hub in background.
	var wg sync.WaitGroup

	// Parse stream-json on stdout and broadcast readable content.
	streamJSON := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			msg := extractStreamMessage(line)
			if msg != "" {
				e.hub.Broadcast(analysisID, []byte(msg))
			}
		}
	}

	streamStderr := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
		for scanner.Scan() {
			e.hub.Broadcast(analysisID, []byte("[stderr] "+scanner.Text()))
		}
	}

	wg.Add(2)
	go streamJSON(stdoutPR)
	go streamStderr(stderrPR)

	log.Info().
		Str("binary", e.cfg.AgentBinary).
		Str("work_dir", workDir).
		Msg("Starting agent process")

	startTime := time.Now()
	err = cmd.Run()
	// Close write ends so the streaming goroutines finish.
	_ = stdoutPW.Close()
	_ = stderrPW.Close()
	wg.Wait()
	duration := time.Since(startTime)

	log.Info().
		Str("work_dir", workDir).
		Dur("duration", duration).
		Err(err).
		Msg("Agent process finished")

	return err
}

// failAnalysis marks an analysis as failed, unless it already has a terminal status.
func (e *Executor) failAnalysis(ctx context.Context, analysisID, detail string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	log.Error().Err(err).Str("analysis_id", analysisID).Str("detail", detail).Msg("Analysis failed")
	// Use a fresh context in case the original was cancelled (e.g. shutdown).
	dbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Don't overwrite terminal statuses (cancelled, completed, timed_out).
	a, getErr := e.queries.GetAnalysis(dbCtx, analysisID)
	if getErr == nil && (a.Status == "cancelled" || a.Status == "completed" || a.Status == "timed_out") {
		log.Info().Str("analysis_id", analysisID).Str("status", a.Status).Msg("Analysis already in terminal status, not overwriting")
	} else {
		_ = e.queries.SetAnalysisCompleted(dbCtx, analysisID, "failed", errMsg)
	}
	e.hub.Broadcast(analysisID, []byte("[system] Analysis failed: "+detail))
	// CloseRoom and log upload handled by deferred functions in run().
}

// updateStatus updates the analysis status detail.
func (e *Executor) updateStatus(ctx context.Context, analysisID, status, detail string) error {
	return e.queries.UpdateAnalysisStatus(ctx, analysisID, status, detail, "")
}

// gatherAnalysisContext collects prior findings, annotations, and notes from
// recent completed analyses to inject into the prompt as context.
func (e *Executor) gatherAnalysisContext(ctx context.Context, projectID string, packages []models.SoftwarePackage) *models.AnalysisContext {
	return gatherAnalysisContext(ctx, e.queries, e.encryptor, e.store, projectID, packages)
}

// uploadOutputDir parses the output directory, encrypts each file with the
// analysis DEK, uploads to S3, and records the result in the database.
func (e *Executor) uploadOutputDir(ctx context.Context, outputDir, analysisID string) {
	// Retrieve the analysis to get its DEK.
	analysis, err := e.queries.GetAnalysis(ctx, analysisID)
	if err != nil {
		log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to load analysis for result upload")
		return
	}
	if len(analysis.EncryptedDEK) == 0 {
		log.Error().Str("analysis_id", analysisID).Msg("Analysis has no DEK, cannot encrypt results")
		return
	}
	dek, err := e.encryptor.UnwrapDEK(analysis.EncryptedDEK, analysis.DEKNonce)
	if err != nil {
		log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to unwrap analysis DEK")
		return
	}

	// Load packages for per-package SARIF file matching.
	var packages []models.SoftwarePackage
	if pkgs, pkgErr := e.queries.ListAnalysisPackages(ctx, analysisID); pkgErr == nil {
		packages = pkgs
	}
	pkgByID := make(map[string]models.SoftwarePackage, len(packages))
	for _, p := range packages {
		pkgByID[p.ID] = p
	}

	results, err := ParseOutputDir(outputDir, analysisID, analysis.ProjectID, packages)
	if err != nil {
		log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to parse output dir")
		return
	}

	// Store the resolved git commit SHA on the analysis record.
	if results.GitCommit != "" {
		if err := e.queries.SetAnalysisGitCommit(ctx, analysisID, results.GitCommit); err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to store git commit on analysis")
		} else {
			log.Info().Str("analysis_id", analysisID).Str("git_commit", results.GitCommit).Msg("Recorded analysis git commit")
		}
		// Propagate to findings.
		for i := range results.Findings {
			results.Findings[i].GitCommit = results.GitCommit
		}
	}

	for i := range results.Results {
		localPath := filepath.Join(outputDir, results.Results[i].Filename)
		s3Key := fmt.Sprintf("analyses/%s/%s", analysisID, results.Results[i].Filename)
		results.Results[i].S3Key = s3Key

		plaintext, err := os.ReadFile(localPath)
		if err != nil {
			log.Error().Err(err).Str("file", localPath).Msg("Failed to read result file")
			continue
		}
		results.Results[i].FileSize = int64(len(plaintext))

		ciphertext, err := crypto.Encrypt(dek, plaintext)
		if err != nil {
			log.Error().Err(err).Str("file", localPath).Msg("Failed to encrypt result file")
			continue
		}

		if err := e.store.Upload(ctx, s3Key, bytes.NewReader(ciphertext), int64(len(ciphertext)), "application/octet-stream"); err != nil {
			log.Error().Err(err).Str("s3_key", s3Key).Msg("Failed to upload encrypted result")
			continue
		}
		if err := e.queries.CreateAnalysisResult(ctx, &results.Results[i]); err != nil {
			log.Error().Err(err).Msg("Failed to save result record")
			continue
		}

		if results.Results[i].ResultType == "sarif" {
			attempted := false
			uploadURL := ""
			uploadErrMsg := ""

			if e.ghInteg != nil {
				if results.Results[i].PackageID != nil {
					if pkg, ok := pkgByID[*results.Results[i].PackageID]; ok && pkg.SARIFUploadEnabled && pkg.GitHubOwner != "" && pkg.GitHubRepo != "" && pkg.InstallationID != 0 {
						attempted = true
						log.Info().
							Str("analysis_id", analysisID).
							Str("result_id", results.Results[i].ID).
							Str("package_id", pkg.ID).
							Str("package", pkg.Name).
							Msg("Attempting GitHub SARIF upload for package result")
						url, upErr := e.ghInteg.UploadSARIFForPackage(ctx, &pkg, plaintext)
						if upErr != nil {
							uploadErrMsg = upErr.Error()
							log.Warn().Err(upErr).
								Str("analysis_id", analysisID).
								Str("result_id", results.Results[i].ID).
								Str("package_id", pkg.ID).
								Msg("GitHub SARIF upload failed for package result")
						} else {
							uploadURL = url
						}
					}
				}

				if !attempted {
					if ghCfg, cfgErr := e.queries.GetProjectGitHubConfig(ctx, analysis.ProjectID); cfgErr == nil && ghCfg.SARIFUploadEnabled && ghCfg.InstallationID != 0 {
						attempted = true
						log.Info().
							Str("analysis_id", analysisID).
							Str("result_id", results.Results[i].ID).
							Msg("Attempting GitHub SARIF upload via project-level config")
						url, upErr := e.ghInteg.UploadSARIFForProject(ctx, analysis.ProjectID, plaintext)
						if upErr != nil {
							uploadErrMsg = upErr.Error()
							log.Warn().Err(upErr).
								Str("analysis_id", analysisID).
								Str("result_id", results.Results[i].ID).
								Msg("GitHub SARIF upload failed for project result")
						} else {
							uploadURL = url
						}
					}
				}
			}

			if attempted {
				if uploadURL == "" && uploadErrMsg == "" {
					uploadErrMsg = "Upload attempted but no GitHub alerts URL was returned"
				}
				if err := e.queries.SetResultSARIFUploadStatus(ctx, results.Results[i].ID, true, uploadURL, uploadErrMsg); err != nil {
					log.Warn().Err(err).Str("result_id", results.Results[i].ID).Msg("Failed to persist SARIF upload tracking status")
				}
				if uploadURL != "" {
					if err := e.queries.SetAnalysisSARIFUploadURL(ctx, analysisID, uploadURL); err != nil {
						log.Warn().Err(err).Str("analysis_id", analysisID).Msg("Failed to record analysis SARIF upload URL")
					}
					e.hub.Broadcast(analysisID, []byte("[system] SARIF results uploaded to GitHub"))
				} else {
					e.hub.Broadcast(analysisID, []byte("[error] GitHub SARIF upload failed: "+uploadErrMsg))
				}
			} else {
				log.Info().
					Str("analysis_id", analysisID).
					Str("result_id", results.Results[i].ID).
					Msg("Skipping GitHub SARIF upload: no eligible package or project GitHub config")
			}
		}

		// Link findings to the result record now that we have the result ID.
		for j := range results.Findings {
			// During parse we temporarily use the SARIF filename as ResultID.
			if results.Findings[j].ResultID == results.Results[i].Filename {
				results.Findings[j].ResultID = results.Results[i].ID
			}
		}
	}

	// Store individual findings.
	if len(results.Findings) > 0 {
		if err := e.queries.CreateFindingsBatch(ctx, results.Findings); err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to save findings")
		} else {
			log.Info().Int("count", len(results.Findings)).Str("analysis_id", analysisID).Msg("Saved individual findings")
		}
	}
}

// extractStreamMessage parses a stream-json line from the Claude CLI and
// returns a human-readable string suitable for the WebSocket feed. Returns ""
// for events that should be silently skipped.
//
// Claude CLI stream-json emits events like:
//
//	{"type":"assistant","message":{"content":[{"type":"text","text":"..."},{"type":"tool_use","name":"Bash","input":{"command":"...","description":"..."}}]}}
//	{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"...","is_error":false}]}}
//	{"type":"result","result":"...","subtype":"..."}
func extractStreamMessage(line []byte) string {
	if len(line) == 0 || line[0] != '{' {
		return ""
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return ""
	}

	var eventType string
	if err := json.Unmarshal(raw["type"], &eventType); err != nil {
		return ""
	}

	switch eventType {
	case "assistant":
		return parseAssistantEvent(raw)
	case "user":
		return parseUserEvent(raw)
	case "result":
		return parseResultEvent(raw)
	case "system":
		var msg struct{ Message string }
		if err := json.Unmarshal(line, &msg); err == nil && msg.Message != "" {
			return "[system] " + msg.Message
		}
		return ""
	default:
		return ""
	}
}

// parseAssistantEvent extracts readable output from an assistant stream event.
func parseAssistantEvent(raw map[string]json.RawMessage) string {
	// The content array lives inside .message.content
	var msg struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw["message"], &msg); err != nil || len(msg.Content) == 0 {
		return ""
	}

	var parts []string
	for _, block := range msg.Content {
		var base struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(block, &base) != nil {
			continue
		}
		switch base.Type {
		case "text":
			var tb struct{ Text string }
			if json.Unmarshal(block, &tb) == nil && tb.Text != "" {
				parts = append(parts, tb.Text)
			}
		case "thinking":
			// Show a brief preview of the thinking content.
			var tb struct{ Thinking string }
			if json.Unmarshal(block, &tb) == nil && tb.Thinking != "" {
				t := tb.Thinking
				if len(t) > 200 {
					t = t[:200] + "…"
				}
				parts = append(parts, "[thinking] "+t)
			}
		case "tool_use":
			parts = append(parts, formatToolUse(block))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return ""
}

// formatToolUse renders a tool_use content block as a compact human-readable string.
func formatToolUse(block json.RawMessage) string {
	var tu struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(block, &tu) != nil {
		return "[tool] unknown"
	}

	// Try to extract a useful summary from the input depending on the tool.
	var inputMap map[string]json.RawMessage
	if json.Unmarshal(tu.Input, &inputMap) != nil {
		return fmt.Sprintf("[tool] %s", tu.Name)
	}

	var detail string
	switch tu.Name {
	case "Bash":
		detail = jsonString(inputMap["description"])
		if detail == "" {
			detail = truncate(jsonString(inputMap["command"]), 120)
		}
	case "Read":
		detail = jsonString(inputMap["file_path"])
	case "Write":
		detail = jsonString(inputMap["file_path"])
	case "Edit":
		detail = jsonString(inputMap["file_path"])
	case "Agent":
		detail = jsonString(inputMap["description"])
		if detail == "" {
			detail = truncate(jsonString(inputMap["prompt"]), 120)
		}
	case "WebFetch":
		detail = jsonString(inputMap["url"])
	case "ToolSearch":
		detail = jsonString(inputMap["query"])
	default:
		// For unknown tools, try "description", "file_path", or "command" generically.
		for _, key := range []string{"description", "file_path", "command", "query", "url"} {
			if v, ok := inputMap[key]; ok {
				detail = truncate(jsonString(v), 120)
				if detail != "" {
					break
				}
			}
		}
	}

	if detail != "" {
		return fmt.Sprintf("[tool] %s: %s", tu.Name, detail)
	}
	return fmt.Sprintf("[tool] %s", tu.Name)
}

// parseUserEvent extracts brief output from user/tool_result events.
func parseUserEvent(raw map[string]json.RawMessage) string {
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw["message"], &msg) != nil {
		return ""
	}

	// Content may be an array of tool_result objects.
	var blocks []struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return ""
	}

	for _, b := range blocks {
		if b.Type == "tool_result" {
			summary := truncate(b.Content, 200)
			if b.IsError {
				return "[error] " + summary
			}
			if summary != "" {
				return "[result] " + summary
			}
			return "[result] (ok)"
		}
	}
	// Skip user text messages (these are injected prompts, too verbose).
	return ""
}

// parseResultEvent extracts the final result summary.
func parseResultEvent(raw map[string]json.RawMessage) string {
	var result string
	if err := json.Unmarshal(raw["result"], &result); err != nil || result == "" {
		return ""
	}
	return "[result] " + truncate(result, 500)
}

// jsonString extracts a Go string from a json.RawMessage that contains a JSON string.
func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// truncate shortens s to at most n characters, appending "…" if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
