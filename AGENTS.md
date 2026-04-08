# Agent Notes — SWAMP

Last updated: 2025-07-14

## Project Purpose

AI-powered Software Assurance Marketplace. Users submit Git repositories for automated security analysis by an AI agent, which produces SARIF vulnerability reports and optional exploit validation.

## Tech Stack

| Component | Technology | Notes |
|-----------|------------|-------|
| Backend | Go 1.26, chi/v5 router | `cmd/server/main.go` entry point |
| Database | PostgreSQL 16 | pgx/v5 driver, goose/v3 migrations |
| Object Storage | S3 (MinIO for dev) | aws-sdk-go-v2; stores results, backups |
| Frontend | Next.js 14 (App Router) | React 18, TanStack React Query v5, Tailwind CSS 3 |
| Auth | OIDC (CILogon) | Cookie-based sessions |
| Agent | Claude CLI | Configurable binary, two-phase analysis |
| Dev Environment | VS Code DevContainer | docker-compose: app + postgres + minio |

## Project Layout

```
cmd/server/main.go              — Entry point
internal/config/config.go       — envconfig-based configuration
internal/crypto/crypto.go       — Envelope encryption (AES-256-GCM/HKDF)
internal/db/db.go               — pgxpool connection
internal/db/migrate.go          — goose migration runner
internal/db/queries.go          — All SQL queries (hand-written, no ORM)
internal/db/migrations/         — SQL migration files
internal/models/models.go       — Go structs with JSON tags
internal/storage/storage.go     — S3 upload/download/delete
internal/handlers/handlers.go   — Handler struct + helpers
internal/handlers/auth.go       — OIDC login/callback, sessions, middleware
internal/handlers/groups.go     — Group CRUD, members, invites
internal/handlers/projects.go   — Project CRUD with access control
internal/handlers/packages.go   — Software package CRUD
internal/handlers/analyses.go   — Analysis CRUD + cancel
internal/handlers/results.go    — Result list/get/download
internal/handlers/api_keys.go   — API key CRUD + auth middleware
internal/handlers/admin.go      — Admin user/role management
internal/handlers/backup.go     — Backup list/trigger handlers
internal/agent/prompt.go        — Two-phase prompt templates
internal/agent/executor.go      — Fork/exec agent, manage lifecycle
internal/agent/parser.go        — Parse SARIF output
internal/ws/terminal.go         — WebSocket hub for streaming
internal/router/router.go       — chi routes + CORS + middleware
internal/backup/service.go      — Backup creation, encryption, S3 upload
internal/openapi/spec.go        — Embedded OpenAPI handler
internal/openapi/spec.yaml      — OpenAPI 3.0 specification
internal/frontend/              — Embedded SPA (build tag: embed_frontend)
frontend/src/app/               — Next.js App Router pages
frontend/src/components/        — React components
frontend/src/lib/api.ts         — Typed API client
scripts/restore.sh              — Backup restore script
```

## Database Schema (migration 001)

18 tables: `users`, `sessions`, `groups`, `group_members`, `group_invites`, `user_roles`, `aup_agreements`, `projects`, `software_packages`, `analysis_runs`, `analysis_packages`, `analysis_results`, `api_keys`, `documents`, `backups`, `backup_settings`, `app_config`, `audit_log`. All UUID primary keys.

## Important Patterns

- **Queries**: All in `internal/db/queries.go` as methods on `Queries` struct wrapping `*pgxpool.Pool`. Raw SQL, no ORM.
- **Handlers**: Methods on `Handler` struct holding cfg, queries, store, encryptor, backupSvc, executor.
- **Context keys**: `type contextKey string`, constants `sessionContextKey`, `userContextKey`, roles via `contextKey("user_roles")`.
- **Auth flow**: OIDC → create/update user → create session → set cookie. Dev mode has `POST /dev-login`.
- **Agent execution**: Fork/exec `claude` CLI in temp directory, two-phase (security analysis → exploit validation), results uploaded to S3.
- **Embedded frontend**: Production builds use `-tags embed_frontend`. `DistFS()` returns the embedded FS.
- **Embedded migrations**: via `//go:embed` in `migrations_embed.go`.
- **API proxy**: Next.js rewrites `/api/*` → backend in dev.

## How to Run

```bash
make dev              # backend (air) + frontend (next dev)
make dev-backend      # just Go with air
make dev-frontend     # just Next.js
make migrate          # apply migrations
make build-prod       # single binary with embedded frontend
```

## Current State

### What's done
- Full backend: config, DB, crypto, storage, all handlers, agent engine, WS hub, router, backup
- OpenAPI 3.0 spec at `/api/v1/openapi.yaml` with all endpoints documented
- Frontend: dashboard, projects (list, create, detail with tabs), groups (list, create, detail with members/invites), analyses (detail with live WebSocket streaming + SARIF viewer + markdown report), API keys, admin pages (users, backups), settings, login page
- Components: SARIFViewer (SARIF table), MarkdownReport (simple markdown renderer), AnalysisStatus (status badge), GroupManager (members + invites)
- DevContainer with Postgres + MinIO
- Dockerfile (multi-stage production), Dockerfile.dev, K8s manifests
- Makefile with all common targets
- VS Code workspace config (swamp.code-workspace)
- Restore script (scripts/restore.sh)
- Compiles cleanly (`go build ./...` and `go vet ./...` pass)
- 70 source files total

### What has NOT been done yet
- `npm install` has not been run in `frontend/`
- No tests exist (Go or frontend)
- No form validation beyond basic HTML
- No error boundary or loading skeleton components
- No authentication/authorization has been tested end-to-end
