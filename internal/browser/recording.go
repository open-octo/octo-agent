package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Recording is a recorded browser workflow in its editable, replayable form. It
// serializes to YAML — human-readable, hand-editable, git-versionable — which is
// the "editable steps" surface. Replay reads it back; self-heal writes it back.
type Recording struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Params      []Param  `yaml:"params,omitempty"`
	Outputs     []Output `yaml:"outputs,omitempty"`
	Steps       []Step   `yaml:"steps"`
}

// Param is a replay-time input; {{name}} placeholders in step values/urls are
// substituted from it (falling back to Default). Secret marks a password-class
// value: it is never given a recorded Default, and at replay the runtime
// collects it out-of-band (masked prompt / env / session cache) so the value
// never enters the conversation.
type Param struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Default     string `yaml:"default,omitempty"`
	Secret      bool   `yaml:"secret,omitempty"`
}

// Output is a value replay exposes to its caller — the handoff surface that lets
// a recording feed a downstream step. Steps bind a produced value to an Output
// by name via Step.Bind (a download binds the captured file path; an extract
// binds the evaluated string). Type controls aggregation: "file[]" collects
// every bound value in order; "file"/"string" (and anything else) take the last.
type Output struct {
	Name string `yaml:"name"`
	Type string `yaml:"type,omitempty"` // file | file[] | string
}

// Step is one action. Selector is within its document; Frame (a same-origin
// iframe selector) scopes it via the " >>> " convention. Label is a human note;
// replay ignores it.
type Step struct {
	Action   string `yaml:"action"` // navigate | click | type | select | upload | wait | download | extract | key
	URL      string `yaml:"url,omitempty"`
	Frame    string `yaml:"frame,omitempty"`
	Selector string `yaml:"selector,omitempty"`
	Value    string `yaml:"value,omitempty"`
	Label    string `yaml:"label,omitempty"`
	// Hint is a form field's accessible name (placeholder/name/aria-label/id or
	// its <label> text). It's the deterministic fallback for type/select/upload:
	// when the positional Selector drifts, replay re-locates the field by Hint
	// before giving up to the healer — the field-input analogue of Label steering
	// a click by visible text.
	Hint      string   `yaml:"hint,omitempty"`
	Bind      string   `yaml:"bind,omitempty"`       // download/extract: write the produced value into this named Output
	JS        string   `yaml:"js,omitempty"`         // extract: expression whose value is bound
	Network   bool     `yaml:"network,omitempty"`    // wait: settle for network (fetch/XHR) idle instead of a fixed delay
	TimeoutMS int      `yaml:"timeout_ms,omitempty"` // wait: fixed delay (ms) when no selector; also caps the network-idle settle
	Verify    *Verify  `yaml:"verify,omitempty"`
	Anchors   *Anchors `yaml:"anchors,omitempty"` // element fingerprint for scored re-identification (nil = legacy resolution)
}

// Anchors is a step target's fingerprint: redundant identification signals
// captured at record time so replay can re-identify the element by scoring
// (resolveAnchoredTarget) instead of trusting one selector. Selectors are
// alternates built with different strategies than Step.Selector; Role/Tag are
// the element's role attribute and tag name; NeighborText is the nearest stable
// label-like text next to it. The step's visible text lives in Step.Label. A
// nil Anchors keeps the legacy resolution paths — old YAML replays unchanged.
//
// A pointer field keeps Step comparable (recoverStep relies on *step == before);
// the healer never mutates Anchors, so pointer identity is the right semantics.
type Anchors struct {
	Selectors    []string `yaml:"selectors,omitempty"`
	Role         string   `yaml:"role,omitempty"`
	Tag          string   `yaml:"tag,omitempty"`
	NeighborText string   `yaml:"neighbor_text,omitempty"`
}

// Verify is an optional post-step assertion. Exists waits for a selector; Text
// waits for body text to contain a string; URL waits for location.href to contain
// a substring (auto-set to the destination host on navigate steps, to catch a
// redirect to a different host — a login/error page — whose DOM often mirrors the
// target). Empty means the implicit check only (the step's own target became
// present).
type Verify struct {
	Exists string `yaml:"exists,omitempty"`
	Text   string `yaml:"text,omitempty"`
	URL    string `yaml:"url,omitempty"`
}

// Healer is called when a step fails. It may inspect the page and mutate *step
// to repair it (e.g. fix a drifted selector); returning nil means "retry now".
// A non-nil return aborts replay. Provided by the caller (the tool layer wires
// an LLM-backed healer); the engine itself stays LLM-free.
type Healer func(ctx context.Context, page *Page, step *Step, cause error) error

// CompileRecording turns a recording into an editable Recording. It seeds a leading
// navigate from the start URL (when it's a real page, not the about:blank tab
// octo opens) and then walks the captured events in order — including the
// top-level navigations the recorder captured, so a multi-page demonstration
// replays from the right pages.
func CompileRecording(name, description, startURL string, events []RecordedEvent) Recording {
	// Collapse consecutive identical raw events (a jittery double-fire / re-
	// dispatched event) BEFORE parameterization. dropConsecutiveDupeSteps can't
	// catch a repeated `type` afterwards, because each gets a distinct {{param}}
	// placeholder as its value — so a spurious extra param+step would survive.
	events = dedupeConsecutiveEvents(events)
	// Layer-1 deterministic compression: drop provably-redundant events (typing
	// corrections, select corrections) before the LLM distill sees them — keeps
	// the baseline clean and reduces the chance the distiller hallucinates a
	// reason to keep a fumble.
	events = compressEvents(events)
	events = collapseNewTabDetours(events)
	s := Recording{Name: name, Description: description}
	if startURL != "" && startURL != "about:blank" {
		s.Steps = append(s.Steps, navStep(startURL))
	}
	// Deterministic auto-parameterization: every value the user typed becomes a
	// declared {{param}} whose Default is the recorded value — so replay with no
	// params reproduces the demonstration exactly, yet each input is now a named,
	// overridable knob (no LLM distiller required). Password fields are declared
	// without a Default so the secret never lands in the YAML.
	seen := map[string]bool{}
	addParam := func(hint, def, desc string, secret bool) string {
		root := slugParam(hint)
		if root == "" {
			root = "value"
		}
		nm := root
		for i := 2; seen[nm]; i++ {
			nm = fmt.Sprintf("%s%d", root, i)
		}
		seen[nm] = true
		// Default and Secret are independent concerns: Default is written
		// whenever a recorded value exists; Secret tags password-class params.
		// A secret param NEVER gets a Default, even if its event somehow
		// carried a value — the YAML must stay free of plaintext (callers that
		// merely want "no default", like upload's file, pass secret=false).
		p := Param{Name: nm, Description: desc, Secret: secret}
		if def != "" && !secret {
			p.Default = def
		}
		s.Params = append(s.Params, p)
		return nm
	}
	// addDownloadOutput auto-declares a file[] Output for a download step to bind
	// the captured file to, so downstream steps / the workflow can use it.
	outSeen := map[string]bool{}
	addDownloadOutput := func(filename string) string {
		root := slugParam(filename)
		if root == "" {
			root = "downloaded_file"
		}
		nm := root
		for i := 2; outSeen[nm]; i++ {
			nm = fmt.Sprintf("%s%d", root, i)
		}
		outSeen[nm] = true
		s.Outputs = append(s.Outputs, Output{Name: nm, Type: "file[]"})
		return nm
	}
	for _, e := range events {
		if e.Type == "navigate" {
			// Skip an echo of the page we're already on (start URL or the prior nav).
			if n := len(s.Steps); n > 0 && s.Steps[n-1].Action == "navigate" && s.Steps[n-1].URL == e.URL {
				continue
			}
			s.Steps = append(s.Steps, navStep(e.URL))
			continue
		}
		if e.Type == "upload" {
			// The user clicked an upload control, then picked a file. Replay
			// clicks the control and feeds the file through the chooser, so the
			// preceding click (the button) is the better trigger than the
			// possibly-transient file input. The file itself can't be captured
			// (browsers hide the path) so it's auto-parameterized (no default).
			// It is NOT a secret: a missing path stays a plain missing-param
			// error, never a masked prompt.
			fp := addParam("file", "", "path to the file to upload", false)
			up := Step{Action: "upload", Frame: e.Frame, Selector: e.Selector, Value: "{{" + fp + "}}", Label: e.Text}
			// The trigger click is often separated from the file-pick event by
			// auto-inserted waits (the chooser click fires the network probe /
			// modal observer), so look back PAST wait steps for it — requiring
			// strict adjacency left both the click AND an input-targeting upload
			// step in the recording, and a hidden <input type=file> has no
			// clickable box to replay against. Carry the click's hint and
			// fingerprint too, so the merged step resolves and heals like the
			// click would have.
			j := len(s.Steps) - 1
			for j >= 0 && s.Steps[j].Action == "wait" {
				j--
			}
			if j >= 0 && s.Steps[j].Action == "click" {
				c := s.Steps[j]
				up.Selector, up.Frame, up.Label, up.Hint, up.Anchors = c.Selector, c.Frame, c.Label, c.Hint, c.Anchors
				s.Steps = append(s.Steps[:j], s.Steps[j+1:]...)
			}
			s.Steps = append(s.Steps, up)
			continue
		}
		if e.Type == "wait" {
			// Auto-inserted wait: the recorder detected a condition the next step
			// depends on (network activity, a modal appearing). Emit a wait step so
			// the next action only fires once the page has settled.
			st := Step{Action: "wait", Frame: e.Frame}
			switch e.WaitKind {
			case "network":
				st.Network = true
				st.TimeoutMS = e.TimeoutMS
			case "element":
				st.Selector = e.Selector
			}
			s.Steps = append(s.Steps, st)
			continue
		}
		if e.Type == "download" {
			// Auto-detected download: a click that triggered a browser download.
			// Emit a download step that replay uses to capture the file. Bind the
			// captured file path to an auto-declared output so downstream steps or
			// the workflow can use it.
			st := Step{Action: "download", Frame: e.Frame, Selector: e.Selector, Label: e.Text, Hint: e.Field, Anchors: eventAnchors(e)}
			outName := addDownloadOutput(e.DownloadName)
			st.Bind = outName
			s.Steps = append(s.Steps, st)
			continue
		}
		if e.Selector == "" {
			continue
		}
		st := Step{Frame: e.Frame, Selector: e.Selector, Label: e.Text, Hint: e.Field, Anchors: eventAnchors(e)}
		switch {
		case e.Type == "click":
			st.Action = "click"
		case e.Type == "change" && e.Tag == "SELECT":
			st.Action = "select"
			st.Value = e.Value
		case e.Type == "change":
			st.Action = "type"
			hint := e.Field
			if hint == "" {
				hint = hintFromSelector(e.Selector)
			}
			desc := ""
			if e.Secret {
				desc = "secret value (not stored; provide at replay)"
			}
			pn := addParam(hint, e.Value, desc, e.Secret)
			st.Value = "{{" + pn + "}}"
		case e.Type == "enter":
			// Enter in a text input — the submit gesture. Without a blur no change
			// event fired, so the typed value only exists as this event's snapshot:
			// emit the type step for it first (parameterized like a change), unless
			// the field was already typed moments ago (blur → change → Enter).
			st.Action = "key"
			st.Value = "enter"
			alreadyTyped := false
			if n := len(s.Steps); n > 0 {
				last := s.Steps[n-1]
				alreadyTyped = last.Action == "type" && last.Selector == e.Selector && last.Frame == e.Frame
			}
			if (e.Value != "" || e.Secret) && !alreadyTyped {
				hint := e.Field
				if hint == "" {
					hint = hintFromSelector(e.Selector)
				}
				desc := ""
				if e.Secret {
					desc = "secret value (not stored; provide at replay)"
				}
				pn := addParam(hint, e.Value, desc, e.Secret)
				s.Steps = append(s.Steps, Step{Action: "type", Frame: e.Frame, Selector: e.Selector, Label: e.Text, Hint: hint, Value: "{{" + pn + "}}", Anchors: eventAnchors(e)})
			}
		default:
			continue
		}
		s.Steps = append(s.Steps, st)
	}
	s.Steps = dropConsecutiveDupeSteps(s.Steps)
	return s
}

// eventAnchors builds a step's Anchors from a captured event's fingerprint
// fields, or nil when the event carries none (an event captured by an older
// recorder, or a synthetic one) — nil keeps the legacy resolution paths.
func eventAnchors(e RecordedEvent) *Anchors {
	if len(e.AltSelectors) == 0 && e.Role == "" && e.NeighborText == "" {
		return nil
	}
	return &Anchors{
		Selectors:    e.AltSelectors,
		Role:         e.Role,
		Tag:          strings.ToLower(e.Tag),
		NeighborText: e.NeighborText,
	}
}

// compressEvents applies deterministic Layer-1 rules to drop provably-redundant
// events from a recording BEFORE the LLM distill sees them. These are patterns
// where a later event makes the earlier one irrelevant AND no side-effect event
// sits between them that would make the intermediate steps meaningful.
//
// Rules (all conservative — each is provably safe):
//  1. Overwrite typing/selecting: consecutive type|change events on the same
//     selector (same frame) with no navigate/download/wait/click between → keep
//     only the last (the earlier value is overwritten / re-selected).
//  2. A-B-A click backtrack: click A → (only clicks, no nav/wait/download)* →
//     click A again → delete the detour AND the return click (final state = A
//     clicked once). Only fires when every intermediate event is a click, because
//     any non-click intermediate could have done something irreversible.
//
// Events are processed in order; the function returns a new slice.
func compressEvents(events []RecordedEvent) []RecordedEvent {
	if len(events) < 2 {
		return events
	}
	out := make([]RecordedEvent, 0, len(events))
	for i := 0; i < len(events); i++ {
		e := events[i]
		// Rule 1: same-selector overwrite for type/change — peek at the next
		// surviving event; if it overwrites us, skip emitting this one.
		if e.Type == "type" || e.Type == "change" {
			if j := nextSurvivingClickable(events, i); j >= 0 {
				nxt := events[j]
				if nxt.Selector == e.Selector && nxt.Frame == e.Frame &&
					(nxt.Type == "type" || nxt.Type == "change") &&
					!hasSideEffectBetween(events, i, j) {
					continue // overwritten by nxt
				}
			}
		}
		// Rule 2: A-B-A click backtrack — if this click re-appears later with only
		// clicks between, this whole stretch is a detour; skip to the last A.
		if e.Type == "click" && e.Selector != "" {
			if lastA := findABABacktrack(events, i); lastA > i {
				out = append(out, e) // emit the first A (final state = clicked A)
				i = lastA            // skip detour and the return click
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// nextSurvivingClickable returns the index of the next event after i that is a
// type|change|click, or -1 if none. Used to peek whether a later same-selector
// event overwrites event i.
func nextSurvivingClickable(events []RecordedEvent, i int) int {
	for j := i + 1; j < len(events); j++ {
		switch events[j].Type {
		case "type", "change", "click":
			return j
		}
	}
	return -1
}

// hasSideEffectBetween reports whether any event between i and j (exclusive) is
// a state-changing action that would make intermediate steps meaningful:
// navigate, download, wait, or upload.
func hasSideEffectBetween(events []RecordedEvent, i, j int) bool {
	for k := i + 1; k < j; k++ {
		switch events[k].Type {
		case "navigate", "download", "wait", "upload":
			return true
		}
	}
	return false
}

// findABABacktrack looks for a later click with the same selector as events[i],
// where EVERY event between i and that later click is itself a click. Returns the
// index of the later matching click, or -1 if none qualifies.
func findABABacktrack(events []RecordedEvent, i int) int {
	for j := i + 1; j < len(events); j++ {
		mid := events[j]
		if mid.Type != "click" {
			return -1 // non-click intermediate — can't safely compress
		}
		if mid.Selector == events[i].Selector && mid.Frame == events[i].Frame {
			return j
		}
	}
	return -1
}

// navStep builds a navigate step, auto-attaching a URL verify pinned to the
// destination host. This surfaces a redirect to a different host (a login or
// error page, whose DOM often mirrors the target and would otherwise let replay
// proceed on the wrong page) as an explicit failure. It matches the host only —
// scheme-agnostic (an http→https upgrade is not flagged) and blind to same-host
// path rewrites (canonical/locale), so benign redirects don't cause false
// failures. The check is a plain substring of location.href, so it's also
// hand-editable to a fuller URL fragment when a workflow wants a tighter assert.
func navStep(u string) Step {
	st := Step{Action: "navigate", URL: u}
	if h := hostOf(u); h != "" {
		st.Verify = &Verify{URL: h}
	}
	return st
}

// hostOf returns the host of an http(s) URL, or "".
func hostOf(raw string) string {
	pu, err := url.Parse(raw)
	if err != nil || pu.Host == "" || (pu.Scheme != "http" && pu.Scheme != "https") {
		return ""
	}
	return pu.Host
}

// stepSummaryLine renders one step as a human-readable line describing what it
// does and what its verification checks (for the agent to recite to the user).
func stepSummaryLine(i int, s Step) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d. ", i+1))
	switch s.Action {
	case "navigate":
		b.WriteString(fmt.Sprintf("→ 导航到 %s", truncate(s.URL, 60)))
		if s.Verify != nil && s.Verify.URL != "" {
			b.WriteString(fmt.Sprintf("\n   检查：URL 包含「%s」", s.Verify.URL))
		} else {
			b.WriteString("\n   检查：页面加载完成")
		}
	case "click":
		b.WriteString(fmt.Sprintf("→ 点击「%s」", lineLabel(s)))
		if s.Verify != nil && s.Verify.Exists != "" {
			b.WriteString(fmt.Sprintf("\n   检查：元素「%s」出现", s.Verify.Exists))
		} else {
			b.WriteString(fmt.Sprintf("\n   检查：点击后页面/元素处于预期状态"))
		}
	case "type":
		b.WriteString(fmt.Sprintf("→ 在「%s」中输入", lineLabel(s)))
		b.WriteString(fmt.Sprintf("\n   检查：字段值为「%s」", s.Value))
	case "select":
		b.WriteString(fmt.Sprintf("→ 选择「%s」", lineLabel(s)))
		b.WriteString(fmt.Sprintf("\n   检查：选项「%s」已选中", s.Value))
	case "wait":
		if s.Network {
			b.WriteString("→ 等待网络请求完成")
			b.WriteString("\n   检查：数据加载完毕（网络空闲）")
		} else if s.Selector != "" {
			b.WriteString(fmt.Sprintf("→ 等待元素「%s」出现", s.Selector))
			b.WriteString(fmt.Sprintf("\n   检查：元素「%s」可见", s.Selector))
		} else {
			b.WriteString(fmt.Sprintf("→ 等待 %dms", s.TimeoutMS))
			b.WriteString("\n   检查：延迟结束")
		}
	case "download":
		b.WriteString(fmt.Sprintf("→ 下载文件（绑定输出：%s）", s.Bind))
		b.WriteString("\n   检查：文件已保存到本地")
	case "extract":
		b.WriteString(fmt.Sprintf("→ 提取数据（绑定输出：%s）", s.Bind))
		b.WriteString("\n   检查：数据已写入输出")
	case "upload":
		b.WriteString(fmt.Sprintf("→ 上传文件到「%s」", lineLabel(s)))
		b.WriteString("\n   检查：文件已上传")
	case "key":
		b.WriteString(fmt.Sprintf("→ 在「%s」中按 %s", lineLabel(s), s.Value))
		b.WriteString("\n   检查：提交成功")
	default:
		b.WriteString(fmt.Sprintf("→ %s", s.Action))
	}
	return b.String()
}

// lineLabel returns a human-readable name for a step's target: its Label, Hint,
// or a truncated Selector — so a click reads "点击「提交」" not "点击「#btn」".
func lineLabel(s Step) string {
	if s.Label != "" {
		return s.Label
	}
	if s.Hint != "" {
		return s.Hint
	}
	if s.Selector != "" {
		return truncate(s.Selector, 40)
	}
	return "(未识别)"
}

// truncate cuts s to max runes, appending "…" when it shortens.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// SummarizeRecording renders a recording as a numbered, human-readable run-
// plan with each step's verification check — for the agent to recite back to the
// user after record_stop so the user can confirm or request changes. Uses the
// user-facing language (Chinese) by convention; steps are 1-indexed.
func SummarizeRecording(r Recording) string {
	var b strings.Builder
	if r.Description != "" {
		b.WriteString(fmt.Sprintf("录制描述：%s\n", r.Description))
	}
	b.WriteString(fmt.Sprintf("共 %d 步。请确认以下操作步骤：\n", len(r.Steps)))
	for i, s := range r.Steps {
		b.WriteString("\n")
		b.WriteString(stepSummaryLine(i, s))
	}
	b.WriteString("\n\n请确认以上步骤是否正确、检验环节是否充分，或告诉我哪里需要修改。")
	return b.String()
}

// slugParam reduces a field hint to a safe {{param}} identifier: lowercase, with
// runs of non-alphanumerics collapsed to a single underscore and trimmed.
func slugParam(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if b.Len() > 0 && !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// hintFromSelector recovers a naming hint from a selector when the recorder
// captured no field name — pulling an id (#foo) or a stable attribute value
// (name/aria-label/data-testid/data-test), else "".
func hintFromSelector(sel string) string {
	if m := selectorAttrRe.FindStringSubmatch(sel); m != nil {
		return m[1]
	}
	if strings.HasPrefix(sel, "#") {
		id := sel[1:]
		if i := strings.IndexAny(id, " >.:["); i >= 0 {
			id = id[:i]
		}
		return id
	}
	return ""
}

var selectorAttrRe = regexp.MustCompile(`\[(?:name|aria-label|data-testid|data-test)="([^"]+)"\]`)

// collapseNewTabDetours drops the click chain whose only outcome was opening a
// new tab: the tab's first navigation (tagged NewTab by the recorder, and only
// for tabs the PAGE spawned — the target had an opener) reaches the same URL
// directly, so replaying the menu hops that spawned it is pure fragility (the
// Zhihu detour: homepage → popover toggle → "写文章" → new tab; those popover
// clicks were the least replayable steps in the recording). Walk back over
// clicks and waits only — a type/select/upload/enter/download/navigate is
// state-bearing and ends the detour. A tab the USER opened by hand carries no
// opener, is never tagged, and keeps its preceding clicks.
func collapseNewTabDetours(events []RecordedEvent) []RecordedEvent {
	out := make([]RecordedEvent, 0, len(events))
	for _, e := range events {
		if e.Type == "navigate" && e.NewTab {
			for len(out) > 0 {
				t := out[len(out)-1].Type
				if t != "click" && t != "wait" {
					break
				}
				out = out[:len(out)-1]
			}
		}
		out = append(out, e)
	}
	return out
}

// dedupeConsecutiveEvents drops a raw event identical to its immediate
// predecessor (same type/selector/frame/value/tag) — a double-fire or a
// framework's re-dispatched event. navigate is exempt (CompileRecording collapses
// navigate echoes itself). Conservative: only exact consecutive duplicates.
func dedupeConsecutiveEvents(events []RecordedEvent) []RecordedEvent {
	out := make([]RecordedEvent, 0, len(events))
	for i, e := range events {
		if i > 0 && e.Type != "navigate" {
			p := events[i-1]
			if e.Type == p.Type && e.Selector == p.Selector && e.Frame == p.Frame && e.Value == p.Value && e.Tag == p.Tag {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// dropConsecutiveDupeSteps removes a step identical to its immediate predecessor
// (same action/frame/selector/value) — a double-fire the user didn't intend (a
// jittery double-click, a re-dispatched event). Conservative on purpose: only
// exact consecutive duplicates, never reordering or dropping distinct steps, so
// it can't strip a step the workflow actually needs. navigate is exempt.
func dropConsecutiveDupeSteps(steps []Step) []Step {
	out := make([]Step, 0, len(steps))
	for i, st := range steps {
		if i > 0 {
			p := steps[i-1]
			if st.Action != "navigate" && st.Action == p.Action && st.Frame == p.Frame && st.Selector == p.Selector && st.Value == p.Value {
				continue
			}
		}
		out = append(out, st)
	}
	return out
}

// RecordingGenerator asks an LLM to refine a recording into a clean recording. It is a
// plain string→string call so this package needn't import the agent/provider
// layers; the app wires a sender-backed implementation.
type RecordingGenerator func(ctx context.Context, system, user string) (string, error)

// GenerateRecording turns a recording into a recording. The deterministic CompileRecording
// is always the baseline (its selectors are ground truth). When gen is set, the
// LLM refines that baseline — dropping detours/retries, parameterizing variable
// inputs, labeling — but is constrained to the captured selectors; any output
// that fails to parse or invents a selector falls back to the baseline. So the
// LLM only ever cleans up real events, never hallucinates targets.
func GenerateRecording(ctx context.Context, name, startURL string, events []RecordedEvent, gen RecordingGenerator) Recording {
	base := CompileRecording(name, "", startURL, events)
	if gen == nil {
		return base
	}
	baseYAML, err := MarshalRecording(base)
	if err != nil {
		return base
	}
	const system = "You clean a recorded browser workflow into a minimal, correct, replayable recording. " +
		"RULES: (1) Use ONLY CSS selectors that appear in the provided baseline — never invent or alter a selector. " +
		"(2) Drop redundant back-and-forth and retries; keep the intended linear path. " +
		"(3) Replace user-specific input values with {{param}} and declare each in params (keep upload's {{file}}, every declared param name, and any secret: true marker unchanged). " +
		"(4) Preserve step order and all navigate steps. " +
		"(5) Preserve every download step and its bind (keep every declared output name and its type: file[] unchanged — do not drop or rename outputs). " +
		"(6) Write description as a short statement of what the workflow does. " +
		"(7) You may omit each step's anchors block — it is re-attached automatically; never invent one. " +
		"Output ONLY the recording as YAML (keys: name, description, params, outputs, steps), no prose, no code fences."
	user := fmt.Sprintf("Baseline (the only valid selectors are those here):\n%s\n\nRaw events in order:\n%s\n\nReturn the cleaned recording YAML.", baseYAML, renderTrace(events))

	prompt := user
	for attempt := 0; ; attempt++ {
		out, err := gen(ctx, system, prompt)
		if err != nil {
			slog.Warn("browser: recording distill failed, keeping deterministic baseline", "recording", name, "err", err)
			return base
		}
		refined, err := ParseRecording([]byte(stripFences(out)))
		if err != nil || len(refined.Steps) == 0 {
			slog.Warn("browser: recording distill output unusable, keeping deterministic baseline steps", "recording", name, "err", err, "steps", len(refined.Steps))
			return withDescription(base, refined.Description)
		}
		refined.Name = name
		// The distiller rewrites the param list from prose and can drop the secret
		// marker; backfill it by name from the deterministic baseline so a password
		// param is still secret after distillation.
		secretParams := map[string]bool{}
		for _, p := range base.Params {
			if p.Secret {
				secretParams[p.Name] = true
			}
		}
		for i := range refined.Params {
			if secretParams[refined.Params[i].Name] {
				refined.Params[i].Secret = true
			}
		}
		// The distiller may also drop a secret param's DECLARATION while keeping
		// its {{placeholder}} in a step. Left alone, replay would treat it as a
		// non-secret missing param — the plaintext-in-conversation leak the secret
		// marker exists to close. Re-attach the baseline declaration for any
		// baseline secret param the refined steps still reference.
		declared := map[string]bool{}
		for _, p := range refined.Params {
			declared[p.Name] = true
		}
		for _, p := range base.Params {
			if !p.Secret || declared[p.Name] {
				continue
			}
			if paramReferenced(&refined, p.Name) {
				refined.Params = append(refined.Params, p)
			}
		}
		if bad := invalidSelectors(refined, base); len(bad) > 0 {
			// Precision guard: the model used selectors it wasn't given. Name
			// the violations and let it correct itself ONCE — a wholesale
			// silent fallback threw away otherwise-good refinements (observed:
			// both distill attempts of a real recording were discarded, so the
			// cleanup pass never ran at all). On the second miss the refined
			// steps stay untrustworthy; the prose description still isn't —
			// keep it so the recordings manifest isn't left with a bare name.
			if attempt == 0 {
				slog.Warn("browser: recording distill used selectors not in the recording, retrying with violations named", "recording", name, "invalid", strings.Join(bad, " | "))
				prompt = user + "\n\nYour previous attempt was REJECTED because these selectors do not appear in the baseline:\n" + strings.Join(bad, "\n") + "\nReturn the cleaned recording again, using ONLY selectors copied verbatim from the baseline."
				continue
			}
			slog.Warn("browser: recording distill used a selector not in the recording, keeping deterministic baseline steps", "recording", name)
			return withDescription(base, refined.Description)
		}
		backfillAnchors(&refined, base)
		return refined
	}
}

// backfillAnchors re-attaches each refined step's Anchors from the baseline step
// with the same frame+selector. The distiller routinely drops the anchors block
// when rewriting steps; since selectorsSubset already guarantees every refined
// selector came from the baseline, the lookup is deterministic — no reliance on
// the LLM echoing anchors through. Refined steps that already carry anchors are
// left alone.
func backfillAnchors(refined *Recording, base Recording) {
	byTarget := map[string]*Anchors{}
	for i := range base.Steps {
		st := &base.Steps[i]
		if st.Anchors != nil && st.Selector != "" {
			byTarget[st.Frame+"\x00"+st.Selector] = st.Anchors
		}
	}
	for i := range refined.Steps {
		st := &refined.Steps[i]
		if st.Anchors == nil && st.Selector != "" {
			st.Anchors = byTarget[st.Frame+"\x00"+st.Selector]
		}
	}
}

func withDescription(s Recording, desc string) Recording {
	if desc != "" {
		s.Description = desc
	}
	return s
}

func renderTrace(events []RecordedEvent) string {
	var sb strings.Builder
	for i, e := range events {
		// Never put a secret field's value in the prompt sent to the LLM distiller.
		// (The recorder already captures it empty; this is defense in depth so the
		// plaintext can't reach an off-machine provider or be re-inlined.)
		val := e.Value
		if e.Secret {
			val = "[secret]"
		}
		fmt.Fprintf(&sb, "%d. %s selector=%q frame=%q tag=%s text=%q value=%q\n", i+1, e.Type, e.Selector, e.Frame, e.Tag, e.Text, val)
	}
	return sb.String()
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

// selectorsSubset reports whether every selector/frame the refined recording uses
// was present in the baseline (the captured ground truth).
func selectorsSubset(refined, base Recording) bool {
	return len(invalidSelectors(refined, base)) == 0
}

// invalidSelectors lists the selectors/frames the refined recording uses that
// the baseline never captured — the violations to feed back to the distiller
// on its retry, so it can correct them instead of the whole refinement being
// silently discarded.
func invalidSelectors(refined, base Recording) []string {
	allowed := map[string]bool{"": true}
	for _, st := range base.Steps {
		allowed[st.Selector] = true
		allowed[st.Frame] = true
	}
	var bad []string
	seen := map[string]bool{}
	for _, st := range refined.Steps {
		for _, sel := range []string{st.Selector, st.Frame} {
			if !allowed[sel] && !seen[sel] {
				seen[sel] = true
				bad = append(bad, sel)
			}
		}
	}
	return bad
}

// StepDigest renders a compact one-line summary of what a replay does, e.g.
// `navigate www.zhihu.com → click "热榜" → click "日元跌破 1 美元兑 162 日元…"`.
// The recordings manifest uses it when a recording has no description, so the model
// can still see the recording's full path — including its final step, which is
// what tells it the replay ends somewhere specific.
func (s Recording) StepDigest() string {
	var parts []string
	for _, st := range s.Steps {
		switch st.Action {
		case "wait":
			continue
		case "navigate":
			parts = append(parts, "navigate "+truncRunes(digestURL(st.URL), 40))
		default:
			label := st.Label
			if label == "" {
				label = st.Hint
			}
			if label == "" {
				label = st.Selector
			}
			if label == "" {
				parts = append(parts, st.Action)
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %q", st.Action, truncRunes(label, 30)))
		}
	}
	// Keep the head and the final step: the ending is the part a bare name hides.
	if len(parts) > 8 {
		parts = append(parts[:6], "…", parts[len(parts)-1])
	}
	return strings.Join(parts, " → ")
}

func digestURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Hand-edited relative/malformed URL: still never let a query string
		// (which can carry session tokens) into the system prompt.
		raw, _, _ = strings.Cut(raw, "?")
		return raw
	}
	return u.Host + strings.TrimSuffix(u.Path, "/")
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// MarshalRecording renders the recording to YAML.
func MarshalRecording(s Recording) ([]byte, error) { return yaml.Marshal(s) }

// ParseRecording parses a recording from YAML.
func ParseRecording(data []byte) (Recording, error) {
	var s Recording
	err := yaml.Unmarshal(data, &s)
	return s, err
}

// SaveRecording writes a recording to a YAML file.
func SaveRecording(path string, s Recording) error {
	data, err := MarshalRecording(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadRecording reads a recording from a YAML file.
func LoadRecording(path string) (Recording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Recording{}, err
	}
	return ParseRecording(data)
}

// ListRecordings reads every *.yaml recording in dir and returns them sorted by
// name. A missing dir yields nil; an unreadable or unparseable file (a
// half-written or hand-broken recording) is skipped rather than sinking the whole
// list — this feeds the system-prompt manifest, which must stay robust.
func ListRecordings(dir string) []Recording {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Recording
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		s, err := LoadRecording(filepath.Join(dir, e.Name()))
		if err != nil || s.Name == "" {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DigestElement is one interactive element: its visible text and a generated
// selector. Used to give a healer (or live loop) a textual view of the page so
// it can repair a drifted selector by intent.
type DigestElement struct {
	Text     string `json:"text"`
	Selector string `json:"selector"`
}

// InteractiveDigest lists interactive elements (with generated selectors) in the
// document or a same-origin iframe, capped to max. Text-only, so it feeds any
// model — no vision needed when the DOM/AX is reachable.
func InteractiveDigest(ctx context.Context, page *Page, frame string, max int) ([]DigestElement, error) {
	if max <= 0 {
		max = 60
	}
	// A cross-origin iframe's elements are only enumerable in its own session; run
	// the digest there (frame cleared) so the healer sees the OOPIF's real elements.
	if frame != "" {
		if cp, ok := page.oopifPage(ctx, frame); ok {
			return InteractiveDigest(ctx, cp, "", max)
		}
	}
	doc := "document"
	if frame != "" {
		doc = fmt.Sprintf("(document.querySelector(%s)||{}).contentDocument", jsString(frame))
	}
	// Eval uses returnByValue, so return the array directly — wrapping it in
	// JSON.stringify would yield a JSON *string* that fails to unmarshal into
	// []DigestElement.
	expr := fmt.Sprintf(`(function(){
	  var d = %s; if(!d) return [];
	  function sel(el){
	    if(el.id) return '#'+CSS.escape(el.id);
	    for(var i=0;i<4;i++){var a=['data-testid','data-test','name','aria-label'][i];var v=el.getAttribute&&el.getAttribute(a);if(v)return el.tagName.toLowerCase()+'['+a+'="'+CSS.escape(v)+'"]';}
	    var parts=[],node=el,depth=0;
	    while(node&&node.nodeType===1&&node.tagName!=='BODY'&&depth<5){var part=node.tagName.toLowerCase();var p=node.parentElement;if(p){var same=[].slice.call(p.children).filter(function(c){return c.tagName===node.tagName;});if(same.length>1)part+=':nth-of-type('+(same.indexOf(node)+1)+')';}parts.unshift(part);node=p;depth++;}
	    return parts.join(' > ');
	  }
	  var out=[];
	  var els=d.querySelectorAll('a,button,input,select,textarea,[role=button],[role=menuitem],[role=tab],label');
	  // Visible = has layout boxes and not visibility:hidden. (The old
	  // offsetParent===null check also dropped position:fixed controls, which are
	  // null-offsetParent in Chrome but very much clickable — fixed nav bars and
	  // floating buttons were invisible to the healer.)
	  for(var i=0;i<els.length && out.length<%d;i++){var el=els[i]; if(el.getClientRects().length===0 || getComputedStyle(el).visibility==='hidden') continue; var t=(el.textContent||el.value||el.getAttribute('aria-label')||el.getAttribute('placeholder')||'').trim().slice(0,50); out.push({text:t, selector:sel(el)});}
	  return out;
	})()`, doc, max)
	var digest []DigestElement
	if err := page.Eval(ctx, expr, &digest); err != nil {
		return nil, err
	}
	return digest, nil
}

func (s Step) target() string {
	if s.Frame != "" && s.Selector != "" {
		return s.Frame + frameDelim + s.Selector
	}
	return s.Selector
}

// sameLocation reports whether two URL strings address the same document —
// component-wise, so the browser's normalization of "http://host" to
// "http://host/" doesn't defeat the comparison.
func sameLocation(a, b string) bool {
	if a == b {
		return true
	}
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return false
	}
	pa, pb := ua.Path, ub.Path
	if pa == "" {
		pa = "/"
	}
	if pb == "" {
		pb = "/"
	}
	return ua.Scheme == ub.Scheme && ua.Host == ub.Host && pa == pb &&
		ua.RawQuery == ub.RawQuery && ua.Fragment == ub.Fragment
}

// subst replaces {{name}} placeholders with param values, verbatim. Used where
// the surrounding context is plain text (typed values, select options, file
// paths, verify comparisons). URL and JS contexts need the escaping variants
// below — a raw value can silently reshape the URL or the expression.
func subst(s string, params map[string]string) string {
	for k, v := range params {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// substURL is subst for navigate targets: each param value is percent-encoded
// as pure data (everything outside RFC 3986 unreserved), so a value containing
// spaces, '&', '#', '%' or '/' can't silently reshape the URL. The template's
// own structure (its query '&'/'=', path slashes) is untouched.
func substURL(s string, params map[string]string) string {
	enc := make(map[string]string, len(params))
	for k, v := range params {
		enc[k] = escapeURLValue(v)
	}
	return subst(s, enc)
}

// escapeURLValue percent-encodes every byte outside RFC 3986 unreserved
// (A-Z a-z 0-9 - _ . ~). Byte-wise, so UTF-8 multibyte becomes the standard
// %XX%XX form — correct for a data value in either path or query position.
func escapeURLValue(v string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// substJS is subst for extract expressions: values typically land inside a JS
// string literal in the template, so escape the characters that could end the
// literal or alter the value. \' and \" both evaluate to the bare quote in
// either quote style in JS, so escaping both quote types is safe regardless of
// how the template quotes the placeholder.
func substJS(s string, params map[string]string) string {
	enc := make(map[string]string, len(params))
	for k, v := range params {
		enc[k] = escapeJSStringValue(v)
	}
	return subst(s, enc)
}

func escapeJSStringValue(v string) string {
	return jsStringEscaper.Replace(v)
}

var jsStringEscaper = strings.NewReplacer(
	`\`, `\\`,
	`'`, `\'`,
	`"`, `\"`,
	"\n", `\n`,
	"\r", `\r`,
)

// ReplayOptions tunes a replay. StepTimeout bounds the per-step wait for a
// target to appear (default 15s — generous for slow back-ends). Healer, when
// set, is consulted on a step failure. Browser, when set, lets a click follow a
// new tab it opens (target=_blank / window.open). DownloadDir is where download
// steps land their files (required only if the recording has download steps).
type ReplayOptions struct {
	StepTimeout time.Duration
	Healer      Healer
	Browser     *Browser
	DownloadDir string
	// Progress, when set, receives one human-readable line as each step starts
	// (and when self-heal intervenes) — the live view of a replay that can run
	// for minutes. Lines are built from the step's RAW template fields, never
	// substituted param values, so a secret param can't leak through them.
	Progress func(line string)
}

// emitProgress is the nil-safe Progress call.
func (o ReplayOptions) emitProgress(line string) {
	if o.Progress != nil {
		o.Progress(line)
	}
}

// stepProgressLine names a step for the live progress feed: the action plus
// the most human-meaningful identifier the step carries (label > hint >
// selector; URL for navigate; the wait condition for waits). Values are
// deliberately absent — a type step's value may hold a substituted secret.
func stepProgressLine(i, total int, st *Step) string {
	name := st.Label
	if name == "" {
		name = st.Hint
	}
	if name == "" {
		name = st.Selector
	}
	switch st.Action {
	case "navigate":
		name = st.URL
	case "wait":
		switch {
		case st.Selector != "":
			name = st.Selector
		case st.Network:
			name = "network idle"
		default:
			name = fmt.Sprintf("%dms", st.TimeoutMS)
		}
	}
	return fmt.Sprintf("[%d/%d] %s %s", i, total, st.Action, name)
}

// ReplayRecording runs a recording deterministically (no LLM), substituting params. Each
// step implicitly waits for its target to appear (handling slow loads) and
// checks any explicit Verify. On a step failure it calls the healer; if the
// healer repairs the step, replay continues and reports modified=true so the
// caller can write the corrected recording back. A click that opens a new tab swaps
// the active page for subsequent steps; finalPage is the page replay ended on
// (so the caller can keep its session pointed at the right tab). outputs holds
// the recording's declared Outputs as bound during replay (download paths, extracted
// values) — the handoff surface for composing this recording with later steps.
func ReplayRecording(ctx context.Context, page *Page, recording *Recording, params map[string]string, opts ReplayOptions) (modified bool, finalPage *Page, outputs map[string]any, err error) {
	if opts.StepTimeout <= 0 {
		opts.StepTimeout = 15 * time.Second
	}
	if err := unknownParams(recording, params); err != nil {
		return false, page, nil, err
	}
	full := mergedParams(recording, params)
	if missing := unresolvedPlaceholders(recording, full); len(missing) > 0 {
		// Fail before running any step: a {{name}} left unresolved would
		// otherwise be sent to the browser as literal text (a navigate to
		// ".../item/{{item_id}}", a type into a field with that as its
		// value) — silently doing the wrong thing instead of erroring. This
		// also catches a caller that means to run a different, similarly
		// named recording and passes params that don't match this one at all.
		return false, page, nil, fmt.Errorf("replay %q: missing required param(s): %s", recording.Name, strings.Join(missing, ", "))
	}
	// stepHard bounds one step's TOTAL wall time — implicit waits, retries and
	// heal rounds included — as a backstop for CDP calls that never answer: a
	// crashed renderer (e.g. after Chrome killed the page) leaves every command
	// pending forever, and the per-wait timeouts are themselves implemented as
	// CDP polls, so they hang with it. Without this bound a dead tab stalls
	// replay until the caller's context — an entire turn — expires. Generous on
	// purpose: it must never cut a legitimately slow step, only a hung one.
	stepHard := 8 * opts.StepTimeout
	if floor := 2 * navigateLoadTimeout; stepHard < floor {
		stepHard = floor
	}
	binds := map[string][]string{}
	cur := page
	for i := range recording.Steps {
		opts.emitProgress(stepProgressLine(i+1, len(recording.Steps), &recording.Steps[i]))
		stepCtx, cancel := context.WithTimeout(ctx, stepHard)
		np, runErr := runStep(stepCtx, opts.Browser, cur, &recording.Steps[i], full, opts.StepTimeout, opts.DownloadDir, binds)
		if runErr == nil {
			cancel()
			cur = np
			continue
		}
		np, healed, runErr := recoverStep(stepCtx, opts, cur, &recording.Steps[i], full, runErr, &modified, binds)
		cancel()
		if runErr != nil {
			return modified, cur, nil, fmt.Errorf("step %d (%s): %w", i+1, recording.Steps[i].Action, runErr)
		}
		if healed {
			opts.emitProgress(fmt.Sprintf("[%d/%d] recovered — continuing", i+1, len(recording.Steps)))
		}
		cur = np
	}
	return modified, cur, assembleOutputs(recording.Outputs, binds), nil
}

// assembleOutputs turns the ordered values bound during replay into the recording's
// declared outputs. A "file[]" output yields the full ordered slice (empty slice
// if never bound); every other type yields the last bound value ("" if never
// bound). Only declared outputs surface — a stray bind to an undeclared name is
// ignored, keeping the handoff contract exactly what the recording promised.
func assembleOutputs(outs []Output, binds map[string][]string) map[string]any {
	res := make(map[string]any, len(outs))
	for _, o := range outs {
		vals := binds[o.Name]
		if o.Type == "file[]" {
			if vals == nil {
				vals = []string{}
			}
			res[o.Name] = vals
			continue
		}
		if len(vals) > 0 {
			res[o.Name] = vals[len(vals)-1]
		} else {
			res[o.Name] = ""
		}
	}
	return res
}

// maxHealRounds bounds how many times the LLM healer is consulted for one step,
// so a healer that keeps returning a wrong selector can't loop indefinitely.
const maxHealRounds = 3

// recoverStep tries to get a failed step to pass, in escalating order:
//  1. Deterministic structural recovery — dismiss a blocking overlay (a cookie/
//     consent banner or modal covering the target) and retry. No LLM; works even
//     when no healer is wired.
//  2. The LLM healer, up to maxHealRounds times — each round may repair the
//     selector differently as the page settles; the new error feeds the next
//     round. Stops early when the healer makes no change or errors.
//
// It returns the page to continue on, whether a heal mutated the step (for the
// caller's write-back via *modified), and a non-nil error only if the step could
// not be recovered.
func recoverStep(ctx context.Context, opts ReplayOptions, page *Page, step *Step, params map[string]string, cause error, modified *bool, binds map[string][]string) (*Page, bool, error) {
	// 1. Structural: a blocking overlay is the most common non-selector failure.
	if dismissed, _ := page.DismissOverlay(ctx); dismissed {
		opts.emitProgress("step failed — dismissed a blocking overlay, retrying")
		if np, err := runStep(ctx, opts.Browser, page, step, params, opts.StepTimeout, opts.DownloadDir, binds); err == nil {
			return np, true, nil
		}
	}
	if opts.Healer == nil {
		return page, false, fmt.Errorf("%w (self-heal skipped: no healer model available)", cause)
	}
	// 2. LLM healer, multi-round. Every exit path names the heal outcome —
	// returning the bare cause made a failed heal indistinguishable from no
	// heal ever running.
	for round := 0; round < maxHealRounds; round++ {
		opts.emitProgress(fmt.Sprintf("step failed — self-heal round %d/%d", round+1, maxHealRounds))
		before := *step
		if herr := opts.Healer(ctx, page, step, cause); herr != nil {
			return page, false, fmt.Errorf("%w (self-heal gave up: %v)", cause, herr)
		}
		if step.Selector != before.Selector {
			opts.emitProgress("self-heal proposed " + step.Selector + " — retrying")
		}
		if step.Selector != before.Selector {
			// The healer's replacement selector is authoritative for this
			// retry: the recorded fingerprint is exactly what just failed to
			// match the page, so re-gating the healed selector through
			// resolveAnchoredTarget would reject every repair — an anchored
			// step could never heal. Dropping the stale anchors also reaches
			// the YAML via the caller's write-back, so the healed step stays
			// replayable next time instead of deadlocking again.
			step.Anchors = nil
		}
		np, retryErr := runStep(ctx, opts.Browser, page, step, params, opts.StepTimeout, opts.DownloadDir, binds)
		if retryErr == nil {
			if *step != before {
				*modified = true
			}
			return np, true, nil
		}
		if *step == before {
			// healer made no change — further rounds won't help
			return page, false, fmt.Errorf("%w (self-heal returned the unchanged step)", retryErr)
		}
		cause = retryErr // feed the fresh failure to the next round
	}
	return page, false, fmt.Errorf("%w (self-heal tried %d rounds without success)", cause, maxHealRounds)
}

// unknownParams rejects a caller-supplied param key the recording doesn't
// declare. mergedParams alone would silently drop it (nothing in the recording's
// steps references it), which hides the two failure modes this guards
// against: a typo'd param name, or a caller replaying the wrong recording
// entirely for the request it actually has in hand.
func unknownParams(recording *Recording, params map[string]string) error {
	declared := make(map[string]bool, len(recording.Params))
	for _, p := range recording.Params {
		declared[p.Name] = true
	}
	for k := range params {
		if !declared[k] {
			return fmt.Errorf("replay %q: unknown param %q (declared: %s)", recording.Name, k, strings.Join(paramNames(recording.Params), ", "))
		}
	}
	return nil
}

func paramNames(params []Param) []string {
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	return names
}

// paramReferenced reports whether any step field subst() expands (URL, Value,
// JS, Verify.Text, Verify.URL) contains a {{name}} placeholder — the same
// scan unresolvedPlaceholders does, but for a single param.
func paramReferenced(recording *Recording, name string) bool {
	want := "{{" + name + "}}"
	for _, st := range recording.Steps {
		for _, field := range []string{st.URL, st.Value, st.JS} {
			if strings.Contains(field, want) {
				return true
			}
		}
		if st.Verify != nil && (strings.Contains(st.Verify.Text, want) || strings.Contains(st.Verify.URL, want)) {
			return true
		}
	}
	return false
}

// placeholderRe matches a {{name}} substitution placeholder in a step field.
var placeholderRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

// unresolvedPlaceholders scans every step field that subst() ever expands
// (URL, Value, JS, Verify.Text, Verify.URL) and returns the deduplicated
// names referenced there that full does not resolve — a missing param with
// no default, most commonly. Order follows first occurrence in the steps.
func unresolvedPlaceholders(recording *Recording, full map[string]string) []string {
	seen := map[string]bool{}
	var missing []string
	scan := func(s string) {
		for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
			name := m[1]
			if _, ok := full[name]; ok || seen[name] {
				continue
			}
			seen[name] = true
			missing = append(missing, name)
		}
	}
	for _, st := range recording.Steps {
		scan(st.URL)
		scan(st.Value)
		scan(st.JS)
		if st.Verify != nil {
			scan(st.Verify.Text)
			scan(st.Verify.URL)
		}
	}
	return missing
}

// MissingRequiredParams reports which {{name}} placeholders in recording's steps
// remain unresolved after overlaying params on the recording's declared defaults
// — the same check ReplayRecording performs internally before running any step,
// exposed so a caller can surface a clear error to the model (which then
// decides whether to re-invoke with values or ask the user) instead of
// letting ReplayRecording fail outright.
func MissingRequiredParams(recording *Recording, params map[string]string) []string {
	return unresolvedPlaceholders(recording, mergedParams(recording, params))
}

// mergedParams overlays caller params on declared defaults.
func mergedParams(recording *Recording, params map[string]string) map[string]string {
	out := map[string]string{}
	for _, p := range recording.Params {
		if p.Default != "" {
			out[p.Name] = p.Default
		}
	}
	for k, v := range params {
		out[k] = v
	}
	return out
}

// runStep executes one step on page and returns the page subsequent steps should
// run on — the same page, except a click that opens a new tab returns that tab.
// A download/extract step records its produced value into binds under step.Bind;
// downloadDir is where a download step lands its file.
func runStep(ctx context.Context, b *Browser, page *Page, step *Step, params map[string]string, waitTimeout time.Duration, downloadDir string, binds map[string][]string) (*Page, error) {
	target := step.target()
	switch step.Action {
	case "navigate":
		dest := substURL(step.URL, params)
		// Already there → don't reload. A recorded navigate can be the echo of
		// an in-page transition a preceding click already reproduced (SPA tab
		// switches that re-announce the current URL); reloading resets the very
		// page state that click just set up. Compared component-wise (the
		// browser normalizes "http://host" to "http://host/"), and the step's
		// Verify still runs below, so this never skips a real move.
		var cur string
		_ = page.Eval(ctx, "location.href", &cur)
		if !sameLocation(cur, dest) {
			if err := page.Navigate(ctx, dest); err != nil {
				return page, err
			}
		}
	case "wait":
		// Wait for an element if a selector is given (the robust form); else settle
		// for network idle when asked (the SPA-aware form — waits until fetch/XHR
		// activity stops rather than guessing a delay); else a fixed delay. All
		// three are natural primitives for letting a page settle between steps.
		if target != "" {
			if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
				return page, err
			}
		} else if step.Network {
			to := waitTimeout
			if step.TimeoutMS > 0 {
				to = time.Duration(step.TimeoutMS) * time.Millisecond
			}
			if err := page.WaitForNetworkIdle(ctx, 0, to); err != nil {
				return page, err
			}
		} else {
			ms := step.TimeoutMS
			if ms <= 0 {
				ms = 1000
			}
			if ms > 30000 {
				ms = 30000
			}
			select {
			case <-ctx.Done():
				return page, ctx.Err()
			case <-time.After(time.Duration(ms) * time.Millisecond):
			}
		}
	case "click":
		// Anchored steps resolve by fingerprint scoring (and explicitly fail into
		// the healer on a mismatch — never a blind positional click). Legacy steps
		// resolve by visible text when the recording captured it, else wait for the
		// positional selector.
		if step.Anchors != nil {
			t, err := page.resolveAnchoredTarget(ctx, step.Frame, step.Selector, step.Label, step.Anchors, waitTimeout)
			if err != nil {
				return page, err
			}
			target = t
		} else if a := strings.TrimSpace(step.Label); len(a) >= 2 {
			target = page.resolveClickTarget(ctx, step.Frame, step.Selector, a, waitTimeout)
		} else if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return page, err
		}
		// Follow a new tab the click may open (target=_blank / SPA window.open).
		np, err := clickTarget(ctx, b, page, target)
		if err != nil {
			return page, err
		}
		page = np
	case "type":
		// Anchored steps resolve by fingerprint (a field with no accessible name
		// still has its neighbor label); legacy steps re-locate by the accessible-
		// name hint when the positional selector drifted.
		if step.Anchors != nil {
			t, err := page.resolveAnchoredTarget(ctx, step.Frame, step.Selector, step.Label, step.Anchors, waitTimeout)
			if err != nil {
				return page, err
			}
			target = t
		} else if h := strings.TrimSpace(step.Hint); h != "" {
			target = page.resolveFieldTarget(ctx, step.Frame, step.Selector, h, waitTimeout)
		} else if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return page, err
		}
		val := subst(step.Value, params)
		if err := page.TypeText(ctx, target, val); err != nil {
			return page, err
		}
		// If a non-empty value didn't land, a disabled/re-rendering/framework input
		// swallowed the programmatic insert — clear and retry once before failing
		// into the healer. Checks non-empty (not exact match) so masked/formatted
		// inputs that transform the value aren't wrongly flagged.
		if val != "" && !page.fieldNonEmpty(ctx, target) {
			page.clearField(ctx, target)
			if err := page.TypeText(ctx, target, val); err != nil {
				return page, err
			}
			if !page.fieldNonEmpty(ctx, target) {
				return page, fmt.Errorf("type: %q did not accept input", target)
			}
		}
	case "key":
		// A recorded Enter in a text input (form submit / autocomplete confirm) —
		// the only key recorded today. Page.Key dispatches to the focused element,
		// so focus the field with a real (trusted) click first.
		if step.Anchors != nil {
			t, err := page.resolveAnchoredTarget(ctx, step.Frame, step.Selector, step.Label, step.Anchors, waitTimeout)
			if err != nil {
				return page, err
			}
			target = t
		} else if h := strings.TrimSpace(step.Hint); h != "" {
			target = page.resolveFieldTarget(ctx, step.Frame, step.Selector, h, waitTimeout)
		} else if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return page, err
		}
		key := strings.ToLower(strings.TrimSpace(subst(step.Value, params)))
		if key != "enter" {
			return page, fmt.Errorf("key: unsupported key %q (only enter is recorded)", step.Value)
		}
		if err := page.Click(ctx, target); err != nil {
			return page, err
		}
		if err := page.Key(ctx, key); err != nil {
			return page, err
		}
	case "select":
		if step.Anchors != nil {
			t, err := page.resolveAnchoredTarget(ctx, step.Frame, step.Selector, step.Label, step.Anchors, waitTimeout)
			if err != nil {
				return page, err
			}
			target = t
		} else if h := strings.TrimSpace(step.Hint); h != "" {
			target = page.resolveFieldTarget(ctx, step.Frame, step.Selector, h, waitTimeout)
		} else if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return page, err
		}
		if err := page.SelectOption(ctx, target, subst(step.Value, params)); err != nil {
			return page, err
		}
	case "upload":
		// The upload trigger is a labeled control (e.g. "Choose file"), so prefer
		// its visible text the same way clicks do, then its field hint, before
		// falling back to the positional selector (→ healer).
		if step.Anchors != nil {
			t, err := page.resolveAnchoredTarget(ctx, step.Frame, step.Selector, step.Label, step.Anchors, waitTimeout)
			if err != nil {
				return page, err
			}
			target = t
		} else if a := strings.TrimSpace(step.Label); len(a) >= 2 {
			target = page.resolveClickTarget(ctx, step.Frame, step.Selector, a, waitTimeout)
		} else if h := strings.TrimSpace(step.Hint); h != "" {
			target = page.resolveFieldTarget(ctx, step.Frame, step.Selector, h, waitTimeout)
		} else if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return page, err
		}
		// Click the upload control and feed the file through the chooser — works
		// for both a button that opens a chooser and a direct file input.
		if err := page.UploadViaChooser(ctx, target, []string{subst(step.Value, params)}); err != nil {
			return page, err
		}
	case "download":
		// Capture the file the trigger produces (client-side-generated files never
		// appear as a plain HTTP response — only what lands on disk is the file).
		// Resolve the trigger like a click (text/label first) so a drifted export
		// button still fires. Unlike click this uses a plain page.Click, not
		// clickTarget: a download trigger doesn't navigate, so there's no new tab
		// to follow. Verify BEFORE bind so a verify-triggered retry (recoverStep
		// re-runs the whole step) doesn't append the file to the output twice.
		if b == nil {
			return page, fmt.Errorf("download: no browser session")
		}
		if downloadDir == "" {
			return page, fmt.Errorf("download: no download directory configured")
		}
		if step.Anchors != nil {
			t, err := page.resolveAnchoredTarget(ctx, step.Frame, step.Selector, step.Label, step.Anchors, waitTimeout)
			if err != nil {
				return page, err
			}
			target = t
		} else if a := strings.TrimSpace(step.Label); len(a) >= 2 {
			target = page.resolveClickTarget(ctx, step.Frame, step.Selector, a, waitTimeout)
		} else if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return page, err
		}
		path, err := b.CaptureDownload(ctx, downloadDir, func() error { return page.Click(ctx, target) })
		if err != nil {
			return page, err
		}
		if err := verify(ctx, page, step, params); err != nil {
			return page, err
		}
		bind(binds, step.Bind, path)
		return page, nil
	case "extract":
		// Read a value off the page (a report id, a status) and bind it — the
		// scalar analogue of a download for feeding a downstream step. A JSON string
		// result is unwrapped to its text; anything else is kept as its JSON form.
		// Verify before bind for the same no-double-bind reason as download.
		if strings.TrimSpace(step.JS) == "" {
			return page, fmt.Errorf("extract: js is required")
		}
		var raw json.RawMessage
		if err := page.Eval(ctx, substJS(step.JS, params), &raw); err != nil {
			return page, err
		}
		val := string(raw)
		var s string
		if json.Unmarshal(raw, &s) == nil {
			val = s
		}
		if err := verify(ctx, page, step, params); err != nil {
			return page, err
		}
		bind(binds, step.Bind, val)
		return page, nil
	default:
		return page, fmt.Errorf("unknown action %q", step.Action)
	}
	return page, verify(ctx, page, step, params)
}

// bind appends a produced value under name, tolerating a nil map (a direct
// runStep caller that doesn't collect outputs) and an empty name (an unbound
// download/extract step, which just discards its value).
func bind(binds map[string][]string, name, value string) {
	if binds == nil || name == "" {
		return
	}
	binds[name] = append(binds[name], value)
}

// clickTarget clicks target on page, following a new tab the click opens when a
// Browser is available (replay/tools path). Without a Browser it is a plain
// click on the same page.
func clickTarget(ctx context.Context, b *Browser, page *Page, target string) (*Page, error) {
	if b != nil {
		return b.ClickFollow(ctx, page, target)
	}
	if err := page.Click(ctx, target); err != nil {
		return page, err
	}
	return page, nil
}

func verify(ctx context.Context, page *Page, step *Step, params map[string]string) error {
	if step.Verify == nil {
		return nil
	}
	if step.Verify.Exists != "" {
		sel := step.Verify.Exists
		if step.Frame != "" {
			sel = step.Frame + frameDelim + sel
		}
		if err := page.WaitFor(ctx, sel, 10*time.Second); err != nil {
			return fmt.Errorf("verify exists %q: %w", step.Verify.Exists, err)
		}
	}
	if want := subst(step.Verify.Text, params); want != "" {
		deadline := time.Now().Add(10 * time.Second)
		expr := fmt.Sprintf("(document.body.innerText||'').indexOf(%s) >= 0", jsString(want))
		for {
			var ok bool
			if err := page.Eval(ctx, expr, &ok); err != nil {
				return err
			}
			if ok {
				return nil
			}
			if time.Now().After(deadline) {
				// Report the unsubstituted field: it names the param
				// ({{password}}) without carrying the resolved value, which
				// may be a secret bound for the page only.
				return fmt.Errorf("verify text %q not found", step.Verify.Text)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	if want := subst(step.Verify.URL, params); want != "" {
		// Poll: a redirect (e.g. to a login/error host) may resolve a beat after
		// the load event. A bare host (no '/', the auto-generated form) is matched
		// against location's actual host, so a bounce to a different host whose URL
		// merely echoes ours in a query param (…/login?return_to=ourhost) doesn't
		// falsely pass; a hand-authored fragment containing '/' falls back to a
		// scheme-agnostic href substring. Either form ignores an http→https upgrade.
		deadline := time.Now().Add(10 * time.Second)
		var expr string
		if strings.Contains(want, "/") {
			expr = fmt.Sprintf("(location.href||'').indexOf(%s) >= 0", jsString(want))
		} else {
			expr = fmt.Sprintf("(function(){try{return new URL(location.href).host===%s}catch(e){return (location.href||'').indexOf(%s)>=0}})()", jsString(want), jsString(want))
		}
		for {
			var ok bool
			if err := page.Eval(ctx, expr, &ok); err != nil {
				return err
			}
			if ok {
				return nil
			}
			if time.Now().After(deadline) {
				var cur string
				_ = page.Eval(ctx, "location.href", &cur)
				// Report the unsubstituted field (param name, not the resolved
				// value, which may be a secret). cur is live page state, not a
				// param, so it stays.
				return fmt.Errorf("verify url: expected to stay on %q, but landed on %q", step.Verify.URL, cur)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return nil
}
