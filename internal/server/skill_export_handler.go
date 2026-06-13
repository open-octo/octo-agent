package server

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// handleExportSkill streams a skill directory as a .zip download. Skills are
// plain directories (SKILL.md plus bundled resources), so a zip is the natural
// portable form — the same shape POST /api/skills/import accepts back.
func (s *Server) handleExportSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing skill name")
		return
	}

	dir := ""
	for _, sk := range s.skillReg.All() {
		if sk.Name == name {
			dir = sk.Dir
			break
		}
	}
	if dir == "" {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		writeError(w, http.StatusNotFound, "skill directory not found")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".zip"))

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Walk the skill dir, writing each file under a top-level <name>/ prefix so
	// the archive unpacks into a self-contained skill folder.
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		zf, err := zw.Create(filepath.ToSlash(filepath.Join(name, rel)))
		if err != nil {
			return nil
		}
		_, _ = io.Copy(zf, f)
		return nil
	})
}
