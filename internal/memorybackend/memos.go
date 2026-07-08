package memorybackend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// memosBackend talks to a self-hosted github.com/MemTensor/MemOS server
// (the "memos" backend option — MemTensor/MemOS specifically, not
// agiresearch/MemOS or usememos/memos). Endpoints and auth verified against
// src/memos/api/routers/server_router.go, product_models.py, and
// middleware/auth.py in the MemTensor/MemOS repo.
type memosBackend struct {
	cfg    Config
	client *http.Client
}

func newMemOS(cfg Config) *memosBackend {
	return &memosBackend{cfg: cfg, client: http.DefaultClient}
}

func (b *memosBackend) Name() string { return "memos" }

func (b *memosBackend) url(path string) string {
	return strings.TrimRight(b.cfg.BaseURL, "/") + "/product" + path
}

func (b *memosBackend) do(ctx context.Context, url string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("memorybackend/memos: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("memorybackend/memos: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if b.cfg.APIKey != "" {
		req.Header.Set("Authorization", b.cfg.APIKey)
	} else {
		// Auth is disabled by default (AUTH_ENABLED=false); identity comes
		// from this header instead of a key.
		req.Header.Set("X-User-Name", b.cfg.namespace())
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("memorybackend/memos: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("memorybackend/memos: %s returned %s", url, resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("memorybackend/memos: decode response: %w", err)
	}
	return nil
}

func (b *memosBackend) Store(ctx context.Context, content string) error {
	body := map[string]any{
		"user_id":  b.cfg.namespace(),
		"messages": []map[string]any{{"role": "user", "content": content}},
	}
	return b.do(ctx, b.url("/add"), body, nil)
}

type memosSearchResponse struct {
	Data struct {
		TextMem []struct {
			Memories []struct {
				ID       string `json:"id"`
				Memory   string `json:"memory"`
				Metadata struct {
					Relativity float64 `json:"relativity"`
				} `json:"metadata"`
			} `json:"memories"`
		} `json:"text_mem"`
	} `json:"data"`
}

func (b *memosBackend) Recall(ctx context.Context, query string) ([]Result, error) {
	body := map[string]any{
		"query":   query,
		"user_id": b.cfg.namespace(),
		"top_k":   5,
	}
	var resp memosSearchResponse
	if err := b.do(ctx, b.url("/search"), body, &resp); err != nil {
		return nil, err
	}
	var results []Result
	for _, group := range resp.Data.TextMem {
		for _, m := range group.Memories {
			results = append(results, Result{ID: m.ID, Content: m.Memory, Score: m.Metadata.Relativity})
		}
	}
	return results, nil
}
