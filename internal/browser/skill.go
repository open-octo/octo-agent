package browser

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Skill is a recorded browser workflow in its editable, replayable form. It
// serializes to YAML — human-readable, hand-editable, git-versionable — which is
// the "editable steps" surface. Replay reads it back; self-heal writes it back.
type Skill struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description,omitempty"`
	Params      []Param `yaml:"params,omitempty"`
	Steps       []Step  `yaml:"steps"`
}

// Param is a replay-time input; {{name}} placeholders in step values/urls are
// substituted from it (falling back to Default).
type Param struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Default     string `yaml:"default,omitempty"`
}

// Step is one action. Selector is within its document; Frame (a same-origin
// iframe selector) scopes it via the " >>> " convention. Label is a human note;
// replay ignores it.
type Step struct {
	Action    string  `yaml:"action"` // navigate | click | type | select | upload | wait
	URL       string  `yaml:"url,omitempty"`
	Frame     string  `yaml:"frame,omitempty"`
	Selector  string  `yaml:"selector,omitempty"`
	Value     string  `yaml:"value,omitempty"`
	Label     string  `yaml:"label,omitempty"`
	TimeoutMS int     `yaml:"timeout_ms,omitempty"` // wait: fixed delay when no selector
	Verify    *Verify `yaml:"verify,omitempty"`
}

// Verify is an optional post-step assertion. Exists waits for a selector; Text
// waits for body text to contain a string. Empty means the implicit check only
// (the step's own target became present).
type Verify struct {
	Exists string `yaml:"exists,omitempty"`
	Text   string `yaml:"text,omitempty"`
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
	s := Skill{Name: name, Description: description}
	if startURL != "" && startURL != "about:blank" {
		s.Steps = append(s.Steps, Step{Action: "navigate", URL: startURL})
	}
	hasUpload := false
	for _, e := range events {
		if e.Type == "navigate" {
			// Skip an echo of the page we're already on (start URL or the prior nav).
			if n := len(s.Steps); n > 0 && s.Steps[n-1].Action == "navigate" && s.Steps[n-1].URL == e.URL {
				continue
			}
			s.Steps = append(s.Steps, Step{Action: "navigate", URL: e.URL})
			continue
		}
		if e.Type == "upload" {
			// The user clicked an upload control, then picked a file. Replay
			// clicks the control and feeds the file through the chooser, so the
			// preceding click (the button) is the better trigger than the
			// possibly-transient file input. The file itself can't be captured
			// (browsers hide the path) so it's auto-parameterized.
			up := Step{Action: "upload", Frame: e.Frame, Selector: e.Selector, Value: "{{file}}", Label: e.Text}
			if n := len(s.Steps); n > 0 && s.Steps[n-1].Action == "click" {
				up.Selector, up.Frame, up.Label = s.Steps[n-1].Selector, s.Steps[n-1].Frame, s.Steps[n-1].Label
				s.Steps = s.Steps[:n-1]
			}
			s.Steps = append(s.Steps, up)
			hasUpload = true
			continue
		}
		if e.Selector == "" {
			continue
		}
		st := Step{Frame: e.Frame, Selector: e.Selector, Label: e.Text}
		switch {
		case e.Type == "click":
			st.Action = "click"
		case e.Type == "change" && e.Tag == "SELECT":
			st.Action = "select"
			st.Value = e.Value
		case e.Type == "change":
			st.Action = "type"
			st.Value = e.Value
		default:
			continue
		}
		s.Steps = append(s.Steps, st)
	}
	if hasUpload {
		s.Params = append(s.Params, Param{Name: "file", Description: "path to the file to upload"})
	}
	s.Steps = dropConsecutiveDupeSteps(s.Steps)
	return s
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
		"(5) Write description as a short statement of what the workflow does, then the natural phrases a user would say to invoke it — in the page's own language AND English (e.g. \"打开知乎热榜并点第一条。当用户说\\\"知乎热榜\\\"、\\\"zhihu hot\\\"时触发\"). This description is the auto-trigger cue, so make those phrases concrete. " +
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
		fmt.Fprintf(&sb, "%d. %s selector=%q frame=%q tag=%s text=%q value=%q\n", i+1, e.Type, e.Selector, e.Frame, e.Tag, e.Text, e.Value)
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
// new tab it opens (target=_blank / window.open).
type ReplayOptions struct {
	StepTimeout time.Duration
	Healer      Healer
	Browser     *Browser
}

// ReplaySkill runs a skill deterministically (no LLM), substituting params. Each
// step implicitly waits for its target to appear (handling slow loads) and
// checks any explicit Verify. On a step failure it calls the healer; if the
// healer repairs the step, replay continues and reports modified=true so the
// caller can write the corrected skill back. A click that opens a new tab swaps
// the active page for subsequent steps; finalPage is the page replay ended on
// (so the caller can keep its session pointed at the right tab).
func ReplaySkill(ctx context.Context, page *Page, skill *Skill, params map[string]string, opts ReplayOptions) (modified bool, finalPage *Page, err error) {
	if opts.StepTimeout <= 0 {
		opts.StepTimeout = 15 * time.Second
	}
	full := mergedParams(skill, params)
	cur := page
	for i := range skill.Steps {
		np, runErr := runStep(ctx, opts.Browser, cur, &skill.Steps[i], full, opts.StepTimeout)
		if runErr == nil {
			cur = np
			continue
		}
		if opts.Healer == nil {
			return modified, cur, fmt.Errorf("step %d (%s): %w", i+1, skill.Steps[i].Action, runErr)
		}
		before := skill.Steps[i]
		if herr := opts.Healer(ctx, cur, &skill.Steps[i], runErr); herr != nil {
			return modified, cur, fmt.Errorf("step %d (%s): %w", i+1, skill.Steps[i].Action, runErr)
		}
		np, retryErr := runStep(ctx, opts.Browser, cur, &skill.Steps[i], full, opts.StepTimeout)
		if retryErr != nil {
			return modified, cur, fmt.Errorf("step %d (%s) after heal: %w", i+1, skill.Steps[i].Action, retryErr)
		}
		cur = np
		if skill.Steps[i] != before {
			modified = true
		}
	}
	return modified, cur, nil
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
func runStep(ctx context.Context, b *Browser, page *Page, step *Step, params map[string]string, waitTimeout time.Duration) (*Page, error) {
	target := step.target()
	switch step.Action {
	case "navigate":
		if err := page.Navigate(ctx, subst(step.URL, params)); err != nil {
			return page, err
		}
	case "wait":
		// Wait for an element if a selector is given (the robust form), else a
		// fixed delay — the natural primitive for letting an SPA settle between
		// steps. Recordings/edits commonly need it; without it run_skill rejected
		// the step as "unknown action".
		if target != "" {
			if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
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
		if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
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
		if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return page, err
		}
		if err := page.SelectOption(ctx, target, subst(step.Value, params)); err != nil {
			return page, err
		}
	case "upload":
		// The upload trigger is a labeled control (e.g. "Choose file"), so prefer
		// its text the same way clicks do.
		if a := strings.TrimSpace(step.Label); len(a) >= 2 {
			target = page.resolveClickTarget(ctx, step.Frame, step.Selector, a, waitTimeout)
		} else if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return page, err
		}
		// Click the upload control and feed the file through the chooser — works
		// for both a button that opens a chooser and a direct file input.
		if err := page.UploadViaChooser(ctx, target, []string{subst(step.Value, params)}); err != nil {
			return page, err
		}
	default:
		return page, fmt.Errorf("unknown action %q", step.Action)
	}
	return page, verify(ctx, page, step, params)
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
	return nil
}
