package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Leihb/octo-agent/internal/skills"
)

// ─── POST /api/skills/import ────────────────────────────────────────────────

// handleImportSkill installs a skill into the user-level root from one of:
//   - a GitHub source (tree URL or owner/repo/sub/path shorthand),
//   - an uploaded zip referenced by its /api/uploads/<name> URL,
//   - a local zip file or skill directory path on the server machine.
//
// The skill content is fetched onto this machine by the user's own request —
// octo redistributes nothing (see `octo skills add` for the same rationale).
func (s *Server) handleImportSkill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
		Force  bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		writeError(w, http.StatusBadRequest, "source is required")
		return
	}

	name, desc, err := importSkill(source, req.Force)
	if err != nil {
		switch {
		case errors.Is(err, skills.ErrExists):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, errBadImportSource):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// Refresh the registry and manifest so the list — and new sessions — see
	// the skill immediately (same pattern as toggle/delete).
	s.skillReg.Reload()
	s.skillsManifest = skills.RenderManifest(s.skillReg)

	writeJSON(w, http.StatusOK, map[string]any{"name": name, "description": desc})
}

// errBadImportSource marks user-correctable source problems (HTTP 400), as
// opposed to install failures (HTTP 500).
var errBadImportSource = errors.New("invalid import source")

func importSkill(source string, force bool) (name, desc string, err error) {
	destRoot := skills.UserRoot()

	switch {
	// Uploaded archive: POST /api/upload returned /api/uploads/<name>; map it
	// back to ~/.octo/uploads. Basename only — no traversal.
	case strings.HasPrefix(source, "/api/uploads/"):
		dir, err := ensureUploadsDir()
		if err != nil {
			return "", "", err
		}
		p := filepath.Join(dir, filepath.Base(source))
		if _, err := os.Stat(p); err != nil {
			return "", "", joinBadSource("uploaded file not found: " + filepath.Base(source))
		}
		return skills.InstallZip(p, destRoot, force)

	// Local path on the server machine: a zip or a skill directory.
	// filepath.IsAbs covers Windows drive paths (C:\…) that the "/" prefix
	// check misses.
	case strings.HasPrefix(source, "/"), strings.HasPrefix(source, "~"), filepath.IsAbs(source):
		p := source
		if strings.HasPrefix(p, "~") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", "", err
			}
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		fi, err := os.Stat(p)
		if err != nil {
			return "", "", joinBadSource("path not found: " + p)
		}
		if fi.IsDir() {
			return skills.InstallDir(p, destRoot, force)
		}
		if strings.EqualFold(filepath.Ext(p), ".zip") {
			return skills.InstallZip(p, destRoot, force)
		}
		return "", "", joinBadSource("local source must be a .zip file or a skill directory")

	// GitHub: tree URL or owner/repo shorthand.
	default:
		src, perr := skills.ParseSource(source)
		if perr != nil {
			return "", "", joinBadSource(perr.Error())
		}
		return skills.Install(src, destRoot, force)
	}
}

func joinBadSource(msg string) error {
	return fmt.Errorf("%w: %s", errBadImportSource, msg)
}
