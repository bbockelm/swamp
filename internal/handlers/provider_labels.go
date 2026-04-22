package handlers

import (
	"context"
	"strings"
)

// resolveProviderLabel returns a human-readable label for a provider reference
// stored in analysis agent_config or token usage rows.
func (h *Handler) resolveProviderLabel(ctx context.Context, projectID, providerID, providerSource string) string {
	providerID = strings.TrimSpace(providerID)
	providerSource = strings.TrimSpace(providerSource)
	if providerID == "" {
		return ""
	}

	switch providerSource {
	case "global":
		if prov, err := h.queries.GetLLMProvider(ctx, providerID); err == nil {
			return strings.TrimSpace(prov.Label)
		}
	case "project":
		if key, err := h.queries.GetProjectProviderKey(ctx, providerID); err == nil {
			if projectID == "" || key.ProjectID == projectID {
				return strings.TrimSpace(key.Label)
			}
		}
	case "env":
		switch providerID {
		case "env-anthropic":
			return "Anthropic (env)"
		case "env-external":
			return "External LLM (env)"
		}
	}

	// Backwards-compatible fallback for older analyses that may not have a
	// provider_source recorded.
	if prov, err := h.queries.GetLLMProvider(ctx, providerID); err == nil {
		return strings.TrimSpace(prov.Label)
	}
	if key, err := h.queries.GetProjectProviderKey(ctx, providerID); err == nil {
		if projectID == "" || key.ProjectID == projectID {
			return strings.TrimSpace(key.Label)
		}
	}
	if providerID == "env-anthropic" {
		return "Anthropic (env)"
	}
	if providerID == "env-external" {
		return "External LLM (env)"
	}

	return ""
}
