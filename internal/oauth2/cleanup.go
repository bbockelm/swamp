package oauth2

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// StartCleanupLoop starts a background goroutine that periodically:
// 1. Deletes dynamically registered clients that are older than unusedMaxAge
//    and have never been used to authenticate (last_used_at IS NULL).
// 2. Removes expired tokens from all token tables.
// 3. Cleans up stale rate limiter buckets.
func (h *Handlers) StartCleanupLoop(ctx context.Context, unusedMaxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Delete unused dynamic clients.
				n, err := h.provider.Storage.DeleteUnusedDynamicClients(ctx, unusedMaxAge)
				if err != nil {
					log.Error().Err(err).Msg("OAuth2 cleanup: failed to delete unused dynamic clients")
				} else if n > 0 {
					log.Info().Int64("count", n).Msg("OAuth2 cleanup: deleted unused dynamically registered clients")
				}

				// Purge expired tokens.
				if err := h.provider.Storage.CleanupExpiredTokens(ctx); err != nil {
					log.Error().Err(err).Msg("OAuth2 cleanup: failed to purge expired tokens")
				}

				// Clean up stale rate limiter buckets (older than 1 hour).
				h.dcrLimiter.Cleanup(1 * time.Hour)

			case <-ctx.Done():
				return
			}
		}
	}()
}
