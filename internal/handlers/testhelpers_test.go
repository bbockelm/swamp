package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/internal/storage"

	"net/http"
)

// testInstanceKeyHex is a fixed 32-byte hex key used by all handler tests.
const testInstanceKeyHex = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// testDB opens a *pgxpool.Pool from DATABASE_URL and skips the test if the
// environment variable is not set. The pool is closed via t.Cleanup.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("testDB: connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// memStore is an in-memory implementation of storage.Storer used for tests.
type memStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{objects: make(map[string][]byte)}
}

func (m *memStore) Upload(_ context.Context, key string, reader io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.objects[key] = data
	m.mu.Unlock()
	return nil
}

func (m *memStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	data, ok := m.objects[key]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("memStore: key not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.objects, key)
	m.mu.Unlock()
	return nil
}

func (m *memStore) GenerateKey(analysisID, filename string) string {
	return fmt.Sprintf("analyses/%s/%s", analysisID, filename)
}

func (m *memStore) ListKeys(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// Verify memStore satisfies the Storer interface at compile time.
var _ storage.Storer = (*memStore)(nil)

// newTestHandler constructs a Handler suitable for integration tests.
// The encryptor uses testInstanceKeyHex; executor, backupSvc, and ghClient
// are left nil.
func newTestHandler(t *testing.T, pool *pgxpool.Pool, store storage.Storer) *Handler {
	t.Helper()
	enc, err := crypto.NewEncryptor(testInstanceKeyHex)
	if err != nil {
		t.Fatalf("newTestHandler: encryptor: %v", err)
	}
	return New(&config.Config{}, db.NewQueries(pool), store, enc)
}

// insertTestUser inserts a user row and registers a cleanup that deletes it.
func insertTestUser(t *testing.T, pool *pgxpool.Pool) *models.User {
	t.Helper()
	q := db.NewQueries(pool)
	u := &models.User{
		DisplayName: "Test User",
		Email:       fmt.Sprintf("test-%d@example.com", time.Now().UnixNano()),
		Status:      "active",
	}
	if err := q.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("insertTestUser: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", u.ID)
	})
	return u
}

// insertTestProject inserts a project owned by ownerID and registers cleanup.
func insertTestProject(t *testing.T, pool *pgxpool.Pool, ownerID string) *models.Project {
	t.Helper()
	q := db.NewQueries(pool)
	p := &models.Project{
		Name:        fmt.Sprintf("test-project-%d", time.Now().UnixNano()),
		Description: "Integration test project",
		OwnerID:     ownerID,
		Status:      "active",
	}
	if err := q.CreateProject(context.Background(), p); err != nil {
		t.Fatalf("insertTestProject: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID)
	})
	return p
}

// insertTestAnalysis inserts an analysis row with the given status and owner.
// For "importing" status it uses CreateExternalAnalysis; for other statuses it
// uses CreateAnalysis and then patches the status directly.
func insertTestAnalysis(t *testing.T, pool *pgxpool.Pool, projectID, userID, status string) *models.Analysis {
	t.Helper()
	enc, err := crypto.NewEncryptor(testInstanceKeyHex)
	if err != nil {
		t.Fatalf("insertTestAnalysis: encryptor: %v", err)
	}
	dek, err := crypto.GenerateDEK()
	if err != nil {
		t.Fatalf("insertTestAnalysis: GenerateDEK: %v", err)
	}
	encDEK, nonce, err := enc.WrapDEK(dek)
	if err != nil {
		t.Fatalf("insertTestAnalysis: WrapDEK: %v", err)
	}

	q := db.NewQueries(pool)
	a := &models.Analysis{
		ProjectID:    projectID,
		TriggeredBy:  userID,
		Status:       status,
		Environment:  "import",
		TriggerEvent: "manual",
		EncryptedDEK: encDEK,
		DEKNonce:     nonce,
	}

	if status == "importing" {
		if err := q.CreateExternalAnalysis(context.Background(), a); err != nil {
			t.Fatalf("insertTestAnalysis (importing): %v", err)
		}
	} else {
		a.Status = status
		if err := q.CreateAnalysis(context.Background(), a); err != nil {
			t.Fatalf("insertTestAnalysis: %v", err)
		}
		// Patch the status to the requested value (CreateAnalysis hard-codes "pending").
		if status != "pending" {
			if _, err := pool.Exec(context.Background(),
				"UPDATE analyses SET status=$2 WHERE id=$1", a.ID, status); err != nil {
				t.Fatalf("insertTestAnalysis: patch status: %v", err)
			}
			a.Status = status
		}
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM analysis_results WHERE analysis_id=$1", a.ID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM analyses WHERE id=$1", a.ID)
	})
	return a
}

// insertTestAPIKey inserts an API key row for userID and returns the plaintext
// token. The caller can use it in an "Authorization: Bearer <token>" header.
func insertTestAPIKey(t *testing.T, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("insertTestAPIKey: rand: %v", err)
	}
	token := hex.EncodeToString(raw)
	prefix := token[:apiKeyPrefixLen]
	keyHash := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(keyHash[:])

	q := db.NewQueries(pool)
	k := &models.APIKey{
		Name:      "test-key",
		KeyHash:   hashHex,
		KeyPrefix: prefix,
		UserID:    userID,
	}
	if err := q.CreateAPIKey(context.Background(), k); err != nil {
		t.Fatalf("insertTestAPIKey: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM api_keys WHERE id=$1", k.ID)
	})
	return token
}

// withUser injects a user into the request context, simulating what the
// RequireAuth / RequireAuthOrAPIKey middleware does.
func withUser(r *http.Request, user *models.User) *http.Request {
	ctx := context.WithValue(r.Context(), userContextKey, user)
	return r.WithContext(ctx)
}

// withChiParams injects chi URL parameters into the request context.
func withChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
