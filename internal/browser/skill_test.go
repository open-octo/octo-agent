package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCompileAndRoundTrip(t *testing.T) {
	events := []RecordedEvent{
		{Type: "click", Selector: "#b", Tag: "BUTTON", Text: "Go"},
		{Type: "change", Selector: "#s", Tag: "SELECT", Value: "b"},
		{Type: "change", Selector: "#q", Tag: "INPUT", Value: "hello"},
	}
	s := CompileSkill("demo", "a demo", "https://example.com/start", events)
	if len(s.Steps) != 4 {
		t.Fatalf("want 4 steps (navigate + 3), got %d", len(s.Steps))
	}
	if s.Steps[0].Action != "navigate" || s.Steps[1].Action != "click" ||
		s.Steps[2].Action != "select" || s.Steps[3].Action != "type" {
		t.Fatalf("unexpected actions: %+v", s.Steps)
	}
	data, err := MarshalSkill(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "action: select") {
		t.Fatalf("yaml missing select step:\n%s", data)
	}
	back, err := ParseSkill(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Steps) != 4 || back.Steps[2].Value != "b" {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
}

// TestCompileUploadMerge: a click on the upload button followed by a file
// selection compiles to a single upload step on the button, auto-parameterized.
func TestCompileUploadMerge(t *testing.T) {
	events := []RecordedEvent{
		{Type: "click", Selector: ".upload-btn", Tag: "BUTTON", Text: "Upload affected booking list"},
		{Type: "upload", Selector: "input[type=file]", Tag: "INPUT", Value: "C:\\fakepath\\x.xlsx"},
	}
	s := CompileSkill("u", "", "https://x/start", events)
	// navigate + upload (the click was merged in)
	if len(s.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d: %+v", len(s.Steps), s.Steps)
	}
	up := s.Steps[1]
	if up.Action != "upload" || up.Selector != ".upload-btn" || up.Value != "{{file}}" {
		t.Fatalf("bad upload step: %+v", up)
	}
	if len(s.Params) != 1 || s.Params[0].Name != "file" {
		t.Fatalf("want a file param, got %+v", s.Params)
	}
}

// TestUploadViaChooser drives the chooser-based upload: clicking a button opens
// a (intercepted) file chooser and the file is set on the underlying input.
func TestUploadViaChooser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>up</title>
<input type="file" id="f" style="display:none">
<button id="btn" onclick="document.getElementById('f').click()">Upload</button>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#btn", 5*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	dir := t.TempDir()
	xlsx := dir + "/report.xlsx"
	if err := os.WriteFile(xlsx, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := page.UploadViaChooser(ctx, "#btn", []string{xlsx}); err != nil {
		t.Fatalf("upload via chooser: %v", err)
	}
	var name string
	if err := page.Eval(ctx, "document.querySelector('#f').files[0].name", &name); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if name != "report.xlsx" {
		t.Fatalf("file not set via chooser: %q", name)
	}
}

// TestGenerateSkillDistill: the LLM distiller drops a fumble (a wrong click +
// going back) and parameterizes a value — and the precision guard rejects any
// output that invents a selector, falling back to the deterministic baseline.
func TestGenerateSkillDistill(t *testing.T) {
	ctx := context.Background()
	events := []RecordedEvent{
		{Type: "click", Selector: "#wrong-tab", Tag: "A", Text: "Oops"}, // a detour
		{Type: "click", Selector: "#search", Tag: "BUTTON", Text: "Search"},
		{Type: "change", Selector: "#q", Tag: "INPUT", Value: "ORDER-123"},
	}

	// A generator that returns a cleaned skill (drops the detour, params the value).
	clean := func(_ context.Context, _, _ string) (string, error) {
		return "name: x\nsteps:\n" +
			"  - {action: navigate, url: 'https://x/start'}\n" +
			"  - {action: click, selector: '#search'}\n" +
			"  - {action: type, selector: '#q', value: '{{order}}'}\n", nil
	}
	s := GenerateSkill(ctx, "demo", "https://x/start", events, clean)
	if len(s.Steps) != 3 { // navigate + search + type (detour dropped)
		t.Fatalf("distill should drop the detour; got %d steps: %+v", len(s.Steps), s.Steps)
	}
	if s.Steps[2].Value != "{{order}}" {
		t.Fatalf("value not parameterized: %+v", s.Steps[2])
	}

	// A generator that invents a selector not in the recording -> must be rejected.
	cheat := func(_ context.Context, _, _ string) (string, error) {
		return "name: x\nsteps:\n  - {action: click, selector: '#invented'}\n", nil
	}
	s2 := GenerateSkill(ctx, "demo", "https://x/start", events, cheat)
	for _, st := range s2.Steps {
		if st.Selector == "#invented" {
			t.Fatal("precision guard failed: accepted an invented selector")
		}
	}
}

// TestReplaySkillSelfHeal: a step has a stale selector, the implicit wait fails,
// the healer repairs the selector, replay retries and succeeds — and reports the
// skill as modified so the caller can write the fix back.
func TestReplaySkillSelfHeal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>heal</title>
<button id="real">Go</button>
<script>window.clicks=0;document.getElementById('real').addEventListener('click',function(){window.clicks++});</script>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}

	skill := &Skill{
		Name: "heal-demo",
		Steps: []Step{
			{Action: "navigate", URL: srv.URL, Verify: &Verify{Exists: "#real"}},
			{Action: "click", Selector: "#stale"}, // wrong selector — will fail
		},
	}

	healed := false
	heal := func(_ context.Context, _ *Page, step *Step, _ error) error {
		// Stand in for the LLM healer: correct the drifted selector.
		if step.Selector == "#stale" {
			step.Selector = "#real"
			healed = true
			return nil
		}
		return context.DeadlineExceeded
	}

	modified, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 2 * time.Second, Healer: heal})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !healed {
		t.Fatal("healer was not invoked")
	}
	if !modified {
		t.Fatal("expected modified=true after heal (for write-back)")
	}
	if skill.Steps[1].Selector != "#real" {
		t.Fatalf("step not corrected in place: %q", skill.Steps[1].Selector)
	}
	var clicks int
	if err := page.Eval(ctx, "window.clicks", &clicks); err != nil {
		t.Fatal(err)
	}
	if clicks < 1 {
		t.Fatalf("healed click did not register (clicks=%d)", clicks)
	}
}
