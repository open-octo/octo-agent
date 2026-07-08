package memorybackend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// mem0CloudBaseURL is the fixed hosted-Platform endpoint, used when Mode is
// "cloud" and BaseURL is left blank — there's only one, unlike self-hosted
// deployments which need an explicit address.
const mem0CloudBaseURL = "https://api.mem0.ai"

// mem0Backend talks to either a self-hosted github.com/mem0ai/mem0 server
// (the server/ FastAPI stack, no version prefix in its routes — verified
// against server/main.py and server/auth.py in the mem0ai/mem0 repo) or,
// when cfg.Mode == "cloud", the hosted mem0 Platform API (api.mem0.ai) —
// verified against docs.mem0.ai/platform/quickstart. The two differ in
// endpoint path, auth header, and part of the search request body; the
// response shape for search is close enough (both return flat {id, memory,
// score} objects) that decoding is shared.
type mem0Backend struct {
	cfg    Config
	client *http.Client
}

func newMem0(cfg Config) *mem0Backend {
	return &mem0Backend{cfg: cfg, client: http.DefaultClient}
}

func (b *mem0Backend) Name() string { return "mem0" }

func (b *mem0Backend) cloud() bool { return b.cfg.Mode == "cloud" }

func (b *mem0Backend) addPath() string {
	if b.cloud() {
		return "/v3/memories/add/"
	}
	return "/memories"
}

func (b *mem0Backend) searchPath() string {
	if b.cloud() {
		return "/v3/memories/search/"
	}
	return "/search"
}

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
		if b.cloud() {
			req.Header.Set("Authorization", "Token "+b.cfg.APIKey)
		} else {
			req.Header.Set("X-API-Key", b.cfg.APIKey)
		}
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
	return b.do(ctx, b.url(b.addPath()), body, nil)
}

// mem0SearchResponse is decoded tolerantly. The self-hosted server's /search
// delegates serialization to the core mem0 library, which (per
// mem0/memory/main.py's _search_vector_store) returns flat {id, memory,
// score} objects — "memory" is the field this code expects to hit. The
// content/text fallbacks in mem0ResultText are a hedge against future/
// self-hosted-fork field-name drift, not because the current shape is
// unverified. Each result is kept as a raw map and resolved by
// mem0ResultText/mem0ResultID/mem0ResultScore below.
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
	body := map[string]any{"query": query}
	if b.cloud() {
		// Platform API scopes by a top-level "filters" object rather than a
		// bare user_id, and doesn't document a top_k equivalent.
		body["filters"] = map[string]any{"user_id": b.cfg.namespace()}
	} else {
		body["user_id"] = b.cfg.namespace()
		body["top_k"] = 5
	}
	var resp mem0SearchResponse
	if err := b.do(ctx, b.url(b.searchPath()), body, &resp); err != nil {
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
