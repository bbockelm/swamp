.PHONY: help dev dev-backend dev-frontend build build-backend build-frontend build-prod prod test lint migrate migrate-down migrate-status docker docker-dev clean

DATABASE_URL ?= postgres://swamp:swamp@localhost:5432/swamp?sslmode=disable
S3_ENDPOINT ?= http://localhost:9000
S3_BUCKET ?= swamp-artifacts
S3_ACCESS_KEY ?= minioadmin
S3_SECRET_KEY ?= minioadmin

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# --- Development ---

dev: ## Run backend + frontend concurrently
	@echo "Starting backend and frontend..."
	@make dev-backend &
	@make dev-frontend
	@wait

dev-backend: ## Run Go backend with hot reload (air)
	cd $(CURDIR) && air -c .air.toml

dev-frontend: ## Run Next.js dev server
	cd frontend && npm run dev

# --- Build ---

build: build-backend build-frontend ## Build everything

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/bbockelm/swamp/internal/version.Version=$(VERSION) -X github.com/bbockelm/swamp/internal/version.Commit=$(COMMIT)

build-backend: ## Build Go binary (dev — no embedded frontend)
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/swamp-server ./cmd/server

build-frontend: ## Build Next.js frontend (static export)
	cd frontend && npm run build

build-prod: build-frontend ## Build single production binary with embedded frontend
	rm -rf internal/frontend/dist
	cp -r frontend/out internal/frontend/dist
	CGO_ENABLED=0 go build -tags embed_frontend -ldflags "$(LDFLAGS)" -o bin/swamp-server ./cmd/server
	rm -rf internal/frontend/dist

prod: build-prod ## Build and run production binary locally (uses current env)
	@echo "Starting production binary on :$(APP_PORT)..."
	APP_ENV=production BASE_URL=http://localhost:$(APP_PORT) ./bin/swamp-server

APP_PORT ?= 8080

# --- Database ---

migrate: ## Run database migrations
	goose -dir internal/db/migrations postgres "$(DATABASE_URL)" up

migrate-down: ## Roll back last migration
	goose -dir internal/db/migrations postgres "$(DATABASE_URL)" down

migrate-status: ## Show migration status
	goose -dir internal/db/migrations postgres "$(DATABASE_URL)" status

# --- Testing ---

test: ## Run Go tests
	go test ./... -v

lint: ## Lint Go code
	golangci-lint run ./...

lint-frontend: ## Lint frontend
	cd frontend && npm run lint

# --- Docker ---

docker: ## Build production Docker image
	docker build -t swamp:latest .

docker-dev: ## Build all-in-one dev Docker image
	docker build -t swamp:latest .
	docker build -t swamp-dev:latest -f Dockerfile.dev .

# --- Cleanup ---

clean: ## Remove build artifacts
	rm -rf bin/ tmp/
	rm -rf frontend/.next frontend/out
	rm -rf frontend/node_modules
	rm -rf internal/frontend/dist
