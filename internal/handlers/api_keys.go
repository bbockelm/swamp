package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/models"
)

const apiKeyPrefixLen = 8

// RequireAuthOrAPIKey is middleware that accepts either a session cookie or
// an API key in the Authorization header (Bearer token).
func (h *Handler) RequireAuthOrAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try session cookie first (reuse RequireAuth logic inline).
		cookie, err := r.Cookie("swamp_session")
		if err == nil && cookie.Value != "" {
			tokenHash := sha256.Sum256([]byte(cookie.Value))
			sess, err := h.queries.GetSession(r.Context(), tokenHash[:])
			if err == nil {
				user, err := h.queries.GetUser(r.Context(), sess.UserID)
				if err == nil {
					ctx := context.WithValue(r.Context(), sessionContextKey, sess)
					ctx = context.WithValue(ctx, userContextKey, user)
					dbRoles, _ := h.queries.ListUserRoles(ctx, user.ID)
					roleStrs := make([]string, len(dbRoles))
					for i, role := range dbRoles {
						roleStrs[i] = role.Role
					}
					ctx = context.WithValue(ctx, contextKey("user_roles"), roleStrs)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
		}

		// Try API key in Authorization header.
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			if len(token) >= apiKeyPrefixLen {
				prefix := token[:apiKeyPrefixLen]
				apiKey, err := h.queries.GetAPIKeyByPrefix(r.Context(), prefix)
				if err == nil {
					keyHash := sha256.Sum256([]byte(token))
					if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(keyHash[:])), []byte(apiKey.KeyHash)) == 1 {
						_ = h.queries.UpdateAPIKeyLastUsed(r.Context(), apiKey.ID)
						user, err := h.queries.GetUser(r.Context(), apiKey.UserID)
						if err == nil {
							ctx := context.WithValue(r.Context(), userContextKey, user)
							dbRoles, _ := h.queries.ListUserRoles(ctx, user.ID)
							roleStrs := make([]string, len(dbRoles))
							for i, role := range dbRoles {
								roleStrs[i] = role.Role
							}
							ctx = context.WithValue(ctx, contextKey("user_roles"), roleStrs)
							next.ServeHTTP(w, r.WithContext(ctx))
							return
						}
					}
				}
			}
		}

		respondError(w, http.StatusUnauthorized, "Authentication required")
	})
}

// --- API Keys CRUD ---

func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	keys, err := h.queries.ListUserAPIKeys(r.Context(), user.ID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list API keys")
		respondError(w, http.StatusInternalServerError, "Failed to list API keys")
		return
	}
	respondJSON(w, http.StatusOK, keys)
}

func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var req struct {
		Name      string `json:"name"`
		ExpiresIn string `json:"expires_in"` // e.g. "30d", "90d", "never"
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "Key name is required")
		return
	}

	// Generate random API key.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate key")
		return
	}
	token := hex.EncodeToString(raw)
	prefix := token[:apiKeyPrefixLen]
	keyHash := sha256.Sum256([]byte(token))

	apiKey := &models.APIKey{
		Name:      req.Name,
		KeyHash:   hex.EncodeToString(keyHash[:]),
		KeyPrefix: prefix,
		UserID:    user.ID,
	}

	// Parse expiration.
	switch req.ExpiresIn {
	case "30d":
		exp := time.Now().Add(30 * 24 * time.Hour)
		apiKey.ExpiresAt = &exp
	case "90d":
		exp := time.Now().Add(90 * 24 * time.Hour)
		apiKey.ExpiresAt = &exp
	case "365d", "1y":
		exp := time.Now().Add(365 * 24 * time.Hour)
		apiKey.ExpiresAt = &exp
	case "never", "":
		// No expiration
	default:
		respondError(w, http.StatusBadRequest, "Invalid expires_in value")
		return
	}

	if err := h.queries.CreateAPIKey(r.Context(), apiKey); err != nil {
		log.Error().Err(err).Msg("Failed to create API key")
		respondError(w, http.StatusInternalServerError, "Failed to create API key")
		return
	}

	// Return the full token only once; it's never stored.
	respondJSON(w, http.StatusCreated, map[string]any{
		"id":         apiKey.ID,
		"name":       apiKey.Name,
		"key":        token,
		"key_prefix": apiKey.KeyPrefix,
		"expires_at": apiKey.ExpiresAt,
		"created_at": apiKey.CreatedAt,
	})
}

func (h *Handler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	// Verify ownership unless admin.
	if !UserHasRole(r.Context(), RoleAdmin) {
		keys, err := h.queries.ListUserAPIKeys(r.Context(), user.ID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to verify key ownership")
			return
		}
		found := false
		for _, k := range keys {
			if k.ID == keyID {
				found = true
				break
			}
		}
		if !found {
			respondError(w, http.StatusForbidden, "Not your API key")
			return
		}
	}

	if err := h.queries.RevokeAPIKey(r.Context(), keyID); err != nil {
		log.Error().Err(err).Msg("Failed to revoke API key")
		respondError(w, http.StatusInternalServerError, "Failed to revoke API key")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
