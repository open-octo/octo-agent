package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/version"
)

// ModelsPath is the path appended to BaseURL to list the served models.
const ModelsPath = "/v1/models"

// maxModelsBodyBytes caps the listing response. Relay gateways can serve
// hundreds of models with verbose metadata each; a few MB is still far above
// that, and bounds a misbehaving server.
const maxModelsBodyBytes = 4 << 20

// modelsURL returns BaseURL + ModelsPath with the same "/v1" handling as
// endpointURL, so the listing hits the server the chat requests go to.
func (c *Client) modelsURL() string {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + ModelsPath
}

// ListModels returns the sorted, de-duplicated model ids the server reports
// at GET /v1/models. It is a single user-initiated call, so it neither
// retries nor waits on the endpoint's rate limiter.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.modelsURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("User-Agent", version.UserAgent())
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: list models: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("openai: read models response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if len(body) > maxErrorBodyBytes {
			body = body[:maxErrorBodyBytes]
		}
		var apiErr apiError
		if jerr := json.Unmarshal(body, &apiErr); jerr == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("openai: decode models response: %w", err)
	}
	seen := make(map[string]bool, len(list.Data))
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
