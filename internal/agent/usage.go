package agent

import (
	"encoding/json"
	"strings"

	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/pricing"
)

// estimateCost calculates the estimated cost for a token usage record
// using the shared pricing table. Returns 0 if the model is not recognized.
func estimateCost(u *models.TokenUsage) float64 {
	return pricing.EstimateCost(u.Model, u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
}

// ExtractTokenUsage parses raw stdout log lines (one JSON object per line)
// and returns aggregated per-model token usage.
//
// It handles two formats:
//
//  1. Claude CLI "assistant" events — contain message.usage with per-message
//     token counts, and message.model for the model name.
//
//  2. OpenCode "step_finish" events — contain part.tokens with per-step
//     token counts and part.cost for the cost. The model is taken from
//     the most recent "text" or "tool_use" event's sessionID association
//     or from the step metadata.
func ExtractTokenUsage(logLines []string) []models.TokenUsage {
	totals := make(map[string]*models.TokenUsage) // keyed by model

	for _, line := range logLines {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		var eventType string
		if err := json.Unmarshal(raw["type"], &eventType); err != nil {
			continue
		}

		switch eventType {
		case "assistant":
			parseClaudeAssistantUsage(raw, totals)
		case "step_finish":
			parseOpenCodeStepFinishUsage(raw, totals)
		}
	}

	out := make([]models.TokenUsage, 0, len(totals))
	for _, u := range totals {
		// If cost wasn't provided by the stream format (e.g. Claude CLI),
		// estimate it from the pricing table.
		if u.CostUSD == 0 {
			u.CostUSD = estimateCost(u)
		}
		out = append(out, *u)
	}
	return out
}

// parseClaudeAssistantUsage extracts usage from a Claude CLI "assistant" event.
//
// Example:
//
//	{"type":"assistant","message":{"model":"claude-sonnet-4-6","usage":{
//	  "input_tokens":3,"cache_creation_input_tokens":4453,
//	  "cache_read_input_tokens":20342,"output_tokens":123},...}}
func parseClaudeAssistantUsage(raw map[string]json.RawMessage, totals map[string]*models.TokenUsage) {
	var msg struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw["message"], &msg); err != nil || msg.Usage == nil {
		return
	}
	model := msg.Model
	if model == "" {
		model = "unknown"
	}

	u := getOrCreate(totals, model)
	u.InputTokens += msg.Usage.InputTokens
	u.OutputTokens += msg.Usage.OutputTokens
	u.CacheReadTokens += msg.Usage.CacheReadInputTokens
	u.CacheWriteTokens += msg.Usage.CacheCreationInputTokens
}

// parseOpenCodeStepFinishUsage extracts usage from an OpenCode "step_finish" event.
//
// Example:
//
//	{"type":"step_finish","part":{"tokens":{"total":10555,"input":10471,
//	  "output":84,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0.05}}
func parseOpenCodeStepFinishUsage(raw map[string]json.RawMessage, totals map[string]*models.TokenUsage) {
	var part struct {
		Tokens *struct {
			Input  int64 `json:"input"`
			Output int64 `json:"output"`
			Cache  *struct {
				Read  int64 `json:"read"`
				Write int64 `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
		Cost    float64 `json:"cost"`
		ModelID string  `json:"modelID"`
	}
	if err := json.Unmarshal(raw["part"], &part); err != nil || part.Tokens == nil {
		return
	}

	model := part.ModelID
	if model == "" {
		model = "opencode"
	}

	u := getOrCreate(totals, model)
	u.InputTokens += part.Tokens.Input
	u.OutputTokens += part.Tokens.Output
	if part.Tokens.Cache != nil {
		u.CacheReadTokens += part.Tokens.Cache.Read
		u.CacheWriteTokens += part.Tokens.Cache.Write
	}
	u.CostUSD += part.Cost
}

// isTokenBearingEvent checks if a raw JSON line is a token-usage event
// (Claude "assistant" with usage or OpenCode "step_finish" with tokens).
// This is used by the streaming goroutines to forward raw JSON for live
// token tracking on the frontend.
func isTokenBearingEvent(line []byte) bool {
	if len(line) == 0 || line[0] != '{' {
		return false
	}
	var raw struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &raw) != nil {
		return false
	}
	switch raw.Type {
	case "step_finish":
		return true
	case "assistant":
		// Only if it contains usage data.
		var msg struct {
			Message struct {
				Usage *json.RawMessage `json:"usage"`
			} `json:"message"`
		}
		return json.Unmarshal(line, &msg) == nil && msg.Message.Usage != nil
	}
	return false
}

func getOrCreate(m map[string]*models.TokenUsage, model string) *models.TokenUsage {
	if u, ok := m[model]; ok {
		return u
	}
	u := &models.TokenUsage{Model: model}
	m[model] = u
	return u
}
