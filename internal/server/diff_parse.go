package server

import (
	"strings"
)

// Unified-diff parsing for the sidebar Git Diff panel
// (dev-docs/git-diff-panel-design.md). Pure text → structure, no IO, so the
// interesting cases (renames, binary markers, truncation) are table-testable.
//
// Truncation lives here rather than in the frontend because it has to happen at
// the data source: a 200k-line diff must never cross the wire in the first
// place.

const (
	// diffFileMaxLines caps one file's rendered hunk lines. Beyond this the
	// panel is unreadable anyway; the single-file endpoint serves the rest on
	// demand.
	diffFileMaxLines = 2000
	// diffResponseMaxLines caps the whole aggregate response. Files past the
	// budget keep their metadata and drop their patch.
	diffResponseMaxLines = 20000
)

// diffLine is one rendered line of a hunk. Kind is "context", "add" or "del".
type diffLine struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

// diffHunk is one @@ block.
type diffHunk struct {
	Header string     `json:"header"`
	Lines  []diffLine `json:"lines"`
}

// diffPatch is a file's content change. Paths are repo-relative as git prints
// them, with /dev/null kept verbatim for added and deleted files.
type diffPatch struct {
	OldPath string     `json:"old_path"`
	NewPath string     `json:"new_path"`
	Hunks   []diffHunk `json:"hunks"`
}

// newDiffPatch builds a patch whose Hunks always marshals as an array. A pure
// rename or a mode-only change legitimately has none, and `"hunks": null` would
// reach the client as something it has to null-check on every render.
func newDiffPatch(oldPath, newPath string, hunks []diffHunk) *diffPatch {
	if hunks == nil {
		hunks = []diffHunk{}
	}
	return &diffPatch{OldPath: oldPath, NewPath: newPath, Hunks: hunks}
}

// diffFile is one changed file in the panel's file list.
type diffFile struct {
	Path string `json:"path"`
	// OldPath is only set for renames and copies.
	OldPath string `json:"old_path,omitempty"`
	Status  string `json:"status"`
	Staged  bool   `json:"staged"`
	Adds    int    `json:"adds"`
	Dels    int    `json:"dels"`
	Binary  bool   `json:"binary"`
	// Truncated means Patch holds a prefix of the change; TotalLines reports
	// the full size so the UI can say how much is missing.
	Truncated bool `json:"truncated"`
	// Omitted means the aggregate line budget ran out before this file, so no
	// patch was rendered at all.
	Omitted    bool       `json:"omitted"`
	TotalLines int        `json:"total_lines"`
	Patch      *diffPatch `json:"patch"`
}

// diffRepo groups one repository's changes.
type diffRepo struct {
	Root   string     `json:"root"`
	Name   string     `json:"name"`
	Branch string     `json:"branch"`
	Commit string     `json:"commit,omitempty"`
	Files  []diffFile `json:"files"`
	// Error degrades a single repository without failing the response: the
	// other repositories still render.
	Error string `json:"error,omitempty"`
}

// diffResponse is the aggregate endpoint's body.
type diffResponse struct {
	Repos          []diffRepo `json:"repos"`
	TruncatedFiles int        `json:"truncated_files"`
	OmittedFiles   int        `json:"omitted_files"`
}

// diffFileSummary is a file list entry in summary mode, where the badge only
// needs counts and no git diff is run at all.
type diffFileSummary struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Staged bool   `json:"staged"`
}

// diffRepoSummary is a repository in summary mode.
type diffRepoSummary struct {
	Root  string            `json:"root"`
	Name  string            `json:"name"`
	Files []diffFileSummary `json:"files"`
	Error string            `json:"error,omitempty"`
}

// diffSummaryResponse is the ?summary=1 body.
type diffSummaryResponse struct {
	Repos []diffRepoSummary `json:"repos"`
}

// filePatch is a parsed patch before it is matched against a git status entry.
type filePatch struct {
	oldPath    string
	newPath    string
	binary     bool
	hunks      []diffHunk
	adds       int
	dels       int
	totalLines int
}

// parseUnifiedDiff splits `git diff` output into one filePatch per file.
//
// Paths come from the ---/+++ lines when present because those carry exactly
// one path each and so stay unambiguous for filenames containing spaces; the
// `diff --git a/x b/y` header is only consulted for binary files and pure
// mode/rename changes, which have no ---/+++ pair.
func parseUnifiedDiff(out string) []*filePatch {
	var patches []*filePatch
	var cur *filePatch
	var hunk *diffHunk

	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.hunks = append(cur.hunks, *hunk)
			hunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			patches = append(patches, cur)
			cur = nil
		}
	}

	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			old, new_ := splitDiffGitHeader(strings.TrimPrefix(line, "diff --git "))
			cur = &filePatch{oldPath: old, newPath: new_}
		case cur == nil:
			// Preamble or trailing junk; nothing to attach it to.
		case strings.HasPrefix(line, "--- ") && hunk == nil:
			cur.oldPath = stripDiffPathPrefix(strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ ") && hunk == nil:
			cur.newPath = stripDiffPathPrefix(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			hunk = &diffHunk{Header: line}
		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			cur.binary = true
		case hunk == nil:
			// Extended header line (index, mode, similarity, rename from/to).
		case strings.HasPrefix(line, "+"):
			hunk.Lines = append(hunk.Lines, diffLine{Kind: "add", Content: line[1:]})
			cur.adds++
			cur.totalLines++
		case strings.HasPrefix(line, "-"):
			hunk.Lines = append(hunk.Lines, diffLine{Kind: "del", Content: line[1:]})
			cur.dels++
			cur.totalLines++
		case strings.HasPrefix(line, " "):
			hunk.Lines = append(hunk.Lines, diffLine{Kind: "context", Content: line[1:]})
			cur.totalLines++
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" — an annotation on the previous
			// line, carried as context so nothing is silently dropped.
			hunk.Lines = append(hunk.Lines, diffLine{Kind: "context", Content: line})
			cur.totalLines++
		case line == "":
			// git's trailing newline after the last hunk.
		default:
			// Unknown line inside a hunk: end the hunk rather than guess.
			flushHunk()
		}
	}
	flushFile()
	return patches
}

// splitDiffGitHeader pulls the two paths out of a `a/x b/y` header. Filenames
// containing " b/" make this ambiguous, which is why it is only the fallback.
func splitDiffGitHeader(rest string) (string, string) {
	if i := strings.Index(rest, " b/"); i >= 0 {
		return stripDiffPathPrefix(rest[:i]), stripDiffPathPrefix(rest[i+1:])
	}
	if a, b, ok := strings.Cut(rest, " "); ok {
		return stripDiffPathPrefix(a), stripDiffPathPrefix(b)
	}
	return stripDiffPathPrefix(rest), stripDiffPathPrefix(rest)
}

// stripDiffPathPrefix removes git's a/ or b/ prefix and the trailing tab it
// appends when a filename needs it. /dev/null is passed through.
func stripDiffPathPrefix(p string) string {
	p = strings.TrimSuffix(p, "\t")
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	if p == "/dev/null" {
		return p
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// truncatePatch cuts hunks so at most max lines are rendered. It reports
// whether anything was dropped and how many lines were kept.
func truncatePatch(p *diffPatch, max int) (truncated bool, kept int) {
	if p == nil {
		return false, 0
	}
	for i := range p.Hunks {
		lines := p.Hunks[i].Lines
		if kept+len(lines) <= max {
			kept += len(lines)
			continue
		}
		room := max - kept
		if room > 0 {
			p.Hunks[i].Lines = lines[:room]
			kept += room
			p.Hunks = p.Hunks[:i+1]
		} else {
			p.Hunks = p.Hunks[:i]
		}
		return true, kept
	}
	return false, kept
}
