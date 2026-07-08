package memorybackend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// mem0Backend talks to a self-hosted github.com/mem0ai/mem0 server (the
// server/ FastAPI stack, no version prefix in its routes — verified against
// server/main.py and server/auth.py in the mem0ai/mem0 repo).
type mem0Backend struct {
	cfg    Config
	client *http.Client
}

func newMem0(cfg Config) *mem0Backend {
	return &mem0Backend{cfg: cfg, client: http.DefaultClient}
}

func (b *mem0Backend) Name() string { return "mem0" }

func (b *mem0Backend) url(path string) string {
	return strings.TrimRight(b.cfg.BaseURL, "/") + path
}

func (b *mem0Backend) do(ctx context.Context, url string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("memorybackend/mem0: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("memorybackend/mem0: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if b.cfg.APIKey != "" {
		req.Header.Set("X-API-Key", b.cfg.APIKey)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("memorybackend/mem0: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("memorybackend/mem0: %s returned %s", url, resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("memorybackend/mem0: decode response: %w", err)
	}
	return nil
}

func (b *mem0Backend) Store(ctx context.Context, content string) error {
	body := map[string]any{
		"messages": []map[string]any{{"role": "user", "content": content}},
		"user_id":  b.cfg.namespace(),
	}
	return b.do(ctx, b.url("/memories"), body, nil)
}

// mem0SearchResponse is decoded tolerantly: the self-hosted server's /search
// response delegates serialization to the core mem0 library rather than
// defining its own schema in server/main.py, so the exact per-result field
// name for the memory text isn't verified from source. Each result is kept
// as a raw map and resolved by resultText/resultScore below.
type mem0SearchResponse struct {
	Results []map[string]json.RawMessage `json:"results"`
}

func mem0ResultText(item map[string]json.RawMessage) string {
	for _, key := range []string{"memory", "content", "text"} {
		raw, ok := item[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return s
		}
	}
	return ""
}

func mem0ResultID(item map[string]json.RawMessage) string {
	raw, ok := item["id"]
	if !ok {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func mem0ResultScore(item map[string]json.RawMessage) float64 {
	raw, ok := item["score"]
	if !ok {
		return 0
	}
	var f float64
	_ = json.Unmarshal(raw, &f)
	return f
}

func (b *mem0Backend) Recall(ctx context.Context, query string) ([]Result, error) {
	body := map[string]any{
		"query":   query,
		"user_id": b.cfg.namespace(),
		"top_k":   5,
	}
	var resp mem0SearchResponse
	if err := b.do(ctx, b.url("/search"), body, &resp); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(resp.Results))
	for _, item := range resp.Results {
		text := mem0ResultText(item)
		if text == "" {
			continue
		}
		results = append(results, Result{ID: mem0ResultID(item), Content: text, Score: mem0ResultScore(item)})
	}
	return results, nil
}
