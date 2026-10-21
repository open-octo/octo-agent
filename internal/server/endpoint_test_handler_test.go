package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

type testEndpointResp struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func postTestEndpoint(t *testing.T, srv *Server, id, body string) (int, testEndpointResp) {
	t.Helper()
	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/"+id+"/test", body)
	var out testEndpointResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
	}
	return w.Code, out
}

// Two endpoints on one provider with different keys: each is tested with its
// own key and headers, which /api/config/test (provider + base URL lookup,
// no headers) cannot do.
func TestTestEndpoint_UsesItsOwnConnection(t *testing.T) {
	setTestHome(t)
	t.Setenv("CUSTOM_API_KEY", "")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-work" || r.Header.Get("X-Tenant") != "acme" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "personal", Provider: "custom", BaseURL: upstream.URL + "/v1", Protocol: "openai", APIKey: "sk-personal", Models: []config.EndpointModel{{Model: "m"}}},
			{ID: "work", Provider: "custom", BaseURL: upstream.URL + "/v1", Protocol: "openai", APIKey: "sk-work", Headers: map[string]string{"X-Tenant": "acme"}, Models: []config.EndpointModel{{Model: "m"}}},
		},
		Default: "personal::m",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	if code, got := postTestEndpoint(t, srv, "work", `{"model":"m"}`); code != http.StatusOK || !got.OK {
		t.Fatalf("work: code=%d resp=%+v, want ok", code, got)
	}
	if code, got := postTestEndpoint(t, srv, "personal", `{"model":"m"}`); code != http.StatusOK || got.OK || !strings.Contains(got.Message, "401") {
		t.Fatalf("personal: code=%d resp=%+v, want ok=false with 401", code, got)
	}
}

func TestTestEndpoint_EnvKeyWins(t *testing.T) {
	setTestHome(t)
	t.Setenv("CUSTOM_API_KEY", "sk-env")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-env" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "relay", Provider: "custom", BaseURL: upstream.URL + "/v1", Protocol: "openai", APIKey: "sk-stored", Models: []config.EndpointModel{{Model: "m"}}},
		},
		Default: "relay::m",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	if code, got := postTestEndpoint(t, srv, "relay", `{"model":"m"}`); code != http.StatusOK || !got.OK {
		t.Fatalf("code=%d resp=%+v, want ok with the env key", code, got)
	}
}

func TestTestEndpoint_BadRequests(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "relay", Provider: "custom", BaseURL: "http://127.0.0.1:1/v1", Protocol: "openai", APIKey: "k", Models: []config.EndpointModel{{Model: "m"}}},
		},
		Default: "relay::m",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	if code, _ := postTestEndpoint(t, srv, "missing", `{"model":"m"}`); code != http.StatusNotFound {
		t.Fatalf("missing endpoint: code=%d, want 404", code)
	}
	if code, _ := postTestEndpoint(t, srv, "relay", `{}`); code != http.StatusBadRequest {
		t.Fatalf("no model: code=%d, want 400", code)
	}
}
