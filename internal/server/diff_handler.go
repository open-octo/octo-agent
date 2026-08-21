package server

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// ─── GET /api/sessions/{id}/diff ────────────────────────────────────────────
//
// Serves the uncommitted changes in the repositories a session can touch, for
// the sidebar Git Diff review panel (dev-docs/git-diff-panel-design.md).
//
// The aggregate endpoint takes a session id and nothing else: the directory set
// is resolved server-side, the same model handleNativeOpenFolder uses, because
// a caller-controlled path here would be an arbitrary-file-read hole. The
// single-file endpoint does take a repo and path, and both are checked back
// against a freshly resolved root set and a live git status.

func (s *Server) handleGetSessionDiff(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	if _, err := agent.LoadSession(id); err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if !gitAvailable() {
		writeError(w, http.StatusInternalServerError, errGitMissing.Error())
		return
	}

	roots := s.diffRepoRootsForSession(id)
	if r.URL.Query().Get("summary") == "1" {
		writeJSON(w, http.StatusOK, buildDiffSummary(roots))
		return
	}
	writeJSON(w, http.StatusOK, buildDiffResponse(roots))
}

// ─── GET /api/sessions/{id}/diff/file?repo=…&path=… ─────────────────────────
//
// One file's complete diff, for a file the aggregate response truncated or
// omitted. The per-file line cap does not apply — the user asked for all of it
// — but the untracked read cap and the binary skip still do.

func (s *Server) handleGetSessionFileDiff(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if id == "" || repo == "" || rel == "" {
		writeError(w, http.StatusBadRequest, "missing session id, repo or path")
		return
	}
	if _, err := agent.LoadSession(id); err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if !gitAvailable() {
		writeError(w, http.StatusInternalServerError, errGitMissing.Error())
		return
	}

	// Both query parameters are matched against server-derived values and then
	// discarded: root below comes from diffRepoRootsForSession (git's own
	// rev-parse output) and entry.path from git status, so nothing the caller
	// wrote reaches a subprocess or a file operation. Same model as
	// handleGetArtifact, which opens the transcript's copy of a path rather
	// than the request's.
	//
	// First whitelist: the repository must be one this session resolves to.
	root, ok := matchedRoot(s.diffRepoRootsForSession(id), repo)
	if !ok {
		writeError(w, http.StatusForbidden, "repository not in this session's scope")
		return
	}

	entries, err := gitStatus(root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Second whitelist: the path must be a file git currently reports as
	// changed, so an unchanged file elsewhere in the repository is not readable
	// through this endpoint.
	var entry *statusEntry
	for i := range entries {
		if entries[i].path == rel {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		writeError(w, http.StatusNotFound, "path is not a changed file in this repository")
		return
	}

	var patch *filePatch
	if !entry.untracked() {
		// Scope the diff to this file. A rename needs both sides on the
		// pathspec, otherwise git reports it as an unrelated add.
		paths := []string{entry.path}
		if entry.origPath != "" {
			paths = append(paths, entry.origPath)
		}
		out, err := gitDiff(root, paths...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		patch = indexPatches(parseUnifiedDiff(out))[entry.path]
	}

	file := buildDiffFile(root, *entry, patch, diffFileNoLimit)
	writeJSON(w, http.StatusOK, map[string]any{"file": file})
}

// diffFileNoLimit disables per-file truncation for the single-file endpoint.
const diffFileNoLimit = -1

// buildDiffSummary runs status only — no diff, no file reads — because the
// badge just needs counts.
func buildDiffSummary(roots []string) diffSummaryResponse {
	resp := diffSummaryResponse{Repos: []diffRepoSummary{}}
	for _, root := range roots {
		repo := diffRepoSummary{Root: root, Name: filepath.Base(root), Files: []diffFileSummary{}}
		entries, err := gitStatus(root)
		if err != nil {
			repo.Error = err.Error()
			resp.Repos = append(resp.Repos, repo)
			continue
		}
		if len(entries) == 0 {
			continue
		}
		for _, e := range entries {
			repo.Files = append(repo.Files, diffFileSummary{
				Path:   e.path,
				Status: e.status(),
				Staged: e.staged(),
			})
		}
		resp.Repos = append(resp.Repos, repo)
	}
	return resp
}

// buildDiffResponse collects every repository's changes and applies both
// truncation layers. A repository whose git commands fail keeps its slot with
// an error attached rather than failing the whole response.
func buildDiffResponse(roots []string) diffResponse {
	resp := diffResponse{Repos: []diffRepo{}}
	budget := diffResponseMaxLines

	for _, root := range roots {
		repo := diffRepo{Root: root, Name: filepath.Base(root), Files: []diffFile{}}
		repo.Branch, repo.Commit = gitHeadInfo(root)

		entries, err := gitStatus(root)
		if err != nil {
			repo.Error = err.Error()
			resp.Repos = append(resp.Repos, repo)
			continue
		}
		if len(entries) == 0 {
			// A clean repository is not worth a group header.
			continue
		}

		out, err := gitDiff(root)
		if err != nil {
			repo.Error = err.Error()
			resp.Repos = append(resp.Repos, repo)
			continue
		}
		byPath := indexPatches(parseUnifiedDiff(out))

		for _, e := range entries {
			limit := diffFileMaxLines
			if budget <= 0 {
				limit = 0 // budget spent: metadata only
			}
			f := buildDiffFile(root, e, byPath[e.path], limit)
			if f.Truncated {
				resp.TruncatedFiles++
			}
			if f.Omitted {
				resp.OmittedFiles++
			}
			if f.Patch != nil {
				budget -= countPatchLines(f.Patch)
			}
			repo.Files = append(repo.Files, f)
		}
		resp.Repos = append(resp.Repos, repo)
	}
	return resp
}

// indexPatches keys parsed patches by the path git status reports, which is the
// post-change path for renames.
func indexPatches(patches []*filePatch) map[string]*filePatch {
	m := make(map[string]*filePatch, len(patches))
	for _, p := range patches {
		key := p.newPath
		if key == "/dev/null" {
			key = p.oldPath // deletion
		}
		m[key] = p
	}
	return m
}

// buildDiffFile turns one status entry plus its patch into a file list item.
// limit is the per-file line cap, 0 to render metadata only, or diffFileNoLimit
// for no cap at all.
func buildDiffFile(root string, e statusEntry, p *filePatch, limit int) diffFile {
	f := diffFile{
		Path:    e.path,
		OldPath: e.origPath,
		Status:  e.status(),
		Staged:  e.staged(),
	}

	if e.untracked() {
		if limit == 0 {
			f.Omitted = true
			return f
		}
		patch, binary, readTruncated, err := synthUntrackedPatch(root, e.path)
		if err != nil {
			// Vanished or unreadable between status and read: report it as a
			// file with no renderable content instead of failing the repo.
			f.Binary = true
			return f
		}
		if binary {
			f.Binary = true
			return f
		}
		f.Adds = countPatchLines(patch)
		f.TotalLines = f.Adds
		f.Patch = patch
		f.Truncated = readTruncated
		if limit != diffFileNoLimit {
			if cut, _ := truncatePatch(f.Patch, limit); cut {
				f.Truncated = true
			}
		}
		return f
	}

	if p == nil {
		// In the index but with no content delta: a pure rename, or a mode-only
		// change. Nothing to render, and not an error.
		return f
	}
	f.Binary = p.binary
	f.Adds, f.Dels, f.TotalLines = p.adds, p.dels, p.totalLines
	if p.binary {
		return f
	}
	if limit == 0 {
		f.Omitted = true
		return f
	}
	f.Patch = newDiffPatch(p.oldPath, p.newPath, p.hunks)
	if limit != diffFileNoLimit {
		if cut, _ := truncatePatch(f.Patch, limit); cut {
			f.Truncated = true
		}
	}
	return f
}

func countPatchLines(p *diffPatch) int {
	if p == nil {
		return 0
	}
	n := 0
	for _, h := range p.Hunks {
		n += len(h.Lines)
	}
	return n
}

// matchedRoot finds the session-resolved repository root the caller named and
// returns the server's own string for it, not the caller's. Handing the
// resolved copy onward is what keeps request data out of the git subprocess —
// the comparison authorises, the returned value is what gets used.
func matchedRoot(roots []string, want string) (string, bool) {
	want = filepath.Clean(want)
	for _, root := range roots {
		if root == want {
			return root, true
		}
	}
	return "", false
}
