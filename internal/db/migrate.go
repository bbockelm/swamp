package db

import (
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog/log"
)

// RunMigrations applies all pending database migrations.
func RunMigrations(databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("opening db for migrations: %w", err)
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(MigrationsFS)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("setting goose dialect: %w", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	log.Info().Msg("Database migrations applied")
	return nil
}
