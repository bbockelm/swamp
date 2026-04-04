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

func resolveAnthropicAPIKey(ctx context.Context, queries *db.Queries, enc *crypto.Encryptor, cfg *config.Config, analysis *models.Analysis) (string, error) {
	if queries != nil && enc != nil && analysis != nil && analysis.ProjectID != "" {
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
	}

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

	return "", fmt.Errorf("no Anthropic API key configured for project %s", analysis.ProjectID)
}
