package agent

import (
	"context"
	"fmt"
	"math"
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

// K8sExecutor manages running analyses as Kubernetes jobs.
type K8sExecutor struct {
	cfg        *config.Config
	queries    *db.Queries
	store      *storage.Store
	hub        *ws.Hub
	encryptor  *crypto.Encryptor
	k8s        K8sClient
	tokenStore *WorkerTokenStore

	mu       sync.Mutex
	running  map[string]*k8sAnalysisState // analysisID → state
	countsem chan struct{}

	stopSync context.CancelFunc
}

// k8sAnalysisState tracks the state of a K8s-based analysis.
type k8sAnalysisState struct {
	jobName   string
	cancel    context.CancelFunc
	startedAt time.Time
}

// NewK8sExecutor creates a new K8s-based executor.
func NewK8sExecutor(
	cfg *config.Config,
	queries *db.Queries,
	store *storage.Store,
	hub *ws.Hub,
	enc *crypto.Encryptor,
	tokenStore *WorkerTokenStore,
) (*K8sExecutor, error) {
	client, err := NewK8sClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating K8s client: %w", err)
	}

	return &K8sExecutor{
		cfg:        cfg,
		queries:    queries,
		store:      store,
		hub:        hub,
		encryptor:  enc,
		k8s:        client,
		tokenStore: tokenStore,
		running:    make(map[string]*k8sAnalysisState),
		countsem:   make(chan struct{}, cfg.MaxConcurrentAnalyses),
	}, nil
}

// CanPersist returns true — K8s jobs survive server restarts.
func (e *K8sExecutor) CanPersist() bool {
	return true
}

// AgentReady returns true if the K8s executor is configured.
func (e *K8sExecutor) AgentReady() bool {
	return e.cfg.K8sWorkerImage != ""
}

// Start performs startup reconciliation and begins the sync loop.
func (e *K8sExecutor) Start(ctx context.Context) {
	// Reconcile: find existing analysis jobs in our namespace and track them.
	e.reconcileExistingJobs(ctx)

	syncCtx, cancel := context.WithCancel(ctx)
	e.stopSync = cancel
	go e.syncLoop(syncCtx)
}

// Shutdown cleans up running analyses.
func (e *K8sExecutor) Shutdown(ctx context.Context) {
	if e.stopSync != nil {
		e.stopSync()
	}
	// Don't delete jobs on shutdown — they persist and we'll reconcile on restart.
	log.Info().Msg("K8s executor shutdown (jobs will persist)")
}

// Submit launches a new analysis as a K8s job.
func (e *K8sExecutor) Submit(analysis *models.Analysis, packages []models.SoftwarePackage) {
	go e.launchJob(analysis, packages)
}

// Cancel terminates a running analysis job.
func (e *K8sExecutor) Cancel(analysisID string) {
	e.mu.Lock()
	state, ok := e.running[analysisID]
	e.mu.Unlock()

	if ok {
		if state.cancel != nil {
			state.cancel()
		}
		// Delete the job and let Kubernetes cascade cleanup to the pod.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := e.k8s.DeleteJob(ctx, e.cfg.K8sNamespace, state.jobName); err != nil {
			log.Error().Err(err).Str("job", state.jobName).Msg("Failed to delete worker job")
		}
		e.tokenStore.RevokeAnalysis(analysisID)

		e.mu.Lock()
		delete(e.running, analysisID)
		e.mu.Unlock()
	}
}

// IsRunning reports whether the executor is tracking a given analysis.
func (e *K8sExecutor) IsRunning(analysisID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.running[analysisID]
	return ok
}

// launchJob creates a K8s job for the analysis.
func (e *K8sExecutor) launchJob(analysis *models.Analysis, packages []models.SoftwarePackage) {
	e.hub.Broadcast(analysis.ID, []byte("[system] Analysis queued, waiting for available slot..."))

	// Acquire semaphore.
	e.countsem <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.MaxAnalysisDuration)

	// Build the Anthropic API proxy URL.  Workers call this instead of
	// api.anthropic.com directly, so the real API key stays server-side.
	anthropicProxyURL := strings.TrimRight(e.cfg.BaseURL, "/") + "/api/v1/internal/worker/anthropic"

	// Build the generic LLM proxy URL for non-Anthropic providers.
	llmProxyURL := strings.TrimRight(e.cfg.BaseURL, "/") + "/api/v1/internal/worker/llm"

	// Gather analysis context (prior findings + notes) for the worker.
	analysisCtx := gatherAnalysisContext(ctx, e.queries, e.encryptor, e.store, analysis.ProjectID, packages)

	// Determine effective model: per-analysis overrides global config.
	effectiveModel := analysis.AgentModel
	if effectiveModel == "" {
		effectiveModel = e.cfg.AgentModel
	}

	// Resolve effective LLM config (global + per-project overrides).
	var project *models.Project
	if e.queries != nil && analysis.ProjectID != "" {
		project, _ = e.queries.GetProject(ctx, analysis.ProjectID)
	}
	llmConfig := ResolveEffectiveLLMConfig(e.cfg, project)

	// For external/nrp/custom providers, the worker normally routes through the
	// SWAMP LLM proxy. In K8s dev mode (K8S_DIRECT_LLM=true), the SWAMP server
	// may not be reachable by pods, so we resolve the real API key and endpoint
	// server-side and pass them directly in the token exchange response.
	extLLMProxyURL := ""
	extLLMDirectKey := ""
	if llmConfig.Provider == "external" {
		if e.cfg.K8sDirectLLM {
			creds, err := resolveExternalLLMDirect(ctx, e.queries, e.encryptor, e.cfg, analysis)
			if err != nil {
				if llmConfig.Fallback == "anthropic" {
					log.Warn().Err(err).Str("analysis_id", analysis.ID).Msg("External LLM unavailable for K8s direct mode, falling back to Anthropic")
					llmConfig.Provider = "anthropic"
				} else {
					e.failAnalysis(analysis.ID, "External LLM credentials unavailable", err)
					cancel()
					<-e.countsem
					return
				}
			} else {
				extLLMProxyURL = creds.EndpointURL
				extLLMDirectKey = creds.APIKey
				log.Debug().Str("analysis_id", analysis.ID).Str("endpoint", creds.EndpointURL).Msg("K8s direct LLM mode: passing credentials directly to worker")
			}
		} else {
			extLLMProxyURL = llmProxyURL
		}
	}

	// Issue one-time token for the worker.
	token, err := e.tokenStore.IssueToken(
		analysis.ID,
		packages,
		effectiveModel,
		anthropicProxyURL,
		analysis.CustomPrompt,
		analysisCtx,
		10*time.Minute, // worker must exchange within 10 minutes
		llmConfig.Provider,
		extLLMProxyURL,
		extLLMDirectKey,
		llmConfig.AnalysisModel,
		llmConfig.PoCModel,
	)
	if err != nil {
		e.failAnalysis(analysis.ID, "Failed to issue worker token", err)
		cancel()
		<-e.countsem
		return
	}

	jobName := fmt.Sprintf("swamp-analysis-%s", analysis.ID[:8])
	job := e.buildJobSpec(jobName, analysis.ID, token)

	// Mark running before creating the job.
	e.mu.Lock()
	e.running[analysis.ID] = &k8sAnalysisState{
		jobName:   jobName,
		cancel:    cancel,
		startedAt: time.Now(),
	}
	e.mu.Unlock()

	if err := e.queries.SetAnalysisStarted(ctx, analysis.ID); err != nil {
		log.Error().Err(err).Str("analysis_id", analysis.ID).Msg("Failed to mark analysis started")
	}

	e.hub.Broadcast(analysis.ID, []byte("[system] Creating worker job..."))
	if err := e.k8s.CreateJob(ctx, e.cfg.K8sNamespace, job); err != nil {
		e.failAnalysis(analysis.ID, "Failed to create worker job", err)
		e.mu.Lock()
		delete(e.running, analysis.ID)
		e.mu.Unlock()
		cancel()
		<-e.countsem
		return
	}

	e.hub.Broadcast(analysis.ID, []byte("[system] Worker job "+jobName+" created"))

	// Watch the job in background. When it completes, release semaphore.
	go e.watchJob(ctx, cancel, analysis.ID, jobName)
}

// watchJob monitors a worker job until it completes or fails.
func (e *K8sExecutor) watchJob(ctx context.Context, cancel context.CancelFunc, analysisID, jobName string) {
	defer func() {
		cancel()
		<-e.countsem
		e.mu.Lock()
		delete(e.running, analysisID)
		e.mu.Unlock()
		e.tokenStore.RevokeAnalysis(analysisID)
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Str("analysis_id", analysisID).Msg("Job watch context cancelled")
			return
		case <-ticker.C:
			status, err := e.k8s.GetJobPhase(ctx, e.cfg.K8sNamespace, jobName)
			if err != nil {
				log.Warn().Err(err).Str("job", jobName).Msg("Failed to get job status")
				continue
			}

			switch status {
			case "Succeeded":
				log.Info().Str("job", jobName).Msg("Worker job completed successfully")
				// The worker reports its own status. Just clean up tracking.
				return
			case "Failed":
				log.Warn().Str("job", jobName).Msg("Worker job failed")
				e.failAnalysis(analysisID, "Worker job failed", nil)
				return
			case "Unknown":
				log.Warn().Str("job", jobName).Msg("Worker job in unknown state")
				// "Pending" and "Running" — keep watching.
			}
		}
	}
}

// buildJobSpec creates the Job JSON for a worker analysis.
func (e *K8sExecutor) buildJobSpec(jobName, analysisID, workerToken string) map[string]any {
	labels := map[string]string{
		"app.kubernetes.io/name":       "swamp-worker",
		"app.kubernetes.io/component":  "analysis",
		"app.kubernetes.io/managed-by": "swamp",
		"swamp/analysis-id":            analysisID,
	}
	// Merge custom labels.
	for k, v := range e.cfg.ParseWorkerLabels() {
		labels[k] = v
	}

	falseVal := false
	trueVal := true
	runAsUser := int64(65534) // nobody
	runAsGroup := int64(65534)

	env := []map[string]any{
		{"name": "SWAMP_WORKER_MODE", "value": "true"},
		{"name": "SWAMP_WORKER_TOKEN", "value": workerToken},
		{"name": "SWAMP_WORKER_SERVER", "value": e.cfg.BaseURL},
		{"name": "SWAMP_WORKER_ANALYSIS", "value": analysisID},
		{"name": "AGENT_BINARY", "value": e.cfg.AgentBinary},
		{"name": "MAX_ANALYSIS_DURATION", "value": e.cfg.MaxAnalysisDuration.String()},
	}

	// Container spec.
	container := map[string]any{
		"name":            "worker",
		"image":           e.cfg.K8sWorkerImage,
		"imagePullPolicy": "Always",
		"env":             env,
		"resources": map[string]any{
			"requests": map[string]string{
				"cpu":    e.cfg.K8sWorkerCPURequest,
				"memory": e.cfg.K8sWorkerMemRequest,
			},
			"limits": map[string]string{
				"cpu":    e.cfg.K8sWorkerCPULimit,
				"memory": e.cfg.K8sWorkerMemLimit,
			},
		},
		"securityContext": map[string]any{
			"runAsNonRoot":             &trueVal,
			"runAsUser":                &runAsUser,
			"runAsGroup":               &runAsGroup,
			"allowPrivilegeEscalation": &falseVal,
			"readOnlyRootFilesystem":   &trueVal,
			"capabilities": map[string]any{
				"drop": []string{"ALL"},
			},
			"seccompProfile": map[string]any{
				"type": "RuntimeDefault",
			},
		},
		"volumeMounts": []map[string]any{
			{"name": "work", "mountPath": "/work"},
			{"name": "tmp", "mountPath": "/tmp"},
			{"name": "home", "mountPath": "/home/worker"},
		},
	}

	// Build the container list.
	containers := []any{container}

	// Pod spec.
	podSpec := map[string]any{
		"restartPolicy":                "Never",
		"serviceAccountName":           e.cfg.K8sWorkerServiceAccount,
		"automountServiceAccountToken": &falseVal,
		"enableServiceLinks":           &falseVal,
		"containers":                   containers,
		"volumes": []map[string]any{
			{"name": "work", "emptyDir": map[string]any{"sizeLimit": "4Gi"}},
			{"name": "tmp", "emptyDir": map[string]any{"sizeLimit": "1Gi"}},
			{"name": "home", "emptyDir": map[string]any{"sizeLimit": "1Gi"}},
		},
		"securityContext": map[string]any{
			"runAsNonRoot": &trueVal,
			"runAsUser":    &runAsUser,
			"runAsGroup":   &runAsGroup,
			"fsGroup":      &runAsGroup,
			"seccompProfile": map[string]any{
				"type": "RuntimeDefault",
			},
		},
	}

	// Node selector.
	if ns := e.cfg.ParseNodeSelector(); len(ns) > 0 {
		podSpec["nodeSelector"] = ns
	}

	// Tolerations.
	if e.cfg.K8sWorkerTolerations != "" {
		tolerations := parseTolerations(e.cfg.K8sWorkerTolerations)
		if len(tolerations) > 0 {
			podSpec["tolerations"] = tolerations
		}
	}

	jobSpec := map[string]any{
		"backoffLimit": int32(0),
		"template": map[string]any{
			"metadata": map[string]any{
				"labels": labels,
			},
			"spec": podSpec,
		},
	}
	if seconds := int64(math.Ceil(e.cfg.MaxAnalysisDuration.Seconds())); seconds > 0 {
		jobSpec["activeDeadlineSeconds"] = seconds
	}
	if e.cfg.K8sPodTTLSeconds > 0 {
		jobSpec["ttlSecondsAfterFinished"] = int32(e.cfg.K8sPodTTLSeconds)
	}

	annotations := e.cfg.ParseWorkerAnnotations()
	if len(annotations) > 0 {
		jobSpec["template"].(map[string]any)["metadata"].(map[string]any)["annotations"] = annotations
	}

	job := map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":      jobName,
			"namespace": e.cfg.K8sNamespace,
			"labels":    labels,
		},
		"spec": jobSpec,
	}

	if len(annotations) > 0 {
		job["metadata"].(map[string]any)["annotations"] = annotations
	}

	return job
}

// reconcileExistingJobs finds running analysis jobs and re-tracks them.
func (e *K8sExecutor) reconcileExistingJobs(ctx context.Context) {
	jobs, err := e.k8s.ListJobs(ctx, e.cfg.K8sNamespace, "app.kubernetes.io/name=swamp-worker")
	if err != nil {
		log.Error().Err(err).Msg("Failed to list existing analysis jobs for reconciliation")
		return
	}

	for _, job := range jobs {
		analysisID, ok := job.Labels["swamp/analysis-id"]
		if !ok || job.Phase == "Succeeded" || job.Phase == "Failed" {
			continue
		}
		log.Info().Str("job", job.Name).Str("analysis_id", analysisID).
			Str("phase", job.Phase).Msg("Reconciling existing worker job")

		_, cancelFunc := context.WithTimeout(ctx, e.cfg.MaxAnalysisDuration)
		e.mu.Lock()
		e.running[analysisID] = &k8sAnalysisState{
			jobName:   job.Name,
			cancel:    cancelFunc,
			startedAt: time.Now(),
		}
		e.mu.Unlock()

		go e.watchJob(ctx, cancelFunc, analysisID, job.Name)
	}

	log.Info().Int("tracked", len(e.running)).Msg("K8s executor reconciliation complete")
}

// syncLoop periodically cleans up and checks pod status.
func (e *K8sExecutor) syncLoop(ctx context.Context) {
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

// failAnalysis marks an analysis as failed, unless it already has a terminal status.
func (e *K8sExecutor) failAnalysis(analysisID, detail string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	log.Error().Err(err).Str("analysis_id", analysisID).Str("detail", detail).Msg("Analysis failed")
	dbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, getErr := e.queries.GetAnalysis(dbCtx, analysisID)
	if getErr == nil && (a.Status == "cancelled" || a.Status == "completed" || a.Status == "timed_out") {
		return
	}
	_ = e.queries.SetAnalysisCompleted(dbCtx, analysisID, "failed", errMsg)
	e.hub.Broadcast(analysisID, []byte("[system] Analysis failed: "+detail))
}

// TokenStore returns the worker token store for use by API handlers.
func (e *K8sExecutor) TokenStore() *WorkerTokenStore {
	return e.tokenStore
}

// Hub returns the WebSocket hub for use by worker handlers.
func (e *K8sExecutor) Hub() *ws.Hub {
	return e.hub
}

// parseTolerations parses "key=value:effect,key2:effect2" into toleration objects.
func parseTolerations(s string) []map[string]string {
	var result []map[string]string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		toleration := map[string]string{}
		parts := strings.SplitN(t, ":", 2)
		if len(parts) == 2 {
			toleration["effect"] = strings.TrimSpace(parts[1])
		}
		kvParts := strings.SplitN(parts[0], "=", 2)
		toleration["key"] = strings.TrimSpace(kvParts[0])
		if len(kvParts) == 2 {
			toleration["operator"] = "Equal"
			toleration["value"] = strings.TrimSpace(kvParts[1])
		} else {
			toleration["operator"] = "Exists"
		}
		result = append(result, toleration)
	}
	return result
}
