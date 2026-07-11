package memorybackend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// agentMemoryBackend talks to a self-hosted github.com/rohitg00/agentmemory
// server (the "agentmemory" backend option — a Node/TypeScript memory server
// on port 3111). Endpoints, request bodies, and auth verified against
// src/triggers/api.ts, src/functions/remember.ts, and src/functions/search.ts
// in the rohitg00/agentmemory repo.
//
// Two endpoints are used: /agentmemory/remember to store (its content is
// indexed for retrieval immediately) and /agentmemory/search with
// format:"narrative" to recall — search returns the full stored text as each
// result's "narrative" field, whereas /agentmemory/smart-search only returns
// titles, not the content itself.
type agentMemoryBackend struct {
	cfg    Config
	client *http.Client
}

func newAgentMemory(cfg Config) *agentMemoryBackend {
	return &agentMemoryBackend{cfg: cfg, client: http.DefaultClient}
}

func (b *agentMemoryBackend) Name() string { return "agentmemory" }

func (b *agentMemoryBackend) url(path string) string {
	return strings.TrimRight(b.cfg.BaseURL, "/") + path
}

func (b *agentMemoryBackend) do(ctx context.Context, path string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("memorybackend/agentmemory: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url(path), bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("memorybackend/agentmemory: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Auth is off by default (localhost is open); a Bearer token is only
	// required when the server is started with AGENTMEMORY_SECRET set.
	if b.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.cfg.APIKey)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("memorybackend/agentmemory: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("memorybackend/agentmemory: %s returned %s", b.url(path), resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("memorybackend/agentmemory: decode response: %w", err)
	}
	return nil
}

func (b *agentMemoryBackend) Store(ctx context.Context, content string) error {
	body := map[string]any{
		"content": content,
		"project": b.cfg.namespace(),
	}
	return b.do(ctx, "/agentmemory/remember", body, nil)
}

// agentMemorySearchResponse decodes the narrative-format /agentmemory/search
// response. Each result carries the observation id, its title, and the full
// stored text as "narrative" (memoryToObservation maps a remembered memory's
// content onto this field server-side), plus a hybrid-retrieval score.
type agentMemorySearchResponse struct {
	Results []struct {
		ObsID     string  `json:"obsId"`
		Title     string  `json:"title"`
		Narrative string  `json:"narrative"`
		Score     float64 `json:"score"`
	} `json:"results"`
}

func (b *agentMemoryBackend) Recall(ctx context.Context, query string) ([]Result, error) {
	body := map[string]any{
		"query":   query,
		"project": b.cfg.namespace(),
		"format":  "narrative",
		"limit":   5,
	}
	var resp agentMemorySearchResponse
	if err := b.do(ctx, "/agentmemory/search", body, &resp); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		content := r.Narrative
		if content == "" {
			content = r.Title
		}
		if content == "" {
			continue
		}
		results = append(results, Result{ID: r.ObsID, Content: content, Score: r.Score})
	}
	return results, nil
}
