package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/rs/zerolog/log"
)

// WorkerTokenStore manages one-time tokens for worker pod authentication.
// Tokens are stored in memory and expire after a TTL. Each token can only
// be exchanged once for a session credential.
//
// Session and proxy tokens are persisted to the database (as SHA-256
// hashes — cleartext tokens never reach the DB) so they survive server
// restarts regardless of executor mode.
type WorkerTokenStore struct {
	mu          sync.Mutex
	tokens      map[string]*workerTokenEntry // keyed by token hash (in-memory only)
	sessions    map[string]*WorkerSession    // keyed by session token hash (cached from DB)
	proxyTokens map[string]string            // keyed by proxy token hash → analysisID (cached from DB)
	lastUsed    map[string]time.Time         // keyed by token hash → last DB touch time (debounce)
	queries     *db.Queries                  // nil = no persistence (tests / local executor)
}

const (
	// touchDebounce is the minimum interval between DB last_used_at updates
	// for the same token. Reduces write load during bursts of proxy requests.
	touchDebounce = 5 * time.Minute

	// staleTokenAge is how long since last use before a token is considered
	// stale and eligible for cleanup.
	staleTokenAge = 24 * time.Hour
)

type workerTokenEntry struct {
	analysisID      string
	packages        []models.SoftwarePackage
	agentModel      string
	proxyURL        string // Anthropic API proxy URL on the SWAMP server
	customPrompt    string
	analysisContext *models.AnalysisContext
	createdAt       time.Time
	ttl             time.Duration
	used            bool
}

// WorkerSession is the session created after a successful token exchange.
type WorkerSession struct {
	AnalysisID   string                   `json:"analysis_id"`
	Packages     []models.SoftwarePackage `json:"packages"`
	AgentModel   string                   `json:"agent_model,omitempty"`
	CustomPrompt string                   `json:"custom_prompt,omitempty"`
	CreatedAt    time.Time                `json:"created_at"`
}

// WorkerExchangeResponse is returned to the worker after token exchange.
type WorkerExchangeResponse struct {
	SessionToken    string                 `json:"session_token"`
	ProxyToken      string                 `json:"proxy_token"` // separate credential for Anthropic proxy only
	AnalysisID      string                 `json:"analysis_id"`
	Packages        []workerPackageInfo    `json:"packages"`
	AgentModel      string                 `json:"agent_model,omitempty"`
	ProxyURL        string                 `json:"proxy_url"`
	CustomPrompt    string                 `json:"custom_prompt,omitempty"`
	AnalysisContext *models.AnalysisContext `json:"analysis_context,omitempty"`
}

type workerPackageInfo struct {
	Name           string `json:"name"`
	GitURL         string `json:"git_url"`
	GitBranch      string `json:"git_branch"`
	GitCommit      string `json:"git_commit"`
	AnalysisPrompt string `json:"analysis_prompt"`
}

// NewWorkerTokenStore creates a new token store without DB persistence.
func NewWorkerTokenStore() *WorkerTokenStore {
	return &WorkerTokenStore{
		tokens:      make(map[string]*workerTokenEntry),
		sessions:    make(map[string]*WorkerSession),
		proxyTokens: make(map[string]string),
		lastUsed:    make(map[string]time.Time),
	}
}

// NewWorkerTokenStoreWithDB creates a token store backed by the database.
// Existing sessions and proxy tokens are loaded from the DB on creation.
func NewWorkerTokenStoreWithDB(queries *db.Queries) *WorkerTokenStore {
	s := &WorkerTokenStore{
		tokens:      make(map[string]*workerTokenEntry),
		sessions:    make(map[string]*WorkerSession),
		proxyTokens: make(map[string]string),
		lastUsed:    make(map[string]time.Time),
		queries:     queries,
	}
	s.loadFromDB()
	return s
}

// IssueToken creates a one-time token for a worker pod.
// Returns the raw token (to be injected as env var).
// proxyURL is the Anthropic API proxy endpoint on the SWAMP server;
// the real API key never leaves this process.
func (s *WorkerTokenStore) IssueToken(analysisID string, packages []models.SoftwarePackage, agentModel, proxyURL, customPrompt string, analysisCtx *models.AnalysisContext, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating worker token: %w", err)
	}
	token := hex.EncodeToString(raw)
	hash := hashToken(token)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[hash] = &workerTokenEntry{
		analysisID:      analysisID,
		packages:        packages,
		agentModel:      agentModel,
		proxyURL:        proxyURL,
		customPrompt:    customPrompt,
		analysisContext: analysisCtx,
		createdAt:       time.Now(),
		ttl:             ttl,
	}
	return token, nil
}

// ExchangeToken validates and consumes a one-time token, returning a session.
// The token is invalidated after use. Returns the exchange response or an error.
func (s *WorkerTokenStore) ExchangeToken(token string) (*WorkerExchangeResponse, error) {
	hash := hashToken(token)

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.tokens[hash]
	if !ok {
		return nil, fmt.Errorf("invalid or expired worker token")
	}
	if entry.used {
		return nil, fmt.Errorf("worker token already used")
	}
	if time.Since(entry.createdAt) > entry.ttl {
		delete(s.tokens, hash)
		return nil, fmt.Errorf("worker token expired")
	}

	// Mark token as used and remove it.
	entry.used = true
	delete(s.tokens, hash)

	// Generate session token.
	sessionRaw := make([]byte, 32)
	if _, err := rand.Read(sessionRaw); err != nil {
		return nil, fmt.Errorf("generating session token: %w", err)
	}
	sessionToken := hex.EncodeToString(sessionRaw)
	sessionHash := hashToken(sessionToken)

	session := &WorkerSession{
		AnalysisID:   entry.analysisID,
		Packages:     entry.packages,
		AgentModel:   entry.agentModel,
		CustomPrompt: entry.customPrompt,
		CreatedAt:    time.Now(),
	}
	s.sessions[sessionHash] = session

	// Generate a separate proxy token used only for Anthropic API proxy
	// authentication. This token is set as ANTHROPIC_API_KEY in the worker
	// env and is intentionally separate from the session token so that
	// even if Claude reads it, it cannot call other worker endpoints.
	proxyRaw := make([]byte, 32)
	if _, err := rand.Read(proxyRaw); err != nil {
		return nil, fmt.Errorf("generating proxy token: %w", err)
	}
	proxyToken := hex.EncodeToString(proxyRaw)
	proxyHash := hashToken(proxyToken)
	s.proxyTokens[proxyHash] = entry.analysisID

	// Persist to DB (write-through cache).
	s.persistToken(sessionHash, "session", entry.analysisID, session)
	s.persistToken(proxyHash, "proxy", entry.analysisID, nil)

	// Build package info for the response.
	pkgInfos := make([]workerPackageInfo, len(entry.packages))
	for i, p := range entry.packages {
		pkgInfos[i] = workerPackageInfo{
			Name:           p.Name,
			GitURL:         p.GitURL,
			GitBranch:      p.GitBranch,
			GitCommit:      p.GitCommit,
			AnalysisPrompt: p.AnalysisPrompt,
		}
	}

	return &WorkerExchangeResponse{
		SessionToken:    sessionToken,
		ProxyToken:      proxyToken,
		AnalysisID:      entry.analysisID,
		Packages:        pkgInfos,
		AgentModel:      entry.agentModel,
		ProxyURL:        entry.proxyURL,
		CustomPrompt:    entry.customPrompt,
		AnalysisContext: entry.analysisContext,
	}, nil
}

// ValidateSession checks a session token and returns the associated session.
func (s *WorkerTokenStore) ValidateSession(sessionToken string) (*WorkerSession, error) {
	hash := hashToken(sessionToken)

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[hash]
	if !ok {
		return nil, fmt.Errorf("invalid worker session")
	}
	s.touchToken(hash)
	return session, nil
}

// RevokeSession removes a worker session (called when analysis completes).
func (s *WorkerTokenStore) RevokeSession(sessionToken string) {
	hash := hashToken(sessionToken)

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, hash)
	delete(s.lastUsed, hash)
	s.deleteToken(hash)
}

// ValidateProxyToken checks a proxy token and returns the associated analysis ID.
// Proxy tokens are separate from session tokens and can only be used for
// Anthropic API proxy requests.
func (s *WorkerTokenStore) ValidateProxyToken(proxyToken string) (string, error) {
	hash := hashToken(proxyToken)

	s.mu.Lock()
	defer s.mu.Unlock()

	analysisID, ok := s.proxyTokens[hash]
	if !ok {
		return "", fmt.Errorf("invalid proxy token")
	}
	s.touchToken(hash)
	return analysisID, nil
}

// RevokeAnalysis removes all tokens and sessions for a given analysis ID.
func (s *WorkerTokenStore) RevokeAnalysis(analysisID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for hash, entry := range s.tokens {
		if entry.analysisID == analysisID {
			delete(s.tokens, hash)
		}
	}
	for hash, session := range s.sessions {
		if session.AnalysisID == analysisID {
			delete(s.sessions, hash)
			delete(s.lastUsed, hash)
		}
	}
	for hash, id := range s.proxyTokens {
		if id == analysisID {
			delete(s.proxyTokens, hash)
			delete(s.lastUsed, hash)
		}
	}
	s.deleteTokensByAnalysis(analysisID)
}

// CleanupExpired removes expired one-time tokens from memory and stale
// session/proxy tokens from both the in-memory cache and the database.
// Call periodically (e.g. every 30s–60s).
func (s *WorkerTokenStore) CleanupExpired() {
	s.mu.Lock()

	now := time.Now()
	for hash, entry := range s.tokens {
		if now.Sub(entry.createdAt) > entry.ttl {
			delete(s.tokens, hash)
		}
	}

	s.mu.Unlock()

	// Clean stale tokens from DB. This also catches tokens from previous
	// server instances that were never cleaned up (crashes, bugs, etc.).
	if s.queries == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n, err := s.queries.DeleteStaleWorkerTokens(ctx, staleTokenAge)
	if err != nil {
		log.Error().Err(err).Msg("Failed to delete stale worker tokens from DB")
		return
	}
	if n > 0 {
		log.Info().Int64("count", n).Msg("Cleaned up stale worker tokens from DB")
		// Reload the in-memory cache to reflect DB deletions.
		s.mu.Lock()
		s.sessions = make(map[string]*WorkerSession)
		s.proxyTokens = make(map[string]string)
		s.mu.Unlock()
		s.loadFromDB()
	}
}

// MarshalExchangeResponse serializes the exchange response.
func MarshalExchangeResponse(resp *WorkerExchangeResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// touchToken updates the last_used_at timestamp for a token in the database,
// debounced to avoid excessive writes. Caller must hold s.mu.
func (s *WorkerTokenStore) touchToken(hash string) {
	if s.queries == nil {
		return
	}
	now := time.Now()
	if last, ok := s.lastUsed[hash]; ok && now.Sub(last) < touchDebounce {
		return
	}
	s.lastUsed[hash] = now
	// Fire-and-forget DB update outside the lock.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.queries.TouchWorkerToken(ctx, hash); err != nil {
			log.Debug().Err(err).Msg("Failed to touch worker token last_used_at")
		}
	}()
}

// persistToken writes a session or proxy token hash to the database.
func (s *WorkerTokenStore) persistToken(tokenHash, tokenType, analysisID string, session *WorkerSession) {
	if s.queries == nil {
		return
	}
	var data []byte
	if session != nil {
		var err error
		data, err = json.Marshal(session)
		if err != nil {
			log.Error().Err(err).Msg("Failed to marshal worker session for DB")
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.queries.CreateWorkerToken(ctx, tokenHash, tokenType, analysisID, data); err != nil {
		log.Error().Err(err).Str("type", tokenType).Msg("Failed to persist worker token to DB")
	}
}

// deleteToken removes a single token hash from the database.
func (s *WorkerTokenStore) deleteToken(tokenHash string) {
	if s.queries == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.queries.DeleteWorkerToken(ctx, tokenHash); err != nil {
		log.Error().Err(err).Msg("Failed to delete worker token from DB")
	}
}

// deleteTokensByAnalysis removes all tokens for an analysis from the database.
func (s *WorkerTokenStore) deleteTokensByAnalysis(analysisID string) {
	if s.queries == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.queries.DeleteWorkerTokensByAnalysis(ctx, analysisID); err != nil {
		log.Error().Err(err).Msg("Failed to delete worker tokens from DB")
	}
}

// loadFromDB loads all session and proxy tokens from the database into memory.
func (s *WorkerTokenStore) loadFromDB() {
	if s.queries == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionRows, proxyTokens, err := s.queries.LoadAllWorkerTokens(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to load worker tokens from DB")
		return
	}

	for hash, data := range sessionRows {
		var ws WorkerSession
		if err := json.Unmarshal(data, &ws); err != nil {
			log.Warn().Err(err).Str("hash", hash[:8]+"...").Msg("Skipping corrupt worker session")
			continue
		}
		s.sessions[hash] = &ws
	}
	s.proxyTokens = proxyTokens

	log.Info().
		Int("sessions", len(s.sessions)).
		Int("proxy_tokens", len(s.proxyTokens)).
		Msg("Loaded worker tokens from database")
}
