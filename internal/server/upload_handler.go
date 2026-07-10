package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ─── POST /api/upload ───────────────────────────────────────────────────────

// Upload destination under ~/.octo/uploads/.
const uploadsDirName = "uploads"

// maxUploadBytes is the hard cap on a single upload request body. Keep in sync
// with MAX_ATTACHMENT_BYTES in web/src/components/chat/Composer.svelte.
const maxUploadBytes = 32 << 20 // 32 MB

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	// MaxBytesReader is the real ceiling: ParseMultipartForm's argument only
	// bounds in-memory buffering (the overflow spills to temp files), so
	// without this a client could stream an arbitrarily large body to disk.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("parse form: %v", err))
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no files uploaded")
		return
	}

	uploadDir, err := ensureUploadsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	results := make([]uploadResult, 0, len(files))
	for _, fh := range files {
		src, err := fh.Open()
		if err != nil {
			results = append(results, uploadResult{Name: fh.Filename, Error: err.Error()})
			continue
		}

		// Sanitize filename: basename only, no path traversal.
		name := filepath.Base(fh.Filename)
		name = strings.ReplaceAll(name, "..", "_")
		// De-duplicate by timestamp prefix.
		dstName := fmt.Sprintf("%d_%s", time.Now().UnixNano(), name)
		dstPath := filepath.Join(uploadDir, dstName)

		dst, err := os.Create(dstPath)
		if err != nil {
			src.Close()
			results = append(results, uploadResult{Name: fh.Filename, Error: err.Error()})
			continue
		}
		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			results = append(results, uploadResult{Name: fh.Filename, Error: err.Error()})
			continue
		}

		// Return a URL the frontend can use to reference the file.
		results = append(results, uploadResult{
			Name:     fh.Filename,
			URL:      "/api/uploads/" + dstName,
			Size:     fh.Size,
			MimeType: fh.Header.Get("Content-Type"),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"files": results})
}

type uploadResult struct {
	Name     string `json:"name"`
	URL      string `json:"url,omitempty"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Error    string `json:"error,omitempty"`
}
