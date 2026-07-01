package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Skill is a recorded browser workflow in its editable, replayable form. It
// serializes to YAML — human-readable, hand-editable, git-versionable — which is
// the "editable steps" surface. Replay reads it back; self-heal writes it back.
type Skill struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Params      []Param  `yaml:"params,omitempty"`
	Outputs     []Output `yaml:"outputs,omitempty"`
	Steps       []Step   `yaml:"steps"`
}

// Param is a replay-time input; {{name}} placeholders in step values/urls are
// substituted from it (falling back to Default).
type Param struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Default     string `yaml:"default,omitempty"`
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
	Action   string `yaml:"action"` // navigate | click | type | select | upload | wait | download | extract
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
	Hint      string  `yaml:"hint,omitempty"`
	Bind      string  `yaml:"bind,omitempty"`       // download/extract: write the produced value into this named Output
	JS        string  `yaml:"js,omitempty"`         // extract: expression whose value is bound
	Network   bool    `yaml:"network,omitempty"`    // wait: settle for network (fetch/XHR) idle instead of a fixed delay
	TimeoutMS int     `yaml:"timeout_ms,omitempty"` // wait: fixed delay (ms) when no selector; also caps the network-idle settle
	Verify    *Verify `yaml:"verify,omitempty"`
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

// CompileSkill turns a recording into an editable Skill. It seeds a leading
// navigate from the start URL (when it's a real page, not the about:blank tab
// octo opens) and then walks the captured events in order — including the
// top-level navigations the recorder captured, so a multi-page demonstration
// replays from the right pages.
func CompileSkill(name, description, startURL string, events []RecordedEvent) Skill {
	// Collapse consecutive identical raw events (a jittery double-fire / re-
	// dispatched event) BEFORE parameterization. dropConsecutiveDupeSteps can't
	// catch a repeated `type` afterwards, because each gets a distinct {{param}}
	// placeholder as its value — so a spurious extra param+step would survive.
	events = dedupeConsecutiveEvents(events)
	s := Skill{Name: name, Description: description}
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
		p := Param{Name: nm, Description: desc}
		if !secret {
			p.Default = def
		}
		s.Params = append(s.Params, p)
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
			fp := addParam("file", "", "path to the file to upload", true)
			up := Step{Action: "upload", Frame: e.Frame, Selector: e.Selector, Value: "{{" + fp + "}}", Label: e.Text}
			if n := len(s.Steps); n > 0 && s.Steps[n-1].Action == "click" {
				up.Selector, up.Frame, up.Label = s.Steps[n-1].Selector, s.Steps[n-1].Frame, s.Steps[n-1].Label
				s.Steps = s.Steps[:n-1]
			}
			s.Steps = append(s.Steps, up)
			continue
		}
		if e.Selector == "" {
			continue
		}
		st := Step{Frame: e.Frame, Selector: e.Selector, Label: e.Text, Hint: e.Field}
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
		default:
			continue
		}
		s.Steps = append(s.Steps, st)
	}
	s.Steps = dropConsecutiveDupeSteps(s.Steps)
	return s
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

// dedupeConsecutiveEvents drops a raw event identical to its immediate
// predecessor (same type/selector/frame/value/tag) — a double-fire or a
// framework's re-dispatched event. navigate is exempt (CompileSkill collapses
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

// SkillGenerator asks an LLM to refine a recording into a clean skill. It is a
// plain string→string call so this package needn't import the agent/provider
// layers; the app wires a sender-backed implementation.
type SkillGenerator func(ctx context.Context, system, user string) (string, error)

// GenerateSkill turns a recording into a skill. The deterministic CompileSkill
// is always the baseline (its selectors are ground truth). When gen is set, the
// LLM refines that baseline — dropping detours/retries, parameterizing variable
// inputs, labeling — but is constrained to the captured selectors; any output
// that fails to parse or invents a selector falls back to the baseline. So the
// LLM only ever cleans up real events, never hallucinates targets.
func GenerateSkill(ctx context.Context, name, startURL string, events []RecordedEvent, gen SkillGenerator) Skill {
	base := CompileSkill(name, "", startURL, events)
	if gen == nil {
		return base
	}
	baseYAML, err := MarshalSkill(base)
	if err != nil {
		return base
	}
	const system = "You clean a recorded browser workflow into a minimal, correct, replayable skill. " +
		"RULES: (1) Use ONLY CSS selectors that appear in the provided baseline — never invent or alter a selector. " +
		"(2) Drop redundant back-and-forth and retries; keep the intended linear path. " +
		"(3) Replace user-specific input values with {{param}} and declare each in params (keep upload's {{file}}). " +
		"(4) Preserve step order and all navigate steps. " +
		"(5) Write description as a short statement of what the workflow does. " +
		"Output ONLY the skill as YAML (keys: name, description, params, steps), no prose, no code fences."
	user := fmt.Sprintf("Baseline (the only valid selectors are those here):\n%s\n\nRaw events in order:\n%s\n\nReturn the cleaned skill YAML.", baseYAML, renderTrace(events))

	out, err := gen(ctx, system, user)
	if err != nil {
		return base
	}
	refined, err := ParseSkill([]byte(stripFences(out)))
	if err != nil || len(refined.Steps) == 0 {
		return base
	}
	refined.Name = name
	if !selectorsSubset(refined, base) {
		return base // precision guard: the model used a selector it wasn't given
	}
	return refined
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

// selectorsSubset reports whether every selector/frame the refined skill uses
// was present in the baseline (the captured ground truth).
func selectorsSubset(refined, base Skill) bool {
	allowed := map[string]bool{"": true}
	for _, st := range base.Steps {
		allowed[st.Selector] = true
		allowed[st.Frame] = true
	}
	for _, st := range refined.Steps {
		if !allowed[st.Selector] || !allowed[st.Frame] {
			return false
		}
	}
	return true
}

// MarshalSkill renders the skill to YAML.
func MarshalSkill(s Skill) ([]byte, error) { return yaml.Marshal(s) }

// ParseSkill parses a skill from YAML.
func ParseSkill(data []byte) (Skill, error) {
	var s Skill
	err := yaml.Unmarshal(data, &s)
	return s, err
}

// SaveSkill writes a skill to a YAML file.
func SaveSkill(path string, s Skill) error {
	data, err := MarshalSkill(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadSkill reads a skill from a YAML file.
func LoadSkill(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	return ParseSkill(data)
}

// ListSkills reads every *.yaml recording in dir and returns them sorted by
// name. A missing dir yields nil; an unreadable or unparseable file (a
// half-written or hand-broken skill) is skipped rather than sinking the whole
// list — this feeds the system-prompt manifest, which must stay robust.
func ListSkills(dir string) []Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		s, err := LoadSkill(filepath.Join(dir, e.Name()))
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
	  for(var i=0;i<els.length && out.length<%d;i++){var el=els[i]; if(el.offsetParent===null) continue; var t=(el.textContent||el.value||el.getAttribute('aria-label')||el.getAttribute('placeholder')||'').trim().slice(0,50); out.push({text:t, selector:sel(el)});}
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

func subst(s string, params map[string]string) string {
	for k, v := range params {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// ReplayOptions tunes a replay. StepTimeout bounds the per-step wait for a
// target to appear (default 15s — generous for slow back-ends). Healer, when
// set, is consulted on a step failure. Browser, when set, lets a click follow a
// new tab it opens (target=_blank / window.open). DownloadDir is where download
// steps land their files (required only if the skill has download steps).
type ReplayOptions struct {
	StepTimeout time.Duration
	Healer      Healer
	Browser     *Browser
	DownloadDir string
}

// ReplaySkill runs a skill deterministically (no LLM), substituting params. Each
// step implicitly waits for its target to appear (handling slow loads) and
// checks any explicit Verify. On a step failure it calls the healer; if the
// healer repairs the step, replay continues and reports modified=true so the
// caller can write the corrected skill back. A click that opens a new tab swaps
// the active page for subsequent steps; finalPage is the page replay ended on
// (so the caller can keep its session pointed at the right tab). outputs holds
// the skill's declared Outputs as bound during replay (download paths, extracted
// values) — the handoff surface for composing this recording with later steps.
func ReplaySkill(ctx context.Context, page *Page, skill *Skill, params map[string]string, opts ReplayOptions) (modified bool, finalPage *Page, outputs map[string]any, err error) {
	if opts.StepTimeout <= 0 {
		opts.StepTimeout = 15 * time.Second
	}
	full := mergedParams(skill, params)
	binds := map[string][]string{}
	cur := page
	for i := range skill.Steps {
		np, runErr := runStep(ctx, opts.Browser, cur, &skill.Steps[i], full, opts.StepTimeout, opts.DownloadDir, binds)
		if runErr == nil {
			cur = np
			continue
		}
		np, healed, runErr := recoverStep(ctx, opts, cur, &skill.Steps[i], full, runErr, &modified, binds)
		if runErr != nil {
			return modified, cur, nil, fmt.Errorf("step %d (%s): %w", i+1, skill.Steps[i].Action, runErr)
		}
		_ = healed
		cur = np
	}
	return modified, cur, assembleOutputs(skill.Outputs, binds), nil
}

// assembleOutputs turns the ordered values bound during replay into the skill's
// declared outputs. A "file[]" output yields the full ordered slice (empty slice
// if never bound); every other type yields the last bound value ("" if never
// bound). Only declared outputs surface — a stray bind to an undeclared name is
// ignored, keeping the handoff contract exactly what the skill promised.
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
		if np, err := runStep(ctx, opts.Browser, page, step, params, opts.StepTimeout, opts.DownloadDir, binds); err == nil {
			return np, true, nil
		}
	}
	if opts.Healer == nil {
		return page, false, cause
	}
	// 2. LLM healer, multi-round.
	for round := 0; round < maxHealRounds; round++ {
		before := *step
		if herr := opts.Healer(ctx, page, step, cause); herr != nil {
			return page, false, cause
		}
		np, retryErr := runStep(ctx, opts.Browser, page, step, params, opts.StepTimeout, opts.DownloadDir, binds)
		if retryErr == nil {
			if *step != before {
				*modified = true
			}
			return np, true, nil
		}
		if *step == before {
			return page, false, retryErr // healer made no change — further rounds won't help
		}
		cause = retryErr // feed the fresh failure to the next round
	}
	return page, false, cause
}

// mergedParams overlays caller params on declared defaults.
func mergedParams(skill *Skill, params map[string]string) map[string]string {
	out := map[string]string{}
	for _, p := range skill.Params {
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
		if err := page.Navigate(ctx, subst(step.URL, params)); err != nil {
			return page, err
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
		// When the recording captured the element's visible text, resolve by text
		// (verifying or replacing the drift-prone positional selector) — this is
		// what makes replay survive layout changes and stops silent wrong-element
		// clicks. resolveClickTarget already polled for existence.
		if a := strings.TrimSpace(step.Label); len(a) >= 2 {
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
		// Re-locate the field by its accessible-name hint when the positional
		// selector drifted (the type/select analogue of a text-anchored click).
		if h := strings.TrimSpace(step.Hint); h != "" {
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
	case "select":
		if h := strings.TrimSpace(step.Hint); h != "" {
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
		if a := strings.TrimSpace(step.Label); len(a) >= 2 {
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
		if a := strings.TrimSpace(step.Label); len(a) >= 2 {
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
		if err := page.Eval(ctx, subst(step.JS, params), &raw); err != nil {
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
				return fmt.Errorf("verify text %q not found", want)
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
				return fmt.Errorf("verify url: expected to stay on %q, but landed on %q", want, cur)
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
