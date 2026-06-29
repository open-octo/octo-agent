package browser

import (
	"context"
	"fmt"
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
	Action   string  `yaml:"action"` // navigate | click | type | select | upload
	URL      string  `yaml:"url,omitempty"`
	Frame    string  `yaml:"frame,omitempty"`
	Selector string  `yaml:"selector,omitempty"`
	Value    string  `yaml:"value,omitempty"`
	Label    string  `yaml:"label,omitempty"`
	Verify   *Verify `yaml:"verify,omitempty"`
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

// CompileSkill turns a recording into an editable Skill. The first step
// navigates to the start URL; subsequent steps come from the captured events.
func CompileSkill(name, description, startURL string, events []RecordedEvent) Skill {
	s := Skill{Name: name, Description: description}
	if startURL != "" {
		s.Steps = append(s.Steps, Step{Action: "navigate", URL: startURL})
	}
	for _, e := range events {
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
	return s
}

// MarshalSkill renders the skill to YAML.
func MarshalSkill(s Skill) ([]byte, error) { return yaml.Marshal(s) }

// ParseSkill parses a skill from YAML.
func ParseSkill(data []byte) (Skill, error) {
	var s Skill
	err := yaml.Unmarshal(data, &s)
	return s, err
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
// set, is consulted on a step failure.
type ReplayOptions struct {
	StepTimeout time.Duration
	Healer      Healer
}

// ReplaySkill runs a skill deterministically (no LLM), substituting params. Each
// step implicitly waits for its target to appear (handling slow loads) and
// checks any explicit Verify. On a step failure it calls the healer; if the
// healer repairs the step, replay continues and reports modified=true so the
// caller can write the corrected skill back.
func ReplaySkill(ctx context.Context, page *Page, skill *Skill, params map[string]string, opts ReplayOptions) (modified bool, err error) {
	if opts.StepTimeout <= 0 {
		opts.StepTimeout = 15 * time.Second
	}
	full := mergedParams(skill, params)
	for i := range skill.Steps {
		runErr := runStep(ctx, page, &skill.Steps[i], full, opts.StepTimeout)
		if runErr == nil {
			continue
		}
		if opts.Healer == nil {
			return modified, fmt.Errorf("step %d (%s): %w", i+1, skill.Steps[i].Action, runErr)
		}
		before := skill.Steps[i]
		if herr := opts.Healer(ctx, page, &skill.Steps[i], runErr); herr != nil {
			return modified, fmt.Errorf("step %d (%s): %w", i+1, skill.Steps[i].Action, runErr)
		}
		if retryErr := runStep(ctx, page, &skill.Steps[i], full, opts.StepTimeout); retryErr != nil {
			return modified, fmt.Errorf("step %d (%s) after heal: %w", i+1, skill.Steps[i].Action, retryErr)
		}
		if skill.Steps[i] != before {
			modified = true
		}
	}
	return modified, nil
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

func runStep(ctx context.Context, page *Page, step *Step, params map[string]string, waitTimeout time.Duration) error {
	target := step.target()
	switch step.Action {
	case "navigate":
		if err := page.Navigate(ctx, subst(step.URL, params)); err != nil {
			return err
		}
	case "click":
		if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return err
		}
		if err := page.Click(ctx, target); err != nil {
			return err
		}
	case "type":
		if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return err
		}
		if err := page.TypeText(ctx, target, subst(step.Value, params)); err != nil {
			return err
		}
	case "select":
		if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return err
		}
		if err := page.SelectOption(ctx, target, subst(step.Value, params)); err != nil {
			return err
		}
	case "upload":
		if err := page.WaitFor(ctx, target, waitTimeout); err != nil {
			return err
		}
		if err := page.Upload(ctx, target, []string{subst(step.Value, params)}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown action %q", step.Action)
	}
	return verify(ctx, page, step, params)
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
