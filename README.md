# SWAMP — Software Assurance Marketplace

AI-powered security analysis platform for Git repositories. Submits code to an AI agent for automated vulnerability assessment, exploit validation, and SARIF report generation.

## Tech Stack

| Component       | Technology                                                           |
| --------------- | -------------------------------------------------------------------- |
| Backend         | Go 1.24, chi/v5 router                                               |
| Database        | PostgreSQL 16 (pgx/v5)                                               |
| Object Storage  | S3-compatible (MinIO for dev)                                        |
| Frontend        | Next.js 14 (App Router), React 18, TanStack Query v5, Tailwind CSS 3 |
| Auth            | OIDC (CILogon)                                                       |
| Analysis Engine | Claude CLI (configurable)                                            |
| Dev Environment | VS Code DevContainer (docker-compose)                                |

## Quick Start (DevContainer)

1. Open in VS Code and reopen in container
2. `make migrate` — apply database migrations
3. `make dev` — starts backend (hot-reload) and frontend

## Manual Setup

```bash
# Prerequisites: Go 1.24+, Node 20+, PostgreSQL 16+, S3-compatible storage

# Backend
export DATABASE_URL="postgres://swamp:swamp@localhost:5432/swamp?sslmode=disable"
export S3_ENDPOINT="http://localhost:9000"
export S3_BUCKET="swamp-artifacts"
export S3_ACCESS_KEY="minioadmin"
export S3_SECRET_KEY="minioadmin"

go mod download
go build -o bin/swamp-server ./cmd/server
./bin/swamp-server

# Frontend (separate terminal)
cd frontend && npm install && npm run dev
```

## Project Layout

```
cmd/server/main.go          — Go server entry point
internal/config/             — envconfig-based configuration
internal/crypto/             — Envelope encryption (AES-256-GCM, HKDF-SHA256)
internal/db/                 — PostgreSQL connection, migrations, queries
internal/models/             — Go structs with JSON tags
internal/storage/            — S3 upload/download/delete
internal/handlers/           — REST API handlers
internal/agent/              — AI agent executor (prompt, run, parse)
internal/ws/                 — WebSocket hub for analysis streaming
internal/router/             — chi routes + middleware
internal/backup/             — Backup creation, encryption, storage
internal/frontend/           — Embedded SPA (production builds)
frontend/                    — Next.js frontend
deploy/k8s/                  — Kubernetes deployment manifests
deploy/dev/                  — Dev supervisord config
```

## Build

```bash
make build-backend     # Go binary (no frontend)
make build-frontend    # Next.js static export
make build-prod        # Single binary with embedded frontend
make docker            # Production Docker image
```

## Database

```bash
make migrate           # Apply pending migrations
make migrate-down      # Rollback last migration
make migrate-status    # Check status
```

## Configuration

All settings via environment variables:

| Variable                  | Default           | Description                           |
| ------------------------- | ----------------- | ------------------------------------- |
| `APP_ENV`                 | `development`     | Environment mode                      |
| `APP_PORT`                | `8080`            | HTTP listen port                      |
| `DATABASE_URL`            | (required)        | PostgreSQL connection string          |
| `S3_ENDPOINT`             | (required)        | S3-compatible endpoint                |
| `S3_BUCKET`               | `swamp-artifacts` | S3 bucket name                        |
| `S3_ACCESS_KEY`           | (required)        | S3 access key                         |
| `S3_SECRET_KEY`           | (required)        | S3 secret key                         |
| `S3_USE_SSL`              | `false`           | Use HTTPS for S3                      |
| `S3_USE_PATH_STYLE`       | `true`            | Use path-style S3 URLs                |
| `INSTANCE_KEY`            | (auto-generated)  | 32-byte hex master encryption key     |
| `OIDC_ISSUER`             |                   | OIDC provider URL                     |
| `OIDC_CLIENT_ID`          |                   | OIDC client ID                        |
| `OIDC_CLIENT_SECRET`      |                   | OIDC client secret                    |
| `AGENT_BINARY`            | `claude`          | AI agent CLI binary                   |
| `AGENT_MODEL`             |                   | AI model override (empty = default)   |
| `AGENT_API_KEY`           |                   | API key for the AI agent              |
| `AGENT_API_KEY_FILE`      |                   | Path to file containing agent API key |
| `MAX_CONCURRENT_ANALYSES` | `2`               | Max parallel analyses                 |
| `MAX_ANALYSIS_DURATION`   | `30m`             | Timeout per analysis                  |
| `AUP_VERSION`             | `1.0`             | Required AUP version                  |

### Executor and Kubernetes options

These options control where analyses run:

| Variable                     | Default                        | Description                                                                      |
| ---------------------------- | ------------------------------ | -------------------------------------------------------------------------------- |
| `EXECUTOR_MODE`              | `process`                      | `local`, `process`, or `kubernetes`                                              |
| `PROCESS_STATE_DIR`          | `.swamp/processes`             | State dir for process executor                                                   |
| `K8S_NAMESPACE`              | `swamp`                        | Namespace where SWAMP creates analysis Jobs                                      |
| `K8S_WORKER_IMAGE`           | (required for kubernetes mode) | Worker image used by analysis Jobs                                               |
| `K8S_WORKER_SERVICE_ACCOUNT` | `swamp-worker`                 | Service account attached to worker Pods                                          |
| `K8S_WORKER_CPU_REQUEST`     | `500m`                         | Worker CPU request                                                               |
| `K8S_WORKER_CPU_LIMIT`       | `2`                            | Worker CPU limit                                                                 |
| `K8S_WORKER_MEM_REQUEST`     | `512Mi`                        | Worker memory request                                                            |
| `K8S_WORKER_MEM_LIMIT`       | `2Gi`                          | Worker memory limit                                                              |
| `K8S_WORKER_NODE_SELECTOR`   |                                | Worker node selector (`k=v,k2=v2`)                                               |
| `K8S_WORKER_TOLERATIONS`     |                                | Worker tolerations (`k=v:Effect,...`)                                            |
| `K8S_WORKER_LABELS`          |                                | Extra labels for worker Jobs/Pods (`k=v,k2=v2`)                                  |
| `K8S_WORKER_ANNOTATIONS`     |                                | Extra annotations for worker Jobs/Pods (`k=v,k2=v2`)                             |
| `K8S_POD_TTL_SECONDS`        | `3600`                         | Job TTL after completion (`ttlSecondsAfterFinished`)                             |
| `K8S_DIRECT_LLM`             | `false`                        | Dev mode: worker Pods call external LLM endpoint directly instead of SWAMP proxy |
| `KUBECONFIG`                 |                                | Path to kubeconfig used by the server; if empty, in-cluster credentials are used |

Notes:

- In `kubernetes` mode, SWAMP creates Kubernetes Jobs (not raw Pods).
- `KUBECONFIG` is read by the SWAMP server process that creates Jobs.
- Set `K8S_DIRECT_LLM=true` for development clusters where worker Pods cannot reach SWAMP at a public URL. In this mode, SWAMP passes the resolved external LLM API key and endpoint directly to workers.
- The admin settings UI persists equivalent keys in DB (for example `k8s_kubeconfig`), which override environment values when present.

### Kubernetes setup (minimal permissions)

The SWAMP server only needs permission to create, list, get, watch, and delete Jobs in one namespace.

1. Create namespace and a dedicated service account for SWAMP job submission:

```bash
kubectl create namespace swamp
kubectl -n swamp create serviceaccount swamp-job-launcher
```

2. Grant minimal RBAC for Job lifecycle operations:

```bash
kubectl -n swamp create role swamp-job-launcher \
	--verb=create,get,list,watch,delete \
	--resource=jobs.batch

kubectl -n swamp create rolebinding swamp-job-launcher \
	--role=swamp-job-launcher \
	--serviceaccount=swamp:swamp-job-launcher
```

3. Generate a kubeconfig bound to that service account token:

```bash
CLUSTER_NAME=$(kubectl config view --minify -o jsonpath='{.clusters[0].name}')
API_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
CA_DATA=$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')
TOKEN=$(kubectl -n swamp create token swamp-job-launcher)

cat > /tmp/swamp-kubeconfig <<EOF
apiVersion: v1
kind: Config
clusters:
- name: ${CLUSTER_NAME}
  cluster:
    server: ${API_SERVER}
    certificate-authority-data: ${CA_DATA}
contexts:
- name: swamp-job-launcher@${CLUSTER_NAME}
  context:
    cluster: ${CLUSTER_NAME}
    namespace: swamp
    user: swamp-job-launcher
current-context: swamp-job-launcher@${CLUSTER_NAME}
users:
- name: swamp-job-launcher
  user:
    token: ${TOKEN}
EOF
```

4. Configure SWAMP to use Kubernetes executor with that kubeconfig:

```bash
export EXECUTOR_MODE=kubernetes
export K8S_NAMESPACE=swamp
export KUBECONFIG=/tmp/swamp-kubeconfig
export K8S_WORKER_IMAGE=ghcr.io/<org>/<worker-image>:<tag>
export K8S_WORKER_SERVICE_ACCOUNT=swamp-worker
```

### Kubernetes analysis runner image

The image referenced by `K8S_WORKER_IMAGE` must contain:

- The SWAMP server binary (it runs in `SWAMP_WORKER_MODE=true` for worker jobs and `SWAMP_LLM_PROXY_MODE=true` for sidecars)
- `git` (the analysis workflow and prompts expect repository operations)
- Node + npm runtime
- Agent CLIs used by workers:
	- `claude` (from `@anthropic-ai/claude-code`)
	- `opencode` (from `opencode-ai`) for external LLM mode
- `python3` and CA certificates

This repository includes a dedicated runner image definition:

- `Dockerfile.worker`

Build and push example:

```bash
docker build -f Dockerfile.worker -t ghcr.io/<org>/swamp-worker:<tag> .
docker push ghcr.io/<org>/swamp-worker:<tag>
export K8S_WORKER_IMAGE=ghcr.io/<org>/swamp-worker:<tag>
```

Do not bake provider API keys into this image; keys are injected at runtime by SWAMP (token exchange / proxy flow).

5. Start SWAMP server normally.

If SWAMP itself runs inside Kubernetes and should use in-cluster credentials instead, leave `KUBECONFIG` unset.

## Security

- Envelope encryption: Master key → HKDF → KEK → per-document DEK (AES-256-GCM)
- Sessions: HMAC-SHA256 derived secret, SHA-256 hashed tokens
- API keys: SHA-256 hashed, constant-time comparison
- Agent sandboxing: Optional `sandbox-exec` (macOS seatbelt)
- Backup encryption: Hierarchical key derivation (general + per-backup keys)

## License

MIT
