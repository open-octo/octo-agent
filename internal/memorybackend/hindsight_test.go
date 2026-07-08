package memorybackend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHindsightStore(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer srv.Close()

	b := newHindsight(Config{BaseURL: srv.URL, Namespace: "proj", APIKey: "secret"})
	if err := b.Store(context.Background(), "hello world"); err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/v1/default/banks/proj/memories"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret")
	}
	items, ok := gotBody["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("body items = %v, want a one-element array", gotBody["items"])
	}
	item := items[0].(map[string]any)
	if item["content"] != "hello world" {
		t.Errorf("item content = %v, want %q", item["content"], "hello world")
	}
}

func TestHindsightStoreNoAPIKeyOmitsAuthHeader(t *testing.T) {
	var gotAuth string
	sawAuth := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, sawAuth = r.Header.Get("Authorization"), r.Header.Get("Authorization") != ""
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer srv.Close()

	b := newHindsight(Config{BaseURL: srv.URL})
	if err := b.Store(context.Background(), "hi"); err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}
	if sawAuth {
		t.Errorf("Authorization header = %q, want none (hindsight defaults to no auth)", gotAuth)
	}
}

func TestHindsightRecall(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": "m1", "text": "likes tabs", "scores": map[string]any{"final": 0.9}},
			},
		})
	}))
	defer srv.Close()

	b := newHindsight(Config{BaseURL: srv.URL, Namespace: "proj"})
	results, err := b.Recall(context.Background(), "indentation preference")
	if err != nil {
		t.Fatalf("Recall: unexpected error: %v", err)
	}

	if want := "/v1/default/banks/proj/memories/recall"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotBody["query"] != "indentation preference" {
		t.Errorf("query = %v, want %q", gotBody["query"], "indentation preference")
	}
	if len(results) != 1 || results[0].ID != "m1" || results[0].Content != "likes tabs" || results[0].Score != 0.9 {
		t.Errorf("results = %+v, want one result {m1, likes tabs, 0.9}", results)
	}
}

func TestHindsightErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := newHindsight(Config{BaseURL: srv.URL})
	if err := b.Store(context.Background(), "hi"); err == nil {
		t.Fatal("Store against a 401 response: want error, got nil")
	}
}
