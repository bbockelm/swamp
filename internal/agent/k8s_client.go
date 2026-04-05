package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// K8sClient is a lightweight Kubernetes API client that uses the
// in-cluster service account credentials. This avoids pulling in the
// massive client-go dependency.
type K8sClient interface {
	CreatePod(ctx context.Context, namespace string, pod map[string]any) error
	DeletePod(ctx context.Context, namespace, name string) error
	GetPodPhase(ctx context.Context, namespace, name string) (string, error)
	ListPods(ctx context.Context, namespace, labelSelector string) ([]PodInfo, error)
}

// PodInfo is minimal pod metadata returned from list/get operations.
type PodInfo struct {
	Name   string
	Phase  string
	Labels map[string]string
}

// inClusterK8sClient uses the in-cluster service account token and CA cert.
type inClusterK8sClient struct {
	host       string
	tokenPath  string
	httpClient *http.Client
}

const (
	k8sTokenPath  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	k8sCACertPath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	k8sHostEnv    = "KUBERNETES_SERVICE_HOST"
	k8sPortEnv    = "KUBERNETES_SERVICE_PORT"
)

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

	return &inClusterK8sClient{
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

// token reads the current service account token (it may be rotated).
func (c *inClusterK8sClient) token() (string, error) {
	data, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return "", fmt.Errorf("reading service account token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// doRequest performs an authenticated K8s API request.
func (c *inClusterK8sClient) doRequest(ctx context.Context, method, path string, body any) ([]byte, int, error) {
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
	req.Header.Set("Authorization", "Bearer "+tok)
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

// CreatePod creates a pod in the given namespace.
func (c *inClusterK8sClient) CreatePod(ctx context.Context, namespace string, pod map[string]any) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods", namespace)
	body, status, err := c.doRequest(ctx, "POST", path, pod)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("CreatePod returned %d: %s", status, truncateBody(body))
	}
	return nil
}

// DeletePod deletes a pod by name.
func (c *inClusterK8sClient) DeletePod(ctx context.Context, namespace, name string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", namespace, name)
	body, status, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}
	if status == 404 {
		return nil // already gone
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("DeletePod returned %d: %s", status, truncateBody(body))
	}
	return nil
}

// GetPodPhase returns the current phase of a pod (Pending, Running, Succeeded, Failed, Unknown).
func (c *inClusterK8sClient) GetPodPhase(ctx context.Context, namespace, name string) (string, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", namespace, name)
	body, status, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return "", err
	}
	if status == 404 {
		return "Unknown", nil
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("GetPod returned %d: %s", status, truncateBody(body))
	}

	var podResp struct {
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &podResp); err != nil {
		return "", fmt.Errorf("parsing pod status: %w", err)
	}
	return podResp.Status.Phase, nil
}

// ListPods lists pods matching a label selector.
func (c *inClusterK8sClient) ListPods(ctx context.Context, namespace, labelSelector string) ([]PodInfo, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, labelSelector)
	body, status, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("ListPods returned %d: %s", status, truncateBody(body))
	}

	var listResp struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("parsing pod list: %w", err)
	}

	pods := make([]PodInfo, len(listResp.Items))
	for i, item := range listResp.Items {
		pods[i] = PodInfo{
			Name:   item.Metadata.Name,
			Phase:  item.Status.Phase,
			Labels: item.Metadata.Labels,
		}
	}
	return pods, nil
}

func truncateBody(b []byte) string {
	s := string(b)
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
