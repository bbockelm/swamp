package agent

import (
	"context"
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
		Provider:      cfg.AgentProvider,
		AnalysisModel: cfg.ExternalLLMAnalysisModel,
		PoCModel:      cfg.ExternalLLMPoCModel,
		Fallback:      cfg.ExternalLLMFallback,
	}
	if project != nil {
		if project.AgentProvider != nil && *project.AgentProvider != "" {
			result.Provider = *project.AgentProvider
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
			return ExternalLLMCredentials{}, fmt.Errorf("read external LLM API key file: %w", err)
		}
		key = strings.TrimSpace(string(keyData))
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

// resolveExternalLLMAPIKey determines which external LLM API key to use for an analysis.
// Resolution order:
//  1. Project-specific key stored encrypted in the database (provider = "external_llm").
//  2. Global ExternalLLMAPIKey / ExternalLLMAPIKeyFile from config (if project allows global key).
func resolveExternalLLMAPIKey(ctx context.Context, queries *db.Queries, enc *crypto.Encryptor, cfg *config.Config, analysis *models.Analysis) (string, error) {
	if queries != nil && enc != nil && analysis != nil && analysis.ProjectID != "" {
		k, err := queries.GetActiveProviderKey(ctx, analysis.ProjectID, "external_llm")
		if err == nil {
			dek, err := enc.UnwrapDEK(k.EncryptedDEK, k.DEKNonce)
			if err != nil {
				return "", fmt.Errorf("unwrap project external LLM key DEK: %w", err)
			}
			pt, err := crypto.Decrypt(dek, k.EncryptedKey)
			if err != nil {
				return "", fmt.Errorf("decrypt project external LLM key: %w", err)
			}
			key := strings.TrimSpace(string(pt))
			if key != "" {
				return key, nil
			}
		}
		if err != nil && err != pgx.ErrNoRows {
			return "", fmt.Errorf("lookup project external LLM key: %w", err)
		}

		// No project key found. Check if global key is allowed.
		project, err := queries.GetProject(ctx, analysis.ProjectID)
		if err != nil {
			return "", fmt.Errorf("lookup project: %w", err)
		}
		if !project.UsesGlobalKey {
			return "", fmt.Errorf("project %s does not have an external LLM API key configured", analysis.ProjectID)
		}
	}

	// Fall back to global external LLM key.
	if cfg.ExternalLLMAPIKey != "" {
		return strings.TrimSpace(cfg.ExternalLLMAPIKey), nil
	}
	if cfg.ExternalLLMAPIKeyFile != "" {
		keyData, err := os.ReadFile(cfg.ExternalLLMAPIKeyFile)
		if err != nil {
			return "", fmt.Errorf("read external LLM API key file: %w", err)
		}
		k := strings.TrimSpace(string(keyData))
		if k != "" {
			return k, nil
		}
	}

	return "", fmt.Errorf("no external LLM API key configured")
}
