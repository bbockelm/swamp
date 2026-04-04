package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/agent"
	"github.com/bbockelm/swamp/internal/backup"
	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/router"
	"github.com/bbockelm/swamp/internal/storage"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	})

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

	// Clean up any backups stuck in "running" state from previous server instances,
	// then start periodic reconciliation loop.
	if err := backupSvc.ReconcileStaleBackups(ctx); err != nil {
		log.Error().Err(err).Msg("Failed to reconcile stale backups")
	}
	backupSvc.StartReconcileLoop(ctx)

	// Executor lifecycle: mark stale jobs and start sync loop.
	exec.Start(ctx)

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
		return fmt.Errorf("server shutdown: %w", err)
	}

	log.Info().Msg("Server stopped gracefully")
	return nil
}
