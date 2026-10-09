package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

type remoteModelsResp struct {
	OK      bool     `json:"ok"`
	Models  []string `json:"models"`
	Message string   `json:"message"`
}

func getRemoteModels(t *testing.T, srv *Server, id string) (int, remoteModelsResp) {
	t.Helper()
	w := doJSON(t, srv, http.MethodGet, "/api/config/endpoints/"+id+"/remote-models", "")
	var out remoteModelsResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
	}
	return w.Code, out
}

func TestListRemoteModels_UsesStoredConnection(t *testing.T) {
	setTestHome(t)
	t.Setenv("CUSTOM_API_KEY", "")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-stored" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"b-model"},{"id":"a-model"}]}`))
	}))
	defer upstream.Close()

	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "relay", Provider: "custom", BaseURL: upstream.URL + "/v1", Protocol: "openai", APIKey: "sk-stored", Models: []config.EndpointModel{{Model: "a-model"}}},
			{ID: "wrong-key", Provider: "custom", BaseURL: upstream.URL, Protocol: "openai", APIKey: "sk-other", Models: []config.EndpointModel{{Model: "x"}}},
			{ID: "anth", Provider: "custom", BaseURL: upstream.URL, Protocol: "anthropic", APIKey: "sk-stored", Models: []config.EndpointModel{{Model: "y"}}},
		},
		Default: "relay::a-model",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	code, got := getRemoteModels(t, srv, "relay")
	if code != http.StatusOK || !got.OK {
		t.Fatalf("relay: code=%d resp=%+v", code, got)
	}
	if want := []string{"a-model", "b-model"}; !reflect.DeepEqual(got.Models, want) {
		t.Fatalf("relay models = %v, want %v", got.Models, want)
	}

	code, got = getRemoteModels(t, srv, "wrong-key")
	if code != http.StatusOK || got.OK || !strings.Contains(got.Message, "401") {
		t.Fatalf("wrong-key: code=%d resp=%+v, want ok=false with 401", code, got)
	}

	code, got = getRemoteModels(t, srv, "anth")
	if code != http.StatusOK || got.OK || got.Message == "" {
		t.Fatalf("anthropic protocol: code=%d resp=%+v, want ok=false with message", code, got)
	}

	if code, _ = getRemoteModels(t, srv, "missing"); code != http.StatusNotFound {
		t.Fatalf("missing endpoint: code=%d, want 404", code)
	}

	// The vendor env var wins over the stored key, as it does for chat.
	t.Setenv("CUSTOM_API_KEY", "sk-stored")
	code, got = getRemoteModels(t, srv, "wrong-key")
	if code != http.StatusOK || !got.OK {
		t.Fatalf("wrong-key with env key: code=%d resp=%+v, want ok", code, got)
	}
}
