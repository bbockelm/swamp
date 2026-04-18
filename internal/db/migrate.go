package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

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

// MaxEmbeddedMigrationVersion returns the highest migration version number
// from the embedded migration files. Migration files are expected to be named
// like "001_name.sql", "002_name.sql", etc.
func MaxEmbeddedMigrationVersion() (int64, error) {
	entries, err := fs.ReadDir(MigrationsFS, "migrations")
	if err != nil {
		return 0, fmt.Errorf("reading embedded migrations: %w", err)
	}
	var maxVersion int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := filepath.Base(e.Name())
		parts := strings.SplitN(name, "_", 2)
		if len(parts) < 2 {
			continue
		}
		v, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}
		if v > maxVersion {
			maxVersion = v
		}
	}
	if maxVersion == 0 {
		return 0, fmt.Errorf("no migrations found")
	}
	return maxVersion, nil
}
