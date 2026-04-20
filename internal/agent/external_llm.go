package agent

import (
	"bufio"
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
)

// outputBroadcaster is a minimal interface for sending output to connected
// WebSocket clients. Satisfied by *ws.Hub (server-side) and workerStreamerHub
// (inside K8s pods / detached processes).
type outputBroadcaster interface {
	Broadcast(analysisID string, data []byte)
}

// resetOpenCodeState removes opencode's data and state directories so that
// a subsequent run in the same workDir starts a fresh session. Without this,
// opencode may detect the previous session and skip or behave unexpectedly.
func resetOpenCodeState(workDir string) {
	for _, sub := range []string{"data", "state", "cache"} {
		dir := filepath.Join(workDir, sub)
		if err := os.RemoveAll(dir); err != nil {
			log.Warn().Err(err).Str("dir", dir).Msg("Failed to remove opencode state dir")
		}
	}
}

// writeOpenCodeConfig writes opencode configuration files to workDir.
//
// The shape intentionally mirrors known-good custom OpenAI-compatible config:
// custom provider + npm adapter + explicit model mapping.
func writeOpenCodeConfig(workDir, baseURL, apiKey, model string) error {
	const (
		providerName = "custom"
		maxContext   = 200000
		maxOutput    = 16384
	)

	providerCfg := map[string]any{
		"npm":  "@ai-sdk/openai-compatible",
		"name": "Custom OpenAI-Compatible",
		"options": map[string]any{
			"baseURL": baseURL,
			"apiKey":  apiKey,
		},
	}

	// opencode requires at least one model in the provider config or it
	// fails with "no providers found".  Use the real model name when
	// available; otherwise fall back to a placeholder.
	cfgModel := model
	if cfgModel == "" {
		cfgModel = "auto"
	}
	providerCfg["models"] = map[string]any{
		cfgModel: map[string]any{
			"name": cfgModel,
			"limit": map[string]any{
				"context": maxContext,
				"output":  maxOutput,
			},
		},
	}

	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			providerName: providerCfg,
		},
		"model":       providerName + "/" + cfgModel,
		"small_model": providerName + "/" + cfgModel,
		"permission":  "allow",
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal opencode config: %w", err)
	}

	// Write config to the XDG_CONFIG_HOME path. We use a dedicated "config"
	// subdirectory as XDG_CONFIG_HOME so that opencode's data directory
	// (XDG_DATA_HOME) doesn't collide and overwrite the config files.
	configDir := filepath.Join(workDir, "config", "opencode")
	if err := os.MkdirAll(configDir, 0750); err != nil {
		return fmt.Errorf("create opencode config dir: %w", err)
	}
	for _, p := range []string{
		filepath.Join(configDir, "config.json"),
		filepath.Join(configDir, "opencode.json"),
		filepath.Join(workDir, "opencode.json"), // project root fallback + debug visibility
	} {
		if err := os.WriteFile(p, data, 0640); err != nil {
			return fmt.Errorf("write opencode config %s: %w", p, err)
		}
	}
	return nil
}

// runOpenCodeAgent invokes the opencode CLI with the given prompt and streams
// output to the WebSocket hub. It is the external-LLM equivalent of runAgent.
//
// baseURL is the OpenAI-compatible endpoint (either the real endpoint for
// local/process executors, or the sidecar proxy URL for K8s).
// apiKey is the bearer token; for K8s sidecar this is a placeholder since the
// proxy injects the real key.
func (e *Executor) runOpenCodeAgent(ctx context.Context, workDir, prompt, analysisID, baseURL, apiKey, model string) error {
	return runOpenCodeProcess(ctx, e.cfg.OpenCodeBinary, workDir, prompt, analysisID, baseURL, apiKey, model, e.hub)
}

// runOpenCodeProcess is the shared implementation used by both the local
// Executor and the K8s worker (via runWorkerOpenCode in worker.go).
func runOpenCodeProcess(ctx context.Context, binary, workDir, prompt, analysisID, baseURL, apiKey, model string, hub outputBroadcaster) error {
	if err := writeOpenCodeConfig(workDir, baseURL, apiKey, model); err != nil {
		return fmt.Errorf("write opencode config: %w", err)
	}

	// Write prompt to file (for audit) and feed via stdin so it never
	// appears in /proc/*/cmdline.
	promptFile := filepath.Join(workDir, "prompt.txt")
	if err := os.WriteFile(promptFile, []byte(prompt), 0640); err != nil {
		return fmt.Errorf("write prompt file: %w", err)
	}

	args := []string{"run", "--format", "json"}
	if model != "" {
		args = append(args, "--model", "custom/"+model)
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)
	// Use separate subdirectories for XDG paths to prevent opencode's
	// data initialization from overwriting config files.
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("HOME=%s", workDir),
		"SHELL=/bin/bash",
		fmt.Sprintf("XDG_CONFIG_HOME=%s", filepath.Join(workDir, "config")),
		fmt.Sprintf("XDG_DATA_HOME=%s", filepath.Join(workDir, "data")),
		fmt.Sprintf("XDG_CACHE_HOME=%s", filepath.Join(workDir, "cache")),
		fmt.Sprintf("XDG_STATE_HOME=%s", filepath.Join(workDir, "state")),
	)

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

	stdoutPR, stdoutPW := io.Pipe()
	stderrPR, stderrPW := io.Pipe()
	cmd.Stdout = io.MultiWriter(stdoutFile, stdoutPW)
	cmd.Stderr = io.MultiWriter(stderrFile, stderrPW)

	var wg sync.WaitGroup

	broadcast := func(msg string) {
		hub.Broadcast(analysisID, []byte(msg))
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPR)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			raw := scanner.Bytes()
			msg := extractOpenCodeMessage(raw)
			if msg != "" {
				broadcast(msg)
			}
			// Forward raw JSON for token-bearing events so the frontend
			// can extract live token usage.
			if isTokenBearingEvent(raw) {
				broadcast(string(raw))
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPR)
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
		for scanner.Scan() {
			broadcast("[stderr] " + scanner.Text())
		}
	}()

	log.Info().
		Str("binary", binary).
		Str("work_dir", workDir).
		Str("model", model).
		Msg("Starting opencode agent process")

	startTime := time.Now()
	err = cmd.Run()
	_ = stdoutPW.Close()
	_ = stderrPW.Close()
	wg.Wait()

	log.Info().
		Str("work_dir", workDir).
		Dur("duration", time.Since(startTime)).
		Err(err).
		Msg("opencode agent process finished")

	return err
}

// extractOpenCodeMessage parses a JSON event line from opencode --format json
// output and returns a human-readable string for the WebSocket feed.
// Returns "" for events that should be silently skipped.
//
// opencode emits session-level events. Common shapes:
//
//	{"type":"text","part":{"type":"text","text":"..."}}
//	{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"input":{...},"output":"...","title":"..."}}}
//	{"type":"step_start","part":{"type":"step-start"}}
//	{"type":"step_finish","part":{"type":"step-finish","tokens":{...},"cost":0}}
func extractOpenCodeMessage(line []byte) string {
	if len(line) == 0 || line[0] != '{' {
		// Non-JSON line — pass through as-is (trimmed).
		s := strings.TrimSpace(string(line))
		if s == "" {
			return ""
		}
		return s
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		// Not valid JSON — pass through.
		return strings.TrimSpace(string(line))
	}

	var eventType string
	if err := json.Unmarshal(raw["type"], &eventType); err != nil {
		return ""
	}

	switch eventType {
	case "text":
		var part struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw["part"], &part) == nil && part.Text != "" {
			return part.Text
		}

	case "tool_use":
		var part struct {
			Tool  string `json:"tool"`
			State struct {
				Title  string                     `json:"title"`
				Input  map[string]json.RawMessage `json:"input"`
				Output string                     `json:"output"`
			} `json:"state"`
		}
		if json.Unmarshal(raw["part"], &part) != nil {
			return ""
		}
		toolName := part.Tool
		if toolName == "" {
			return ""
		}
		// Build tool description: prefer title, then extract from input.
		detail := part.State.Title
		if detail == "" {
			detail = openCodeToolDetail(toolName, part.State.Input)
		}
		msg := ""
		if detail != "" {
			msg = fmt.Sprintf("[tool] %s: %s", toolName, detail)
		} else {
			msg = "[tool] " + toolName
		}
		// Append tool output if present.
		output := strings.TrimSpace(part.State.Output)
		if output != "" {
			msg += "\n[result] " + truncate(output, 200)
		}
		return msg

	case "error":
		// Try top-level "error" field first — can be a string or an object.
		if errRaw, ok := raw["error"]; ok {
			// Try as plain string.
			var errStr string
			if json.Unmarshal(errRaw, &errStr) == nil && errStr != "" {
				return "[error] " + errStr
			}
			// Try as object: {"name":"...","data":{"message":"..."}}
			var errObj struct {
				Name string `json:"name"`
				Data struct {
					Message string `json:"message"`
				} `json:"data"`
			}
			if json.Unmarshal(errRaw, &errObj) == nil {
				if errObj.Data.Message != "" {
					prefix := errObj.Name
					if prefix == "" {
						prefix = "error"
					}
					return "[error] " + prefix + ": " + errObj.Data.Message
				}
				if errObj.Name != "" {
					return "[error] " + errObj.Name
				}
			}
		}
		// Fallback: try part.error as string.
		var part struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw["part"], &part) == nil && part.Error != "" {
			return "[error] " + part.Error
		}

	case "step_start", "step_finish":
		return ""
	}
	return ""
}

// checkOpenCodeFatalError scans an opencode agent_stdout.log and returns an
// error message if the output contains only error events and no real work (no
// text or tool_use events). This detects cases where opencode exits 0 but
// emitted only a connection or API error.
func checkOpenCodeFatalError(stdoutLogPath string) string {
	f, err := os.Open(stdoutLogPath)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	var lastError string
	hasWork := false
	hasError := false

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		var eventType string
		if json.Unmarshal(raw["type"], &eventType) != nil {
			continue
		}
		switch eventType {
		case "text", "tool_use":
			hasWork = true
		case "error":
			hasError = true
			lastError = extractOpenCodeMessage(line)
		}
	}

	if hasError && !hasWork {
		if lastError != "" {
			return lastError
		}
		return "Agent emitted only error events with no analysis output"
	}
	return ""
}

// openCodeToolDetail extracts a concise description from a tool-call's args.
func openCodeToolDetail(toolName string, args map[string]json.RawMessage) string {
	switch toolName {
	case "bash", "Bash":
		if d := jsonString(args["description"]); d != "" {
			return d
		}
		return truncate(jsonString(args["command"]), 120)
	case "read", "Read":
		return jsonString(args["file_path"])
	case "write", "Write", "edit", "Edit":
		return jsonString(args["file_path"])
	default:
		for _, key := range []string{"description", "file_path", "command", "query", "url"} {
			if v, ok := args[key]; ok {
				if d := truncate(jsonString(v), 120); d != "" {
					return d
				}
			}
		}
	}
	return ""
}
