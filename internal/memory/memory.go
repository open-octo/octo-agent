// Package memory implements octo's cross-session memory as plain markdown
// files the agent manages with its own file tools — the Claude Code model.
//
// Layout: ~/.octo/memories/<project-slug>/
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
	"path/filepath"
	"strings"
)

// IndexFile is the per-project memory index, loaded into the system prompt.
const IndexFile = "MEMORY.md"

// Injection budget for MEMORY.md — mirrors Claude Code's 200 lines / 25KB cap.
const (
	maxInjectLines = 200
	maxInjectBytes = 25 * 1024
)

// NormalizeDir returns the form of p that Dir slugs, so callers comparing a
// directory against a stored one (a project's working_dir, say) agree with the
// memory layout instead of doing their own string munging.
func NormalizeDir(p string) string {
	return resolveSymlinks(p)
}

// resolveSymlinks returns the symlink-free form of p so one directory always
// maps to one slug, however it was reached — /tmp and /private/tmp on macOS, a
// symlinked checkout, a project whose working dir was typed one way and a
// session's cwd another. Falls back to p when it can't be resolved.
func resolveSymlinks(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// RootDir returns ~/.octo/memories — the parent holding every per-repo slug
// directory (see Dir). Callers that enumerate all project memories (e.g. the
// serve memory panel) read it instead of hard-coding the layout.
func RootDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("memory: cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, ".octo", "memories"), nil
}

// Dir returns the memory directory for projectDir: ~/.octo/memories/<slug>.
// The path is normalized here, the one place it happens, so every caller —
// DirForProject, HomeDir, a directory named on the command line — agrees on the
// slug for a given directory however that directory was spelled.
func Dir(projectDir string) (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, dirSlug(resolveSymlinks(projectDir))), nil
}

// HomeDir returns the memory directory for the user's home directory — the
// shared tier every session reads.
//
// Dir normalizes the path it is given, and os.UserHomeDir reports $HOME
// unresolved, so on a machine whose home sits behind a symlink (enterprise
// autofs or NFS layouts, /home → /net/home) the normalized slug differs from
// the one earlier versions wrote under. Falling back to the unresolved slug
// when it is the one holding notes keeps those users' global memory reachable
// instead of silently starting them over in an empty directory. Everyone else —
// the overwhelming majority, whose home path resolves to itself — is unaffected,
// and a fresh install takes the normalized path.
func HomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("memory: cannot resolve home dir: %w", err)
	}
	dir, err := Dir(home)
	if err != nil {
		return "", err
	}
	if legacy, lerr := legacyHomeDir(home); lerr == nil && legacy != dir && hasNotes(legacy) && !hasNotes(dir) {
		return legacy, nil
	}
	return dir, nil
}

// legacyHomeDir is the pre-normalization slug for home: the same computation as
// Dir, minus the symlink resolution.
func legacyHomeDir(home string) (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, dirSlug(home)), nil
}

// hasNotes reports whether dir holds any markdown — the test for "this is where
// the user's memory actually lives", as opposed to a directory some earlier
// EnsureDir created and nothing ever wrote to.
func hasNotes(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			return true
		}
	}
	return false
}

// DirForProject resolves the memory directory for a session belonging to the
// project rooted at projectDir. An empty projectDir means the session belongs
// to no project — a task — and resolves to the home directory every session
// shares, which is also what a caller gets by passing the home directory
// itself, so no caller needs to special-case it.
//
// Project membership is the caller's fact to establish, not this package's to
// guess. Under `octo serve` it comes from the session-group registry: a session
// filed under a project inherits that project's directory (see
// server.ProjectDirForSession); a loose task is filed under none and passes "".
// On the CLI the working directory *is* the project — you cd somewhere to work
// on it — so cmd/octo passes its cwd. Running from the home directory needs no
// special case: Dir(home) is the home directory, so it lands on the shared tier
// on its own.
//
// This deliberately does not consult git. A project is whatever the user made a
// project, which is not the same question as whether a directory happens to be
// a checkout: plenty of real work lives in directories git knows nothing about,
// and plenty of checkouts are passed through rather than worked on.
func DirForProject(projectDir string) (string, error) {
	if projectDir == "" {
		return HomeDir()
	}
	return Dir(projectDir)
}

// dirSlug derives a stable, human-readable directory name from a project
// directory: the basename plus a short hash of the full path, so two projects
// sharing a basename (e.g. two checkouts of "app") don't collide. Callers reach
// it through Dir, which normalizes the path first.
func dirSlug(projectDir string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(projectDir))
	base := Slugify(filepath.Base(projectDir))
	if base == "" {
		return fmt.Sprintf("project-%08x", h.Sum32())
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
	// dedupe inheritedDirs against dir so home-dir doesn't double-inject;
	// drop empty entries too — an unresolvable home dir would otherwise
	// print an "Inherited memories" header followed by a blank path, and
	// flip the shared-tier wording off (len check below).
	var filtered []string
	for _, d := range inheritedDirs {
		if d != "" && d != dir {
			filtered = append(filtered, d)
		}
	}
	inheritedDirs = filtered

	// With no inherited dirs, dir IS the shared tier: this working directory is
	// not a project of its own (see DirForProject), so calling it "this
	// project" — and pointing a fallback at an "inherited" tier that is never
	// introduced and whose path is never given — would be incoherent in
	// exactly the case that needs the guidance most.
	shared := len(inheritedDirs) == 0

	var b strings.Builder
	b.WriteString("# Memory (from past sessions)\n\n")
	if shared {
		b.WriteString("Durable notes you keep live in:\n  " + dir + "\n" +
			"This working directory is not a project of its own, so these notes are the shared set — every session reads them, whichever directory it runs in.\n")
	} else {
		b.WriteString("Durable notes you keep for this project live in:\n  " + dir + "\n")
	}
	if len(inheritedDirs) > 0 {
		b.WriteString("\nInherited memories (shared across all projects) live in:\n")
		for _, d := range inheritedDirs {
			b.WriteString("  " + d + "\n")
		}
	}
	b.WriteString("\n" + IndexFile + " is the index, loaded here every session; topic files beside it hold detail and load on demand.\n\n")
	b.WriteString("Manage memory yourself with your file tools — that directory is writable:\n")
	b.WriteString("- When the user states a lasting preference, gives feedback or a correction, or shares something worth recalling in future sessions, save it (append to " + IndexFile + ", or to a topic file linked from it).\n")
	b.WriteString("- Save what you worked out the hard way too: a non-obvious environment or tooling behaviour that cost you real time and would cost it again. Nobody will tell you that one — the signal is your own struggle, so it is the easiest kind to keep re-discovering.\n")
	b.WriteString("- Keep " + IndexFile + " a concise index; move long detail into topic files.\n")
	b.WriteString("- For a load-bearing rule you must not skip, write it in full under a '## 必须遵守' (always-apply) section, or, if it only matters for certain tasks, under a '## 触发提醒' section with a leading '(触发: keyword1, keyword2)' clause. Rules in those sections are re-surfaced to you at the point of action; everything else stays a pointer index.\n")
	b.WriteString("- Edit or delete entries that become wrong or obsolete. Don't store one-off task details, or anything already in .octorules or the repo's own docs.\n")
	if len(inheritedDirs) > 0 {
		b.WriteString("- When saving new memories, sort them by scope: write project-specific facts (repo conventions, tech stack, architecture) to the project memory above; write cross-project or personal preferences (coding style, tool defaults, name, role, habits) to the inherited (home) memory. If unsure, prefer the project memory — it can always be moved later.\n")
	}
	// The paths above are resolved from THIS session's working directory, which
	// is often not the repo the user actually asked about ("fix the login bug in
	// project X" from a scratch workspace). Without this, such a fact is filed
	// under the working directory's memory, where the session that later works
	// inside project X never reads it.
	// "created on first write" rests on write_file's MkdirAll
	// (internal/tools/write_file.go) — if that tool ever stops creating
	// parent dirs, this guidance silently breaks.
	crossProject := "- A durable fact about a DIFFERENT project than the one above — you were asked to work on another repo — belongs in THAT repo's memory. " +
		"Get its directory with `octo memory path <repo path>` (terminal), then write to it directly: the whole memories tree is writable, and the directory is created on first write. "
	if shared {
		crossProject += "If you can't resolve it, keep it here — these notes are read by every session, so it will still surface; a note filed under the wrong project surfaces nowhere.\n"
	} else {
		crossProject += "If you can't resolve it, save it to the inherited (shared) memory above rather than filing it under this project — shared notes are read by every session, a wrong project's notes by none.\n"
	}
	b.WriteString(crossProject)
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
