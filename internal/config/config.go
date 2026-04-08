package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/hkdf"
)

// Config holds all application configuration, populated from environment variables.
type Config struct {
	// App settings
	AppEnv  string `envconfig:"APP_ENV" default:"development"`
	AppPort string `envconfig:"APP_PORT" default:"8080"`
	BaseURL string `envconfig:"BASE_URL" default:"http://localhost:3000"`

	// Database
	DatabaseURL string `envconfig:"DATABASE_URL" default:""`

	// S3 / MinIO
	S3Endpoint     string `envconfig:"S3_ENDPOINT" default:""`
	S3Bucket       string `envconfig:"S3_BUCKET" default:"swamp-artifacts"`
	S3AccessKey    string `envconfig:"S3_ACCESS_KEY" default:""`
	S3SecretKey    string `envconfig:"S3_SECRET_KEY" default:""`
	S3UsePathStyle bool   `envconfig:"S3_USE_PATH_STYLE" default:"true"`
	S3UseSSL       bool   `envconfig:"S3_USE_SSL" default:"false"`

	// SSO / Auth (CILogon or other OIDC provider)
	OIDCIssuer       string `envconfig:"OIDC_ISSUER" default:""`
	OIDCClientID     string `envconfig:"OIDC_CLIENT_ID" default:""`
	OIDCClientSecret string `envconfig:"OIDC_CLIENT_SECRET" default:""`

	// SessionSecret is derived from the master key at startup (not user-configurable).
	SessionSecret string `envconfig:"-"`

	// Backup
	BackupDir string `envconfig:"BACKUP_DIR" default:"/tmp/swamp-backup"`

	// Instance encryption key
	InstanceKey string `envconfig:"INSTANCE_KEY" default:""`

	// Extra allowed HTTPS domains for OAuth2 DCR redirect URIs (comma-separated).
	OAuthExtraRedirectDomains string `envconfig:"OAUTH_EXTRA_REDIRECT_DOMAINS" default:""`

	// Agent settings
	AgentBinary           string        `envconfig:"AGENT_BINARY" default:"claude"`
	AgentModel            string        `envconfig:"AGENT_MODEL" default:""`
	AgentAPIKeyFile       string        `envconfig:"AGENT_API_KEY_FILE" default:".swamp-agent.key"`
	AgentAPIKey           string        `envconfig:"AGENT_API_KEY" default:""`
	SeatbeltEnabled       bool          `envconfig:"SEATBELT_ENABLED" default:"false"`
	MaxAnalysisDuration   time.Duration `envconfig:"MAX_ANALYSIS_DURATION" default:"30m"`
	MaxConcurrentAnalyses int           `envconfig:"MAX_CONCURRENT_ANALYSES" default:"2"`

	// Current AUP version users must agree to.
	AUPVersion string `envconfig:"AUP_VERSION" default:"1.0"`

	// Executor mode: "local" (in-process fork/exec), "process" (detached daemon, default), or "kubernetes".
	ExecutorMode string `envconfig:"EXECUTOR_MODE" default:"process"`

	// Process executor settings (used when EXECUTOR_MODE=process).
	ProcessStateDir string `envconfig:"PROCESS_STATE_DIR" default:".swamp/processes"`

	// Kubernetes executor settings (used when EXECUTOR_MODE=kubernetes).
	K8sNamespace            string `envconfig:"K8S_NAMESPACE" default:"swamp"`
	K8sWorkerImage          string `envconfig:"K8S_WORKER_IMAGE" default:""`
	K8sWorkerServiceAccount string `envconfig:"K8S_WORKER_SERVICE_ACCOUNT" default:"swamp-worker"`
	K8sWorkerCPURequest     string `envconfig:"K8S_WORKER_CPU_REQUEST" default:"500m"`
	K8sWorkerCPULimit       string `envconfig:"K8S_WORKER_CPU_LIMIT" default:"2"`
	K8sWorkerMemRequest     string `envconfig:"K8S_WORKER_MEM_REQUEST" default:"512Mi"`
	K8sWorkerMemLimit       string `envconfig:"K8S_WORKER_MEM_LIMIT" default:"2Gi"`
	K8sWorkerNodeSelector   string `envconfig:"K8S_WORKER_NODE_SELECTOR" default:""` // key=value,key2=value2
	K8sWorkerTolerations    string `envconfig:"K8S_WORKER_TOLERATIONS" default:""`   // key=value:effect,...
	K8sWorkerLabels         string `envconfig:"K8S_WORKER_LABELS" default:""`        // key=value,key2=value2
	K8sWorkerAnnotations    string `envconfig:"K8S_WORKER_ANNOTATIONS" default:""`   // key=value,key2=value2
	K8sPodTTLSeconds        int    `envconfig:"K8S_POD_TTL_SECONDS" default:"3600"`  // cleanup after completion

	// Worker mode settings (used inside worker pods / detached processes).
	WorkerMode     bool   `envconfig:"SWAMP_WORKER_MODE" default:"false"`
	WorkerToken    string `envconfig:"SWAMP_WORKER_TOKEN" default:""`
	WorkerServer   string `envconfig:"SWAMP_WORKER_SERVER" default:""`
	WorkerAnalysis string `envconfig:"SWAMP_WORKER_ANALYSIS" default:""`
	WorkerLockFile string `envconfig:"SWAMP_WORKER_LOCK_FILE" default:""`
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return &cfg, nil
}

// ValidateServer checks that server-mode required fields are set.
// Workers don't need these, so they aren't enforced by envconfig tags.
func (c *Config) ValidateServer() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.S3Endpoint == "" {
		return fmt.Errorf("S3_ENDPOINT is required")
	}
	if c.S3AccessKey == "" {
		return fmt.Errorf("S3_ACCESS_KEY is required")
	}
	if c.S3SecretKey == "" {
		return fmt.Errorf("S3_SECRET_KEY is required")
	}
	return nil
}

const masterKeyFile = ".swamp-master.key"

// EnsureMasterKey checks whether InstanceKey is set. If empty, tries to load
// from disk; failing that, generates a new random 32-byte key and saves it.
func (c *Config) EnsureMasterKey() error {
	if c.InstanceKey != "" {
		return nil
	}

	data, err := os.ReadFile(masterKeyFile)
	if err == nil {
		key := strings.TrimSpace(string(data))
		if len(key) == 64 {
			c.InstanceKey = key
			log.Info().Str("file", masterKeyFile).Msg("Loaded master key from disk")
			return nil
		}
		log.Warn().Str("file", masterKeyFile).Msg("Key file exists but content is invalid; generating a new key")
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("generating master key: %w", err)
	}
	keyHex := hex.EncodeToString(buf)

	if err := os.WriteFile(masterKeyFile, []byte(keyHex+"\n"), 0600); err != nil {
		return fmt.Errorf("saving master key to %s: %w", masterKeyFile, err)
	}

	c.InstanceKey = keyHex
	log.Info().Str("file", masterKeyFile).Msg("Generated and saved new instance key")
	return nil
}

// DeriveSessionSecret derives the session HMAC secret from the master key
// via HKDF-SHA256. Must be called after EnsureMasterKey.
func (c *Config) DeriveSessionSecret() error {
	if c.InstanceKey == "" {
		return fmt.Errorf("instance key must be set before deriving session secret")
	}
	masterKey, err := hex.DecodeString(c.InstanceKey)
	if err != nil {
		return fmt.Errorf("decoding master key: %w", err)
	}
	r := hkdf.New(sha256.New, masterKey, nil, []byte("swamp-session-secret"))
	buf := make([]byte, 32)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("deriving session secret: %w", err)
	}
	c.SessionSecret = hex.EncodeToString(buf)
	return nil
}

// LoadAgentKeyFile reads the agent API key from a file if not already set.
func (c *Config) LoadAgentKeyFile() error {
	if c.AgentAPIKey != "" {
		log.Info().Msg("Agent API key configured via environment")
		return nil
	}

	file := c.AgentAPIKeyFile
	if file != "" {
		if _, err := os.Stat(file); err != nil {
			log.Warn().Str("file", file).Msg("Agent API key file not found; analysis will be unavailable")
			return nil
		}
	}
	if file == "" {
		log.Warn().Msg("No agent API key configured (set AGENT_API_KEY or AGENT_API_KEY_FILE); analysis will be unavailable")
		return nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("reading agent API key file %s: %w", file, err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return fmt.Errorf("agent API key file %s is empty", file)
	}
	c.AgentAPIKey = key
	log.Info().Str("file", file).Str("agent", c.AgentBinary).Msg("Loaded agent API key from file")
	return nil
}

// IsDevelopment returns true if running in development mode.
func (c *Config) IsDevelopment() bool {
	return c.AppEnv == "development"
}

// IsKubernetesExecutor returns true if the executor mode is kubernetes.
func (c *Config) IsKubernetesExecutor() bool {
	return c.ExecutorMode == "kubernetes"
}

// IsProcessExecutor returns true if the executor mode is process.
func (c *Config) IsProcessExecutor() bool {
	return c.ExecutorMode == "process"
}

// IsWorkerMode returns true if this process is a worker pod.
func (c *Config) IsWorkerMode() bool {
	return c.WorkerMode
}

// ParseNodeSelector parses K8sWorkerNodeSelector ("key=val,key2=val2") into a map.
func (c *Config) ParseNodeSelector() map[string]string {
	return parseKeyValuePairs(c.K8sWorkerNodeSelector)
}

// ParseWorkerLabels parses K8sWorkerLabels into a map.
func (c *Config) ParseWorkerLabels() map[string]string {
	return parseKeyValuePairs(c.K8sWorkerLabels)
}

// ParseWorkerAnnotations parses the K8sWorkerAnnotations string into a map.
func (c *Config) ParseWorkerAnnotations() map[string]string {
	return parseKeyValuePairs(c.K8sWorkerAnnotations)
}
func parseKeyValuePairs(s string) map[string]string {
	m := make(map[string]string)
	if s == "" {
		return m
	}
	for _, pair := range strings.Split(s, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) == 2 {
			m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return m
}
