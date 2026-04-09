package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
)

// EffectiveLLMConfig holds the resolved LLM configuration for a project,
// merging per-project overrides with global config defaults.
type EffectiveLLMConfig struct {
	// Provider is "anthropic" or "external".
	Provider string
	// AnalysisModel is the model name for Phase 1 (security analysis).
	// Empty string means use the provider's default.
	AnalysisModel string
	// PoCModel is the model name for Phase 2 (exploit validation).
	// Falls back to AnalysisModel if empty.
	PoCModel string
	// Fallback controls retry behaviour when the primary provider fails.
	// "anthropic" = retry with Anthropic. "" = no fallback.
	Fallback string
}

// ResolveEffectiveLLMConfig computes the effective LLM configuration for a project,
// applying per-project overrides on top of global config.
// project may be nil (use global config only).
func ResolveEffectiveLLMConfig(cfg *config.Config, project *models.Project) EffectiveLLMConfig {
	result := EffectiveLLMConfig{
		Provider:      strings.ToLower(strings.TrimSpace(cfg.AgentProvider)),
		AnalysisModel: cfg.ExternalLLMAnalysisModel,
		PoCModel:      cfg.ExternalLLMPoCModel,
		Fallback:      strings.ToLower(strings.TrimSpace(cfg.ExternalLLMFallback)),
	}
	if project != nil {
		if project.AgentProvider != nil && *project.AgentProvider != "" {
			result.Provider = strings.ToLower(strings.TrimSpace(*project.AgentProvider))
		}
		if project.ExternalLLMAnalysisModel != nil && *project.ExternalLLMAnalysisModel != "" {
			result.AnalysisModel = *project.ExternalLLMAnalysisModel
		}
		if project.ExternalLLMPoCModel != nil && *project.ExternalLLMPoCModel != "" {
			result.PoCModel = *project.ExternalLLMPoCModel
		}
		if project.ExternalLLMFallback != nil {
			result.Fallback = *project.ExternalLLMFallback
		}
	}
	// Phase 2 model defaults to Phase 1 model if not separately configured.
	if result.PoCModel == "" {
		result.PoCModel = result.AnalysisModel
	}
	return result
}

// ExternalLLMCredentials holds a resolved API key and endpoint URL for an
// external LLM provider. Used when the worker needs direct access to the LLM.
type ExternalLLMCredentials struct {
	APIKey      string
	EndpointURL string
}

// resolveExternalLLMDirect resolves both the API key and endpoint URL for the
// external LLM configured for an analysis. It checks project-level provider
// keys first (nrp, custom, external_llm), then falls back to global config
// if the project allows global key usage.
func resolveExternalLLMDirect(ctx context.Context, queries *db.Queries, enc *crypto.Encryptor, cfg *config.Config, analysis *models.Analysis) (ExternalLLMCredentials, error) {
	if queries != nil && enc != nil && analysis != nil && analysis.ProjectID != "" {
		for _, provider := range []string{"nrp", "custom", "external_llm"} {
			k, err := queries.GetActiveProviderKey(ctx, analysis.ProjectID, provider)
			if err != nil {
				continue
			}
			dek, err := enc.UnwrapDEK(k.EncryptedDEK, k.DEKNonce)
			if err != nil {
				continue
			}
			pt, err := crypto.Decrypt(dek, k.EncryptedKey)
			if err != nil {
				continue
			}
			key := strings.TrimSpace(string(pt))
			if key == "" {
				continue
			}
			ep := k.EndpointURL
			if ep == "" {
				ep = cfg.ExternalLLMEndpoint
			}
			if ep == "" {
				continue
			}
			return ExternalLLMCredentials{APIKey: key, EndpointURL: ep}, nil
		}

		// No project key found. Check if global key is allowed.
		project, err := queries.GetProject(ctx, analysis.ProjectID)
		if err != nil {
			return ExternalLLMCredentials{}, fmt.Errorf("lookup project: %w", err)
		}
		if !project.UsesGlobalKey {
			return ExternalLLMCredentials{}, fmt.Errorf("project %s does not have an external LLM API key configured", analysis.ProjectID)
		}
	}

	// Fall back to global external LLM config.
	key := strings.TrimSpace(cfg.ExternalLLMAPIKey)
	if key == "" && cfg.ExternalLLMAPIKeyFile != "" {
		keyData, err := os.ReadFile(cfg.ExternalLLMAPIKeyFile)
		if err != nil {
			if !os.IsNotExist(err) {
				return ExternalLLMCredentials{}, fmt.Errorf("read external LLM API key file: %w", err)
			}
			// File does not exist — treat as "not configured".
		} else {
			key = strings.TrimSpace(string(keyData))
		}
	}
	ep := cfg.ExternalLLMEndpoint
	if key != "" && ep != "" {
		return ExternalLLMCredentials{APIKey: key, EndpointURL: ep}, nil
	}

	return ExternalLLMCredentials{}, fmt.Errorf("no external LLM credentials configured")
}

func resolveAnthropicAPIKey(ctx context.Context, queries *db.Queries, enc *crypto.Encryptor, cfg *config.Config, analysis *models.Analysis) (string, error) {
	if queries != nil && enc != nil && analysis != nil && analysis.ProjectID != "" {
		// First, try to get a project-specific API key.
		k, err := queries.GetActiveProviderKey(ctx, analysis.ProjectID, "anthropic")
		if err == nil {
			dek, err := enc.UnwrapDEK(k.EncryptedDEK, k.DEKNonce)
			if err != nil {
				return "", fmt.Errorf("unwrap project provider key DEK: %w", err)
			}
			pt, err := crypto.Decrypt(dek, k.EncryptedKey)
			if err != nil {
				return "", fmt.Errorf("decrypt project provider key: %w", err)
			}
			key := strings.TrimSpace(string(pt))
			if key != "" {
				return key, nil
			}
		}
		if err != nil && err != pgx.ErrNoRows {
			return "", fmt.Errorf("lookup project provider key: %w", err)
		}

		// No project key found. Check if project is allowed to use global key.
		project, err := queries.GetProject(ctx, analysis.ProjectID)
		if err != nil {
			return "", fmt.Errorf("lookup project: %w", err)
		}
		if !project.UsesGlobalKey {
			return "", fmt.Errorf("project %s does not have an API key configured", analysis.ProjectID)
		}
	}

	// Fall back to global key (only if project.UsesGlobalKey is true or no project context).
	if cfg.AgentAPIKeyFile != "" {
		keyData, err := os.ReadFile(cfg.AgentAPIKeyFile)
		if err != nil {
			return "", fmt.Errorf("read agent API key file: %w", err)
		}
		k := strings.TrimSpace(string(keyData))
		if k != "" {
			return k, nil
		}
	}

	if strings.TrimSpace(cfg.AgentAPIKey) != "" {
		return strings.TrimSpace(cfg.AgentAPIKey), nil
	}

	return "", fmt.Errorf("no Anthropic API key configured")
}

// ResolvedProvider holds fully-resolved provider credentials for running an analysis.
type ResolvedProvider struct {
	APISchema string // "anthropic" or "openai"
	BaseURL   string // provider's base URL
	APIKey    string // decrypted API key
	Model     string // model to use (may be empty for auto)
}

// ResolveAnalysisProvider resolves the provider for an analysis from its agent_config.
// If agent_config contains llm_provider_id + provider_source, looks up the provider from DB.
// Returns nil if no explicit provider is configured (caller should fall back to legacy flow).
func ResolveAnalysisProvider(ctx context.Context, queries *db.Queries, enc *crypto.Encryptor, cfg *config.Config, analysis *models.Analysis) (*ResolvedProvider, error) {
	if queries == nil || enc == nil || analysis == nil {
		return nil, nil
	}

	var agentConfig map[string]interface{}
	if len(analysis.AgentConfig) > 0 {
		if err := json.Unmarshal(analysis.AgentConfig, &agentConfig); err != nil {
			return nil, nil
		}
	}
	providerID, _ := agentConfig["llm_provider_id"].(string)
	providerSource, _ := agentConfig["provider_source"].(string)
	if providerID == "" {
		return nil, nil
	}

	switch providerSource {
	case "env":
		// Resolve from environment-configured provider.
		return resolveEnvProvider(providerID, cfg, analysis)

	case "global":
		p, err := queries.GetLLMProvider(ctx, providerID)
		if err != nil {
			return nil, fmt.Errorf("lookup global provider %s: %w", providerID, err)
		}
		if len(p.EncryptedKey) == 0 {
			return nil, fmt.Errorf("global provider %s has no API key configured", providerID)
		}
		dek, err := enc.UnwrapDEK(p.EncryptedDEK, p.DEKNonce)
		if err != nil {
			return nil, fmt.Errorf("unwrap global provider DEK: %w", err)
		}
		pt, err := crypto.Decrypt(dek, p.EncryptedKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt global provider key: %w", err)
		}
		return &ResolvedProvider{
			APISchema: p.APISchema,
			BaseURL:   p.BaseURL,
			APIKey:    strings.TrimSpace(string(pt)),
			Model:     firstNonEmpty(analysis.AgentModel, p.DefaultModel),
		}, nil

	case "project":
		k, err := queries.GetProjectProviderKey(ctx, providerID)
		if err != nil {
			return nil, fmt.Errorf("lookup project provider key %s: %w", providerID, err)
		}
		dek, err := enc.UnwrapDEK(k.EncryptedDEK, k.DEKNonce)
		if err != nil {
			return nil, fmt.Errorf("unwrap project provider key DEK: %w", err)
		}
		pt, err := crypto.Decrypt(dek, k.EncryptedKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt project provider key: %w", err)
		}
		return &ResolvedProvider{
			APISchema: k.APISchema,
			BaseURL:   k.EndpointURL,
			APIKey:    strings.TrimSpace(string(pt)),
			Model:     analysis.AgentModel,
		}, nil

	default:
		return nil, fmt.Errorf("unknown provider_source: %s", providerSource)
	}
}

// firstNonEmpty returns the first non-empty string argument.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolveEnvProvider resolves env-configured provider credentials.
func resolveEnvProvider(providerID string, cfg *config.Config, analysis *models.Analysis) (*ResolvedProvider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("no config available for env provider")
	}

	switch providerID {
	case "env-anthropic":
		// Resolve Anthropic API key from env.
		apiKey := strings.TrimSpace(cfg.AgentAPIKey)
		if apiKey == "" && cfg.AgentAPIKeyFile != "" {
			data, err := os.ReadFile(cfg.AgentAPIKeyFile)
			if err != nil {
				return nil, fmt.Errorf("read agent API key file: %w", err)
			}
			apiKey = strings.TrimSpace(string(data))
		}
		if apiKey == "" {
			return nil, fmt.Errorf("no Anthropic API key configured in environment")
		}
		return &ResolvedProvider{
			APISchema: "anthropic",
			APIKey:    apiKey,
			Model:     firstNonEmpty(analysis.AgentModel, cfg.AgentModel),
		}, nil

	case "env-external":
		// Resolve external LLM API key from env.
		apiKey := strings.TrimSpace(cfg.ExternalLLMAPIKey)
		if apiKey == "" && cfg.ExternalLLMAPIKeyFile != "" {
			data, err := os.ReadFile(cfg.ExternalLLMAPIKeyFile)
			if err != nil {
				return nil, fmt.Errorf("read external LLM API key file: %w", err)
			}
			apiKey = strings.TrimSpace(string(data))
		}
		return &ResolvedProvider{
			APISchema: "openai",
			BaseURL:   cfg.ExternalLLMEndpoint,
			APIKey:    apiKey,
			Model:     firstNonEmpty(analysis.AgentModel, cfg.ExternalLLMAnalysisModel),
		}, nil

	default:
		return nil, fmt.Errorf("unknown env provider: %s", providerID)
	}
}
