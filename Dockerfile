# --- Build frontend (static export) ---
FROM node:22-alpine AS node-builder
WORKDIR /build
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# --- Build backend with embedded frontend ---
FROM golang:1.26-alpine AS go-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=node-builder /build/out internal/frontend/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -tags embed_frontend -o /swamp-server ./cmd/server

# --- Production image (serves as both server and worker) ---
FROM alpine:3.21
RUN apk add --no-cache \
    bash \
    ca-certificates \
    curl \
    git \
    jq \
    make \
    nodejs \
    npm \
    openssh-client \
    postgresql17-client \
    py3-pip \
    python3 \
    && npm install -g @anthropic-ai/claude-code opencode-ai \
    && npm cache clean --force

COPY --from=go-builder /swamp-server /usr/local/bin/swamp-server

WORKDIR /app

ENV APP_ENV=production

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/swamp-server"]
