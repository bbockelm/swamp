#!/bin/bash
set -e

echo "==> Installing Go dependencies..."
cd /workspace
go mod download || true

echo "==> Installing frontend dependencies..."
if [ -d "/workspace/frontend" ]; then
  cd /workspace/frontend
  npm install
fi

echo "==> Installing development tools..."
go install github.com/air-verse/air@latest
go install github.com/pressly/goose/v3/cmd/goose@latest
npm install -g opencode-ai

echo "==> Running database migrations..."
cd /workspace
sleep 2
goose -dir internal/db/migrations postgres "${DATABASE_URL}" up || echo "Migrations will run on first 'make migrate'"

echo "==> Post-create complete!"
