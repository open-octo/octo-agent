package memorybackend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// hindsightBackend talks to a self-hosted github.com/vectorize-io/hindsight
// server. Endpoints and auth verified against
// https://hindsight.vectorize.io/api-reference and
// https://hindsight.vectorize.io/developer/configuration.
type hindsightBackend struct {
	cfg    Config
	client *http.Client
}

func newHindsight(cfg Config) *hindsightBackend {
	return &hindsightBackend{cfg: cfg, client: http.DefaultClient}
}

func (b *hindsightBackend) Name() string { return "hindsight" }

func (b *hindsightBackend) bankURL(suffix string) string {
	return fmt.Sprintf("%s/v1/default/banks/%s%s", strings.TrimRight(b.cfg.BaseURL, "/"), b.cfg.namespace(), suffix)
}

func (b *hindsightBackend) do(ctx context.Context, url string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("memorybackend/hindsight: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("memorybackend/hindsight: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if b.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.cfg.APIKey)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("memorybackend/hindsight: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("memorybackend/hindsight: %s returned %s", url, resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("memorybackend/hindsight: decode response: %w", err)
	}
	return nil
}

func (b *hindsightBackend) Store(ctx context.Context, content string) error {
	body := map[string]any{
		"items": []map[string]any{{"content": content}},
	}
	return b.do(ctx, b.bankURL("/memories"), body, nil)
}

type hindsightRecallResponse struct {
	Results []struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Scores struct {
			Final float64 `json:"final"`
		} `json:"scores"`
	} `json:"results"`
}

func (b *hindsightBackend) Recall(ctx context.Context, query string) ([]Result, error) {
	body := map[string]any{
		"query":      query,
		"max_tokens": 4096,
	}
	var resp hindsightRecallResponse
	if err := b.do(ctx, b.bankURL("/memories/recall"), body, &resp); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, Result{ID: r.ID, Content: r.Text, Score: r.Scores.Final})
	}
	return results, nil
}
