package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s, want GET /v1/models", r.Method, r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer k"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("X-Tenant"), "t1"; got != want {
			t.Errorf("X-Tenant = %q, want %q", got, want)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"qwen3","object":"model"},
			{"id":"llama3.2"},
			{"id":"qwen3"},
			{"id":" "}
		]}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL
	c.Headers = map[string]string{"X-Tenant": "t1"}
	got, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if want := []string{"llama3.2", "qwen3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListModels = %v, want %v", got, want)
	}
}

func TestListModels_BaseWithV1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("keyless client sent Authorization %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c, _ := New("")
	c.BaseURL = srv.URL + "/v1/"
	got, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListModels = %v, want empty", got)
	}
}

func TestListModels_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"auth"}}`))
	}))
	defer srv.Close()

	c, _ := New("bad")
	c.BaseURL = srv.URL
	_, err := c.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("err = %v, want HTTP 401 with server message", err)
	}
}

func TestListModels_NotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>not an api</html>`))
	}))
	defer srv.Close()

	c, _ := New("")
	c.BaseURL = srv.URL
	if _, err := c.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, want decode error", err)
	}
}
