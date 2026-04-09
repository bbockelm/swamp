package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/ory/fosite"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/agent"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/internal/storage"
)

type contextKey string

const userIDKey contextKey = "user_id"

// Server wraps the mcp-go StreamableHTTPServer and provides SWAMP tools.
type Server struct {
	mcpServer *server.MCPServer
	httpSrv   *server.StreamableHTTPServer
	queries   *db.Queries
	store     *storage.Store
	encryptor *crypto.Encryptor
	provider  fosite.OAuth2Provider
	executor  agent.AnalysisExecutor
	baseURL   string
}

// New creates an MCP server with all SWAMP tools registered.
func New(
	queries *db.Queries,
	store *storage.Store,
	encryptor *crypto.Encryptor,
	provider fosite.OAuth2Provider,
	executor agent.AnalysisExecutor,
	baseURL string,
) *Server {
	s := &Server{
		queries:   queries,
		store:     store,
		encryptor: encryptor,
		provider:  provider,
		executor:  executor,
		baseURL:   strings.TrimRight(baseURL, "/"),
	}

	mcpSrv := server.NewMCPServer(
		"swamp",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithInstructions(instructions),
	)

	s.mcpServer = mcpSrv
	s.registerTools()

	s.httpSrv = server.NewStreamableHTTPServer(
		mcpSrv,
		server.WithStateLess(true),
	)

	return s
}

// Handler returns the http.Handler for mounting on a router.
// It wraps the MCP transport with OAuth2 authentication: unauthenticated
// requests receive HTTP 401 with WWW-Authenticate pointing to the
// resource metadata URL, which triggers the client's OAuth2 flow.
func (s *Server) Handler() http.Handler {
	resourceMetadataURL := s.baseURL + "/.well-known/oauth-protected-resource"
	wwwAuth := fmt.Sprintf(`Bearer resource_metadata=%q`, resourceMetadataURL)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for a bearer token on POST (JSON-RPC calls) and GET (SSE stream).
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("WWW-Authenticate", wwwAuth)
			http.Error(w, "OAuth2 bearer token required", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		session := &fosite.DefaultSession{}
		_, ar, err := s.provider.IntrospectToken(r.Context(), token, fosite.AccessToken, session)
		if err != nil {
			log.Debug().Err(err).Msg("MCP: bearer token invalid")
			w.Header().Set("WWW-Authenticate", wwwAuth)
			http.Error(w, "invalid or expired bearer token", http.StatusUnauthorized)
			return
		}

		sub := ar.GetSession().GetSubject()
		if sub == "" {
			w.Header().Set("WWW-Authenticate", wwwAuth)
			http.Error(w, "token has no subject", http.StatusUnauthorized)
			return
		}

		// Inject user ID into context for tool handlers.
		ctx := context.WithValue(r.Context(), userIDKey, sub)
		s.httpSrv.ServeHTTP(w, r.WithContext(ctx))
	})
}

// getUserID extracts the authenticated user ID from the context.
func getUserID(ctx context.Context) (string, error) {
	uid, ok := ctx.Value(userIDKey).(string)
	if !ok || uid == "" {
		return "", fmt.Errorf("not authenticated; provide a valid OAuth2 bearer token")
	}
	return uid, nil
}

// errResult creates an error tool result.
func errResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{mcp.NewTextContent(msg)},
	}
}

// jsonResult creates a JSON-formatted tool result.
func jsonResult(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult("failed to serialize result: " + err.Error())
	}
	return mcp.NewToolResultText(string(b))
}

// getStr gets a string parameter from the tool request.
func getStr(req mcp.CallToolRequest, key string) string {
	v, _ := req.GetArguments()[key].(string)
	return v
}

// getInt gets an integer parameter from the tool request.
func getInt(req mcp.CallToolRequest, key string) int {
	v, _ := req.GetArguments()[key].(float64)
	return int(v)
}

// registerTools adds all SWAMP tools to the MCP server.
func (s *Server) registerTools() {
	// ---- Project Tools ----

	s.mcpServer.AddTool(mcp.NewTool("list_projects",
		mcp.WithDescription("List all projects you have access to. Returns project ID, name, description, and status. Use this first to find the project you want to work with."),
	), s.listProjects)

	s.mcpServer.AddTool(mcp.NewTool("get_project",
		mcp.WithDescription("Get detailed information about a specific project, including group assignments, your access role, and configuration."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
	), s.getProject)

	// ---- Package Tools ----

	s.mcpServer.AddTool(mcp.NewTool("list_packages",
		mcp.WithDescription("List software packages within a project. Each package represents a Git repository to be analyzed. Returns package name, Git URL, branch, and any custom analysis prompt."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
	), s.listPackages)

	s.mcpServer.AddTool(mcp.NewTool("get_package",
		mcp.WithDescription("Get detailed information about a specific software package, including its Git URL, branch, commit, and custom analysis prompt."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
		mcp.WithString("package_id", mcp.Description("The UUID of the package"), mcp.Required()),
	), s.getPackage)

	// ---- Analysis Tools ----

	s.mcpServer.AddTool(mcp.NewTool("list_analyses",
		mcp.WithDescription("List security analyses for a project, ordered by most recent first. Shows analysis status (pending/running/completed/failed/cancelled), the model used, who triggered it, and timing. Use this to find the latest completed analysis."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
	), s.listAnalyses)

	s.mcpServer.AddTool(mcp.NewTool("get_analysis",
		mcp.WithDescription("Get details of a specific analysis, including its status, timing, error messages, and the list of packages that were analyzed."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
		mcp.WithString("analysis_id", mcp.Description("The UUID of the analysis"), mcp.Required()),
	), s.getAnalysis)

	s.mcpServer.AddTool(mcp.NewTool("start_analysis",
		mcp.WithDescription("Start a new security analysis on one or more packages in a project. The analysis runs asynchronously — use get_analysis to poll for completion. Requires write access to the project."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
		mcp.WithString("package_ids", mcp.Description("Comma-separated UUIDs of packages to analyze"), mcp.Required()),
		mcp.WithString("custom_prompt", mcp.Description("Optional additional instructions for the analysis agent")),
		mcp.WithString("agent_model", mcp.Description("Optional: model to use for analysis (e.g. 'claude-sonnet-4-20250514'). Leave empty for default.")),
	), s.startAnalysis)

	// ---- Result Tools ----

	s.mcpServer.AddTool(mcp.NewTool("list_results",
		mcp.WithDescription("List result artifacts from a completed analysis. Results include SARIF security findings, markdown reports, analysis notes, and logs. Each result has a type: 'sarif' (structured findings), 'markdown' (human-readable report), 'analysis_notes' (agent reasoning), 'log' (execution log), 'archive' (full workspace)."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
		mcp.WithString("analysis_id", mcp.Description("The UUID of the analysis"), mcp.Required()),
	), s.listResults)

	s.mcpServer.AddTool(mcp.NewTool("get_result_content",
		mcp.WithDescription("Download and return the content of a result artifact. For SARIF results, returns the full JSON with all findings. For markdown reports, returns the formatted report text. For analysis_notes, returns the agent's reasoning and observations. Use list_results first to see available artifacts and their types."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
		mcp.WithString("analysis_id", mcp.Description("The UUID of the analysis"), mcp.Required()),
		mcp.WithString("result_id", mcp.Description("The UUID of the result artifact"), mcp.Required()),
	), s.getResultContent)

	// ---- Finding Tools ----

	s.mcpServer.AddTool(mcp.NewTool("list_findings",
		mcp.WithDescription("Search and list security findings for a project. Findings are extracted from SARIF results and include vulnerability details, affected file paths, line numbers, code snippets, severity levels, and triage status. Supports filtering to narrow down results.\n\nSeverity levels: 'error' (high/critical), 'warning' (medium), 'note' (low/informational).\nTriage statuses: 'open' (new/untriaged), 'confirmed', 'false_positive', 'not_relevant', 'wont_fix', 'mitigated'."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
		mcp.WithString("level", mcp.Description("Filter by severity: 'error', 'warning', or 'note'")),
		mcp.WithString("rule_id", mcp.Description("Filter by rule ID (e.g. 'security/sql-injection')")),
		mcp.WithString("status", mcp.Description("Filter by triage status: 'open', 'confirmed', 'false_positive', 'not_relevant', 'wont_fix', 'mitigated'")),
		mcp.WithString("analysis_id", mcp.Description("Filter to findings from a specific analysis")),
		mcp.WithString("file_path", mcp.Description("Filter by file path (partial match)")),
		mcp.WithString("search", mcp.Description("Full-text search across finding messages and snippets")),
		mcp.WithNumber("limit", mcp.Description("Max results to return (1-500, default 50)")),
		mcp.WithNumber("offset", mcp.Description("Pagination offset (default 0)")),
	), s.listFindings)

	s.mcpServer.AddTool(mcp.NewTool("get_finding",
		mcp.WithDescription("Get full details of a specific security finding, including the complete SARIF data, code snippet, all triage annotations from team members, and the Git URL of the affected repository."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
		mcp.WithString("finding_id", mcp.Description("The UUID of the finding"), mcp.Required()),
	), s.getFinding)

	s.mcpServer.AddTool(mcp.NewTool("annotate_finding",
		mcp.WithDescription("Add or update your triage annotation on a finding. Use this to mark findings as confirmed bugs, false positives, etc. Each user can have one annotation per finding (updating replaces your previous one)."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
		mcp.WithString("finding_id", mcp.Description("The UUID of the finding"), mcp.Required()),
		mcp.WithString("status", mcp.Description("Triage status: 'open', 'confirmed', 'false_positive', 'not_relevant', 'wont_fix', 'mitigated'"), mcp.Required()),
		mcp.WithString("note", mcp.Description("Optional note explaining the triage decision")),
	), s.annotateFinding)

	s.mcpServer.AddTool(mcp.NewTool("get_findings_summary",
		mcp.WithDescription("Get a compact summary of all open/confirmed findings for a project, organized by file. This is useful for getting a quick overview of remaining security issues that need attention. Excludes findings marked as false_positive or not_relevant."),
		mcp.WithString("project_id", mcp.Description("The UUID of the project"), mcp.Required()),
	), s.getFindingsSummary)
}

// ---- Tool Implementations ----

func (s *Server) listProjects(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	var projects []models.Project
	isAdmin, _ := s.queries.UserHasRole(ctx, userID, "admin")
	if isAdmin {
		projects, err = s.queries.ListAllProjects(ctx)
	} else {
		projects, err = s.queries.ListUserProjects(ctx, userID)
	}
	if err != nil {
		return errResult("failed to list projects: " + err.Error()), nil
	}
	if projects == nil {
		projects = []models.Project{}
	}
	return jsonResult(projects), nil
}

func (s *Server) getProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if projectID == "" {
		return errResult("project_id is required"), nil
	}

	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	project, err := s.queries.GetProject(ctx, projectID)
	if err != nil {
		return errResult("project not found"), nil
	}

	role := "read"
	if wOK, _ := s.queries.UserCanAccessProject(ctx, userID, projectID, "admin"); wOK {
		role = "admin"
	} else if wOK, _ := s.queries.UserCanAccessProject(ctx, userID, projectID, "write"); wOK {
		role = "write"
	}

	return jsonResult(map[string]any{
		"project": project,
		"my_role": role,
	}), nil
}

func (s *Server) listPackages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	packages, err := s.queries.ListProjectPackages(ctx, projectID)
	if err != nil {
		return errResult("failed to list packages: " + err.Error()), nil
	}
	return jsonResult(packages), nil
}

func (s *Server) getPackage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	packageID := getStr(req, "package_id")
	pkg, err := s.queries.GetPackage(ctx, packageID)
	if err != nil || pkg.ProjectID != projectID {
		return errResult("package not found"), nil
	}
	return jsonResult(pkg), nil
}

func (s *Server) listAnalyses(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	analyses, err := s.queries.ListProjectAnalyses(ctx, projectID)
	if err != nil {
		return errResult("failed to list analyses: " + err.Error()), nil
	}
	if analyses == nil {
		analyses = []models.Analysis{}
	}
	return jsonResult(analyses), nil
}

func (s *Server) getAnalysis(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	analysisID := getStr(req, "analysis_id")
	analysis, err := s.queries.GetAnalysis(ctx, analysisID)
	if err != nil || analysis.ProjectID != projectID {
		return errResult("analysis not found"), nil
	}

	packages, _ := s.queries.ListAnalysisPackages(ctx, analysisID)
	return jsonResult(map[string]any{
		"analysis": analysis,
		"packages": packages,
	}), nil
}

func (s *Server) startAnalysis(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "write"); err != nil {
		return errResult(err.Error()), nil
	}

	pkgIDsStr := getStr(req, "package_ids")
	if pkgIDsStr == "" {
		return errResult("package_ids is required (comma-separated UUIDs)"), nil
	}
	pkgIDs := strings.Split(pkgIDsStr, ",")
	for i := range pkgIDs {
		pkgIDs[i] = strings.TrimSpace(pkgIDs[i])
	}

	// Verify packages belong to project.
	for _, pkgID := range pkgIDs {
		pkg, err := s.queries.GetPackage(ctx, pkgID)
		if err != nil || pkg.ProjectID != projectID {
			return errResult("invalid package ID: " + pkgID), nil
		}
	}

	analysis := &models.Analysis{
		ProjectID:    projectID,
		Status:       "pending",
		TriggeredBy:  userID,
		AgentModel:   getStr(req, "agent_model"),
		CustomPrompt: getStr(req, "custom_prompt"),
		AgentConfig:  json.RawMessage(`{}`),
	}

	// Generate per-analysis DEK.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		return errResult("internal error creating analysis"), nil
	}
	encDEK, nonce, err := s.encryptor.WrapDEK(dek)
	if err != nil {
		return errResult("internal error creating analysis"), nil
	}
	analysis.EncryptedDEK = encDEK
	analysis.DEKNonce = nonce

	if err := s.queries.CreateAnalysis(ctx, analysis); err != nil {
		return errResult("failed to create analysis: " + err.Error()), nil
	}

	for _, pkgID := range pkgIDs {
		_ = s.queries.AddAnalysisPackage(ctx, analysis.ID, pkgID)
	}

	// Submit to executor for async processing.
	if s.executor != nil && s.executor.AgentReady() {
		packages, err := s.queries.ListAnalysisPackages(ctx, analysis.ID)
		if err != nil {
			log.Warn().Err(err).Msg("MCP: failed to fetch packages for analysis submission")
		} else {
			s.executor.Submit(analysis, packages)
		}
	}

	return jsonResult(map[string]any{
		"analysis_id": analysis.ID,
		"status":      analysis.Status,
		"message":     "Analysis created and queued. Use get_analysis to poll for status updates.",
	}), nil
}

func (s *Server) listResults(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	analysisID := getStr(req, "analysis_id")
	analysis, err := s.queries.GetAnalysis(ctx, analysisID)
	if err != nil || analysis.ProjectID != projectID {
		return errResult("analysis not found"), nil
	}

	results, err := s.queries.ListAnalysisResults(ctx, analysisID)
	if err != nil {
		return errResult("failed to list results: " + err.Error()), nil
	}
	return jsonResult(results), nil
}

func (s *Server) getResultContent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	analysisID := getStr(req, "analysis_id")
	analysis, err := s.queries.GetAnalysis(ctx, analysisID)
	if err != nil || analysis.ProjectID != projectID {
		return errResult("analysis not found"), nil
	}

	resultID := getStr(req, "result_id")
	result, err := s.queries.GetAnalysisResult(ctx, resultID)
	if err != nil || result.AnalysisID != analysisID {
		return errResult("result not found"), nil
	}

	// Download and decrypt.
	reader, err := s.store.Download(ctx, result.S3Key)
	if err != nil {
		return errResult("failed to download artifact"), nil
	}
	defer func() { _ = reader.Close() }()

	ciphertext, err := io.ReadAll(reader)
	if err != nil {
		return errResult("failed to read artifact"), nil
	}

	var plaintext []byte
	if len(analysis.EncryptedDEK) > 0 {
		dek, err := s.encryptor.UnwrapDEK(analysis.EncryptedDEK, analysis.DEKNonce)
		if err != nil {
			return errResult("failed to decrypt artifact"), nil
		}
		plaintext, err = crypto.Decrypt(dek, ciphertext)
		if err != nil {
			return errResult("failed to decrypt artifact"), nil
		}
	} else {
		plaintext = ciphertext
	}

	// For large binary results (archive), return metadata only.
	if result.ResultType == "archive" {
		return jsonResult(map[string]any{
			"result_type": result.ResultType,
			"filename":    result.Filename,
			"file_size":   result.FileSize,
			"message":     "Archive files are too large to return inline. Use the web UI to download.",
		}), nil
	}

	// Truncate very large results to avoid overwhelming the context.
	content := string(plaintext)
	const maxLen = 200_000
	if len(content) > maxLen {
		content = content[:maxLen] + "\n\n... [truncated at 200KB; use the web UI for full content]"
	}

	return mcp.NewToolResultText(content), nil
}

func (s *Server) listFindings(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	filters := db.FindingFilters{
		Level:      getStr(req, "level"),
		RuleID:     getStr(req, "rule_id"),
		Status:     getStr(req, "status"),
		AnalysisID: getStr(req, "analysis_id"),
		FilePath:   getStr(req, "file_path"),
		Search:     getStr(req, "search"),
		Limit:      getInt(req, "limit"),
		Offset:     getInt(req, "offset"),
	}
	if filters.Limit <= 0 || filters.Limit > 500 {
		filters.Limit = 50
	}
	if filters.Offset < 0 {
		filters.Offset = 0
	}

	findings, err := s.queries.ListProjectFindings(ctx, projectID, filters)
	if err != nil {
		return errResult("failed to list findings: " + err.Error()), nil
	}
	if findings == nil {
		findings = []models.Finding{}
	}

	total, err := s.queries.CountProjectFindings(ctx, projectID, filters)
	if err != nil {
		total = len(findings)
	}

	// Build a more compact representation for MCP consumers.
	type compactFinding struct {
		ID         string `json:"id"`
		RuleID     string `json:"rule_id"`
		Level      string `json:"level"`
		Message    string `json:"message"`
		FilePath   string `json:"file_path"`
		StartLine  int    `json:"start_line"`
		EndLine    int    `json:"end_line"`
		Snippet    string `json:"snippet,omitempty"`
		Status     string `json:"status"`
		GitURL     string `json:"git_url,omitempty"`
		AnalysisID string `json:"analysis_id"`
		GitCommit  string `json:"git_commit,omitempty"`
	}

	compact := make([]compactFinding, len(findings))
	for i, f := range findings {
		compact[i] = compactFinding{
			ID:         f.ID,
			RuleID:     f.RuleID,
			Level:      f.Level,
			Message:    f.Message,
			FilePath:   f.FilePath,
			StartLine:  f.StartLine,
			EndLine:    f.EndLine,
			Snippet:    f.Snippet,
			Status:     f.LatestStatus,
			GitURL:     f.GitURL,
			AnalysisID: f.AnalysisID,
			GitCommit:  f.GitCommit,
		}
	}

	return jsonResult(map[string]any{
		"findings": compact,
		"total":    total,
		"limit":    filters.Limit,
		"offset":   filters.Offset,
		"hint":     "Use get_finding for full details including code snippet and raw SARIF data. Use offset for pagination if total > limit.",
	}), nil
}

func (s *Server) getFinding(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	findingID := getStr(req, "finding_id")
	finding, err := s.queries.GetFinding(ctx, findingID)
	if err != nil || finding.ProjectID != projectID {
		return errResult("finding not found"), nil
	}

	annotations, _ := s.queries.ListFindingAnnotations(ctx, findingID)
	if annotations == nil {
		annotations = []models.FindingAnnotation{}
	}

	return jsonResult(map[string]any{
		"finding":     finding,
		"annotations": annotations,
	}), nil
}

func (s *Server) annotateFinding(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	findingID := getStr(req, "finding_id")
	finding, err := s.queries.GetFinding(ctx, findingID)
	if err != nil || finding.ProjectID != projectID {
		return errResult("finding not found"), nil
	}

	status := getStr(req, "status")
	validStatuses := map[string]bool{
		"open": true, "false_positive": true, "not_relevant": true,
		"confirmed": true, "wont_fix": true, "mitigated": true,
	}
	if !validStatuses[status] {
		return errResult("invalid status; must be one of: open, false_positive, not_relevant, confirmed, wont_fix, mitigated"), nil
	}

	annotation := &models.FindingAnnotation{
		FindingID: findingID,
		UserID:    userID,
		Status:    status,
		Note:      getStr(req, "note"),
	}
	if err := s.queries.UpsertFindingAnnotation(ctx, annotation); err != nil {
		return errResult("failed to save annotation: " + err.Error()), nil
	}

	return jsonResult(annotation), nil
}

func (s *Server) getFindingsSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}
	projectID := getStr(req, "project_id")
	if err := s.requireAccess(ctx, userID, projectID, "read"); err != nil {
		return errResult(err.Error()), nil
	}

	summaries, err := s.queries.GetOpenFindingsSummary(ctx, projectID)
	if err != nil {
		return errResult("failed to get findings summary: " + err.Error()), nil
	}
	if summaries == nil {
		summaries = []models.FindingSummary{}
	}

	// Group by file for a cleaner view.
	type fileSummary struct {
		FilePath string                  `json:"file_path"`
		Findings []models.FindingSummary `json:"findings"`
	}
	byFile := make(map[string]*fileSummary)
	for _, fs := range summaries {
		if _, ok := byFile[fs.FilePath]; !ok {
			byFile[fs.FilePath] = &fileSummary{FilePath: fs.FilePath}
		}
		byFile[fs.FilePath].Findings = append(byFile[fs.FilePath].Findings, fs)
	}

	files := make([]fileSummary, 0, len(byFile))
	for _, f := range byFile {
		files = append(files, *f)
	}

	// Count by level.
	counts := map[string]int{"error": 0, "warning": 0, "note": 0}
	for _, fs := range summaries {
		counts[fs.Level]++
	}

	return jsonResult(map[string]any{
		"total_open":  len(summaries),
		"by_severity": counts,
		"by_file":     files,
		"hint":        "These are the open/confirmed findings excluding false_positive and not_relevant. Use list_findings for full details with pagination.",
	}), nil
}

// requireAccess checks that the user has the specified access level to a project.
func (s *Server) requireAccess(ctx context.Context, userID, projectID, level string) error {
	if projectID == "" {
		return fmt.Errorf("project_id is required")
	}
	ok, err := s.queries.UserCanAccessProject(ctx, userID, projectID, level)
	if err != nil || !ok {
		// Admins can access all projects.
		isAdmin, _ := s.queries.UserHasRole(ctx, userID, "admin")
		if !isAdmin {
			return fmt.Errorf("project not found or insufficient access (requires %s)", level)
		}
	}
	return nil
}
