package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DiscoveredModel is a model returned by a provider's model listing API.
type DiscoveredModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
}

// DiscoverModels queries a provider's API for available models.
// apiSchema is "anthropic" or "openai", baseURL is the provider's base URL,
// and apiKey is the decrypted API key.
func DiscoverModels(ctx context.Context, apiSchema, baseURL, apiKey string) ([]DiscoveredModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	switch apiSchema {
	case "anthropic":
		return discoverAnthropicModels(ctx, baseURL, apiKey)
	case "openai":
		return discoverOpenAIModels(ctx, baseURL, apiKey)
	default:
		return nil, fmt.Errorf("unsupported api_schema: %s", apiSchema)
	}
}

func discoverAnthropicModels(ctx context.Context, baseURL, apiKey string) ([]DiscoveredModel, error) {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	url := baseURL + "/v1/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("anthropic API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	models := make([]DiscoveredModel, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, DiscoveredModel{
			ID:          m.ID,
			DisplayName: m.DisplayName,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func discoverOpenAIModels(ctx context.Context, baseURL, apiKey string) ([]DiscoveredModel, error) {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	url := baseURL + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("openai-compatible API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	models := make([]DiscoveredModel, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, DiscoveredModel{
			ID:          m.ID,
			DisplayName: m.ID,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
