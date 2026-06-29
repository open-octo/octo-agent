// Package protean bridges octo to a local Protean installation for skill
// recording, generation, and deterministic execution.
//
// Protean skills are stored separately from octo skills
// (default ~/.octo/protean-skills) because they use a different on-disk format
// (Agent Skills spec with YAML frontmatter and structured steps). The bridge
// does not try to make them look like octo skills; instead it exposes a single
// run_protean_skill tool that executes them through Protean's step_by_step
// executor, and HTTP handlers that drive record/generate from the web UI.
package protean

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Leihb/octo-agent/internal/config"
)

const (
	defaultRelVenv       = ".octo/protean/.venv"
	defaultRelSkills     = ".octo/protean-skills"
	defaultRelRecordings = ".octo/protean-recordings"
)

// Bridge resolves Protean paths and runs Python commands against the venv.
type Bridge struct {
	cfg config.ProteanConfig
}

// NewBridge builds a Bridge from persisted config, filling in defaults.
func NewBridge(cfg config.ProteanConfig) *Bridge {
	return &Bridge{cfg: cfg}
}

// Venv returns the absolute path to the Protean virtual environment.
func (b *Bridge) Venv() string {
	if b.cfg.VenvPath != "" {
		return b.resolveHome(b.cfg.VenvPath)
	}
	if v := os.Getenv("PROTEAN_VENV"); v != "" {
		return b.resolveHome(v)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, defaultRelVenv)
}

// SkillsDir returns the absolute Protean skills directory.
func (b *Bridge) SkillsDir() string {
	if b.cfg.SkillsDir != "" {
		return b.resolveHome(b.cfg.SkillsDir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, defaultRelSkills)
}

// RecordingsRoot returns the directory that holds all recording sessions.
func (b *Bridge) RecordingsRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, defaultRelRecordings)
}

// RecordingsDir returns a fresh recording directory under the default root.
func (b *Bridge) RecordingsDir() string {
	return filepath.Join(b.RecordingsRoot(), time.Now().Format("20060102-150405"))
}

// resolveHome expands a leading ~ to the user's home directory.
func (b *Bridge) resolveHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Python returns the absolute path to the Python interpreter inside the venv.
func (b *Bridge) Python() string {
	venv := b.Venv()
	if runtime.GOOS == "windows" {
		return filepath.Join(venv, "Scripts", "python.exe")
	}
	return filepath.Join(venv, "bin", "python")
}

// availTTL bounds how long an availability result is reused. Long enough to
// avoid spawning Python on every tool-list build (once per turn), short enough
// that a freshly installed Protean is picked up without a restart.
const availTTL = 30 * time.Second

var availCache sync.Map // python path -> availEntry

type availEntry struct {
	ok      bool
	checked time.Time
}

// Available reports whether the Protean venv and main package look usable.
// The result is cached per Python path for availTTL because the check spawns a
// Python subprocess (`import protean`), and the tool gate calls it every turn.
func (b *Bridge) Available() bool {
	py := b.Python()
	if e, ok := availCache.Load(py); ok {
		if ent := e.(availEntry); time.Since(ent.checked) < availTTL {
			return ent.ok
		}
	}
	ok := b.checkAvailable(py)
	availCache.Store(py, availEntry{ok: ok, checked: time.Now()})
	return ok
}

func (b *Bridge) checkAvailable(py string) bool {
	if _, err := os.Stat(py); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := b.command(ctx, "-c", "import protean")
	return cmd.Run() == nil
}

// command builds an exec.Cmd that runs Python in the Protean venv with the
// environment variables Protean expects.
func (b *Bridge) command(ctx context.Context, args ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if ctx != nil {
		cmd = exec.CommandContext(ctx, b.Python(), args...)
	} else {
		cmd = exec.Command(b.Python(), args...)
	}
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "PROTEAN_SKILLS_DIR="+b.SkillsDir())
	return cmd
}

// GenerateResult holds the output of skill generation.
type GenerateResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SkillDir    string `json:"skill_dir"`
	Error       string `json:"error,omitempty"`
}

// Generate synthesizes a skill from a recording directory using the supplied
// sender (typically app.NewSender) and model name. This lets skill generation
// reuse octo's configured model instead of requiring a separate Protean model
// setup.
func (b *Bridge) Generate(ctx context.Context, recordingDir, taskDesc, modelName string, sender Sender) (*GenerateResult, error) {
	if err := os.MkdirAll(b.SkillsDir(), 0o700); err != nil {
		return nil, fmt.Errorf("create skills dir: %w", err)
	}

	info, err := GenerateSkill(ctx, GenerateOptions{
		RecordingDir: recordingDir,
		SkillsDir:    b.SkillsDir(),
		TaskDesc:     taskDesc,
		ModelName:    modelName,
		Sender:       sender,
	})
	if err != nil {
		return nil, err
	}
	return &GenerateResult{
		Name:        info.Name,
		Description: info.Description,
		SkillDir:    info.SkillDir,
	}, nil
}

// RunResult holds the output of running a Protean skill.
type RunResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// RunSkill executes a named Protean skill using the deterministic step_by_step
// executor. No LLM is invoked during execution.
func (b *Bridge) RunSkill(ctx context.Context, name string) (*RunResult, error) {
	script := runSkillScript
	cmd := b.command(ctx, "-c", script, b.SkillsDir(), name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("run skill: %w\n%s", err, string(out))
	}
	var res RunResult
	if jerr := json.Unmarshal(out, &res); jerr != nil {
		res = RunResult{Success: false, Output: string(out), Error: jerr.Error()}
	}
	return &res, nil
}

// ListSkills returns the names of Protean skills on disk.
func (b *Bridge) ListSkills() ([]string, error) {
	dir := b.SkillsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Info returns availability and configured paths for the web UI.
func (b *Bridge) Info() map[string]any {
	return map[string]any{
		"available":  b.Available(),
		"venv":       b.Venv(),
		"skills_dir": b.SkillsDir(),
	}
}

// InstallIfNeeded is a placeholder for a guided setup. For now it just logs
// instructions; the user must set up Protean manually.
func (b *Bridge) InstallIfNeeded() error {
	if b.Available() {
		return nil
	}
	slog.Info("protean venv not found; user must install Protean", "venv", b.Venv())
	return fmt.Errorf("Protean not found at %s; install it and set protean.venv_path in ~/.octo/config.yml", b.Venv())
}
