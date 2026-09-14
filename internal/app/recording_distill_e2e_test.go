package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/browser"
	"github.com/open-octo/octo-agent/internal/config"
)

// TestDistillE2E_RealModels is the end-to-end check for #2406: a real headless
// Chrome demonstration in the shape of the report (enter 笔记管理 → open the
// latest note → open its comments, then three stray clicks: open the reply box
// → expand a reply → 取消), recorded by the real recorder, distilled by every
// model in the user's ~/.octo/config.yml through the real provider clients.
//
// It runs the distiller four ways per model — with the goal and the effects
// (the design), goal only, effects only, neither (what #2406 reported) — and
// prints the matrix. The assertion is on the design: with goal + effects every
// configured model must keep the three intended clicks and drop the three
// strays. The other variants are logged for comparison, not asserted.
//
// Live network and real keys: never runs in CI. Set OCTO_E2E_DISTILL=1 to run
// it by hand (`OCTO_E2E_DISTILL=1 go test ./internal/app/ -run TestDistillE2E -v`).
// OCTO_E2E_DISTILL_MODELS=composite,ids limits it to some models.
func TestDistillE2E_RealModels(t *testing.T) {
	if os.Getenv("OCTO_E2E_DISTILL") == "" {
		t.Skip("set OCTO_E2E_DISTILL=1 to run the live-model distill check")
	}
	if !browser.ChromeAvailable("") {
		t.Skip("chrome not available")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	type target struct {
		id    string
		entry config.ModelEntry
	}
	var targets []target
	only := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("OCTO_E2E_DISTILL_MODELS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			only[id] = true
		}
	}
	for _, ep := range cfg.Endpoints {
		for _, m := range ep.Models {
			id := ep.CompositeID(m.Model)
			if len(only) > 0 && !only[id] {
				continue
			}
			targets = append(targets, target{id: id, entry: config.EntryFor(ep, m)})
		}
	}
	if len(targets) == 0 {
		t.Skip("no models configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	events, startURL := recordStrayClickDemo(t, ctx)

	// The deterministic half of the design must hold before any model is
	// consulted. 查看评论 is a GET-only in-page tab at the tail, so it is
	// marked too — the design accepts that false positive because the marker
	// is a question, and it is exactly what the Goal line exists to settle:
	// the model must keep 查看评论 (the goal needs it) and drop the strays.
	var marked, clicks []string
	for _, e := range events {
		if e.Type == "click" {
			clicks = append(clicks, e.Text)
			if e.LikelyNoop {
				marked = append(marked, e.Text)
			}
		}
	}
	t.Logf("recorded clicks: %v", clicks)
	t.Logf("likely_noop: %v", marked)
	if strings.Join(marked, ",") != "查看评论,说点什么...,2回复,取消" {
		t.Fatalf("recorder must mark the trailing GET-only/no-request clicks, got %v", marked)
	}

	labelBySelector := map[string]string{}
	for _, e := range events {
		if e.Type == "click" {
			labelBySelector[e.Selector] = e.Text
		}
	}

	const goal = "进笔记管理，找到最新一篇笔记，点开它的评论"
	// A goal that names no step: the marker then has nothing to defer to, and
	// the intended GET-only tail (查看评论) is flagged like the strays. Measures
	// whether the marker makes the model over-delete when the goal is vague.
	const vagueGoal = "帮我录一下这个操作"
	stripEffects := func(in []browser.RecordedEvent) []browser.RecordedEvent {
		out := make([]browser.RecordedEvent, len(in))
		copy(out, in)
		for i := range out {
			out[i].Effects = nil
			out[i].LikelyNoop = false
		}
		return out
	}
	variants := []struct {
		name   string
		goal   string
		events []browser.RecordedEvent
	}{
		{"goal+effects", goal, events},
		{"goal only", goal, stripEffects(events)},
		{"effects only", "", events},
		{"neither", "", stripEffects(events)},
		{"vague+effects", vagueGoal, events},
		{"vague only", vagueGoal, stripEffects(events)},
	}

	type cell struct {
		ok     bool
		detail string
	}
	results := map[string]map[string]cell{}
	for _, tg := range targets {
		sender, err := senderForE2E(cfg, tg.entry)
		if err != nil {
			t.Logf("%s: skipped (%v)", tg.id, err)
			continue
		}
		inner := MakeRecordingGenerator(sender, tg.entry.Model)
		// Keep the model's raw reply so a fallback ("output had no steps") can
		// be diagnosed from the log instead of guessed at.
		var lastRaw string
		gen := func(ctx context.Context, system, user string) (string, error) {
			out, err := inner(ctx, system, user)
			lastRaw = out
			return out, err
		}
		results[tg.id] = map[string]cell{}
		for _, v := range variants {
			started := time.Now()
			lastRaw = ""
			rec, fallback := browser.GenerateRecording(ctx, "查最新评论", startURL, v.goal, v.events, gen)
			if fallback != "" && lastRaw != "" {
				t.Logf("%s %s raw model output:\n%s", tg.id, v.name, lastRaw)
			}
			// Judge by selector, not by the label the model may have dropped
			// from its YAML: the selector is the constrained ground truth.
			var kept []string
			for _, st := range rec.Steps {
				if st.Action == "click" {
					if text := labelBySelector[st.Selector]; text != "" {
						kept = append(kept, text)
					} else {
						kept = append(kept, st.Selector)
					}
				}
			}
			ok, why := judgeDistill(kept, fallback)
			results[tg.id][v.name] = cell{ok, fmt.Sprintf("%s kept=%v (%.0fs)", why, kept, time.Since(started).Seconds())}
			t.Logf("%-28s %-13s %v  %s", tg.id, v.name, ok, results[tg.id][v.name].detail)
		}
	}

	ids := make([]string, 0, len(results))
	for id := range results {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%-26s", "model")
	for _, v := range variants {
		fmt.Fprintf(&sb, " %-14s", v.name)
	}
	for _, id := range ids {
		fmt.Fprintf(&sb, "\n%-26s", id)
		for _, v := range variants {
			mark := "FAIL"
			if results[id][v.name].ok {
				mark = "pass"
			}
			fmt.Fprintf(&sb, " %-14s", mark)
		}
	}
	t.Log(sb.String())
	if len(ids) == 0 {
		t.Fatal("no model could be reached")
	}
	for _, id := range ids {
		if c := results[id]["goal+effects"]; !c.ok {
			t.Errorf("%s: the design (goal + effects) did not clean the recording: %s", id, c.detail)
		}
	}
}

// judgeDistill: the intended path survives, every stray is gone, and the
// refinement was actually applied.
func judgeDistill(kept []string, fallback string) (bool, string) {
	if fallback != "" {
		return false, "baseline kept: " + fallback
	}
	have := map[string]bool{}
	for _, k := range kept {
		have[k] = true
	}
	var problems []string
	for _, want := range []string{"笔记管理", "最新笔记", "查看评论"} {
		if !have[want] {
			problems = append(problems, "lost "+want)
		}
	}
	for _, stray := range []string{"说点什么...", "2回复", "取消"} {
		if have[stray] {
			problems = append(problems, "kept "+stray)
		}
	}
	if len(problems) > 0 {
		return false, strings.Join(problems, ", ")
	}
	return true, "clean"
}

// senderForE2E mirrors the server's per-entry sender construction
// (internal/server senderForEntry): env key first, then the entry's stored
// key; the config's reasoning settings, so a reasoning model distils here the
// way it does in production.
func senderForE2E(cfg config.Config, entry config.ModelEntry) (agent.Sender, error) {
	apiKey := os.Getenv(VendorAPIKeyEnvVar(entry.Provider))
	if apiKey == "" {
		apiKey = entry.APIKey
	}
	if apiKey == "" && !VendorKeyOptional(entry.Provider) {
		return nil, fmt.Errorf("no API key for model %q (provider %q)", entry.Model, entry.Provider)
	}
	return NewSender(SenderOptions{
		Provider:        entry.Provider,
		APIKey:          apiKey,
		BaseURL:         entry.BaseURL,
		Protocol:        entry.Protocol,
		Headers:         entry.Headers,
		RPM:             entry.RPM,
		MaxConcurrency:  entry.MaxConcurrency,
		ReasoningEffort: cfg.ReasoningEffort,
		ShowReasoning:   cfg.EffectiveShowReasoning(nil),
	})
}

// strayClickDemoPage is a small SPA in the shape of the #2406 report: a menu
// of click-handled spans (no interactive tag or role), routes changed by
// pushState with a GET per view, a comment box whose opening and 取消 touch
// nothing on the wire, a reply expander that only GETs, and a 发送 button that
// would POST — never pressed, so nothing in the demonstration writes.
const strayClickDemoPage = `<!doctype html><title>创作中心</title>
<div class="list">
  <div class="d-menu-item"><span class="menu-title-wrapper" id="m-home">首页</span></div>
  <div class="d-menu-item"><span class="menu-title-wrapper" id="m-notes">笔记管理</span></div>
</div>
<div id="notes" hidden>
  <div class="note-item"><span class="note-title" id="n-latest">最新笔记</span></div>
  <div class="note-item"><span class="note-title">旧笔记</span></div>
</div>
<div id="detail" hidden>
  <h2>最新笔记</h2>
  <span class="tab" id="t-comments">查看评论</span>
  <div id="comments" hidden>
    <div class="comment"><span>写得不错</span> <span class="reply-count" id="r-expand">2回复</span>
      <div id="replies" hidden><div>谢谢</div><div>同感</div></div></div>
    <div class="comment-box"><span class="placeholder" id="c-open">说点什么...</span>
      <div id="editor" hidden><textarea></textarea><button id="c-send">发送</button><button id="c-cancel">取消</button></div>
    </div>
  </div>
</div>
<script>
  var $ = function(id){ return document.getElementById(id); };
  $('m-notes').onclick = function(){ history.pushState({}, '', '/notes'); fetch('/api/notes'); $('notes').hidden = false; };
  $('n-latest').onclick = function(){ history.pushState({}, '', '/notes/1'); fetch('/api/notes/1'); $('detail').hidden = false; };
  $('t-comments').onclick = function(){ fetch('/api/notes/1/comments'); $('comments').hidden = false; };
  $('c-open').onclick = function(){ $('editor').hidden = false; };
  $('r-expand').onclick = function(){ fetch('/api/comments/1/replies'); $('replies').hidden = false; };
  $('c-cancel').onclick = function(){ $('editor').hidden = true; };
  $('c-send').onclick = function(){ fetch('/api/comments', {method: 'POST', body: 'x'}); };
</script>`

// recordStrayClickDemo serves the page, records the six clicks through the
// real recorder in headless Chrome, and returns the events plus the start URL.
func recordStrayClickDemo(t *testing.T, ctx context.Context) ([]browser.RecordedEvent, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(strayClickDemoPage))
	}))
	t.Cleanup(srv.Close)

	b, err := browser.Launch(ctx, browser.LaunchOptions{Headless: true})
	if err != nil {
		t.Fatalf("launch chrome: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#m-notes", 30*time.Second); err != nil {
		t.Fatalf("wait for fixture: %v", err)
	}
	rec := browser.NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start recorder: %v", err)
	}
	for _, sel := range []string{"#m-notes", "#n-latest", "#t-comments", "#c-open", "#r-expand", "#c-cancel"} {
		if err := page.WaitFor(ctx, sel, 10*time.Second); err != nil {
			t.Fatalf("wait %s: %v", sel, err)
		}
		if err := page.Click(ctx, sel); err != nil {
			t.Fatalf("click %s: %v", sel, err)
		}
		time.Sleep(700 * time.Millisecond) // a person's pace; lets each click's requests attribute cleanly
	}
	time.Sleep(500 * time.Millisecond)
	rec.Stop()
	return rec.Events(), srv.URL
}
