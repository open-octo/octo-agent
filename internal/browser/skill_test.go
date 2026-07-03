package browser

import (
	"context"
	"fmt"
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
	_, _, _, err = ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected replay to fail: navigation bounced to a different host")
	}
	if !strings.Contains(err.Error(), "verify url") {
		t.Fatalf("expected a url-verify failure, got: %v", err)
	}
}

// TestCompileDedupesRepeatedType: a re-dispatched identical `change` event no
// longer survives parameterization as a spurious extra param+step (the guard that
// value-placeholders had defeated).
func TestCompileDedupesRepeatedType(t *testing.T) {
	events := []RecordedEvent{
		{Type: "change", Selector: "#q", Tag: "INPUT", Field: "query", Value: "abc"},
		{Type: "change", Selector: "#q", Tag: "INPUT", Field: "query", Value: "abc"}, // re-dispatched dup
	}
	s := CompileSkill("demo", "", "", events)
	types := 0
	for _, st := range s.Steps {
		if st.Action == "type" {
			types++
		}
	}
	if types != 1 {
		t.Fatalf("want 1 type step after dedup, got %d: %+v", types, s.Steps)
	}
	if len(s.Params) != 1 {
		t.Fatalf("want 1 param after dedup, got %+v", s.Params)
	}
}

// TestReplayVerifyURLIgnoresHostInQueryParam: a bounce to a different host whose
// URL merely echoes the expected host in a query param must still fail the host
// verify (exact host match, not a naive href substring).
func TestReplayVerifyURLIgnoresHostInQueryParam(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var app *httptest.Server
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><title>login</title><h1>Sign in</h1>`))
	}))
	defer login.Close()
	app = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bounce to the login host, echoing the app host in a query param.
		http.Redirect(w, r, login.URL+"/?return_to="+app.URL+"%2Fcart", http.StatusFound)
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
	_, _, _, err = ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected failure: bounced to the login host despite the app host appearing in a query param")
	}
	if !strings.Contains(err.Error(), "verify url") {
		t.Fatalf("expected a url-verify failure, got: %v", err)
	}
}

// TestReplaySkill_UnknownParamRejected: a caller-supplied param the skill
// doesn't declare (a typo, or params meant for a different, similarly named
// skill) is rejected before any step runs, rather than silently ignored by
// mergedParams — the run_skill near-miss-replay guardrail from #1050 was
// entirely prompt-text; this is a concrete, code-enforced check on top of it.
func TestReplaySkill_UnknownParamRejected(t *testing.T) {
	skill := &Skill{Name: "checkout", Params: []Param{{Name: "city"}}, Steps: []Step{
		{Action: "navigate", URL: "https://example.com/{{city}}"},
	}}
	_, _, _, err := ReplaySkill(context.Background(), nil, skill, map[string]string{"citee": "SFO"}, ReplayOptions{})
	if err == nil {
		t.Fatal("expected an error for an undeclared param")
	}
	if !strings.Contains(err.Error(), "unknown param") || !strings.Contains(err.Error(), `"citee"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestReplaySkill_MissingRequiredParamRejected: a {{placeholder}} referenced
// by a step with no default and no caller-supplied value must fail fast,
// instead of subst() silently sending the literal "{{name}}" text to the
// browser (a navigate to ".../item/{{item_id}}", a type into a field with
// that as its value).
func TestReplaySkill_MissingRequiredParamRejected(t *testing.T) {
	skill := &Skill{Name: "checkout", Params: []Param{{Name: "city"}}, Steps: []Step{
		{Action: "navigate", URL: "https://example.com/{{city}}"},
	}}
	_, _, _, err := ReplaySkill(context.Background(), nil, skill, nil, ReplayOptions{})
	if err == nil {
		t.Fatal("expected an error for an unresolved required param")
	}
	if !strings.Contains(err.Error(), "missing required param") || !strings.Contains(err.Error(), "city") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestReplaySkill_DefaultAndSuppliedParamsResolve: a param with a Default
// needs no caller value, and a caller-supplied value for a param with no
// default is accepted — the validation added above must not reject either.
func TestReplaySkill_DefaultAndSuppliedParamsResolve(t *testing.T) {
	skill := &Skill{Name: "checkout", Params: []Param{
		{Name: "city", Default: "sfo"},
		{Name: "item"},
	}, Steps: []Step{
		{Action: "extract", JS: "'{{city}}/{{item}}'", Bind: "out"},
	}}
	if err := unknownParams(skill, map[string]string{"item": "widget"}); err != nil {
		t.Fatalf("unknownParams: %v", err)
	}
	full := mergedParams(skill, map[string]string{"item": "widget"})
	if missing := unresolvedPlaceholders(skill, full); len(missing) != 0 {
		t.Fatalf("expected no unresolved placeholders, got %v", missing)
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

	// A generator that invents a selector not in the recording -> must be rejected,
	// but its prose description is still trustworthy and must survive the fallback
	// (an empty description leaves the skills manifest with a bare, matchable name).
	cheat := func(_ context.Context, _, _ string) (string, error) {
		return "name: x\ndescription: open the search page\nsteps:\n  - {action: click, selector: '#invented'}\n", nil
	}
	s2 := GenerateSkill(ctx, "demo", "https://x/start", events, cheat)
	for _, st := range s2.Steps {
		if st.Selector == "#invented" {
			t.Fatal("precision guard failed: accepted an invented selector")
		}
	}
	if s2.Description != "open the search page" {
		t.Fatalf("guard fallback should keep the distilled description, got %q", s2.Description)
	}

	// A generator whose output has a description but no usable steps -> baseline
	// steps, distilled description.
	descOnly := func(_ context.Context, _, _ string) (string, error) {
		return "name: x\ndescription: search for an order\n", nil
	}
	s3 := GenerateSkill(ctx, "demo", "https://x/start", events, descOnly)
	if len(s3.Steps) == 0 {
		t.Fatal("steps-empty fallback should keep the baseline steps")
	}
	if s3.Description != "search for an order" {
		t.Fatalf("steps-empty fallback should keep the distilled description, got %q", s3.Description)
	}
}

// TestStepDigest: the digest names each step by its label (falling back to hint,
// then selector), renders navigates as host+path, skips waits, and when long
// keeps the head plus the FINAL step — the ending is what a bare name hides.
func TestStepDigest(t *testing.T) {
	s := Skill{Steps: []Step{
		{Action: "navigate", URL: "https://www.zhihu.com/hot/"},
		{Action: "wait", TimeoutMS: 500},
		{Action: "click", Label: "热榜"},
		{Action: "type", Hint: "search"},
		{Action: "click", Selector: "#go"},
		{Action: "click", Label: "日元跌破 1 美元兑 162 日元关口，创下近 40 年来新低，日本央行已加息"},
	}}
	got := s.StepDigest()
	want := `navigate www.zhihu.com/hot → click "热榜" → type "search" → click "#go" → click "日元跌破 1 美元兑 162 日元关口，创下近 40 年来新…"`
	if got != want {
		t.Fatalf("digest = %q\nwant %q", got, want)
	}

	long := Skill{}
	for i := 0; i < 11; i++ {
		long.Steps = append(long.Steps, Step{Action: "click", Label: fmt.Sprintf("s%d", i)})
	}
	d := long.StepDigest()
	if !strings.Contains(d, "…") || !strings.Contains(d, `"s10"`) {
		t.Fatalf("long digest must elide the middle but keep the final step, got %q", d)
	}

	// Exactly 8 significant steps is the no-elision boundary.
	eight := Skill{}
	for i := 0; i < 8; i++ {
		eight.Steps = append(eight.Steps, Step{Action: "click", Label: fmt.Sprintf("s%d", i)})
	}
	if d := eight.StepDigest(); strings.Contains(d, "…") {
		t.Fatalf("8 steps must not be elided, got %q", d)
	}

	// A skill with only waits digests to "" (the manifest then shows a bare name).
	if d := (Skill{Steps: []Step{{Action: "wait", TimeoutMS: 100}}}).StepDigest(); d != "" {
		t.Fatalf("wait-only skill should digest to empty, got %q", d)
	}
}

// TestRunStepWaitFixedDelay: a wait step with no selector sleeps the requested
// time (the SPA-settling primitive) — and run_skill no longer rejects it.
func TestRunStepWaitFixedDelay(t *testing.T) {
	start := time.Now()
	if _, err := runStep(context.Background(), nil, nil, &Step{Action: "wait", TimeoutMS: 80}, nil, time.Second, "", nil); err != nil {
		t.Fatalf("wait step: %v", err)
	}
	if d := time.Since(start); d < 60*time.Millisecond {
		t.Fatalf("wait returned too fast: %v", d)
	}
}

// TestWaitForNetworkIdle: a wait{network:true} step blocks until an in-flight
// fetch the page kicked off completes, then returns — the SPA-settle primitive.
func TestWaitForNetworkIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(600 * time.Millisecond)
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// On load, fire a slow fetch that flips window.done when it resolves.
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>t</title>
<script>window.done=false;fetch('/slow').then(function(){window.done=true;});</script>`))
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

	skill := &Skill{Name: "x", Steps: []Step{{Action: "wait", Network: true, TimeoutMS: 10000}}}
	start := time.Now()
	if _, _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 15 * time.Second}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	// The in-flight fetch (~600ms) must have settled before the wait returned.
	var done bool
	if err := page.Eval(ctx, "window.done===true", &done); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !done {
		t.Fatal("network-idle wait returned before the in-flight fetch completed")
	}
	if d := time.Since(start); d < 500*time.Millisecond {
		t.Fatalf("network-idle wait returned suspiciously fast (%v) — did it settle?", d)
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
	_, fp, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second, Browser: b})
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
	if _, _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second}); err != nil {
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
	if _, _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second}); err != nil {
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

// TestReplayDismissesOverlayDeterministically: a step's target doesn't exist
// until a consent overlay is accepted; replay recovers by clicking the allow-list
// "Accept" control — no healer wired — then the retry succeeds.
func TestReplayDismissesOverlayDeterministically(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Accept injects the real target #go and removes itself.
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>t</title>
<button id="accept" onclick="var g=document.createElement('button');g.id='go';g.textContent='Go';g.onclick=function(){window.hit=1};document.body.appendChild(g);this.remove();">Accept</button>`))
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

	// #go is absent until the overlay is dismissed; no healer is provided, so this
	// exercises the deterministic structural recovery path.
	skill := &Skill{Name: "x", Steps: []Step{{Action: "click", Selector: "#go"}}}
	modified, _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 2 * time.Second, Browser: b})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if modified {
		t.Fatal("overlay dismissal is runtime-only; it must not mark the skill modified")
	}
	var hit int
	if err := page.Eval(ctx, "window.hit||0", &hit); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if hit != 1 {
		t.Fatalf("target not clicked after overlay dismissal (hit=%d)", hit)
	}
}

// TestReplayMultiRoundHeal: the healer needs two rounds — the first repair is
// still wrong, the second is correct — and replay keeps consulting it (up to the
// cap) instead of giving up after one attempt.
func TestReplayMultiRoundHeal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>t</title>
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
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	skill := &Skill{Name: "x", Steps: []Step{{Action: "click", Selector: "#stale"}}}
	rounds := 0
	heal := func(_ context.Context, _ *Page, step *Step, _ error) error {
		rounds++
		if rounds == 1 {
			step.Selector = "#still-wrong" // a repair that still won't resolve
		} else {
			step.Selector = "#real"
		}
		return nil
	}
	modified, _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 2 * time.Second, Healer: heal, Browser: b})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rounds < 2 {
		t.Fatalf("expected the healer to be consulted across rounds, got %d", rounds)
	}
	if !modified {
		t.Fatal("expected modified=true after a successful heal")
	}
	if skill.Steps[0].Selector != "#real" {
		t.Fatalf("step not corrected: %q", skill.Steps[0].Selector)
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

	modified, _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 2 * time.Second, Healer: heal})
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

// TestAssembleOutputs: file[] collects every bound value in order; other types
// take the last; a declared-but-unbound output defaults (empty slice / "").
func TestAssembleOutputs(t *testing.T) {
	outs := []Output{
		{Name: "files", Type: "file[]"},
		{Name: "id", Type: "string"},
		{Name: "last", Type: "file"},
		{Name: "missing", Type: "file[]"},
	}
	binds := map[string][]string{
		"files": {"/a", "/b"},
		"id":    {"RPT-1"},
		"last":  {"/x", "/y"},
		"stray": {"ignored"}, // undeclared → must not surface
	}
	got := assembleOutputs(outs, binds)
	if f, ok := got["files"].([]string); !ok || len(f) != 2 || f[0] != "/a" || f[1] != "/b" {
		t.Fatalf("files: %#v", got["files"])
	}
	if got["id"] != "RPT-1" {
		t.Fatalf("id: %#v", got["id"])
	}
	if got["last"] != "/y" {
		t.Fatalf("last (want last bound /y): %#v", got["last"])
	}
	if m, ok := got["missing"].([]string); !ok || len(m) != 0 {
		t.Fatalf("missing file[] should be empty slice: %#v", got["missing"])
	}
	if _, ok := got["stray"]; ok {
		t.Fatalf("undeclared bind leaked into outputs: %#v", got)
	}
}

// TestSkillYAMLRoundTripOutputs: the new outputs/bind/js fields survive a
// marshal → parse round-trip (so a hand-edited skill keeps its handoff contract).
func TestSkillYAMLRoundTripOutputs(t *testing.T) {
	s := Skill{
		Name:    "r",
		Outputs: []Output{{Name: "files", Type: "file[]"}, {Name: "url", Type: "string"}},
		Steps: []Step{
			{Action: "download", Selector: "#e", Bind: "files"},
			{Action: "extract", JS: "location.href", Bind: "url"},
		},
	}
	data, err := MarshalSkill(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"outputs:", "type: file[]", "action: download", "bind: files", "action: extract", "js: location.href"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("yaml missing %q:\n%s", want, data)
		}
	}
	back, err := ParseSkill(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Outputs) != 2 || back.Outputs[0].Type != "file[]" {
		t.Fatalf("outputs round-trip: %#v", back.Outputs)
	}
	if back.Steps[0].Bind != "files" || back.Steps[1].JS != "location.href" {
		t.Fatalf("step round-trip: %#v", back.Steps)
	}
}

// TestReplaySkillDownloadBindsOutputs: a download step captures the file the
// trigger produces and binds its path into the skill's declared file[] output.
func TestReplaySkillDownloadBindsOutputs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file.csv" {
			w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
			w.Header().Set("Content-Type", "text/csv")
			w.Write([]byte("a,b,c\n1,2,3\n"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><title>dl</title><a id="dl" href="/file.csv" download>Export</a>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#dl", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	skill := &Skill{
		Name:    "dl",
		Outputs: []Output{{Name: "files", Type: "file[]"}},
		Steps:   []Step{{Action: "download", Selector: "#dl", Bind: "files"}},
	}
	_, _, outputs, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second, Browser: b, DownloadDir: t.TempDir()})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	files, ok := outputs["files"].([]string)
	if !ok || len(files) != 1 {
		t.Fatalf("want 1 file in outputs, got %#v", outputs["files"])
	}
	if _, err := os.Stat(files[0]); err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}
}

// TestReplaySkillExtractBindsOutput: an extract step evaluates JS and binds the
// (unwrapped) string result into a declared output.
func TestReplaySkillExtractBindsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><title>x</title><div id="rid">RPT-42</div>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#rid", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	skill := &Skill{
		Name:    "x",
		Outputs: []Output{{Name: "report_id", Type: "string"}},
		Steps:   []Step{{Action: "extract", JS: "document.querySelector('#rid').textContent", Bind: "report_id"}},
	}
	_, _, outputs, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second, Browser: b})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if outputs["report_id"] != "RPT-42" {
		t.Fatalf("want RPT-42, got %#v", outputs["report_id"])
	}
}

// TestRunStepDownloadExtractGuards: the up-front validation on download/extract
// fires before touching the page, so it needs no browser.
func TestRunStepDownloadExtractGuards(t *testing.T) {
	// download without a Browser session.
	if _, err := runStep(context.Background(), nil, nil, &Step{Action: "download", Selector: "#x", Bind: "f"}, nil, time.Second, "/tmp", nil); err == nil || !strings.Contains(err.Error(), "no browser session") {
		t.Fatalf("want no-browser-session error, got %v", err)
	}
	// extract with no JS.
	if _, err := runStep(context.Background(), nil, nil, &Step{Action: "extract", Bind: "v"}, nil, time.Second, "", nil); err == nil || !strings.Contains(err.Error(), "js is required") {
		t.Fatalf("want js-required error, got %v", err)
	}
}

// TestListSkills: reads *.yaml recordings sorted by name, skipping a nameless
// skill and non-yaml files; a missing dir yields nil.
func TestListSkills(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSkill(dir+"/bravo.yaml", Skill{Name: "bravo", Steps: []Step{{Action: "navigate", URL: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveSkill(dir+"/alpha.yaml", Skill{Name: "alpha", Steps: []Step{{Action: "navigate", URL: "x"}}}); err != nil {
		t.Fatal(err)
	}
	// A parseable YAML with no name (skipped) and a non-yaml file (ignored).
	if err := os.WriteFile(dir+"/noname.yaml", []byte("description: has no name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/notes.txt", []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ListSkills(dir)
	if len(got) != 2 {
		t.Fatalf("want 2 skills (nameless + non-yaml skipped), got %d: %+v", len(got), got)
	}
	if got[0].Name != "alpha" || got[1].Name != "bravo" {
		t.Fatalf("want sorted [alpha bravo], got [%s %s]", got[0].Name, got[1].Name)
	}
	if ListSkills(dir+"/nope") != nil {
		t.Fatal("missing dir should yield nil")
	}
}

// TestReplaySkillDownloadNoDoubleBindOnHeal: a download step whose Verify fails
// first, then passes after a heal, must bind the file exactly once — recoverStep
// re-runs the whole step, so binding before Verify would double-count the output.
func TestReplaySkillDownloadNoDoubleBindOnHeal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file.csv" {
			w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
			w.Header().Set("Content-Type", "text/csv")
			w.Write([]byte("a,b\n1,2\n"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><title>dl</title><a id="dl" href="/file.csv" download>Export</a>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#dl", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	// The Verify requires #done, which doesn't exist yet; the healer injects it so
	// the retry's Verify passes. (The healer leaves the step unchanged.)
	heal := func(ctx context.Context, p *Page, _ *Step, _ error) error {
		return p.Eval(ctx, "(function(){var d=document.createElement('div');d.id='done';document.body.appendChild(d);})()", nil)
	}
	skill := &Skill{
		Name:    "dl",
		Outputs: []Output{{Name: "files", Type: "file[]"}},
		Steps:   []Step{{Action: "download", Selector: "#dl", Bind: "files", Verify: &Verify{Exists: "#done"}}},
	}
	_, _, outputs, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 5 * time.Second, Browser: b, DownloadDir: t.TempDir(), Healer: heal})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	files, ok := outputs["files"].([]string)
	if !ok || len(files) != 1 {
		t.Fatalf("download must bind exactly once across a heal retry, got %#v", outputs["files"])
	}
}
