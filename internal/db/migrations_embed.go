package db

import "embed"

// MigrationsFS contains the SQL migration files embedded at compile time.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
