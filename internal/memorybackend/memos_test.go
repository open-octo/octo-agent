package memorybackend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMemOSStoreNoAPIKeyUsesXUserName(t *testing.T) {
	var gotPath, gotUserNameHeader, gotAuthHeader string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUserNameHeader = r.Header.Get("X-User-Name")
		gotAuthHeader = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200})
	}))
	defer srv.Close()

	b := newMemOS(Config{BaseURL: srv.URL, Namespace: "proj"})
	if err := b.Store(context.Background(), "hello world"); err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}

	if gotPath != "/product/add" {
		t.Errorf("path = %q, want /product/add", gotPath)
	}
	if gotUserNameHeader != "proj" {
		t.Errorf("X-User-Name = %q, want %q", gotUserNameHeader, "proj")
	}
	if gotAuthHeader != "" {
		t.Errorf("Authorization = %q, want none when no APIKey is configured", gotAuthHeader)
	}
	if gotBody["user_id"] != "proj" {
		t.Errorf("user_id = %v, want %q", gotBody["user_id"], "proj")
	}
}

func TestMemOSStoreWithAPIKeyUsesAuthorizationHeader(t *testing.T) {
	var gotAuthHeader, gotUserNameHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		gotUserNameHeader = r.Header.Get("X-User-Name")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200})
	}))
	defer srv.Close()

	b := newMemOS(Config{BaseURL: srv.URL, APIKey: "krlk_secret"})
	if err := b.Store(context.Background(), "hi"); err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}
	if gotAuthHeader != "krlk_secret" {
		t.Errorf("Authorization = %q, want %q", gotAuthHeader, "krlk_secret")
	}
	if gotUserNameHeader != "" {
		t.Errorf("X-User-Name = %q, want none when an APIKey is set", gotUserNameHeader)
	}
}

func TestMemOSRecall(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"text_mem": []map[string]any{
					{
						"cube_id": "proj",
						"memories": []map[string]any{
							{"id": "m1", "memory": "likes tabs", "metadata": map[string]any{"relativity": 0.7}},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	b := newMemOS(Config{BaseURL: srv.URL})
	results, err := b.Recall(context.Background(), "indentation")
	if err != nil {
		t.Fatalf("Recall: unexpected error: %v", err)
	}
	if gotPath != "/product/search" {
		t.Errorf("path = %q, want /product/search", gotPath)
	}
	if len(results) != 1 || results[0].ID != "m1" || results[0].Content != "likes tabs" || results[0].Score != 0.7 {
		t.Errorf("results = %+v, want one result {m1, likes tabs, 0.7}", results)
	}
}
