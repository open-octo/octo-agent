package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
)

// pngBytes is a minimal valid PNG header so NewImageBlock's sniff passes.
var pngBytes = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}

// visionCfg builds a config with one vision endpoint and one text-only model.
func visionCfg(baseURL string) config.Config {
	return config.Config{
		Endpoints: []config.Endpoint{{
			ID: "ep", Provider: "custom", BaseURL: baseURL, APIKey: "k", Protocol: "openai",
			Models: []config.EndpointModel{
				{Model: "seer", Vision: true},
				{Model: "blind", Vision: false},
			},
		}},
		VisionHelper: "ep::seer",
	}
}

// chatCompletion writes an OpenAI-shaped reply carrying content.
func chatCompletion(w http.ResponseWriter, content string) {
	body, _ := json.Marshal(map[string]any{
		"id": "c", "object": "chat.completion", "model": "seer",
		"choices": []any{map[string]any{
			"index": 0, "message": map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
	})
	_, _ = w.Write(body)
}

func TestNewVisionDescriber_NilWhenUnusable(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
	}{
		{"no vision_helper", config.Config{Endpoints: visionCfg("http://x").Endpoints}},
		{"dangling reference", func() config.Config { c := visionCfg("http://x"); c.VisionHelper = "ghost::model"; return c }()},
		{"helper is text-only", func() config.Config { c := visionCfg("http://x"); c.VisionHelper = "ep::blind"; return c }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CUSTOM_API_KEY", "")
			if d := NewVisionDescriber(agent.New(nil, "blind"), tt.cfg); d != nil {
				t.Errorf("NewVisionDescriber() = %T, want nil (feature must stay off)", d)
			}
		})
	}
}

// A helper that resolves but can't be called (no API key) must NOT be nil:
// nil here while the tool gates check only ResolveVisionHelper would let raw
// image blocks through to a text-only endpoint (HTTP 400 every turn). Instead
// Describe fails with the reason, which becomes the per-image fallback text.
func TestNewVisionDescriber_NoKeyDescribesWithClearError(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := visionCfg("http://x")
	// A named vendor: the Custom vendor may run keyless (see the test below).
	cfg.Endpoints[0].Provider = "openai"
	cfg.Endpoints[0].APIKey = ""

	d := NewVisionDescriber(agent.New(nil, "blind"), cfg)
	if d == nil {
		t.Fatal("NewVisionDescriber() = nil; the gates rely on resolve-only semantics")
	}
	if !d.Active() {
		t.Fatal("a text-only primary still needs the describer active")
	}
	_, err := d.Describe(context.Background(), agent.ImageData{MIMEType: "image/png", Data: pngBytes})
	if err == nil {
		t.Fatal("Describe must fail when no key is available")
	}
	if !strings.Contains(err.Error(), "no API key") {
		t.Errorf("error should name the fix, got: %v", err)
	}
}

// A keyless Custom endpoint (a local Ollama vision model) is a valid helper:
// the describer builds, calls the server, and sends no Authorization header.
func TestNewVisionDescriber_KeylessCustomBuilds(t *testing.T) {
	t.Setenv("CUSTOM_API_KEY", "")
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawAuth = r.Header["Authorization"]
		chatCompletion(w, "a cat")
	}))
	defer srv.Close()
	cfg := visionCfg(srv.URL)
	cfg.Endpoints[0].APIKey = ""

	d := NewVisionDescriber(agent.New(nil, "blind"), cfg)
	if d == nil {
		t.Fatal("NewVisionDescriber() = nil, want a describer")
	}
	got, err := d.Describe(context.Background(), agent.ImageData{MIMEType: "image/png", Data: pngBytes})
	if err != nil {
		t.Fatalf("Describe on a keyless custom helper: %v", err)
	}
	if got != "a cat" {
		t.Errorf("Describe = %q, want %q", got, "a cat")
	}
	if sawAuth {
		t.Error("Authorization header sent for a keyless custom vision helper")
	}
}

func TestVisionDescriber_ActiveTracksCurrentModel(t *testing.T) {
	a := agent.New(nil, "blind")
	d := NewVisionDescriber(a, visionCfg("http://x"))
	if d == nil {
		t.Fatal("NewVisionDescriber() = nil, want a describer")
	}
	if !d.Active() {
		t.Error("a text-only model needs descriptions")
	}

	// /model switch to the vision model: descriptions must stop immediately,
	// without anything re-wiring the describer.
	a.SetModel("seer")
	if d.Active() {
		t.Error("a vision model must not get descriptions")
	}
	a.SetModel("blind")
	if !d.Active() {
		t.Error("switching back must resume descriptions")
	}
}

func TestVisionDescriber_DescribeRendersJSON(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		chatCompletion(w, `{"type":"screenshot","text_content":"Sign in\nEmail","elements":[{"label":"Sign in","position":"center","kind":"button"}],"summary":"A login dialog"}`)
	}))
	defer srv.Close()

	d := NewVisionDescriber(agent.New(nil, "blind"), visionCfg(srv.URL))
	desc, err := d.Describe(context.Background(), agent.ImageData{MIMEType: "image/png", Data: pngBytes})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}

	for _, want := range []string{"A login dialog", "Sign in\nEmail", "screenshot", "button", "center"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q:\n%s", want, desc)
		}
	}
	if !strings.Contains(string(gotBody), "image_url") {
		t.Error("request should carry the image (OpenAI protocol uses image_url)")
	}
	if !strings.Contains(string(gotBody), `"model":"seer"`) {
		t.Error("request should target the helper model, not the primary one")
	}
}

// TestVisionDescriber_ThreadsCustomHeaders pins design Test Case #8: a
// vision_helper endpoint's custom Headers must reach the actual HTTP request
// NewVisionDescriber's sender makes, the same as any other sender built via
// SenderOptions.
func TestVisionDescriber_ThreadsCustomHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Tenant-Id")
		chatCompletion(w, `{"summary":"ok"}`)
	}))
	defer srv.Close()

	cfg := config.Config{
		Endpoints: []config.Endpoint{{
			ID: "ep", Provider: "custom", BaseURL: srv.URL, APIKey: "k", Protocol: "openai",
			Headers: map[string]string{"X-Tenant-Id": "abc"},
			Models: []config.EndpointModel{
				{Model: "seer", Vision: true},
				{Model: "blind", Vision: false},
			},
		}},
		VisionHelper: "ep::seer",
	}

	d := NewVisionDescriber(agent.New(nil, "blind"), cfg)
	if _, err := d.Describe(context.Background(), agent.ImageData{MIMEType: "image/png", Data: pngBytes}); err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if gotHeader != "abc" {
		t.Errorf("X-Tenant-Id header = %q, want abc (endpoint's custom header must reach the vision-helper request)", gotHeader)
	}
}

// A helper that answers in prose instead of JSON still described the image;
// throwing that away would spend the retry budget on a working endpoint.
func TestVisionDescriber_NonJSONReplyIsUsedAsIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatCompletion(w, "A red bicycle leaning on a fence.")
	}))
	defer srv.Close()

	d := NewVisionDescriber(agent.New(nil, "blind"), visionCfg(srv.URL))
	desc, err := d.Describe(context.Background(), agent.ImageData{MIMEType: "image/png", Data: pngBytes})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "A red bicycle leaning on a fence." {
		t.Errorf("description = %q, want the raw reply", desc)
	}
}

func TestVisionDescriber_FencedJSONIsParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatCompletion(w, "```json\n{\"summary\":\"A bar chart\",\"text_content\":\"Q1 Q2\"}\n```")
	}))
	defer srv.Close()

	d := NewVisionDescriber(agent.New(nil, "blind"), visionCfg(srv.URL))
	desc, err := d.Describe(context.Background(), agent.ImageData{MIMEType: "image/png", Data: pngBytes})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !strings.Contains(desc, "A bar chart") || !strings.Contains(desc, "Q1 Q2") {
		t.Errorf("fenced JSON was not parsed: %q", desc)
	}
	if strings.Contains(desc, "```") {
		t.Errorf("fence leaked into the description: %q", desc)
	}
}

func TestVisionDescriber_EndpointErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key"}}`))
	}))
	defer srv.Close()

	d := NewVisionDescriber(agent.New(nil, "blind"), visionCfg(srv.URL))
	if _, err := d.Describe(context.Background(), agent.ImageData{MIMEType: "image/png", Data: pngBytes}); err == nil {
		t.Fatal("want an error from a 401 endpoint")
	}
}

func TestVisionDescriber_EmptyReplyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatCompletion(w, "   ")
	}))
	defer srv.Close()

	d := NewVisionDescriber(agent.New(nil, "blind"), visionCfg(srv.URL))
	if _, err := d.Describe(context.Background(), agent.ImageData{MIMEType: "image/png", Data: pngBytes}); err == nil {
		t.Error("an empty description must not be cached as a success")
	}
}

func TestVisionDescriber_RejectsUnsupportedImageFormat(t *testing.T) {
	d := NewVisionDescriber(agent.New(nil, "blind"), visionCfg("http://x"))
	_, err := d.Describe(context.Background(), agent.ImageData{MIMEType: "image/tiff", Data: []byte("II*\x00not-an-image")})
	if err == nil {
		t.Error("want an error for a format no provider accepts")
	}
}

func TestVisionDescribePromptFollowsLanguage(t *testing.T) {
	if !strings.Contains(visionDescribePrompt("zh"), "Chinese") {
		t.Error("zh config should ask for Chinese prose")
	}
	if !strings.Contains(visionDescribePrompt(""), "English") {
		t.Error("empty language should default to English")
	}
	if !strings.Contains(visionDescribePrompt("en"), "verbatim") {
		t.Error("the prompt must insist on verbatim transcription")
	}
}

func TestRenderVisionDescription_EmptyObjectFallsBackToRaw(t *testing.T) {
	raw := `{"type":"","text_content":"","elements":[],"summary":""}`
	if got := renderVisionDescription(raw); got != raw {
		t.Errorf("renderVisionDescription() = %q, want the raw reply back", got)
	}
}
