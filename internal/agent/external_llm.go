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

// writeOpenCodeConfig writes an opencode.json configuration file to workDir.
// It configures the built-in OpenAI provider (which is bundled with opencode
// and requires no npm downloads) with the given base URL.
//
// The API key is supplied via the OPENAI_API_KEY environment variable so it
// does not appear in the config file on disk.
func writeOpenCodeConfig(workDir, baseURL string) error {
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			"openai": map[string]any{
				"options": map[string]any{
					"baseURL": baseURL,
				},
			},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal opencode config: %w", err)
	}
	return os.WriteFile(filepath.Join(workDir, "opencode.json"), data, 0640)
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
	if err := writeOpenCodeConfig(workDir, baseURL); err != nil {
		return fmt.Errorf("write opencode config: %w", err)
	}

	// opencode run [--model openai/<model>] --format json "<prompt>"
	args := []string{"run", "--format", "json"}
	if model != "" {
		args = append(args, "--model", "openai/"+model)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("HOME=%s", workDir),
		fmt.Sprintf("OPENAI_BASE_URL=%s", baseURL),
		fmt.Sprintf("OPENAI_API_KEY=%s", apiKey),
	)

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

	broadcast := func(msg string) {
		hub.Broadcast(analysisID, []byte(msg))
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPR)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			msg := extractOpenCodeMessage(scanner.Bytes())
			if msg != "" {
				broadcast(msg)
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
// opencode emits AI-SDK streaming events. Common shapes:
//
//	{"type":"text-delta","textDelta":"..."}
//	{"type":"tool-call","toolName":"...","args":{...}}
//	{"type":"tool-result","toolName":"...","result":"..."}
//	{"type":"finish","finishReason":"stop","usage":{...}}
//	{"type":"error","error":"..."}
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
		s := strings.TrimSpace(string(line))
		return s
	}

	var eventType string
	if err := json.Unmarshal(raw["type"], &eventType); err != nil {
		return ""
	}

	switch eventType {
	case "text-delta":
		var s string
		if err := json.Unmarshal(raw["textDelta"], &s); err == nil && s != "" {
			return s
		}
	case "tool-call":
		var name string
		_ = json.Unmarshal(raw["toolName"], &name)
		var args map[string]json.RawMessage
		if json.Unmarshal(raw["args"], &args) == nil {
			detail := openCodeToolDetail(name, args)
			if detail != "" {
				return fmt.Sprintf("[tool] %s: %s", name, detail)
			}
		}
		if name != "" {
			return "[tool] " + name
		}
	case "tool-result":
		var name, result string
		_ = json.Unmarshal(raw["toolName"], &name)
		_ = json.Unmarshal(raw["result"], &result)
		if result != "" {
			return "[result] " + truncate(result, 200)
		}
		return "[result] (ok)"
	case "finish":
		var reason string
		_ = json.Unmarshal(raw["finishReason"], &reason)
		if reason != "" && reason != "stop" {
			return "[finish] " + reason
		}
		return ""
	case "error":
		var errMsg string
		_ = json.Unmarshal(raw["error"], &errMsg)
		if errMsg != "" {
			return "[error] " + errMsg
		}
	case "step-finish", "step-start", "usage":
		return ""
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
