// Package pricing embeds the shared model pricing table so it can be
// consumed by any Go package at zero runtime cost.
package pricing

import (
	_ "embed"
	"encoding/json"
	"log"
	"strings"
)

//go:embed model_pricing.json
var modelPricingJSON []byte

// Entry is a single row from model_pricing.json.
// Prices are USD per million tokens.
type Entry struct {
	Substring  string  `json:"substring"`
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// Table is the parsed pricing table, loaded once at init.
var Table []Entry

func init() {
	if err := json.Unmarshal(modelPricingJSON, &Table); err != nil {
		log.Fatalf("pricing: failed to parse model_pricing.json: %v", err)
	}
}

// Lookup returns the pricing entry for a model name, or nil if unknown.
// Matching is case-insensitive substring.
func Lookup(model string) *Entry {
	lower := strings.ToLower(model)
	for i := range Table {
		if strings.Contains(lower, Table[i].Substring) {
			return &Table[i]
		}
	}
	return nil
}

// EstimateCost calculates estimated cost given token counts and a model name.
// Returns 0 for unknown models.
func EstimateCost(model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) float64 {
	e := Lookup(model)
	if e == nil {
		return 0
	}
	return float64(inputTokens)*e.Input/1_000_000 +
		float64(outputTokens)*e.Output/1_000_000 +
		float64(cacheReadTokens)*e.CacheRead/1_000_000 +
		float64(cacheWriteTokens)*e.CacheWrite/1_000_000
}
