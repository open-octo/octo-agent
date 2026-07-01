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

// TestCompileAutoParamsTypeValues: every typed value becomes a declared {{param}}
// whose Default is the recorded value, so a no-param replay reproduces the demo
// exactly while each input is now an overridable knob — no LLM distiller needed.
func TestCompileAutoParamsTypeValues(t *testing.T) {
	events := []RecordedEvent{
		{Type: "change", Selector: "#q", Tag: "INPUT", Field: "Search query", Value: "hello world"},
		{Type: "change", Selector: `input[name="email"]`, Tag: "INPUT", Value: "a@b.com"},
	}
	s := CompileSkill("demo", "", "", events)
	if len(s.Steps) != 2 {
		t.Fatalf("want 2 type steps, got %d: %+v", len(s.Steps), s.Steps)
	}
	// Values are placeholders, not the literal recorded text.
	for _, st := range s.Steps {
		if !strings.HasPrefix(st.Value, "{{") || strings.Contains(st.Value, "hello") {
			t.Fatalf("value not parameterized: %+v", st)
		}
	}
	byName := map[string]Param{}
	for _, p := range s.Params {
		byName[p.Name] = p
	}
	// Field hint drives the name; selector name= is the fallback.
	if p, ok := byName["search_query"]; !ok || p.Default != "hello world" {
		t.Fatalf("search_query param wrong: %+v (params=%+v)", p, s.Params)
	}
	if p, ok := byName["email"]; !ok || p.Default != "a@b.com" {
		t.Fatalf("email param wrong: %+v (params=%+v)", p, s.Params)
	}
	// Round-trip: with no params, replay substitutes the defaults back in.
	full := mergedParams(&s, nil)
	if full["search_query"] != "hello world" || full["email"] != "a@b.com" {
		t.Fatalf("defaults not recoverable: %+v", full)
	}
}

// TestCompileAutoParamsSecretNoDefault: a password field is parameterized without
// a Default, so the plaintext never lands in the YAML.
func TestCompileAutoParamsSecretNoDefault(t *testing.T) {
	events := []RecordedEvent{
		{Type: "change", Selector: "#pw", Tag: "INPUT", Field: "password", Secret: true, Value: "hunter2"},
	}
	s := CompileSkill("demo", "", "", events)
	if len(s.Params) != 1 {
		t.Fatalf("want 1 param, got %+v", s.Params)
	}
	if s.Params[0].Default != "" {
		t.Fatalf("secret default must be empty, got %q", s.Params[0].Default)
	}
	if data, _ := MarshalSkill(s); strings.Contains(string(data), "hunter2") {
		t.Fatalf("secret leaked into yaml:\n%s", data)
	}
}

// TestCompileAutoParamsDedup: two inputs that reduce to the same name get distinct
// params (foo, foo2) so neither value is lost.
func TestCompileAutoParamsDedup(t *testing.T) {
	events := []RecordedEvent{
		{Type: "change", Selector: "#a", Tag: "INPUT", Field: "City", Value: "SFO"},
		{Type: "change", Selector: "#b", Tag: "INPUT", Field: "City", Value: "LAX"},
	}
	s := CompileSkill("demo", "", "", events)
	if len(s.Params) != 2 || s.Params[0].Name == s.Params[1].Name {
		t.Fatalf("want two distinct params, got %+v", s.Params)
	}
	if s.Steps[0].Value == s.Steps[1].Value {
		t.Fatalf("distinct inputs must map to distinct params: %+v", s.Steps)
	}
}

// TestCompileAutoVerifyNavigateHost: navigate steps get an auto URL verify pinned
// to the destination host, so a redirect elsewhere fails instead of proceeding.
func TestCompileAutoVerifyNavigateHost(t *testing.T) {
	events := []RecordedEvent{
		{Type: "navigate", URL: "https://shop.example.com/cart?sid=abc"},
		{Type: "click", Selector: "#pay", Tag: "BUTTON", Text: "Pay"},
	}
	s := CompileSkill("demo", "", "https://shop.example.com/start", events)
	nav := s.Steps[0]
	if nav.Action != "navigate" || nav.Verify == nil || nav.Verify.URL != "shop.example.com" {
		t.Fatalf("leading navigate missing host verify: %+v", nav)
	}
	// The click step carries no auto URL verify.
	for _, st := range s.Steps {
		if st.Action == "click" && st.Verify != nil && st.Verify.URL != "" {
			t.Fatalf("click should not get a url verify: %+v", st)
		}
	}
}

// TestReplayVerifyURLCatchesCrossHostRedirect: a navigate whose server 302s to a
// different host (a stand-in for an SSO/login bounce) fails the auto host verify,
// instead of replay silently continuing on the wrong page.
func TestReplayVerifyURLCatchesCrossHostRedirect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The "login" host the redirect lands on.
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><title>login</title><h1>Sign in</h1>`))
	}))
	defer login.Close()
	// The app host: /start bounces to the login host.
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, login.URL+"/login", http.StatusFound)
	}))
	defer app.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}

	appHost := strings.TrimPrefix(app.URL, "http://")
	skill := &Skill{Name: "x", Steps: []Step{
		{Action: "navigate", URL: app.URL + "/start", Verify: &Verify{URL: appHost}},
	}}
	_, _, err = ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected replay to fail: navigation bounced to a different host")
	}
	if !strings.Contains(err.Error(), "verify url") {
		t.Fatalf("expected a url-verify failure, got: %v", err)
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
	if err := page.WaitFor(ctx, "#btn", testWaitTimeout); err != nil {
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

// TestCompileSkillDropsConsecutiveDupes: a jittery double-fire of the same click
// is collapsed to one step; distinct steps (and a legitimate repeat of the same
// selector that is separated by another action) are preserved.
func TestCompileSkillDropsConsecutiveDupes(t *testing.T) {
	events := []RecordedEvent{
		{Type: "click", Selector: "#go", Tag: "BUTTON", Text: "Go"},
		{Type: "click", Selector: "#go", Tag: "BUTTON", Text: "Go"}, // immediate dup → dropped
		{Type: "change", Selector: "#q", Tag: "INPUT", Value: "x"},
		{Type: "click", Selector: "#go", Tag: "BUTTON", Text: "Go"}, // same selector but not consecutive → kept
	}
	s := CompileSkill("demo", "", "", events)
	clicks := 0
	for _, st := range s.Steps {
		if st.Action == "click" && st.Selector == "#go" {
			clicks++
		}
	}
	if clicks != 2 {
		t.Fatalf("expected 2 #go clicks (one dup dropped), got %d: %+v", clicks, s.Steps)
	}
}

// TestCompileSkillCapturesNavigations: navigations recorded during the demo
// become navigate steps in order; an about:blank start URL is dropped (it's the
// throwaway tab octo opened, not where the user worked).
func TestCompileSkillCapturesNavigations(t *testing.T) {
	events := []RecordedEvent{
		{Type: "navigate", URL: "https://www.zhihu.com/hot"},
		{Type: "navigate", URL: "https://www.zhihu.com/hot"}, // initial-load echo → collapsed
		{Type: "click", Selector: "section a", Tag: "A", Text: "Top story"},
	}
	s := CompileSkill("demo", "", "about:blank", events)
	if len(s.Steps) != 2 {
		t.Fatalf("want navigate+click (blank dropped, echo collapsed), got %d: %+v", len(s.Steps), s.Steps)
	}
	if s.Steps[0].Action != "navigate" || s.Steps[0].URL != "https://www.zhihu.com/hot" {
		t.Fatalf("step 0 = %+v, want navigate to /hot", s.Steps[0])
	}
	if s.Steps[1].Action != "click" {
		t.Fatalf("step 1 = %+v, want click", s.Steps[1])
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

// TestRunStepWaitFixedDelay: a wait step with no selector sleeps the requested
// time (the SPA-settling primitive) — and run_skill no longer rejects it.
func TestRunStepWaitFixedDelay(t *testing.T) {
	start := time.Now()
	if _, err := runStep(context.Background(), nil, nil, &Step{Action: "wait", TimeoutMS: 80}, nil, time.Second); err != nil {
		t.Fatalf("wait step: %v", err)
	}
	if d := time.Since(start); d < 60*time.Millisecond {
		t.Fatalf("wait returned too fast: %v", d)
	}
}

// TestReplayClickFollowsNewTab: a click on a target=_blank link is followed to
// the tab it opens (the Zhihu-hot-item failure mode), and ReplaySkill returns
// that tab as the final page.
func TestReplayClickFollowsNewTab(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/dest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>dest</title><h1 id="dest">DEST</h1>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>home</title><a id="open" href="/dest" target="_blank">Open</a>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	skill := &Skill{Name: "x", Steps: []Step{{Action: "click", Selector: "#open"}}}
	_, fp, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second, Browser: b})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if fp == page {
		t.Fatal("replay did not follow the new tab opened by the click")
	}
	if err := fp.WaitFor(ctx, "#dest", testWaitTimeout); err != nil {
		t.Fatalf("final page is not the destination tab: %v", err)
	}
}

// TestReplayTextAnchorRecoversFromDrift: a recorded positional selector now
// points at the WRONG element after a layout change, but the recorded text steers
// replay to the right one — instead of silently clicking the wrong node.
func TestReplayTextAnchorRecoversFromDrift(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>t</title>
<div>
  <button onclick="window.hit='wrong'">Cancel</button>
  <button onclick="window.hit='right'">Book now</button>
</div>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// The positional selector resolves to the first button ("Cancel") — the wrong
	// one — but the recorded Label is the second button's text.
	skill := &Skill{Name: "x", Steps: []Step{
		{Action: "click", Selector: "div > button:nth-of-type(1)", Label: "Book now"},
	}}
	if _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var hit string
	if err := page.Eval(ctx, "window.hit||''", &hit); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if hit != "right" {
		t.Fatalf("text anchor did not override the drifted selector: window.hit=%q, want %q", hit, "right")
	}
}

// TestReplayFieldHintRecoversFromDrift: a recorded positional selector for a text
// input no longer resolves after a layout change, but the field's accessible-name
// hint (its name) re-locates it — so type lands in the right box instead of failing
// into the healer.
func TestReplayFieldHintRecoversFromDrift(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// The input sits at a different DOM path than the recorded positional
		// selector expects, but keeps its name="q".
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>t</title>
<header><nav><span>x</span></nav></header>
<main><section><div><input name="q" placeholder="Search"></div></section></main>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// A stale positional selector that matches nothing on the current page; the
	// hint "q" (the field name) is the recovery signal.
	skill := &Skill{Name: "x", Steps: []Step{
		{Action: "type", Selector: "body > div:nth-of-type(9) > input", Hint: "q", Value: "hello"},
	}}
	if _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var got string
	if err := page.Eval(ctx, `document.querySelector('input[name="q"]').value`, &got); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got != "hello" {
		t.Fatalf("field hint did not recover the drifted input: value=%q, want %q", got, "hello")
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

	modified, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 2 * time.Second, Healer: heal})
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
