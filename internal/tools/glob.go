package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools/rgembed"
)

// GlobMaxResults caps the number of paths a single glob call returns. The
// LLM rarely needs more than this, and a missing cap could surface tens of
// thousands of paths on large repos (node_modules, vendor/) and blow up
// context.
const GlobMaxResults = 200

// globNoiseDirs are directory names glob always excludes, on top of whatever
// .gitignore already removes. They're high-noise and almost never the user's
// intent; keeping the explicit list means glob behaves the same in a repo that
// hasn't gitignored them (or isn't a git repo at all).
var globNoiseDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, "vendor": {}, ".venv": {},
}

// GlobTool returns paths matching a glob pattern, sorted by modification time
// descending so the most recently touched files appear first.
//
// File enumeration is delegated to ripgrep (`rg --files`), which respects
// .gitignore / .ignore and prunes ignored subtrees efficiently — the same
// engine grep uses, so the two tools agree on what's "in" the project. The
// glob pattern itself is then matched in-process against each path so the
// semantics below are exactly preserved regardless of ripgrep's own glob rules.
//
// Supports `**` for "any directory depth" (e.g. `src/**/*.go`). Other
// segments use the standard path.Match semantics: `*` matches any run of
// non-separator characters, `?` matches a single character, character
// classes via `[…]`.
type GlobTool struct{}

func (GlobTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "glob",
		Description: "Find files matching a glob pattern. Supports `**` for any " +
			"directory depth (e.g. `src/**/*.go`). Returns up to 200 paths sorted " +
			"by modification time descending (most recently changed first). " +
			"Respects .gitignore (uses ripgrep to enumerate), and always skips " +
			"`.git`, `node_modules`, `vendor`, and `.venv`. To see ignored files, " +
			"point grep/read_file at an explicit path instead. Requires `rg`.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern. Use `**` for recursive matching, e.g. `**/*.go`.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Starting directory. Defaults to the current working directory.",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (GlobTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	pattern, _ := input["pattern"].(string)
	if strings.TrimSpace(pattern) == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("glob: pattern is required")
	}

	root := "."
	if p, ok := input["path"].(string); ok && p != "" {
		root = p
	}
	absRoot, err := resolvePathIn(WorkingDir(ctx), root)
	if err != nil {
		return agent.ToolResult{Text: ""}, err
	}

	// Anchor the walk to the pattern's literal (non-wildcard) leading path
	// segments when it has any — e.g. "internal/tools/**/*.go" can only
	// match under "internal/tools", so there's no reason to hand ripgrep
	// the whole repo root and filter everything else out afterward. This
	// is the difference between listing one subtree and listing the entire
	// project on every single glob call, regardless of how specific the
	// pattern is.
	scanRoot := absRoot
	if prefix := literalPathPrefix(pattern); prefix != "" {
		candidate := filepath.Join(absRoot, prefix)
		switch _, statErr := os.Stat(candidate); {
		case statErr == nil:
			// os.Stat resolves case-insensitively on the filesystems most
			// developers actually run this on (macOS APFS, Windows NTFS,
			// both case-insensitive by default) — so a pattern whose
			// casing doesn't match what's on disk (e.g. "Internal/*.go"
			// against a real "internal/") would otherwise get pruned to
			// that wrong-case candidate anyway, and rg would then report
			// matches under the wrong-case path. glob's matching is
			// documented as path.Match semantics, i.e. case-sensitive —
			// on a case-sensitive filesystem (Linux) that mismatch would
			// already fail the stat and never reach here, so only trust
			// the prune once we've confirmed the same case-sensitive
			// result by checking each segment's on-disk name byte-for-byte.
			if onDiskCaseMatches(absRoot, prefix) {
				scanRoot = candidate
			}
			// else: leave scanRoot as absRoot — the prune target only
			// exists via a case-insensitive lookup, so pruning to it would
			// make this pattern match when it shouldn't. Falling back to
			// the unpruned walk keeps the pre-existing case-sensitive
			// matching behavior; globMatch (still case-sensitive) will
			// correctly find nothing.
		case os.IsNotExist(statErr):
			// The literal prefix doesn't exist on disk, so no file can
			// possibly match — skip the rg subprocess and the walk
			// entirely instead of confirming "nothing matched" the slow way.
			text := fmt.Sprintf("(no matches for %q under %s)", pattern, absRoot)
			return agent.ToolResult{Text: text, UI: map[string]any{
				"type":    "file_list",
				"path":    absRoot,
				"entries": []map[string]any{},
				"total":   0,
			}}, nil
		default:
			// Ambiguous error (e.g. permission denied on an ancestor dir) —
			// fall back to the unpruned walk rather than risk a false
			// "no matches".
		}
	}

	files, warning, err := listProjectFiles(ctx, scanRoot)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("glob: %w", err)
	}

	type match struct {
		path  string
		mtime int64
	}
	var matches []match

	for _, p := range files {
		rel, relErr := filepath.Rel(absRoot, p)
		if relErr != nil {
			rel = p
		}
		if hasNoiseDirSegment(rel) {
			continue
		}
		ok, mErr := globMatch(pattern, rel)
		if mErr != nil {
			return agent.ToolResult{Text: ""}, fmt.Errorf("glob: bad pattern %q: %w", pattern, mErr)
		}
		if !ok {
			continue
		}
		var mtime int64
		if info, infoErr := os.Stat(p); infoErr == nil {
			mtime = info.ModTime().UnixNano()
		}
		matches = append(matches, match{path: p, mtime: mtime})
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].mtime > matches[j].mtime })

	totalMatches := len(matches)
	truncated := false
	if totalMatches > GlobMaxResults {
		matches = matches[:GlobMaxResults]
		truncated = true
	}

	var out strings.Builder
	for _, m := range matches {
		out.WriteString(m.path)
		out.WriteByte('\n')
	}

	// UI payload: entries are relative to the search root to keep the card
	// (and the session JSON it persists in) compact.
	entries := make([]map[string]any, 0, min(len(matches), 20))
	for _, m := range matches[:min(len(matches), 20)] {
		name := m.path
		if rel, relErr := filepath.Rel(absRoot, m.path); relErr == nil {
			name = rel
		}
		entries = append(entries, map[string]any{"name": name})
	}
	ui := map[string]any{
		"type":    "file_list",
		"path":    absRoot,
		"entries": entries,
		"total":   totalMatches,
	}

	if out.Len() == 0 {
		text := fmt.Sprintf("(no matches for %q under %s)", pattern, absRoot)
		if warning != "" {
			text += fmt.Sprintf("\n[warning: %s]", warning)
		}
		return agent.ToolResult{Text: text, UI: ui}, nil
	}
	if truncated {
		fmt.Fprintf(&out, "\n[truncated to first %d of %d matches]\n", GlobMaxResults, totalMatches)
	}
	if warning != "" {
		fmt.Fprintf(&out, "\n[warning: %s]\n", warning)
	}
	return agent.ToolResult{Text: out.String(), UI: ui}, nil
}

// literalPathPrefix returns the leading path segments of pattern that
// contain no glob metacharacters, joined with "/". Every match for pattern
// must live under this prefix, so callers use it to anchor the filesystem
// walk to a subtree instead of the whole search root — e.g.
// "internal/tools/**/*.go" yields "internal/tools". Returns "" when the
// pattern has no literal prefix (e.g. "**/*.go", "*.go"), in which case no
// pruning is possible and the full root must be walked.
//
// A ".." segment stops collection (and is not itself included): unlike an
// ordinary literal segment it doesn't name a real path component to anchor
// on, it navigates to the parent directory. Treating it as literal would
// let filepath.Join walk the prefix outside the caller's search root — no
// on-disk path can ever contain a literal ".." segment, so a pattern like
// "../sibling/*.go" can never actually match anything (a leftover ".."
// segment always fails path.Match against a real path component), and
// stopping here just preserves that: it neither prunes nor matches, same
// as before this optimization existed. Empty segments (a leading "/") and
// "." are skipped rather than stopping collection, since neither changes
// which directory the rest of the prefix resolves to.
func literalPathPrefix(pattern string) string {
	segs := strings.Split(filepath.ToSlash(pattern), "/")
	var lit []string
	for _, seg := range segs {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." || strings.ContainsAny(seg, "*?[") {
			break
		}
		lit = append(lit, seg)
	}
	return strings.Join(lit, "/")
}

// onDiskCaseMatches reports whether every segment of prefix (a "/"-joined
// relative path, as returned by literalPathPrefix) matches the real on-disk
// entry name byte-for-byte, walking down from base. It exists because
// os.Stat alone can't tell a case-correct path from a case-mismatched one
// on case-insensitive filesystems — this reads each directory's entries
// and compares names directly, which is unaffected by filesystem case
// folding.
func onDiskCaseMatches(base, prefix string) bool {
	dir := base
	for _, seg := range strings.Split(prefix, "/") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		found := false
		for _, e := range entries {
			if e.Name() == seg {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		dir = filepath.Join(dir, seg)
	}
	return true
}

// listProjectFiles enumerates every file under root that ripgrep would search,
// as absolute paths. `--files` lists candidates respecting .gitignore/.ignore
// and ripgrep's built-in VCS pruning; `--hidden` re-includes dotfiles so glob
// can still find e.g. `.octorules`; `--null` separates paths with NUL so a
// newline inside a filename can't split one path into two. A clean "no files"
// (ripgrep exit code 1) returns an empty slice, not an error.
//
// The returned warning is non-empty when ripgrep exited with an error but still
// produced a partial file list; callers should surface it alongside the results
// rather than treating the call as a failure.
func listProjectFiles(ctx context.Context, root string) ([]string, string, error) {
	rgPath, err := rgembed.Path()
	if err != nil {
		return nil, "", err
	}

	info, err := os.Stat(root)
	if err != nil {
		return nil, "", fmt.Errorf("stat root %q: %w", root, err)
	}
	if !info.IsDir() {
		// rg --files on a regular file emits that file and exits 0; mirror that
		// without shelling out so non-directory roots don't produce a cryptic
		// "not a directory" error from ripgrep.
		return []string{root}, "", nil
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, rgPath, "--files", "--hidden", "--null", root)
	cmd.Stderr = &stderr
	out, err := cmd.Output()

	if err == nil {
		files, parseErr := parseNullSeparatedPaths(out)
		return files, "", parseErr
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil, "", nil // no files under root
	}

	// ripgrep exit code 2 means an error occurred while scanning. If it still
	// managed to list some files, return those and let the caller surface the
	// warning; failing entirely for a single unreadable directory is worse than
	// returning partial results.
	files, _ := parseNullSeparatedPaths(out)
	if len(files) > 0 {
		return files, strings.TrimSpace(stderr.String()), nil
	}

	if stderr.Len() > 0 {
		return nil, "", fmt.Errorf("rg --files: %s", strings.TrimSpace(stderr.String()))
	}
	return nil, "", fmt.Errorf("rg --files: %w", err)
}

// parseNullSeparatedPaths splits rg --null output into individual paths.
func parseNullSeparatedPaths(data []byte) ([]string, error) {
	trimmed := strings.Trim(string(data), "\x00")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\x00"), nil
}

// hasNoiseDirSegment reports whether any path segment of rel names a directory
// in globNoiseDirs. Applied after ripgrep so the exclusion holds even when the
// project hasn't gitignored these dirs.
func hasNoiseDirSegment(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if _, ok := globNoiseDirs[seg]; ok {
			return true
		}
	}
	return false
}

// globMatch matches a pattern against a relative path, supporting `**`
// across path separators (which path.Match by itself does not).
//
// Strategy: split pattern on `/`, then walk path segments and pattern
// segments in lockstep. When a `**` segment is encountered, recurse over
// every possible tail-position the rest of the pattern could match at.
func globMatch(pattern, path string) (bool, error) {
	// Normalise to forward slashes so the same patterns work cross-platform.
	pp := strings.Split(filepath.ToSlash(pattern), "/")
	sp := strings.Split(filepath.ToSlash(path), "/")
	return matchSegments(pp, sp)
}

func matchSegments(pp, sp []string) (bool, error) {
	for i, pat := range pp {
		if pat == "**" {
			// `**` matches zero or more path segments. Try every tail.
			rest := pp[i+1:]
			if len(rest) == 0 {
				return true, nil
			}
			for j := 0; j <= len(sp); j++ {
				ok, err := matchSegments(rest, sp[j:])
				if err != nil {
					return false, err
				}
				if ok {
					return true, nil
				}
			}
			return false, nil
		}
		if len(sp) == 0 {
			return false, nil
		}
		ok, err := filepath.Match(pat, sp[0])
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		sp = sp[1:]
	}
	return len(sp) == 0, nil
}
