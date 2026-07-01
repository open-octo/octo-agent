package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// FileConfig is the on-disk hooks.yml schema. It maps each event to a list of
// hooks, so an event can fan out to several commands (unlike the env shim's one
// command per event). The user-level file lives at ~/.octo/hooks.yml; a
// project-level <cwd>/.octo/hooks.yml layers on top (later phase).
//
//	hooks:
//	  UserPromptSubmit:
//	    - command: "hindsight-retrieve"
//	      timeout: 5s
//	  PostToolUse:
//	    - matcher: "terminal|write_file"   # regexp over the tool name
//	      command: "audit-logger"
type FileConfig struct {
	Hooks map[string][]HookSpec `yaml:"hooks"`
}

// HookSpec is one configured hook. Matcher is a regexp over the tool name,
// honoured only for PreToolUse/PostToolUse. Timeout is a Go duration string
// ("5s"); empty uses the package default.
type HookSpec struct {
	Command string `yaml:"command"`
	Matcher string `yaml:"matcher,omitempty"`
	Timeout string `yaml:"timeout,omitempty"`
}

// UserConfigPath returns ~/.octo/hooks.yml, or "" when the home dir is
// unavailable.
func UserConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".octo", "hooks.yml")
}

// LoadFileConfig reads and parses a hooks.yml. A missing file returns an error
// satisfying os.IsNotExist, which callers treat as "no config" rather than a
// failure.
func LoadFileConfig(path string) (FileConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}
	var fc FileConfig
	if err := yaml.Unmarshal(b, &fc); err != nil {
		return FileConfig{}, fmt.Errorf("hooks: parse %s: %w", path, err)
	}
	return fc, nil
}

// LoadConfig registers every hook in fc on the engine, appending to whatever is
// already registered (env shim, a prior file). A hook is validated as it lands:
// an unknown event name or a bad matcher regexp is a hard error naming the
// offending entry, so a typo surfaces instead of silently doing nothing.
func (e *Engine) LoadConfig(fc FileConfig) error {
	if e == nil {
		return nil
	}
	for name, specs := range fc.Hooks {
		ev := Event(name)
		if !ev.valid() {
			return fmt.Errorf("hooks: unknown event %q in hooks.yml", name)
		}
		for _, s := range specs {
			if err := e.RegisterShellMatched(ev, s.Command, s.Matcher, parseTimeout(s.Timeout)); err != nil {
				return fmt.Errorf("hooks: %s: invalid matcher %q: %w", name, s.Matcher, err)
			}
		}
	}
	return nil
}

// parseTimeout turns a duration string into a Duration, returning 0 (→ package
// default, applied downstream) for empty or unparseable input.
func parseTimeout(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// EngineFromEnvAndFiles builds the production engine: the OCTO_HOOK_* env shim
// plus the user-level ~/.octo/hooks.yml layered on top (append semantics). A
// missing file is fine; a malformed file or a bad entry is surfaced via Notify
// and otherwise ignored, so one broken hook never blocks the session.
func EngineFromEnvAndFiles(seen *SeenSet) *Engine {
	e := EngineFromEnv(seen)
	if p := UserConfigPath(); p != "" {
		switch fc, err := LoadFileConfig(p); {
		case err == nil:
			if cerr := e.LoadConfig(fc); cerr != nil {
				e.notify(cerr.Error())
			}
		case !os.IsNotExist(err):
			e.notify(err.Error())
		}
	}
	return e
}
