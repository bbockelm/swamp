package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/config"
)

// RunLLMProxy starts the LLM API key proxy sidecar.
//
// It exchanges the one-time sidecar token with the SWAMP server to obtain the
// real external LLM API key and endpoint URL (so those credentials never appear
// in the pod spec), then starts an HTTP reverse proxy that listens on
// 127.0.0.1:<LLM_PROXY_PORT>.
//
// All requests forwarded by the proxy have their Authorization header replaced
// with the real API key, so the main worker container can call the proxy
// without any credentials and still reach the external LLM securely.
//
// This function blocks until the HTTP server terminates.
func RunLLMProxy(cfg *config.Config) error {
	if cfg.LLMProxyToken == "" {
		return fmt.Errorf("SWAMP_LLM_PROXY_TOKEN is required in LLM proxy mode")
	}
	if cfg.WorkerServer == "" {
		return fmt.Errorf("SWAMP_WORKER_SERVER is required in LLM proxy mode")
	}

	serverURL := strings.TrimRight(cfg.WorkerServer, "/")

	log.Info().Str("server", serverURL).Msg("LLM proxy: exchanging sidecar token...")

	apiKey, endpointURL, err := exchangeSidecarCredentials(serverURL, cfg.LLMProxyToken)
	if err != nil {
		return fmt.Errorf("sidecar token exchange failed: %w", err)
	}

	// Clear the one-time token from config memory immediately after exchange.
	cfg.LLMProxyToken = ""

	log.Info().Str("upstream", endpointURL).Int("port", cfg.LLMProxyPort).Msg("LLM proxy: starting")

	target, err := url.Parse(endpointURL)
	if err != nil {
		return fmt.Errorf("parsing external LLM endpoint URL %q: %w", endpointURL, err)
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			// Prepend the base path from the target (e.g. /v1 from the endpoint URL).
			req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
			if target.RawQuery != "" && req.URL.RawQuery != "" {
				req.URL.RawQuery = target.RawQuery + "&" + req.URL.RawQuery
			} else if target.RawQuery != "" {
				req.URL.RawQuery = target.RawQuery
			}
			// Replace any incoming Authorization header with the real API key.
			// The main container sends no auth (or a placeholder), so this
			// ensures only the real key is ever sent upstream.
			req.Header.Set("Authorization", "Bearer "+apiKey)
		},
		// Enable immediate flushing for streaming responses (SSE/chunked).
		FlushInterval: -1,
	}

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.LLMProxyPort)
	srv := &http.Server{
		Addr:              addr,
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Info().Str("addr", addr).Str("upstream", endpointURL).Msg("LLM proxy listening")
	return srv.ListenAndServe()
}

// exchangeSidecarCredentials calls the SWAMP server to exchange a one-time
// sidecar token for the external LLM API key and endpoint URL.
func exchangeSidecarCredentials(serverURL, token string) (apiKey, endpointURL string, err error) {
	body, _ := json.Marshal(map[string]string{"token": token})
	exchangeURL := serverURL + "/api/v1/internal/worker/exchange-sidecar"

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 2 * time.Second
			log.Debug().Int("attempt", attempt+1).Dur("backoff", backoff).Msg("LLM proxy: retrying token exchange")
			time.Sleep(backoff)
		}

		resp, err := http.Post(exchangeURL, "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("exchange failed (status %d): %s", resp.StatusCode, string(respBody))
			continue
		}

		var result struct {
			APIKey      string `json:"api_key"`
			EndpointURL string `json:"endpoint_url"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", "", fmt.Errorf("decoding sidecar exchange response: %w", err)
		}
		if result.APIKey == "" || result.EndpointURL == "" {
			return "", "", fmt.Errorf("sidecar exchange response missing api_key or endpoint_url")
		}
		return result.APIKey, result.EndpointURL, nil
	}
	return "", "", fmt.Errorf("sidecar token exchange failed after retries: %w", lastErr)
}

// singleJoiningSlash joins two URL paths with exactly one slash between them.
func singleJoiningSlash(a, b string) string {
	aSlash := strings.HasSuffix(a, "/")
	bSlash := strings.HasPrefix(b, "/")
	switch {
	case aSlash && bSlash:
		return a + b[1:]
	case !aSlash && !bSlash:
		return a + "/" + b
	}
	return a + b
}
