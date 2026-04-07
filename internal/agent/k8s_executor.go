package agent

import (
	"context"
	"fmt"
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

// K8sExecutor manages running analyses as Kubernetes pods.
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
	podName   string
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
	client, err := NewInClusterK8sClient()
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

// CanPersist returns true — K8s pods survive server restarts.
func (e *K8sExecutor) CanPersist() bool {
	return true
}

// AgentReady returns true if the K8s executor is configured.
func (e *K8sExecutor) AgentReady() bool {
	return e.cfg.K8sWorkerImage != ""
}

// Start performs startup reconciliation and begins the sync loop.
func (e *K8sExecutor) Start(ctx context.Context) {
	// Reconcile: find existing analysis pods in our namespace and track them.
	e.reconcileExistingPods(ctx)

	syncCtx, cancel := context.WithCancel(ctx)
	e.stopSync = cancel
	go e.syncLoop(syncCtx)
}

// Shutdown cleans up running analyses.
func (e *K8sExecutor) Shutdown(ctx context.Context) {
	if e.stopSync != nil {
		e.stopSync()
	}
	// Don't delete pods on shutdown — they persist and we'll reconcile on restart.
	log.Info().Msg("K8s executor shutdown (pods will persist)")
}

// Submit launches a new analysis as a K8s pod.
func (e *K8sExecutor) Submit(analysis *models.Analysis, packages []models.SoftwarePackage) {
	go e.launchPod(analysis, packages)
}

// Cancel terminates a running analysis pod.
func (e *K8sExecutor) Cancel(analysisID string) {
	e.mu.Lock()
	state, ok := e.running[analysisID]
	e.mu.Unlock()

	if ok {
		if state.cancel != nil {
			state.cancel()
		}
		// Delete the pod.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := e.k8s.DeletePod(ctx, e.cfg.K8sNamespace, state.podName); err != nil {
			log.Error().Err(err).Str("pod", state.podName).Msg("Failed to delete worker pod")
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

// launchPod creates a K8s pod for the analysis.
func (e *K8sExecutor) launchPod(analysis *models.Analysis, packages []models.SoftwarePackage) {
	e.hub.Broadcast(analysis.ID, []byte("[system] Analysis queued, waiting for available slot..."))

	// Acquire semaphore.
	e.countsem <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.MaxAnalysisDuration)

	// Build the Anthropic API proxy URL.  Workers call this instead of
	// api.anthropic.com directly, so the real API key stays server-side.
	anthropicProxyURL := strings.TrimRight(e.cfg.BaseURL, "/") + "/api/v1/internal/worker/anthropic"

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

	// For external LLM: issue a sidecar token so the proxy container can obtain
	// the real API key from the SWAMP server without it appearing in the pod spec.
	var sidecarToken string
	extLLMProxyURL := ""
	if llmConfig.Provider == "external" {
		extKey, err := resolveExternalLLMAPIKey(ctx, e.queries, e.encryptor, e.cfg, analysis)
		if err != nil {
			if llmConfig.Fallback == "anthropic" {
				log.Warn().Err(err).Str("analysis_id", analysis.ID).Msg("External LLM key unavailable for K8s pod, falling back to Anthropic")
				llmConfig.Provider = "anthropic"
			} else {
				e.failAnalysis(analysis.ID, "External LLM key resolution failed", err)
				cancel()
				<-e.countsem
				return
			}
		} else {
			st, err := e.tokenStore.IssueSidecarToken(
				analysis.ID,
				extKey,
				e.cfg.ExternalLLMEndpoint,
				10*time.Minute,
			)
			if err != nil {
				e.failAnalysis(analysis.ID, "Failed to issue sidecar token", err)
				cancel()
				<-e.countsem
				return
			}
			sidecarToken = st
			extLLMProxyURL = fmt.Sprintf("http://127.0.0.1:%d/v1", e.cfg.LLMProxyPort)
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
		llmConfig.AnalysisModel,
		llmConfig.PoCModel,
	)
	if err != nil {
		e.failAnalysis(analysis.ID, "Failed to issue worker token", err)
		cancel()
		<-e.countsem
		return
	}

	podName := fmt.Sprintf("swamp-analysis-%s", analysis.ID[:8])
	pod := e.buildPodSpec(podName, analysis.ID, token, sidecarToken)

	// Mark running before creating pod.
	e.mu.Lock()
	e.running[analysis.ID] = &k8sAnalysisState{
		podName:   podName,
		cancel:    cancel,
		startedAt: time.Now(),
	}
	e.mu.Unlock()

	if err := e.queries.SetAnalysisStarted(ctx, analysis.ID); err != nil {
		log.Error().Err(err).Str("analysis_id", analysis.ID).Msg("Failed to mark analysis started")
	}

	e.hub.Broadcast(analysis.ID, []byte("[system] Creating worker pod..."))
	if err := e.k8s.CreatePod(ctx, e.cfg.K8sNamespace, pod); err != nil {
		e.failAnalysis(analysis.ID, "Failed to create worker pod", err)
		e.mu.Lock()
		delete(e.running, analysis.ID)
		e.mu.Unlock()
		cancel()
		<-e.countsem
		return
	}

	e.hub.Broadcast(analysis.ID, []byte("[system] Worker pod "+podName+" created"))

	// Watch the pod in background. When it completes, release semaphore.
	go e.watchPod(ctx, cancel, analysis.ID, podName)
}

// watchPod monitors a worker pod until it completes or fails.
func (e *K8sExecutor) watchPod(ctx context.Context, cancel context.CancelFunc, analysisID, podName string) {
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
			log.Info().Str("analysis_id", analysisID).Msg("Pod watch context cancelled")
			return
		case <-ticker.C:
			status, err := e.k8s.GetPodPhase(ctx, e.cfg.K8sNamespace, podName)
			if err != nil {
				log.Warn().Err(err).Str("pod", podName).Msg("Failed to get pod status")
				continue
			}

			switch status {
			case "Succeeded":
				log.Info().Str("pod", podName).Msg("Worker pod completed successfully")
				// The worker reports its own status. Just clean up tracking.
				return
			case "Failed":
				log.Warn().Str("pod", podName).Msg("Worker pod failed")
				e.failAnalysis(analysisID, "Worker pod failed", nil)
				return
			case "Unknown":
				log.Warn().Str("pod", podName).Msg("Worker pod in unknown state")
				// "Pending" and "Running" — keep watching.
			}
		}
	}
}

// buildPodSpec creates the pod JSON for a worker analysis pod.
// sidecarToken is non-empty when an LLM proxy sidecar should be included;
// passing "" omits the sidecar (Anthropic provider or fallback).
func (e *K8sExecutor) buildPodSpec(podName, analysisID, workerToken, sidecarToken string) map[string]any {
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
		"name":  "worker",
		"image": e.cfg.K8sWorkerImage,
		"env":   env,
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

	// Build the container list. Start with the main worker container.
	containers := []any{container}

	// If a sidecar token was issued, add the LLM proxy sidecar container.
	// The sidecar runs the same SWAMP image in proxy mode. It exchanges the
	// one-time sidecar token with the SWAMP server to obtain the real external
	// LLM API key and endpoint, then proxies LLM requests from the worker
	// on 127.0.0.1:<LLMProxyPort>. The main worker container has no access
	// to the real API key.
	if sidecarToken != "" {
		sidecar := map[string]any{
			"name":  "llm-proxy",
			"image": e.cfg.K8sWorkerImage,
			"env": []map[string]any{
				{"name": "SWAMP_LLM_PROXY_MODE", "value": "true"},
				{"name": "SWAMP_WORKER_SERVER", "value": e.cfg.BaseURL},
				{"name": "SWAMP_LLM_PROXY_TOKEN", "value": sidecarToken},
				{"name": "LLM_PROXY_PORT", "value": fmt.Sprintf("%d", e.cfg.LLMProxyPort)},
			},
			"resources": map[string]any{
				"requests": map[string]string{"cpu": "50m", "memory": "64Mi"},
				"limits":   map[string]string{"cpu": "200m", "memory": "256Mi"},
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
		}
		containers = append(containers, sidecar)
	}

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

	// TTL after finished.
	if e.cfg.K8sPodTTLSeconds > 0 {
		ttl := int32(e.cfg.K8sPodTTLSeconds)
		podSpec["ttlSecondsAfterFinished"] = ttl // Note: this is a Job field, not Pod
	}

	pod := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      podName,
			"namespace": e.cfg.K8sNamespace,
			"labels":    labels,
		},
		"spec": podSpec,
	}

	// Merge custom annotations.
	if annotations := e.cfg.ParseWorkerAnnotations(); len(annotations) > 0 {
		pod["metadata"].(map[string]any)["annotations"] = annotations
	}

	return pod
}

// reconcileExistingPods finds running analysis pods and re-tracks them.
func (e *K8sExecutor) reconcileExistingPods(ctx context.Context) {
	pods, err := e.k8s.ListPods(ctx, e.cfg.K8sNamespace, "app.kubernetes.io/name=swamp-worker")
	if err != nil {
		log.Error().Err(err).Msg("Failed to list existing analysis pods for reconciliation")
		return
	}

	for _, pod := range pods {
		analysisID, ok := pod.Labels["swamp/analysis-id"]
		if !ok || pod.Phase == "Succeeded" || pod.Phase == "Failed" {
			continue
		}
		log.Info().Str("pod", pod.Name).Str("analysis_id", analysisID).
			Str("phase", pod.Phase).Msg("Reconciling existing worker pod")

		_, cancelFunc := context.WithTimeout(ctx, e.cfg.MaxAnalysisDuration)
		e.mu.Lock()
		e.running[analysisID] = &k8sAnalysisState{
			podName:   pod.Name,
			cancel:    cancelFunc,
			startedAt: time.Now(),
		}
		e.mu.Unlock()

		go e.watchPod(ctx, cancelFunc, analysisID, pod.Name)
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


