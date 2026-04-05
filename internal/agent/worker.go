package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/models"
)

// RunWorker is the entry point for the worker mode. It runs inside a K8s pod
// or detached process, exchanges the one-time token for a session, runs the
// analysis agent, streams output back to the server, and uploads results.
func RunWorker(cfg *config.Config) error {
	if cfg.WorkerToken == "" {
		return fmt.Errorf("SWAMP_WORKER_TOKEN is required in worker mode")
	}
	if cfg.WorkerServer == "" {
		return fmt.Errorf("SWAMP_WORKER_SERVER is required in worker mode")
	}
	if cfg.WorkerAnalysis == "" {
		return fmt.Errorf("SWAMP_WORKER_ANALYSIS is required in worker mode")
	}

	// Acquire flock on lock file if specified (process executor mode).
	// The lock is held for the entire worker lifetime; the OS releases it
	// automatically when the process exits, letting the parent detect death.
	if cfg.WorkerLockFile != "" {
		lockFile, err := AcquireWorkerLock(cfg.WorkerLockFile)
		if err != nil {
			return fmt.Errorf("acquiring worker lock: %w", err)
		}
		defer func() { _ = lockFile.Close() }()
		_ = os.Unsetenv("SWAMP_WORKER_LOCK_FILE")
	}

	workerToken := cfg.WorkerToken
	serverURL := strings.TrimRight(cfg.WorkerServer, "/")
	analysisID := cfg.WorkerAnalysis

	// Immediately clear the one-time token from the environment so Claude
	// cannot read it from /proc/self/environ or the `env` command.
	_ = os.Unsetenv("SWAMP_WORKER_TOKEN")

	log.Info().
		Str("server", serverURL).
		Str("analysis_id", analysisID).
		Msg("Worker starting, exchanging token...")

	// Exchange one-time token for a session credential.
	session, err := exchangeToken(serverURL, workerToken)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// The session token is stored only in Go memory — not in the environment.
	sessionToken := session.SessionToken
	log.Info().
		Str("analysis_id", session.AnalysisID).
		Int("packages", len(session.Packages)).
		Msg("Token exchange succeeded")

	// Report status: running.
	reportStatus(serverURL, sessionToken, analysisID, "running", "Worker initializing")

	// Set up work directory. Use CWD (set by the parent/orchestrator) so this
	// works in both K8s pods (CWD=/work) and process mode (CWD=/tmp/swamp-worker-*).
	workDir, err := os.Getwd()
	if err != nil {
		workDir = os.Getenv("HOME")
	}
	if workDir == "" {
		workDir = "/work"
	}
	outputDir := filepath.Join(workDir, "output")
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		reportStatus(serverURL, sessionToken, analysisID, "failed", "Failed to create work directory")
		return fmt.Errorf("create work dir: %w", err)
	}

	// Build prompt from packages.
	var prompt string
	if len(session.Packages) == 1 {
		pkg := packageInfoToSoftwarePackage(session.Packages[0])
		prompt = BuildPrompt(&pkg, "phase1", session.CustomPrompt, session.AnalysisContext)
	} else {
		pkgs := make([]models.SoftwarePackage, len(session.Packages))
		for i, p := range session.Packages {
			pkgs[i] = packageInfoToSoftwarePackage(p)
		}
		prompt = BuildMultiPackagePrompt(pkgs, session.CustomPrompt, session.AnalysisContext)
	}

	// Save the prompt and context as output artifacts.
	_ = os.WriteFile(filepath.Join(outputDir, "prompt.md"), []byte(prompt), 0640)
	if ctxText := formatAnalysisContext(session.AnalysisContext); ctxText != "" {
		_ = os.WriteFile(filepath.Join(outputDir, "context.md"), []byte(ctxText), 0640)
	}

	// Create a streamer that posts output lines to the server.
	streamer := newWorkerStreamer(serverURL, sessionToken, analysisID)

	// Run Phase 1.
	reportStatus(serverURL, sessionToken, analysisID, "running", "Phase 1: Security analysis")
	streamer.send("[system] Starting Phase 1: Security analysis")

	ctx, cancel := context.WithTimeout(context.Background(), cfg.MaxAnalysisDuration)
	defer cancel()

	if err := runWorkerAgent(ctx, cfg, session, workDir, prompt, streamer); err != nil {
		reportStatus(serverURL, sessionToken, analysisID, "failed", "Agent execution failed (Phase 1): "+err.Error())
		_ = uploadResults(serverURL, sessionToken, analysisID, outputDir)
		return err
	}

	// Detect if the agent exited 0 but produced no output (e.g. auth failure,
	// missing config). An empty stdout means the agent didn't actually run.
	stdoutLog := filepath.Join(outputDir, "agent_stdout.log")
	if info, err := os.Stat(stdoutLog); err != nil || info.Size() == 0 {
		reportStatus(serverURL, sessionToken, analysisID, "failed", "Agent produced no output (exited successfully but stdout was empty)")
		_ = uploadResults(serverURL, sessionToken, analysisID, outputDir)
		return fmt.Errorf("agent produced no output")
	}

	// Run Phase 2 if Phase 1 produced SARIF.
	sarifPath := filepath.Join(outputDir, "results.sarif")
	if _, err := os.Stat(sarifPath); err == nil {
		reportStatus(serverURL, sessionToken, analysisID, "running", "Phase 2: Exploit validation")
		streamer.send("[system] Starting Phase 2: Exploit validation")
		pkg := packageInfoToSoftwarePackage(session.Packages[0])
		phase2Prompt := BuildPrompt(&pkg, "phase2", "", nil)
		if err := runWorkerAgent(ctx, cfg, session, workDir, phase2Prompt, streamer); err != nil {
			log.Warn().Err(err).Msg("Phase 2 failed (non-fatal)")
		}
	}

	// Upload results.
	streamer.send("[system] Uploading results...")
	if err := uploadResults(serverURL, sessionToken, analysisID, outputDir); err != nil {
		log.Error().Err(err).Msg("Failed to upload results")
	}

	// Read the resolved git commit SHA if the agent recorded it.
	gitCommit := ""
	if shaBytes, err := os.ReadFile(filepath.Join(outputDir, "git_sha.txt")); err == nil {
		gitCommit = strings.TrimSpace(string(shaBytes))
	}

	reportCompletion(serverURL, sessionToken, analysisID, gitCommit)
	streamer.send("[system] Analysis complete")
	streamer.flush()

	log.Info().Str("analysis_id", analysisID).Msg("Worker finished successfully")
	return nil
}

func packageInfoToSoftwarePackage(p workerPackageInfo) models.SoftwarePackage {
	return models.SoftwarePackage{
		Name:           p.Name,
		GitURL:         p.GitURL,
		GitBranch:      p.GitBranch,
		GitCommit:      p.GitCommit,
		AnalysisPrompt: p.AnalysisPrompt,
	}
}

// exchangeToken calls the server to exchange a one-time worker token for a session.
func exchangeToken(serverURL, token string) (*WorkerExchangeResponse, error) {
	body, _ := json.Marshal(map[string]string{"token": token})
	url := serverURL + "/api/v1/internal/worker/exchange"

	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result WorkerExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding exchange response: %w", err)
	}
	return &result, nil
}

// reportStatus sends a status update to the server.
func reportStatus(serverURL, sessionToken, analysisID, status, detail string) {
	body, _ := json.Marshal(map[string]string{
		"analysis_id": analysisID,
		"status":      status,
		"detail":      detail,
	})
	url := serverURL + "/api/v1/internal/worker/status"
	req, err := newRetryableRequest("POST", url, body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create status request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sessionToken)

	resp, err := doWithRetry(req, 5)
	if err != nil {
		log.Error().Err(err).Msg("Failed to report status after retries")
		return
	}
	_ = resp.Body.Close()
}

// reportCompletion sends a completion status that includes the resolved git commit SHA.
func reportCompletion(serverURL, sessionToken, analysisID, gitCommit string) {
	body, _ := json.Marshal(map[string]string{
		"analysis_id": analysisID,
		"status":      "completed",
		"git_commit":  gitCommit,
	})
	url := serverURL + "/api/v1/internal/worker/status"
	req, err := newRetryableRequest("POST", url, body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create completion request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sessionToken)

	resp, err := doWithRetry(req, 5)
	if err != nil {
		log.Error().Err(err).Msg("Failed to report completion after retries")
		return
	}
	_ = resp.Body.Close()
}

// workerStreamer batches and sends output lines to the server.
type workerStreamer struct {
	serverURL    string
	sessionToken string
	analysisID   string
	mu           sync.Mutex
	buffer       []string
	lastFlush    time.Time
}

func newWorkerStreamer(serverURL, sessionToken, analysisID string) *workerStreamer {
	return &workerStreamer{
		serverURL:    serverURL,
		sessionToken: sessionToken,
		analysisID:   analysisID,
		lastFlush:    time.Now(),
	}
}

func (s *workerStreamer) send(msg string) {
	s.mu.Lock()
	s.buffer = append(s.buffer, msg)
	shouldFlush := len(s.buffer) >= 50 || time.Since(s.lastFlush) > 2*time.Second
	s.mu.Unlock()

	if shouldFlush {
		s.flush()
	}
}

func (s *workerStreamer) flush() {
	s.mu.Lock()
	if len(s.buffer) == 0 {
		s.mu.Unlock()
		return
	}
	lines := s.buffer
	s.buffer = nil
	s.lastFlush = time.Now()
	s.mu.Unlock()

	body, _ := json.Marshal(map[string]any{
		"analysis_id": s.analysisID,
		"lines":       lines,
	})
	url := s.serverURL + "/api/v1/internal/worker/stream"
	req, err := newRetryableRequest("POST", url, body)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.sessionToken)

	resp, err := doWithRetry(req, 3)
	if err != nil {
		log.Warn().Err(err).Int("lines", len(lines)).Msg("Failed to stream output after retries")
		return
	}
	_ = resp.Body.Close()
}

// runWorkerAgent executes the Claude CLI inside the worker pod.
func runWorkerAgent(ctx context.Context, cfg *config.Config, session *WorkerExchangeResponse, workDir, prompt string, streamer *workerStreamer) error {
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
	if session.AgentModel != "" {
		args = append(args, "--model", session.AgentModel)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, cfg.AgentBinary, args...)
	cmd.Dir = workDir
	cmd.Env = []string{
		fmt.Sprintf("HOME=%s", workDir),
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
		// Point the Anthropic SDK at the SWAMP server proxy instead of
		// api.anthropic.com. The real API key never reaches this process/pod.
		// ProxyToken is a dedicated credential that can only authenticate
		// to the proxy endpoint — it cannot call other worker APIs.
		fmt.Sprintf("ANTHROPIC_BASE_URL=%s", session.ProxyURL),
		fmt.Sprintf("ANTHROPIC_API_KEY=%s", session.ProxyToken),
		"TERM=xterm-256color",
	}

	// Capture stdout/stderr to log files and stream to server.
	stdoutFile, err := os.Create(filepath.Join(workDir, "output", "agent_stdout.log"))
	if err != nil {
		return fmt.Errorf("create stdout log: %w", err)
	}
	defer func() { _ = stdoutFile.Close() }()

	stderrFile, err := os.Create(filepath.Join(workDir, "output", "agent_stderr.log"))
	if err != nil {
		return fmt.Errorf("create stderr log: %w", err)
	}
	defer func() { _ = stderrFile.Close() }()

	stdoutPR, stdoutPW := io.Pipe()
	stderrPR, stderrPW := io.Pipe()
	cmd.Stdout = io.MultiWriter(stdoutFile, stdoutPW)
	cmd.Stderr = io.MultiWriter(stderrFile, stderrPW)

	var wg sync.WaitGroup

	// Parse stream-json on stdout.
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPR)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			msg := extractStreamMessage(scanner.Bytes())
			if msg != "" {
				streamer.send(msg)
			}
		}
	}()

	// Stream stderr.
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPR)
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
		for scanner.Scan() {
			streamer.send("[stderr] " + scanner.Text())
		}
	}()

	// Periodically flush the streamer.
	flushCtx, flushCancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-flushCtx.Done():
				return
			case <-ticker.C:
				streamer.flush()
			}
		}
	}()

	log.Info().Str("binary", cfg.AgentBinary).Str("work_dir", workDir).Msg("Starting agent process")

	startTime := time.Now()
	err = cmd.Run()
	_ = stdoutPW.Close()
	_ = stderrPW.Close()
	wg.Wait()
	flushCancel()
	streamer.flush()

	duration := time.Since(startTime)
	log.Info().Dur("duration", duration).Err(err).Msg("Agent process finished")

	return err
}

// uploadResults uploads the output directory to the server.
func uploadResults(serverURL, sessionToken, analysisID, outputDir string) error {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("reading output dir: %w", err)
	}

	// If there's an exploits directory, tar it up first.
	exploitsDir := filepath.Join(outputDir, "exploits")
	if fi, err := os.Stat(exploitsDir); err == nil && fi.IsDir() {
		tarPath := filepath.Join(outputDir, "exploits.tar.gz")
		if err := createTarGz(tarPath, exploitsDir); err != nil {
			log.Warn().Err(err).Msg("Failed to create exploits tarball")
		}
		// Re-read entries to include the tarball.
		entries, _ = os.ReadDir(outputDir)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filePath := filepath.Join(outputDir, entry.Name())
		if err := uploadSingleResult(serverURL, sessionToken, analysisID, filePath, entry.Name()); err != nil {
			log.Error().Err(err).Str("file", entry.Name()).Msg("Failed to upload result file")
		}
	}

	return nil
}

// uploadSingleResult uploads a single file to the server.
func uploadSingleResult(serverURL, sessionToken, analysisID, filePath, filename string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("analysis_id", analysisID)
	_ = writer.WriteField("filename", filename)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	_ = writer.Close()

	bodyBytes := buf.Bytes()
	contentType := writer.FormDataContentType()

	url := serverURL + "/api/v1/internal/worker/results"
	req, err := newRetryableRequest("POST", url, bodyBytes)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+sessionToken)

	resp, err := doWithRetry(req, 5)
	if err != nil {
		return fmt.Errorf("upload failed after retries: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("upload failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	log.Info().Str("file", filename).Msg("Uploaded result file")
	return nil
}

// doWithRetry executes an HTTP request with retries on transient failures
// (connection refused, reset, timeout). This lets the worker survive brief
// server restarts during hot-reload or rolling deployments.
func doWithRetry(req *http.Request, maxAttempts int) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 2 * time.Second
			log.Debug().Int("attempt", attempt+1).Dur("backoff", backoff).Str("url", req.URL.String()).Msg("Retrying request")
			time.Sleep(backoff)
		}
		// Reset the body for retries (needed for POST with body).
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

// newRetryableRequest creates an http.Request that supports body replay for retries.
func newRetryableRequest(method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return req, nil
}
