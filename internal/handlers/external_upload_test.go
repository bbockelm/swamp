package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbockelm/swamp/internal/models"
)

// minimalSARIF is a minimal valid SARIF 2.1.0 document.
const minimalSARIF = `{"runs":[]}`

// sarifWithFindings is a SARIF document containing one result.
const sarifWithFindings = `{
  "runs": [{
    "results": [{
      "ruleId": "CWE-89",
      "level": "error",
      "message": {"text": "SQL injection"},
      "locations": [{
        "physicalLocation": {
          "artifactLocation": {"uri": "src/db.go"},
          "region": {
            "startLine": 42,
            "endLine": 42,
            "snippet": {"text": "query(input)"}
          }
        }
      }]
    }]
  }]
}`

// testRouter builds a chi.Mux that wraps RequireAuthOrAPIKey and
// RequireProjectAccess(accessLevel) around a single handler method.
// URL pattern: /projects/{projectID}<subpath>
func testRouter(
	h *Handler,
	accessLevel, subpath, method string,
	handlerFn http.HandlerFunc,
) http.Handler {
	r := chi.NewRouter()
	r.Route("/projects/{projectID}", func(r chi.Router) {
		r.Use(h.RequireAuthOrAPIKey)
		r.Group(func(r chi.Router) {
			r.Use(h.RequireProjectAccess(accessLevel))
			r.Method(method, subpath, handlerFn)
		})
	})
	return r
}

// makeMultipart builds a multipart/form-data body with a "filename" field and
// a "file" part, returning the body and its Content-Type header value.
func makeMultipart(t *testing.T, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("filename", filename)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("makeMultipart: CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("makeMultipart: Write: %v", err)
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

// uploadResult is a helper that calls UploadAnalysisResult on the given pool
// and returns the HTTP status code.
func uploadResult(
	t *testing.T,
	pool *pgxpool.Pool,
	store *memStore,
	user *models.User,
	project *models.Project,
	analysis *models.Analysis,
	filename string,
	data []byte,
) int {
	t.Helper()
	h := newTestHandler(t, pool, store)
	body, ct := makeMultipart(t, filename, data)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", ct)
	req = withChiParams(req, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.UploadAnalysisResult(rr, req)
	return rr.Code
}

// ---- CreateExternalAnalysis ----

func TestCreateExternalAnalysis_Unauthenticated(t *testing.T) {
	pool := testDB(t)
	h := newTestHandler(t, pool, newMemStore())
	project := insertTestProject(t, pool, insertTestUser(t, pool).ID)

	srv := httptest.NewServer(testRouter(
		h, "write", "/analyses/external", http.MethodPost,
		h.CreateExternalAnalysis))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{})
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/projects/%s/analyses/external", srv.URL, project.ID),
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestCreateExternalAnalysis_Success(t *testing.T) {
	pool := testDB(t)
	h := newTestHandler(t, pool, newMemStore())
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)

	body, _ := json.Marshal(map[string]any{
		"git_commit":    "abc123",
		"environment":   "ci",
		"status_detail": "uploaded from CI",
		"trigger_meta":  map[string]string{"repo": "myrepo"},
	})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"projectID": project.ID})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.CreateExternalAnalysis(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var analysis models.Analysis
	if err := json.NewDecoder(rr.Body).Decode(&analysis); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if analysis.ID == "" {
		t.Error("expected analysis.ID to be set")
	}
	if analysis.Status != "importing" {
		t.Errorf("expected status=importing, got %s", analysis.Status)
	}
	if analysis.GitCommit != "abc123" {
		t.Errorf("expected git_commit=abc123, got %s", analysis.GitCommit)
	}
	if analysis.Environment != "ci" {
		t.Errorf("expected environment=ci, got %s", analysis.Environment)
	}
}

func TestCreateExternalAnalysis_DefaultEnvironment(t *testing.T) {
	pool := testDB(t)
	h := newTestHandler(t, pool, newMemStore())
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)

	body, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"projectID": project.ID})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.CreateExternalAnalysis(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var analysis models.Analysis
	_ = json.NewDecoder(rr.Body).Decode(&analysis)
	if analysis.Environment != "import" {
		t.Errorf("expected default environment=import, got %s", analysis.Environment)
	}
}

func TestCreateExternalAnalysis_InvalidTriggerMeta(t *testing.T) {
	pool := testDB(t)
	h := newTestHandler(t, pool, newMemStore())
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)

	// trigger_meta must be a JSON object, not an array.
	body := []byte(`{"trigger_meta": [1,2,3]}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"projectID": project.ID})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.CreateExternalAnalysis(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-object trigger_meta, got %d", rr.Code)
	}
}

func TestCreateExternalAnalysis_APIKey(t *testing.T) {
	pool := testDB(t)
	h := newTestHandler(t, pool, newMemStore())
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	token := insertTestAPIKey(t, pool, user.ID)

	srv := httptest.NewServer(testRouter(
		h, "write", "/analyses/external", http.MethodPost,
		h.CreateExternalAnalysis))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{})
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/projects/%s/analyses/external", srv.URL, project.ID),
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 with API key auth, got %d", resp.StatusCode)
	}
}

// ---- UploadAnalysisResult ----

func TestUploadAnalysisResult_WrongStatus(t *testing.T) {
	pool := testDB(t)
	store := newMemStore()
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "completed")

	code := uploadResult(t, pool, store, user, project, analysis,
		"report.md", []byte("# report"))
	if code != http.StatusConflict {
		t.Errorf("expected 409 for non-importing analysis, got %d", code)
	}
}

func TestUploadAnalysisResult_MarkdownFile(t *testing.T) {
	pool := testDB(t)
	store := newMemStore()
	h := newTestHandler(t, pool, store)
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "importing")

	body, ct := makeMultipart(t, "report.md", []byte("# Analysis Report\n\nNo issues found."))
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", ct)
	req = withChiParams(req, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.UploadAnalysisResult(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Result struct {
			Filename string `json:"filename"`
		} `json:"result"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Result.Filename != "report.md" {
		t.Errorf("expected filename=report.md, got %q", resp.Result.Filename)
	}
}

func TestUploadAnalysisResult_SARIFFile(t *testing.T) {
	pool := testDB(t)
	store := newMemStore()
	h := newTestHandler(t, pool, store)
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "importing")

	body, ct := makeMultipart(t, "results.sarif", []byte(sarifWithFindings))
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", ct)
	req = withChiParams(req, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.UploadAnalysisResult(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Findings []any `json:"findings"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(resp.Findings))
	}
}

func TestUploadAnalysisResult_InvalidSARIF(t *testing.T) {
	pool := testDB(t)
	h := newTestHandler(t, pool, newMemStore())
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "importing")

	body, ct := makeMultipart(t, "bad.sarif", []byte("not json at all"))
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", ct)
	req = withChiParams(req, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.UploadAnalysisResult(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid SARIF, got %d", rr.Code)
	}
}

func TestUploadAnalysisResult_DuplicateFilename(t *testing.T) {
	pool := testDB(t)
	store := newMemStore()
	h := newTestHandler(t, pool, store)
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "importing")

	do := func() int {
		body, ct := makeMultipart(t, "report.md", []byte("# report"))
		req := httptest.NewRequest(http.MethodPost, "/", body)
		req.Header.Set("Content-Type", ct)
		req = withChiParams(req, map[string]string{
			"projectID":  project.ID,
			"analysisID": analysis.ID,
		})
		req = withUser(req, user)
		rr := httptest.NewRecorder()
		h.UploadAnalysisResult(rr, req)
		return rr.Code
	}

	if code := do(); code != http.StatusCreated {
		t.Fatalf("first upload: expected 201, got %d", code)
	}
	if code := do(); code != http.StatusConflict {
		t.Errorf("duplicate upload: expected 409, got %d", code)
	}
}

func TestUploadAnalysisResult_APIKey(t *testing.T) {
	pool := testDB(t)
	store := newMemStore()
	h := newTestHandler(t, pool, store)
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "importing")
	token := insertTestAPIKey(t, pool, user.ID)

	srv := httptest.NewServer(testRouter(
		h, "read", "/analyses/{analysisID}/results", http.MethodPost,
		h.UploadAnalysisResult))
	defer srv.Close()

	body, ct := makeMultipart(t, "notes.md", []byte("some notes"))
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/projects/%s/analyses/%s/results",
			srv.URL, project.ID, analysis.ID),
		body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 with API key auth, got %d", resp.StatusCode)
	}
}

// ---- CompleteAnalysis ----

func TestCompleteAnalysis_NoResults(t *testing.T) {
	pool := testDB(t)
	h := newTestHandler(t, pool, newMemStore())
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "importing")

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
	req = withChiParams(req, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.CompleteAnalysis(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when no results uploaded, got %d", rr.Code)
	}
}

func TestCompleteAnalysis_WrongStatus(t *testing.T) {
	pool := testDB(t)
	h := newTestHandler(t, pool, newMemStore())
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "completed")

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
	req = withChiParams(req, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.CompleteAnalysis(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("expected 409 for non-importing analysis, got %d", rr.Code)
	}
}

func TestCompleteAnalysis_Success(t *testing.T) {
	pool := testDB(t)
	store := newMemStore()
	h := newTestHandler(t, pool, store)
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "importing")

	// Upload one result so CompleteAnalysis sees a non-zero count.
	uploadBody, uploadCT := makeMultipart(t, "report.md", []byte("# report"))
	uploadReq := httptest.NewRequest(http.MethodPost, "/", uploadBody)
	uploadReq.Header.Set("Content-Type", uploadCT)
	uploadReq = withChiParams(uploadReq, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	uploadReq = withUser(uploadReq, user)
	uploadRR := httptest.NewRecorder()
	h.UploadAnalysisResult(uploadRR, uploadReq)
	if uploadRR.Code != http.StatusCreated {
		t.Fatalf("setup: upload failed with %d: %s", uploadRR.Code, uploadRR.Body.String())
	}

	// Now complete the analysis.
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
	req = withChiParams(req, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.CompleteAnalysis(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "completed" {
		t.Errorf("expected status=completed, got %s", resp["status"])
	}
}

func TestCompleteAnalysis_APIKey(t *testing.T) {
	pool := testDB(t)
	store := newMemStore()
	h := newTestHandler(t, pool, store)
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "importing")
	token := insertTestAPIKey(t, pool, user.ID)

	// Upload one result via direct handler call.
	uploadBody, uploadCT := makeMultipart(t, "report.md", []byte("# report"))
	uploadReq := httptest.NewRequest(http.MethodPost, "/", uploadBody)
	uploadReq.Header.Set("Content-Type", uploadCT)
	uploadReq = withChiParams(uploadReq, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	uploadReq = withUser(uploadReq, user)
	uploadRR := httptest.NewRecorder()
	h.UploadAnalysisResult(uploadRR, uploadReq)
	if uploadRR.Code != http.StatusCreated {
		t.Fatalf("setup upload: %d", uploadRR.Code)
	}

	// Complete via test server with API key auth.
	srv := httptest.NewServer(testRouter(
		h, "read", "/analyses/{analysisID}/complete", http.MethodPost,
		h.CompleteAnalysis))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/projects/%s/analyses/%s/complete",
			srv.URL, project.ID, analysis.ID),
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with API key auth, got %d", resp.StatusCode)
	}
}

// ---- Worker idempotency (GetAnalysisResultByFilename) ----

// TestWorkerIdempotency_QueryReturnsExistingRow verifies that after uploading
// a result, GetAnalysisResultByFilename returns the same row — which is the
// foundational check powering the worker idempotency pre-check.
func TestWorkerIdempotency_QueryReturnsExistingRow(t *testing.T) {
	pool := testDB(t)
	store := newMemStore()
	h := newTestHandler(t, pool, store)
	user := insertTestUser(t, pool)
	project := insertTestProject(t, pool, user.ID)
	analysis := insertTestAnalysis(t, pool, project.ID, user.ID, "importing")

	// Upload a result.
	body, ct := makeMultipart(t, "notes.md", []byte("some notes"))
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", ct)
	req = withChiParams(req, map[string]string{
		"projectID":  project.ID,
		"analysisID": analysis.ID,
	})
	req = withUser(req, user)
	rr := httptest.NewRecorder()
	h.UploadAnalysisResult(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// GetAnalysisResultByFilename should return the row we just inserted.
	row, err := h.queries.GetAnalysisResultByFilename(
		context.Background(), analysis.ID, "notes.md")
	if err != nil {
		t.Fatalf("GetAnalysisResultByFilename: %v", err)
	}
	if row.Filename != "notes.md" {
		t.Errorf("expected filename=notes.md, got %q", row.Filename)
	}
	if row.AnalysisID != analysis.ID {
		t.Errorf("expected analysis_id=%s, got %s", analysis.ID, row.AnalysisID)
	}
}
