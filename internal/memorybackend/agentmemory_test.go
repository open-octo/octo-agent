package memorybackend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentMemoryStoreNoAPIKeyOmitsAuth(t *testing.T) {
	var gotPath, gotAuthHeader string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthHeader = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "mem_1"})
	}))
	defer srv.Close()

	b := newAgentMemory(Config{BaseURL: srv.URL, Namespace: "proj"})
	if err := b.Store(context.Background(), "hello world"); err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}

	if gotPath != "/agentmemory/remember" {
		t.Errorf("path = %q, want /agentmemory/remember", gotPath)
	}
	if gotAuthHeader != "" {
		t.Errorf("Authorization = %q, want none when no APIKey is configured", gotAuthHeader)
	}
	if gotBody["content"] != "hello world" {
		t.Errorf("content = %v, want %q", gotBody["content"], "hello world")
	}
	if gotBody["project"] != "proj" {
		t.Errorf("project = %v, want %q", gotBody["project"], "proj")
	}
}

func TestAgentMemoryStoreWithAPIKeyUsesBearer(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "mem_1"})
	}))
	defer srv.Close()

	b := newAgentMemory(Config{BaseURL: srv.URL, APIKey: "s3cret"})
	if err := b.Store(context.Background(), "hi"); err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}
	if gotAuthHeader != "Bearer s3cret" {
		t.Errorf("Authorization = %q, want %q", gotAuthHeader, "Bearer s3cret")
	}
}

func TestAgentMemoryRecall(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"format": "narrative",
			"results": []map[string]any{
				{"obsId": "mem_1", "title": "tabs pref", "narrative": "user likes tabs", "score": 0.7},
			},
		})
	}))
	defer srv.Close()

	b := newAgentMemory(Config{BaseURL: srv.URL, Namespace: "proj"})
	results, err := b.Recall(context.Background(), "indentation")
	if err != nil {
		t.Fatalf("Recall: unexpected error: %v", err)
	}
	if gotPath != "/agentmemory/search" {
		t.Errorf("path = %q, want /agentmemory/search", gotPath)
	}
	if gotBody["format"] != "narrative" {
		t.Errorf("format = %v, want %q", gotBody["format"], "narrative")
	}
	if gotBody["project"] != "proj" {
		t.Errorf("project = %v, want %q", gotBody["project"], "proj")
	}
	if len(results) != 1 || results[0].ID != "mem_1" || results[0].Content != "user likes tabs" || results[0].Score != 0.7 {
		t.Errorf("results = %+v, want one result {mem_1, user likes tabs, 0.7}", results)
	}
}

// TestAgentMemoryRecallFallsBackToTitle covers a result whose narrative is
// empty — the title stands in as content rather than dropping the row.
func TestAgentMemoryRecallFallsBackToTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"obsId": "mem_2", "title": "only a title", "narrative": "", "score": 0.5},
			},
		})
	}))
	defer srv.Close()

	b := newAgentMemory(Config{BaseURL: srv.URL})
	results, err := b.Recall(context.Background(), "q")
	if err != nil {
		t.Fatalf("Recall: unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Content != "only a title" {
		t.Errorf("results = %+v, want content to fall back to the title", results)
	}
}
