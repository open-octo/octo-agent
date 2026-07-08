package memorybackend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMem0Store(t *testing.T) {
	var gotPath, gotAPIKey string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("X-API-Key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	b := newMem0(Config{BaseURL: srv.URL, Namespace: "proj", APIKey: "m0sk_secret"})
	if err := b.Store(context.Background(), "hello world"); err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}

	if gotPath != "/memories" {
		t.Errorf("path = %q, want /memories (no version prefix)", gotPath)
	}
	if gotAPIKey != "m0sk_secret" {
		t.Errorf("X-API-Key = %q, want %q", gotAPIKey, "m0sk_secret")
	}
	if gotBody["user_id"] != "proj" {
		t.Errorf("user_id = %v, want %q", gotBody["user_id"], "proj")
	}
	messages, ok := gotBody["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %v, want a one-element array", gotBody["messages"])
	}
	msg := messages[0].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "hello world" {
		t.Errorf("message = %v, want {role: user, content: hello world}", msg)
	}
}

func TestMem0RecallTolerantFieldNames(t *testing.T) {
	cases := []struct {
		name  string
		field string
	}{
		{"memory field", "memory"},
		{"content field", "content"},
		{"text field", "text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"results": []map[string]any{
						{"id": "m1", c.field: "likes tabs", "score": 0.8},
					},
				})
			}))
			defer srv.Close()

			b := newMem0(Config{BaseURL: srv.URL})
			results, err := b.Recall(context.Background(), "indentation")
			if err != nil {
				t.Fatalf("Recall: unexpected error: %v", err)
			}
			if len(results) != 1 || results[0].Content != "likes tabs" || results[0].Score != 0.8 {
				t.Errorf("results = %+v, want one result {m1, likes tabs, 0.8}", results)
			}
		})
	}
}

func TestMem0CloudStore(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := newMem0(Config{BaseURL: srv.URL, Mode: "cloud", Namespace: "proj", APIKey: "m0sk_secret"})
	if err := b.Store(context.Background(), "hello world"); err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}

	if gotPath != "/v3/memories/add/" {
		t.Errorf("path = %q, want the Platform API's versioned add path", gotPath)
	}
	if gotAuth != "Token m0sk_secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Token m0sk_secret")
	}
	if gotBody["user_id"] != "proj" {
		t.Errorf("user_id = %v, want %q", gotBody["user_id"], "proj")
	}
}

func TestMem0CloudRecall(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": "14e1b28a", "memory": "Allergic to nuts", "score": 0.30},
			},
		})
	}))
	defer srv.Close()

	b := newMem0(Config{BaseURL: srv.URL, Mode: "cloud", Namespace: "proj"})
	results, err := b.Recall(context.Background(), "dietary restrictions")
	if err != nil {
		t.Fatalf("Recall: unexpected error: %v", err)
	}

	if gotPath != "/v3/memories/search/" {
		t.Errorf("path = %q, want the Platform API's versioned search path", gotPath)
	}
	if _, hasTopLevelUserID := gotBody["user_id"]; hasTopLevelUserID {
		t.Error("cloud search body should scope by filters.user_id, not a top-level user_id")
	}
	filters, ok := gotBody["filters"].(map[string]any)
	if !ok || filters["user_id"] != "proj" {
		t.Errorf("filters = %v, want {user_id: proj}", gotBody["filters"])
	}
	if len(results) != 1 || results[0].Content != "Allergic to nuts" || results[0].Score != 0.30 {
		t.Errorf("results = %+v, want one result {14e1b28a, Allergic to nuts, 0.30}", results)
	}
}

func TestMem0NoAPIKeyOmitsHeader(t *testing.T) {
	sawKey := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.Header.Get("X-API-Key") != ""
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	b := newMem0(Config{BaseURL: srv.URL})
	if err := b.Store(context.Background(), "hi"); err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}
	if sawKey {
		t.Error("X-API-Key header sent with no APIKey configured; want none (assumes AUTH_DISABLED for local dev)")
	}
}
