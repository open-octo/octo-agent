package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
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
