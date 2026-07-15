package server

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// docChipRefs strips "[Attached file: …]" notes from display text and returns
// the cleaned text plus one chip ref per note. Image attachments persisted
// under ~/.octo/uploads are returned as "/api/uploads/<name>" so the frontend
// renders a thumbnail; documents and non-uploaded local paths are returned as
// "pdf:<name>" for the document chip. The note in the message text is the only
// persisted trace of an attachment, so this is the single source of chips for
// both the live broadcast and history replay.
func docChipRefs(text string) (cleaned string, refs []string) {
	cleaned, names := agent.StripAttachmentNotes(text)
	paths := agent.AttachmentPaths(text)
	for i, name := range names {
		if i < len(paths) && isImageUpload(paths[i]) {
			refs = append(refs, "/api/uploads/"+filepath.Base(paths[i]))
		} else {
			refs = append(refs, "pdf:"+name)
		}
	}
	return cleaned, refs
}

// isImageUpload reports whether path is an image file persisted in the uploads
// directory, i.e. one that can be served back under /api/uploads/.
func isImageUpload(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return strings.Contains(filepath.ToSlash(path), "/.octo/"+uploadsDirName+"/")
	}
	return false
}

// User attachments sent over the WebSocket message payload. Images arrive as
// data URLs (the composer compresses them client-side); documents were already
// uploaded via POST /api/upload and arrive as an /api/uploads/ URL reference.

// userAttachments is the parsed, persisted form of a WS message's files array.
type userAttachments struct {
	// blocks are image content blocks for the model: decoded bytes plus the
	// path of the on-disk copy the session transcript will reference.
	blocks []agent.ContentBlock
	// images are frontend display refs for the user bubble: an /api/uploads/
	// URL per image thumbnail. Document chips are NOT carried here — they are
	// derived from the notes below (via docChipRefs) so the live bubble and a
	// reloaded transcript share one source.
	images []string
	// notes are text lines folded into the message so the model learns the
	// on-disk path of each document attachment (read_file can open them). They
	// are also what document attachment chips are rendered from.
	notes []string
}

// ensureUploadsDir returns ~/.octo/uploads, creating it if needed.
func ensureUploadsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("uploads: home dir: %w", err)
	}
	dir := filepath.Join(home, ".octo", uploadsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("uploads: mkdir: %w", err)
	}
	return dir, nil
}

// parseUserFiles converts a WS files payload into model blocks, display refs,
// and document path notes. Per-file failures are logged and skipped so one bad
// attachment doesn't drop the rest of the message. When vision is false, image
// data URLs are persisted to disk and referenced by path (so the model can read
// them with read_file) instead of being sent as image blocks.
func parseUserFiles(files []wsUserFile, allowLocalPath, vision bool) userAttachments {
	var att userAttachments
	for _, f := range files {
		switch {
		case f.LocalPath != "" && allowLocalPath:
			// Same-machine (loopback) client: a real local path the user chose
			// via the native dialog (desktop) or the in-app file picker
			// (localhost web). No upload — the agent reads it in place, like a
			// working-dir file. Referenced by its true absolute path.
			abs := f.LocalPath
			if a, err := filepath.Abs(abs); err == nil {
				abs = a
			}
			if _, err := os.Stat(abs); err != nil {
				log.Printf("[ws] native file attachment %q: %v", f.Name, err)
				continue
			}
			att.notes = append(att.notes, agent.AttachmentNote(abs))
		case f.DataURL != "":
			block, url, err := saveImageAttachment(f.Name, f.DataURL)
			if err != nil {
				log.Printf("[ws] image attachment %q: %v", f.Name, err)
				continue
			}
			if vision {
				// Vision model: embed the image directly and pre-fill the bubble
				// thumbnail from the same persisted copy.
				att.images = append(att.images, url)
				att.blocks = append(att.blocks, block)
			} else {
				// Text-only model: hand the model a path note instead of an image
				// block so it can read_file the image and keep working. The thumbnail
				// is derived from the note by docChipRefs so live and replay use the
				// same source.
				att.notes = append(att.notes, agent.AttachmentNote(block.ImagePath))
			}
		case f.Path != "":
			dir, err := ensureUploadsDir()
			if err != nil {
				log.Printf("[ws] file attachment %q: %v", f.Name, err)
				continue
			}
			// The composer sends back the /api/uploads/<name> URL that
			// POST /api/upload returned; map it to the file on disk.
			abs := filepath.Join(dir, filepath.Base(f.Path))
			if _, err := os.Stat(abs); err != nil {
				log.Printf("[ws] file attachment %q: %v", f.Name, err)
				continue
			}
			att.notes = append(att.notes, agent.AttachmentNote(abs))
		}
	}
	return att
}

// saveImageAttachment decodes a base64 data URL, persists the bytes under
// ~/.octo/uploads (the transcript stores the path, never the bytes), and
// returns the model-facing image block plus the /api/uploads/ display URL.
func saveImageAttachment(name, dataURL string) (agent.ContentBlock, string, error) {
	mime, data, err := decodeDataURL(dataURL)
	if err != nil {
		return agent.ContentBlock{}, "", err
	}

	dir, err := ensureUploadsDir()
	if err != nil {
		return agent.ContentBlock{}, "", err
	}

	// Basename only, no traversal; the stored extension is derived from the
	// actual MIME type (the composer re-encodes to JPEG but keeps the original
	// filename) so rehydration and Content-Type sniffing stay truthful.
	base := strings.ReplaceAll(filepath.Base(name), "..", "_")
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "image"
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	dstName := fmt.Sprintf("%d_%s%s", time.Now().UnixNano(), base, extForImageMIME(mime))
	dstPath := filepath.Join(dir, dstName)
	if err := os.WriteFile(dstPath, data, 0o600); err != nil {
		return agent.ContentBlock{}, "", fmt.Errorf("write %s: %w", dstPath, err)
	}

	block := agent.NewImageBlock(mime, data)
	block.ImagePath = dstPath
	return block, "/api/uploads/" + dstName, nil
}

// decodeDataURL splits "data:<mime>;base64,<payload>" into its parts.
func decodeDataURL(dataURL string) (mime string, data []byte, err error) {
	rest, ok := strings.CutPrefix(dataURL, "data:")
	if !ok {
		return "", nil, fmt.Errorf("not a data URL")
	}
	meta, payload, ok := strings.Cut(rest, ",")
	if !ok {
		return "", nil, fmt.Errorf("malformed data URL")
	}
	mime, _, _ = strings.Cut(meta, ";")
	if !strings.HasPrefix(mime, "image/") {
		return "", nil, fmt.Errorf("unsupported attachment type %q", mime)
	}
	data, err = base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, fmt.Errorf("decode base64: %w", err)
	}
	if len(data) == 0 {
		return "", nil, fmt.Errorf("empty image payload")
	}
	return mime, data, nil
}

// imageRefsFromBlocks derives the frontend thumbnail URLs for a message's
// persisted image blocks (blocks without an on-disk copy are skipped).
func imageRefsFromBlocks(blocks []agent.ContentBlock) []string {
	var refs []string
	for _, b := range blocks {
		if b.Type == "image" && b.ImagePath != "" {
			refs = append(refs, "/api/uploads/"+filepath.Base(b.ImagePath))
		}
	}
	return refs
}

// extForImageMIME picks the stored file extension for an image MIME type.
func extForImageMIME(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	default:
		return ".jpg"
	}
}

// ─── GET /api/uploads/{name} ────────────────────────────────────────────────

// handleGetUpload serves a previously uploaded attachment back to the browser;
// image thumbnails in chat history reference /api/uploads/<name>.
func (s *Server) handleGetUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}
	dir, err := ensureUploadsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Uploads are user-supplied bytes served from our origin; without nosniff
	// a no-Origin <script src> include could read one as JS (XSSI).
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Force download for HTML/JS content so it cannot execute in the Octo UI
	// origin (stored XSS). Browsers still render images/pdf normally.
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm") ||
		strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".mjs") {
		w.Header().Set("Content-Disposition", "attachment")
	}
	http.ServeFile(w, r, filepath.Join(dir, name))
}
