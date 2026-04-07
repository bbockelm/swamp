package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/bbockelm/swamp/internal/config"
)

// K8sClient is a lightweight Kubernetes API client for the subset of batch/v1
// and core APIs needed by the executor. It supports either in-cluster service
// account credentials or a kubeconfig file, avoiding a client-go dependency.
type K8sClient interface {
	CreateJob(ctx context.Context, namespace string, job map[string]any) error
	DeleteJob(ctx context.Context, namespace, name string) error
	GetJobPhase(ctx context.Context, namespace, name string) (string, error)
	ListJobs(ctx context.Context, namespace, labelSelector string) ([]JobInfo, error)
}

// JobInfo is minimal job metadata returned from list/get operations.
type JobInfo struct {
	Name   string
	Phase  string
	Labels map[string]string
}

// k8sClient uses either in-cluster credentials or a kubeconfig-specified
// token/client certificate.
type k8sClient struct {
	host        string
	tokenPath   string
	staticToken string
	httpClient  *http.Client
}

const (
	k8sTokenPath  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	k8sCACertPath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	k8sHostEnv    = "KUBERNETES_SERVICE_HOST"
	k8sPortEnv    = "KUBERNETES_SERVICE_PORT"
)

type kubeconfigFile struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                   string `yaml:"server"`
			CertificateAuthority     string `yaml:"certificate-authority"`
			CertificateAuthorityData string `yaml:"certificate-authority-data"`
			InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token                 string `yaml:"token"`
			TokenFile             string `yaml:"tokenFile"`
			ClientCertificate     string `yaml:"client-certificate"`
			ClientCertificateData string `yaml:"client-certificate-data"`
			ClientKey             string `yaml:"client-key"`
			ClientKeyData         string `yaml:"client-key-data"`
			Username              string `yaml:"username"`
			Password              string `yaml:"password"`
			Exec                  any    `yaml:"exec"`
		} `yaml:"user"`
	} `yaml:"users"`
}

// NewK8sClient creates a K8s client from the configured kubeconfig or by
// falling back to in-cluster credentials.
func NewK8sClient(cfg *config.Config) (K8sClient, error) {
	if strings.TrimSpace(cfg.Kubeconfig) != "" {
		return NewK8sClientFromKubeconfig(cfg.Kubeconfig)
	}
	return NewInClusterK8sClient()
}

// NewInClusterK8sClient creates a K8s client using in-cluster credentials.
func NewInClusterK8sClient() (K8sClient, error) {
	host := os.Getenv(k8sHostEnv)
	port := os.Getenv(k8sPortEnv)
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in a Kubernetes cluster (missing %s or %s env vars)", k8sHostEnv, k8sPortEnv)
	}

	// Load CA cert for TLS verification.
	caCert, err := os.ReadFile(k8sCACertPath)
	if err != nil {
		return nil, fmt.Errorf("reading K8s CA cert: %w", err)
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse K8s CA certificate")
	}
	tlsConfig.RootCAs = certPool

	return &k8sClient{
		host:      fmt.Sprintf("https://%s:%s", host, port),
		tokenPath: k8sTokenPath,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		},
	}, nil
}

// NewK8sClientFromKubeconfig creates a K8s client from a kubeconfig file.
func NewK8sClientFromKubeconfig(path string) (K8sClient, error) {
	resolvedPath, err := resolveKubeconfigPath(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig %s: %w", resolvedPath, err)
	}

	var cfg kubeconfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing kubeconfig %s: %w", resolvedPath, err)
	}
	if cfg.CurrentContext == "" {
		return nil, fmt.Errorf("kubeconfig %s has no current-context", resolvedPath)
	}

	ctxEntry, err := findNamedValue(cfg.Contexts, cfg.CurrentContext, func(item struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	}) string {
		return item.Name
	})
	if err != nil {
		return nil, fmt.Errorf("loading current context %q: %w", cfg.CurrentContext, err)
	}

	clusterEntry, err := findNamedValue(cfg.Clusters, ctxEntry.Context.Cluster, func(item struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                   string `yaml:"server"`
			CertificateAuthority     string `yaml:"certificate-authority"`
			CertificateAuthorityData string `yaml:"certificate-authority-data"`
			InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	}) string {
		return item.Name
	})
	if err != nil {
		return nil, fmt.Errorf("loading cluster %q: %w", ctxEntry.Context.Cluster, err)
	}

	userEntry, err := findNamedValue(cfg.Users, ctxEntry.Context.User, func(item struct {
		Name string `yaml:"name"`
		User struct {
			Token                 string `yaml:"token"`
			TokenFile             string `yaml:"tokenFile"`
			ClientCertificate     string `yaml:"client-certificate"`
			ClientCertificateData string `yaml:"client-certificate-data"`
			ClientKey             string `yaml:"client-key"`
			ClientKeyData         string `yaml:"client-key-data"`
			Username              string `yaml:"username"`
			Password              string `yaml:"password"`
			Exec                  any    `yaml:"exec"`
		} `yaml:"user"`
	}) string {
		return item.Name
	})
	if err != nil {
		return nil, fmt.Errorf("loading user %q: %w", ctxEntry.Context.User, err)
	}

	if userEntry.User.Exec != nil {
		return nil, fmt.Errorf("kubeconfig exec auth is not supported by the lightweight client")
	}
	if clusterEntry.Cluster.Server == "" {
		return nil, fmt.Errorf("kubeconfig cluster %q has no server", clusterEntry.Name)
	}

	tlsConfig, err := buildKubeconfigTLSConfig(filepath.Dir(resolvedPath), clusterEntry, userEntry)
	if err != nil {
		return nil, err
	}

	client := &k8sClient{
		host:        strings.TrimRight(clusterEntry.Cluster.Server, "/"),
		staticToken: strings.TrimSpace(userEntry.User.Token),
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
	}

	if userEntry.User.TokenFile != "" {
		client.tokenPath = resolveRelativePath(filepath.Dir(resolvedPath), userEntry.User.TokenFile)
	}
	if client.staticToken == "" && client.tokenPath == "" && userEntry.User.Username != "" {
		return nil, fmt.Errorf("kubeconfig basic auth is not supported by the lightweight client")
	}

	return client, nil
}

// token reads the current service account token (it may be rotated).
func (c *k8sClient) token() (string, error) {
	if c.staticToken != "" {
		return c.staticToken, nil
	}
	if c.tokenPath == "" {
		return "", nil
	}
	data, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return "", fmt.Errorf("reading Kubernetes token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// doRequest performs an authenticated K8s API request.

func (c *k8sClient) doRequest(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	tok, err := c.token()
	if err != nil {
		return nil, 0, err
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshaling request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := c.host + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("creating request: %w", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("K8s API request %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// CreateJob creates a job in the given namespace.
func (c *k8sClient) CreateJob(ctx context.Context, namespace string, job map[string]any) error {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", namespace)
	body, status, err := c.doRequest(ctx, "POST", path, job)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("CreateJob returned %d: %s", status, truncateBody(body))
	}
	return nil
}

// DeleteJob deletes a job by name and cascades cleanup to its pods.
func (c *k8sClient) DeleteJob(ctx context.Context, namespace, name string) error {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s?propagationPolicy=Background", namespace, name)
	body, status, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}
	if status == 404 {
		return nil // already gone
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("DeleteJob returned %d: %s", status, truncateBody(body))
	}
	return nil
}

// GetJobPhase returns the current phase of a job (Pending, Running, Succeeded, Failed, Unknown).
func (c *k8sClient) GetJobPhase(ctx context.Context, namespace, name string) (string, error) {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", namespace, name)
	body, status, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return "", err
	}
	if status == 404 {
		return "Unknown", nil
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("GetJob returned %d: %s", status, truncateBody(body))
	}

	var jobResp struct {
		Status k8sJobStatus `json:"status"`
	}
	if err := json.Unmarshal(body, &jobResp); err != nil {
		return "", fmt.Errorf("parsing job status: %w", err)
	}
	return jobPhase(jobResp.Status), nil
}

// ListJobs lists jobs matching a label selector.
func (c *k8sClient) ListJobs(ctx context.Context, namespace, labelSelector string) ([]JobInfo, error) {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs?labelSelector=%s", namespace, url.QueryEscape(labelSelector))
	body, status, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("ListJobs returned %d: %s", status, truncateBody(body))
	}

	var listResp struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Active     int32 `json:"active"`
				Succeeded  int32 `json:"succeeded"`
				Failed     int32 `json:"failed"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("parsing job list: %w", err)
	}

	jobs := make([]JobInfo, len(listResp.Items))
	for i, item := range listResp.Items {
		jobs[i] = JobInfo{
			Name:   item.Metadata.Name,
			Phase:  jobPhase(k8sJobStatus(item.Status)),
			Labels: item.Metadata.Labels,
		}
	}
	return jobs, nil
}

type k8sJobStatus struct {
	Active     int32 `json:"active"`
	Succeeded  int32 `json:"succeeded"`
	Failed     int32 `json:"failed"`
	Conditions []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"conditions"`
}

func jobPhase(status k8sJobStatus) string {
	for _, condition := range status.Conditions {
		if condition.Status != "True" {
			continue
		}
		switch condition.Type {
		case "Complete":
			return "Succeeded"
		case "Failed":
			return "Failed"
		}
	}
	if status.Active > 0 {
		return "Running"
	}
	if status.Succeeded > 0 {
		return "Succeeded"
	}
	if status.Failed > 0 {
		return "Failed"
	}
	return "Pending"
}

func buildKubeconfigTLSConfig(
	baseDir string,
	clusterEntry struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                   string `yaml:"server"`
			CertificateAuthority     string `yaml:"certificate-authority"`
			CertificateAuthorityData string `yaml:"certificate-authority-data"`
			InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	},
	userEntry struct {
		Name string `yaml:"name"`
		User struct {
			Token                 string `yaml:"token"`
			TokenFile             string `yaml:"tokenFile"`
			ClientCertificate     string `yaml:"client-certificate"`
			ClientCertificateData string `yaml:"client-certificate-data"`
			ClientKey             string `yaml:"client-key"`
			ClientKeyData         string `yaml:"client-key-data"`
			Username              string `yaml:"username"`
			Password              string `yaml:"password"`
			Exec                  any    `yaml:"exec"`
		} `yaml:"user"`
	},
) (*tls.Config, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}

	if clusterEntry.Cluster.InsecureSkipTLSVerify {
		tlsConfig.InsecureSkipVerify = true
	} else {
		caData, err := loadPEMData(baseDir, clusterEntry.Cluster.CertificateAuthority, clusterEntry.Cluster.CertificateAuthorityData)
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig CA for cluster %q: %w", clusterEntry.Name, err)
		}
		if len(caData) > 0 {
			certPool := x509.NewCertPool()
			if !certPool.AppendCertsFromPEM(caData) {
				return nil, fmt.Errorf("failed to parse kubeconfig CA certificate for cluster %q", clusterEntry.Name)
			}
			tlsConfig.RootCAs = certPool
		}
	}

	certData, err := loadPEMData(baseDir, userEntry.User.ClientCertificate, userEntry.User.ClientCertificateData)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig client certificate for user %q: %w", userEntry.Name, err)
	}
	keyData, err := loadPEMData(baseDir, userEntry.User.ClientKey, userEntry.User.ClientKeyData)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig client key for user %q: %w", userEntry.Name, err)
	}
	if len(certData) > 0 || len(keyData) > 0 {
		if len(certData) == 0 || len(keyData) == 0 {
			return nil, fmt.Errorf("kubeconfig user %q must provide both client certificate and client key", userEntry.Name)
		}
		certificate, err := tls.X509KeyPair(certData, keyData)
		if err != nil {
			return nil, fmt.Errorf("parsing kubeconfig client certificate for user %q: %w", userEntry.Name, err)
		}
		certificate.Leaf = parseLeafCertificate(certificate.Certificate)
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}

	return tlsConfig, nil
}

func resolveKubeconfigPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("kubeconfig path is empty")
	}
	if strings.HasPrefix(trimmed, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory for kubeconfig: %w", err)
		}
		trimmed = filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	}
	return filepath.Abs(trimmed)
}

func resolveRelativePath(baseDir, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(baseDir, value)
}

func loadPEMData(baseDir, filePath, encoded string) ([]byte, error) {
	if strings.TrimSpace(encoded) != "" {
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}
		return data, nil
	}
	if strings.TrimSpace(filePath) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(resolveRelativePath(baseDir, strings.TrimSpace(filePath)))
	if err != nil {
		return nil, err
	}
	return data, nil
}

func parseLeafCertificate(chain [][]byte) *x509.Certificate {
	if len(chain) == 0 {
		return nil
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return nil
	}
	return leaf
}

func findNamedValue[T any](items []T, name string, key func(T) string) (T, error) {
	var zero T
	for _, item := range items {
		if key(item) == name {
			return item, nil
		}
	}
	return zero, fmt.Errorf("entry %q not found", name)
}

func truncateBody(b []byte) string {
	s := string(b)
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
