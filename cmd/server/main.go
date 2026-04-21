package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/agent"
	"github.com/bbockelm/swamp/internal/backup"
	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/logbuffer"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/internal/router"
	"github.com/bbockelm/swamp/internal/storage"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	})

	// Check for subcommands.
	if len(os.Args) > 1 && os.Args[1] == "backfill-findings" {
		if err := runBackfillFindings(); err != nil {
			log.Fatal().Err(err).Msg("Backfill failed")
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "fix-result-types" {
		if err := runFixResultTypes(); err != nil {
			log.Fatal().Err(err).Msg("Fix result types failed")
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "fix-finding-counts" {
		if err := runFixFindingCounts(); err != nil {
			log.Fatal().Err(err).Msg("Fix finding counts failed")
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backfill-token-usage" {
		if err := runBackfillTokenUsage(); err != nil {
			log.Fatal().Err(err).Msg("Backfill token usage failed")
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Fatal error loading config")
	}

	// Worker mode: run the analysis agent and stream results back.
	if cfg.IsWorkerMode() {
		if err := agent.RunWorker(cfg); err != nil {
			log.Error().Err(err).Msg("Worker failed")
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		log.Fatal().Err(err).Msg("Fatal error")
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up in-memory log ring buffer (captures info+ for admin UI).
	logBuf := logbuffer.New(10000)
	consoleWriter := &zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	levelWriter := zerolog.MultiLevelWriter(consoleWriter)
	hookWriter := logbuffer.NewHookWriter(logBuf, zerolog.InfoLevel, levelWriter)
	log.Logger = zerolog.New(hookWriter).With().Timestamp().Logger()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := cfg.ValidateServer(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	if err := cfg.EnsureMasterKey(); err != nil {
		return fmt.Errorf("ensuring master key: %w", err)
	}

	if err := cfg.DeriveSessionSecret(); err != nil {
		return fmt.Errorf("deriving session secret: %w", err)
	}

	if err := cfg.LoadAgentKeyFile(); err != nil {
		return fmt.Errorf("loading agent key file: %w", err)
	}

	if err := cfg.LoadExternalLLMKeyFile(); err != nil {
		return fmt.Errorf("loading external LLM key file: %w", err)
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	if err := db.RunMigrations(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	store, err := storage.New(cfg)
	if err != nil {
		return fmt.Errorf("initializing storage: %w", err)
	}

	mux, h, exec := router.New(cfg, pool, store)

	var enc *crypto.Encryptor
	if cfg.InstanceKey != "" {
		enc, err = crypto.NewEncryptor(cfg.InstanceKey)
		if err != nil {
			return fmt.Errorf("initializing encryption: %w", err)
		}
	}

	queries := db.NewQueries(pool)
	backupSvc := backup.NewService(cfg, queries, store, enc)
	h.SetBackupService(backupSvc)
	h.SetExecutor(exec)
	h.SetLogBuffer(logBuf)

	// Clean up any backups stuck in "running" state from previous server instances,
	// then start periodic reconciliation loop.
	if err := backupSvc.ReconcileStaleBackups(ctx); err != nil {
		log.Error().Err(err).Msg("Failed to reconcile stale backups")
	}
	backupSvc.StartReconcileLoop(ctx)
	backupSvc.StartScheduledBackupLoop(ctx)

	// Executor lifecycle: mark stale jobs and start sync loop.
	exec.Start(ctx)

	// Background backfill of token usage for historical analyses.
	go backfillTokenUsageBackground(ctx, queries, store, enc)
	// Background normalization of legacy OpenCode token-usage rows.
	go normalizeOpenCodeTokenUsageBackground(ctx, queries)
	// One-off startup repair for stale SARIF metadata (finding_count / severity_counts).
	go repairSARIFMetadataFromFindingsBackground(ctx, queries)

	// In dev mode, create the admin account and print a one-time login URL.
	if cfg.IsDevelopment() {
		if err := h.GenerateDevLoginLink(ctx); err != nil {
			log.Error().Err(err).Msg("Failed to generate dev login link")
		}
	}

	addr := ":" + cfg.AppPort
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info().
			Str("addr", addr).
			Str("env", cfg.AppEnv).
			Msg("Starting SWAMP server")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Server failed")
		}
	}()

	<-quit
	log.Info().Msg("Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 30*time.Second)
	defer shutdownCancel()

	// Cancel running analyses before stopping the HTTP server.
	exec.Shutdown(shutdownCtx)

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn().Err(err).Msg("Server shutdown with pending connections (connections force-closed)")
	} else {
		log.Info().Msg("Server stopped gracefully")
	}
	return nil
}

// runBackfillFindings extracts findings from existing SARIF files that were
// uploaded before the findings extraction was implemented.
func runBackfillFindings() error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Load instance key from file if not set in environment.
	if err := cfg.EnsureMasterKey(); err != nil {
		return fmt.Errorf("ensuring master key: %w", err)
	}
	if cfg.InstanceKey == "" {
		return fmt.Errorf("SWAMP_INSTANCE_KEY required for decryption")
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	store, err := storage.New(cfg)
	if err != nil {
		return fmt.Errorf("initializing storage: %w", err)
	}

	enc, err := crypto.NewEncryptor(cfg.InstanceKey)
	if err != nil {
		return fmt.Errorf("initializing encryption: %w", err)
	}

	queries := db.NewQueries(pool)

	// Find SARIF results that have no corresponding findings.
	rows, err := pool.Query(ctx, `
		SELECT ar.id, ar.analysis_id, ar.filename, ar.s3_key, a.project_id, a.encrypted_dek, a.dek_nonce, a.git_commit
		FROM analysis_results ar
		JOIN analyses a ON a.id = ar.analysis_id
		LEFT JOIN findings f ON f.result_id = ar.id
		WHERE ar.result_type = 'sarif'
		  AND f.id IS NULL
		ORDER BY ar.created_at DESC
	`)
	if err != nil {
		return fmt.Errorf("querying SARIF results: %w", err)
	}
	defer rows.Close()

	var backfilled, failed int
	for rows.Next() {
		var resultID, analysisID, filename, s3Key, projectID, gitCommit string
		var encryptedDEK, dekNonce []byte

		if err := rows.Scan(&resultID, &analysisID, &filename, &s3Key, &projectID, &encryptedDEK, &dekNonce, &gitCommit); err != nil {
			log.Error().Err(err).Msg("Scan failed")
			failed++
			continue
		}

		log.Info().Str("result_id", resultID).Str("analysis_id", analysisID).Str("filename", filename).Msg("Processing SARIF")

		// Download encrypted file from S3.
		reader, err := store.Download(ctx, s3Key)
		if err != nil {
			log.Error().Err(err).Str("s3_key", s3Key).Msg("Failed to download SARIF")
			failed++
			continue
		}
		ciphertext, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			log.Error().Err(err).Str("s3_key", s3Key).Msg("Failed to read SARIF data")
			failed++
			continue
		}

		// Decrypt.
		dek, err := enc.UnwrapDEK(encryptedDEK, dekNonce)
		if err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to unwrap DEK")
			failed++
			continue
		}

		plaintext, err := crypto.Decrypt(dek, ciphertext)
		if err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to decrypt SARIF")
			failed++
			continue
		}

		// Extract findings.
		findings := agent.ExtractFindingsFromBytes(plaintext, analysisID, projectID)
		if len(findings) == 0 {
			log.Info().Str("analysis_id", analysisID).Msg("No findings in SARIF")
			continue
		}

		// Link findings to result and set git commit.
		for i := range findings {
			findings[i].ResultID = resultID
			findings[i].GitCommit = gitCommit
		}

		// Insert findings.
		if err := queries.CreateFindingsBatch(ctx, findings); err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to insert findings")
			failed++
			continue
		}

		log.Info().Int("count", len(findings)).Str("analysis_id", analysisID).Msg("Backfilled findings")
		backfilled += len(findings)
	}

	log.Info().Int("backfilled", backfilled).Int("failed", failed).Msg("Backfill complete")
	return nil
}

// runFixResultTypes corrects result_type values for existing analysis results
// that were uploaded before the classification logic was fixed.
func runFixResultTypes() error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	// Map old types to a classifier that determines the correct type
	classifyResultType := func(filename string) string {
		switch {
		case strings.HasSuffix(filename, ".sarif"):
			return "sarif"
		case filename == "notes.md":
			return "analysis_notes"
		case filename == "prompt.md":
			return "analysis_prompt"
		case filename == "context.md":
			return "analysis_context"
		case strings.HasSuffix(filename, ".md"):
			return "markdown_report"
		case strings.HasSuffix(filename, ".log"):
			return "agent_log"
		case strings.HasSuffix(filename, ".tar.gz") || strings.HasSuffix(filename, ".tgz"):
			return "exploit_tarball"
		default:
			return "other"
		}
	}

	// Find results with incorrect types
	rows, err := pool.Query(ctx, `
		SELECT id, filename, result_type
		FROM analysis_results
		WHERE result_type IN ('report', 'log', 'artifact', '')
		   OR (result_type = 'markdown_report' AND filename IN ('notes.md', 'prompt.md', 'context.md'))
	`)
	if err != nil {
		return fmt.Errorf("querying results: %w", err)
	}
	defer rows.Close()

	var updated, skipped int
	for rows.Next() {
		var id, filename, currentType string
		if err := rows.Scan(&id, &filename, &currentType); err != nil {
			log.Error().Err(err).Msg("Scan failed")
			continue
		}

		newType := classifyResultType(filename)
		if newType == currentType {
			skipped++
			continue
		}

		_, err := pool.Exec(ctx, `UPDATE analysis_results SET result_type = $1 WHERE id = $2`, newType, id)
		if err != nil {
			log.Error().Err(err).Str("id", id).Msg("Failed to update result type")
			continue
		}

		log.Info().
			Str("id", id).
			Str("filename", filename).
			Str("old_type", currentType).
			Str("new_type", newType).
			Msg("Fixed result type")
		updated++
	}

	log.Info().Int("updated", updated).Int("skipped", skipped).Msg("Fix result types complete")
	return nil
}

// runFixFindingCounts updates finding_count and severity_counts for existing SARIF results
// that were uploaded before the metadata extraction was added to the worker handler.
func runFixFindingCounts() error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := cfg.EnsureMasterKey(); err != nil {
		return fmt.Errorf("ensuring master key: %w", err)
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	store, err := storage.New(cfg)
	if err != nil {
		return fmt.Errorf("initializing storage: %w", err)
	}

	enc, err := crypto.NewEncryptor(cfg.InstanceKey)
	if err != nil {
		return fmt.Errorf("initializing encryption: %w", err)
	}

	queries := db.NewQueries(pool)

	// Find SARIF results with finding_count = 0
	rows, err := pool.Query(ctx, `
		SELECT ar.id, ar.analysis_id, ar.s3_key, a.encrypted_dek, a.dek_nonce
		FROM analysis_results ar
		JOIN analyses a ON a.id = ar.analysis_id
		WHERE ar.result_type = 'sarif' AND ar.finding_count = 0
	`)
	if err != nil {
		return fmt.Errorf("querying results: %w", err)
	}
	defer rows.Close()

	var updated, skipped, failed int
	for rows.Next() {
		var id, analysisID, s3Key string
		var encryptedDEK, dekNonce []byte
		if err := rows.Scan(&id, &analysisID, &s3Key, &encryptedDEK, &dekNonce); err != nil {
			log.Error().Err(err).Msg("Scan failed")
			failed++
			continue
		}

		// Decrypt the DEK
		dek, err := enc.UnwrapDEK(encryptedDEK, dekNonce)
		if err != nil {
			log.Error().Err(err).Str("id", id).Msg("Failed to decrypt DEK")
			failed++
			continue
		}

		// Download the encrypted SARIF
		rc, err := store.Download(ctx, s3Key)
		if err != nil {
			log.Error().Err(err).Str("s3_key", s3Key).Msg("Failed to download")
			failed++
			continue
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			log.Error().Err(err).Str("s3_key", s3Key).Msg("Failed to read download")
			failed++
			continue
		}

		// Decrypt the content
		plaintext, err := crypto.Decrypt(dek, data)
		if err != nil {
			log.Error().Err(err).Str("id", id).Msg("Failed to decrypt content")
			failed++
			continue
		}

		// Parse the SARIF
		summary, findingCount, severityCounts := agent.ParseSARIFBytes(plaintext)
		if findingCount == 0 {
			skipped++
			continue
		}

		// Update the metadata
		if err := queries.UpdateAnalysisResultMetadata(ctx, id, summary, findingCount, severityCounts); err != nil {
			log.Error().Err(err).Str("id", id).Msg("Failed to update metadata")
			failed++
			continue
		}

		log.Info().
			Str("id", id).
			Str("analysis_id", analysisID).
			Int("finding_count", findingCount).
			Str("summary", summary).
			Msg("Fixed finding count")
		updated++
	}

	log.Info().
		Int("updated", updated).
		Int("skipped", skipped).
		Int("failed", failed).
		Msg("Fix finding counts complete")
	return nil
}

// backfillTokenUsageBackground runs the token usage backfill in the background
// at server startup. It processes analyses that have agent_stdout.log results
// but no token usage rows yet.
func backfillTokenUsageBackground(ctx context.Context, queries *db.Queries, store *storage.Store, enc *crypto.Encryptor) {
	if enc == nil || store == nil {
		return
	}

	ids, err := queries.ListAnalysisIDsWithoutUsage(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Token usage backfill: failed to list analyses")
		return
	}
	if len(ids) == 0 {
		return
	}

	log.Info().Int("count", len(ids)).Msg("Token usage backfill: starting")
	backfilled, failed := backfillTokenUsageForIDs(ctx, ids, queries, store, enc)
	log.Info().Int("backfilled", backfilled).Int("failed", failed).Msg("Token usage backfill: complete")
}

// backfillTokenUsageForIDs processes a list of analysis IDs, downloading their
// agent_stdout.log, parsing token usage, and storing it in the DB.
func backfillTokenUsageForIDs(ctx context.Context, ids []string, queries *db.Queries, store *storage.Store, enc *crypto.Encryptor) (backfilled, failed int) {
	for _, analysisID := range ids {
		select {
		case <-ctx.Done():
			return
		default:
		}

		analysis, err := queries.GetAnalysis(ctx, analysisID)
		if err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Token usage backfill: failed to load analysis")
			failed++
			continue
		}

		// Find the agent_stdout.log result.
		results, err := queries.ListAnalysisResults(ctx, analysisID)
		if err != nil {
			failed++
			continue
		}
		var stdoutResult *models.AnalysisResult
		for i := range results {
			if results[i].ResultType == "agent_log" && results[i].Filename == "agent_stdout.log" {
				stdoutResult = &results[i]
				break
			}
		}
		if stdoutResult == nil {
			continue
		}

		// Download and decrypt.
		reader, err := store.Download(ctx, stdoutResult.S3Key)
		if err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Token usage backfill: failed to download log")
			failed++
			continue
		}
		ciphertext, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			failed++
			continue
		}

		dek, err := enc.UnwrapDEK(analysis.EncryptedDEK, analysis.DEKNonce)
		if err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Token usage backfill: failed to unwrap DEK")
			failed++
			continue
		}

		plaintext, err := crypto.Decrypt(dek, ciphertext)
		if err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Token usage backfill: failed to decrypt")
			failed++
			continue
		}

		lines := strings.Split(string(plaintext), "\n")
		usages := agent.ExtractTokenUsage(lines)
		if normalized, changed := agent.ApplyAnalysisTokenUsageIdentity(usages, analysis); changed {
			usages = normalized
		}
		if len(usages) == 0 {
			continue
		}

		if err := queries.ReplaceAnalysisTokenUsage(ctx, analysisID, usages); err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Token usage backfill: failed to store")
			failed++
			continue
		}

		totalIn, totalOut := int64(0), int64(0)
		for _, u := range usages {
			totalIn += u.InputTokens
			totalOut += u.OutputTokens
		}
		log.Info().
			Str("analysis_id", analysisID).
			Int("models", len(usages)).
			Int64("input", totalIn).
			Int64("output", totalOut).
			Msg("Token usage backfill: processed")
		backfilled++
	}
	return
}

// normalizeOpenCodeTokenUsageBackground rewrites legacy token-usage rows where
// model='opencode' so the model/provider come from analysis configuration.
func normalizeOpenCodeTokenUsageBackground(ctx context.Context, queries *db.Queries) {
	ids, err := queries.ListAnalysisIDsWithOpenCodeUsage(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Token usage normalize: failed to list analyses")
		return
	}
	if len(ids) == 0 {
		return
	}

	log.Info().Int("count", len(ids)).Msg("Token usage normalize: starting OpenCode identity rewrite")
	updated, failed := 0, 0
	for _, analysisID := range ids {
		select {
		case <-ctx.Done():
			return
		default:
		}

		analysis, err := queries.GetAnalysis(ctx, analysisID)
		if err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Token usage normalize: failed to load analysis")
			failed++
			continue
		}

		usages, err := queries.GetAnalysisTokenUsage(ctx, analysisID)
		if err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Token usage normalize: failed to load usage")
			failed++
			continue
		}

		normalized, changed := agent.ApplyAnalysisTokenUsageIdentity(usages, analysis)
		if !changed {
			continue
		}
		if err := queries.ReplaceAnalysisTokenUsage(ctx, analysisID, normalized); err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Token usage normalize: failed to store usage")
			failed++
			continue
		}
		updated++
	}

	log.Info().Int("updated", updated).Int("failed", failed).Msg("Token usage normalize: complete")
}

// repairSARIFMetadataFromFindingsBackground performs a one-off metadata repair
// at startup, rebuilding SARIF summaries from the findings table.
func repairSARIFMetadataFromFindingsBackground(ctx context.Context, queries *db.Queries) {
	updated, err := queries.RepairSARIFMetadataFromFindings(ctx)
	if err != nil {
		log.Error().Err(err).Msg("SARIF metadata repair: failed")
		return
	}
	if updated == 0 {
		log.Info().Msg("SARIF metadata repair: no updates needed")
		return
	}
	log.Info().Int64("updated", updated).Msg("SARIF metadata repair: complete")
}

// runBackfillTokenUsage is the CLI subcommand version of the backfill.
func runBackfillTokenUsage() error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := cfg.EnsureMasterKey(); err != nil {
		return fmt.Errorf("ensuring master key: %w", err)
	}
	if cfg.InstanceKey == "" {
		return fmt.Errorf("SWAMP_INSTANCE_KEY required for decryption")
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	store, err := storage.New(cfg)
	if err != nil {
		return fmt.Errorf("initializing storage: %w", err)
	}

	enc, err := crypto.NewEncryptor(cfg.InstanceKey)
	if err != nil {
		return fmt.Errorf("initializing encryption: %w", err)
	}

	queries := db.NewQueries(pool)

	ids, err := queries.ListAnalysisIDsWithoutUsage(ctx)
	if err != nil {
		return fmt.Errorf("listing analyses: %w", err)
	}

	log.Info().Int("count", len(ids)).Msg("Analyses to backfill")
	backfilled, failed := backfillTokenUsageForIDs(ctx, ids, queries, store, enc)
	log.Info().Int("backfilled", backfilled).Int("failed", failed).Msg("Backfill complete")
	return nil
}
