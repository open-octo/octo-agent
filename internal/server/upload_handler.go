package server

import (
	"errors"
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

// maxUploadBytes is the hard cap on a single upload request body. Uploads
// stream to disk (memory use is bounded by maxUploadMemory below), so the cap
// only guards against a runaway multi-GB drop filling ~/.octo/uploads. Keep in
// sync with MAX_FILE_BYTES in web/src/components/chat/Composer.svelte. A var,
// not a const, so the oversize-rejection test doesn't have to build a >512 MB
// body.
var maxUploadBytes int64 = 512 << 20 // 512 MB

// maxUploadMemory bounds the multipart parser's in-memory buffering; anything
// beyond it spills to temp files on the way to ~/.octo/uploads.
const maxUploadMemory = 32 << 20

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	// The server-wide 30s ReadTimeout would cut off a large body on a slow
	// remote link; give the upload request its own generous read deadline.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	// MaxBytesReader is the real ceiling: ParseMultipartForm's argument only
	// bounds in-memory buffering (the overflow spills to temp files), so
	// without this a client could stream an arbitrarily large body to disk.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		// The client pre-checks against the same cap, but multipart boundary
		// overhead can tip a right-at-the-limit file over it server-side —
		// name the limit instead of echoing MaxBytesReader's raw error.
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("file exceeds the %d MB upload limit", maxUploadBytes>>20))
			return
		}
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
			// Don't leave a truncated file behind (e.g. disk full) — nothing
			// ever references or cleans it up.
			os.Remove(dstPath)
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
