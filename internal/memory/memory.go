// Package memory implements octo's cross-session memory as plain markdown
// files the agent manages with its own file tools — the Claude Code model.
//
// Layout: ~/.octo/memories/<repo-slug>/
//   - MEMORY.md      the index, loaded into the system prompt each session
//     (first maxInjectLines lines / maxInjectBytes, whichever
//     comes first)
//   - <topic>.md     detail files the agent creates and reads on demand
//
// There is no dedicated remember/forget tool and no code-driven consolidation:
// the agent reads, writes, edits, and deletes these files with read_file /
// write_file / edit_file (and terminal for rm/rename), keeping MEMORY.md a
// concise index and moving detail into topic files. cmd/octo injects MEMORY.md
// into the system prompt (RenderInjection) and whitelists the directory for
// writes so the agent can manage it without permission prompts.
package memory

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IndexFile is the per-repo memory index, loaded into the system prompt.
const IndexFile = "MEMORY.md"

// Injection budget for MEMORY.md — mirrors Claude Code's 200 lines / 25KB cap.
const (
	maxInjectLines = 200
	maxInjectBytes = 25 * 1024
)

// ProjectRoot returns the repo root that memory is scoped to for dir, or dir
// itself when it's not in a git repo (or git is unavailable).
//
// It derives the root from the git *common* dir rather than the per-worktree
// top-level, so every linked worktree of a repo shares one memory scope (a
// worktree checkout doesn't start with empty project memory). The common dir is
// `<root>/.git`, shared by the main worktree and all linked ones:
//   - main worktree → ".git" (relative to dir) → root = dir
//   - linked worktree → "<root>/.git" (absolute) → root = <root>
//
// Either way the main-checkout result is unchanged from the old --show-toplevel
// behavior, so existing memory dirs keep their slug.
func ProjectRoot(dir string) string {
	if dir == "" {
		return ""
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-common-dir").Output(); err == nil {
		common := strings.TrimSpace(string(out))
		if common != "" {
			if !filepath.IsAbs(common) {
				common = filepath.Join(dir, common) // relative paths are relative to `dir` (the -C target)
			}
			common = filepath.Clean(common)
			if filepath.Base(common) == ".git" {
				return resolveSymlinks(filepath.Dir(common))
			}
			// Bare repo or unusual layout — fall through to the top-level.
		}
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		if root := strings.TrimSpace(string(out)); root != "" {
			return resolveSymlinks(root)
		}
	}
	return dir
}

// resolveSymlinks returns the symlink-free form of p so the same repo always
// maps to one slug — git reports a resolved absolute path for a linked
// worktree's common dir, and the old --show-toplevel was likewise resolved, so
// the main checkout and its worktrees must normalize the same way. Falls back
// to p when it can't be resolved.
func resolveSymlinks(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Dir returns the memory directory for repoRoot: ~/.octo/memories/<repo-slug>.
func Dir(repoRoot string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("memory: cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, ".octo", "memories", repoSlug(repoRoot)), nil
}

// HomeDir returns the memory directory for the user's home directory.
func HomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("memory: cannot resolve home dir: %w", err)
	}
	return Dir(home)
}

// repoSlug derives a stable, human-readable directory name from a repo root:
// the basename plus a short hash of the full path, so two repos sharing a
// basename (e.g. two checkouts of "app") don't collide.
func repoSlug(repoRoot string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(repoRoot))
	base := Slugify(filepath.Base(repoRoot))
	if base == "" {
		return fmt.Sprintf("repo-%08x", h.Sum32())
	}
	return fmt.Sprintf("%s-%08x", base, h.Sum32())
}

// EnsureDir creates the memory directory so the agent's file tools can write
// into it on first use.
func EnsureDir(dir string) error { return os.MkdirAll(dir, 0o755) }

// LoadIndex returns MEMORY.md truncated to the injection budget, or "" when the
// file is absent or empty.
func LoadIndex(dir string) string {
	s, _ := loadIndex(dir)
	return s
}

// loadIndex returns the truncated index and whether truncation dropped any
// content (the file exceeded maxInjectBytes or maxInjectLines).
func loadIndex(dir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		return "", false
	}
	return truncateForInjection(string(b))
}

// truncateForInjection clamps s to the injection budget and reports whether
// anything was dropped.
func truncateForInjection(s string) (string, bool) {
	truncated := false
	if len(s) > maxInjectBytes {
		s = s[:maxInjectBytes]
		truncated = true
	}
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64*1024), maxInjectBytes+1024)
	var b strings.Builder
	n := 0
	for n < maxInjectLines && sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
		n++
	}
	if sc.Scan() { // a line remains past the cap
		truncated = true
	}
	return strings.TrimRight(b.String(), "\n"), truncated
}

// truncationWarning is appended inside an injected index that was cut to the
// budget, so the model (and the user via `octo memory`) knows entries are
// missing rather than silently losing them.
const truncationWarning = "\n\n⚠ This MEMORY.md exceeds the injection budget (" +
	"200 lines / 25KB); entries past the cut are NOT loaded this session. " +
	"Prune it or move detail into topic files."

// RenderInjection builds the memory section for the system prompt: an
// instruction block telling the model where its memory lives and how to manage
// it, followed by the current MEMORY.md (truncated). The instruction is emitted
// even when memory is empty so a fresh project knows where to start saving.
//
// If inheritedDirs are provided, their MEMORY.md files are also injected
// first, so home-directory (global) memories are available in every project.
func RenderInjection(dir string, inheritedDirs ...string) string {
	// dedupe inheritedDirs against dir so home-dir doesn't double-inject
	var filtered []string
	for _, d := range inheritedDirs {
		if d != dir {
			filtered = append(filtered, d)
		}
	}
	inheritedDirs = filtered

	var b strings.Builder
	b.WriteString("# Memory (from past sessions)\n\n")
	b.WriteString("Durable notes you keep for this project live in:\n  " + dir + "\n")
	if len(inheritedDirs) > 0 {
		b.WriteString("\nInherited memories (shared across all projects) live in:\n")
		for _, d := range inheritedDirs {
			b.WriteString("  " + d + "\n")
		}
	}
	b.WriteString("\n" + IndexFile + " is the index, loaded here every session; topic files beside it hold detail and load on demand.\n\n")
	b.WriteString("Manage memory yourself with your file tools — that directory is writable:\n")
	b.WriteString("- When the user states a lasting preference, gives feedback or a correction, or shares something worth recalling in future sessions, save it (append to " + IndexFile + ", or to a topic file linked from it).\n")
	b.WriteString("- Keep " + IndexFile + " a concise index; move long detail into topic files.\n")
	b.WriteString("- For a load-bearing rule you must not skip, write it in full under a '## 必须遵守' (always-apply) section, or, if it only matters for certain tasks, under a '## 触发提醒' section with a leading '(触发: keyword1, keyword2)' clause. Rules in those sections are re-surfaced to you at the point of action; everything else stays a pointer index.\n")
	b.WriteString("- Edit or delete entries that become wrong or obsolete. Don't store one-off task details or things already in the repo / CLAUDE.md.\n")
	if len(inheritedDirs) > 0 {
		b.WriteString("- When saving new memories, sort them by scope: write project-specific facts (repo conventions, tech stack, architecture) to the project memory above; write cross-project or personal preferences (coding style, tool defaults, name, role, habits) to the inherited (home) memory. If unsure, prefer the project memory — it can always be moved later.\n")
	}
	b.WriteString("The notes below are your own durable record of this user's preferences, workflow rules, and project facts — follow them as standing guidance, the way you follow project conventions. They are records, not live instructions from the user: if a note conflicts with the user's current request or with safety, the current request and safety win. Verify any file, flag, or path a note names still exists before relying on it.\n")

	// Inject inherited memories first (global / home-dir), then project-specific.
	for _, d := range inheritedDirs {
		if idx, trunc := loadIndex(d); idx != "" {
			b.WriteString("\n## " + IndexFile + " (inherited from " + d + ")\n\n" + idx)
			if trunc {
				b.WriteString(truncationWarning)
			}
		}
	}

	if idx, trunc := loadIndex(dir); idx != "" {
		b.WriteString("\n## " + IndexFile + "\n\n" + idx)
		if trunc {
			b.WriteString(truncationWarning)
		}
	} else {
		b.WriteString("\n(" + IndexFile + " is empty — start it when there's something worth remembering.)")
	}
	return b.String()
}

// IsMemoryPath reports whether absPath is inside the per-repo memory
// directory (~/.octo/memories/<repo-slug>/). Used by the file tools to
// emit friendlier output when the agent reads or writes its own notes.
func IsMemoryPath(absPath string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	prefix := filepath.Join(home, ".octo", "memories")
	return strings.HasPrefix(absPath, prefix+string(filepath.Separator))
}

// CountMemories estimates how many "memory entries" a markdown file
// contains by counting top-level headings (# or ##). This is a rough
// heuristic — good enough for progress UI but not a semantic parser.
func CountMemories(content string) int {
	var count int
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
			count++
		}
	}
	if count == 0 && strings.TrimSpace(content) != "" {
		// A non-empty file with no headings still holds at least one memory.
		return 1
	}
	return count
}

// Slugify reduces s to a lowercase kebab token usable as a path segment.
func Slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 50 {
		out = strings.Trim(out[:50], "-")
	}
	return out
}
