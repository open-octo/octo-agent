package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

func jpegDataURL(payload []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(payload)
}

func TestParseUserFiles_ImageDataURL(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	payload := []byte{0xFF, 0xD8, 0xFF, 1, 2, 3}
	att := parseUserFiles([]wsUserFile{
		{Name: "shot.png", MimeType: "image/jpeg", DataURL: jpegDataURL(payload)},
	}, false, true)

	if len(att.blocks) != 1 || len(att.images) != 1 || len(att.notes) != 0 {
		t.Fatalf("blocks/images/notes = %d/%d/%d, want 1/1/0",
			len(att.blocks), len(att.images), len(att.notes))
	}
	b := att.blocks[0]
	if b.Type != "image" || b.Image == nil {
		t.Fatalf("block = %+v", b)
	}
	if string(b.Image.Data) != string(payload) {
		t.Errorf("decoded bytes mismatch")
	}
	if b.Image.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType = %q", b.Image.MIMEType)
	}
	// Persisted copy exists, carries a MIME-true extension, and the display
	// URL references it.
	if b.ImagePath == "" {
		t.Fatal("ImagePath not set")
	}
	if filepath.Ext(b.ImagePath) != ".jpg" {
		t.Errorf("stored ext = %q, want .jpg (MIME-derived)", filepath.Ext(b.ImagePath))
	}
	onDisk, err := os.ReadFile(b.ImagePath)
	if err != nil || string(onDisk) != string(payload) {
		t.Errorf("persisted copy: err=%v, match=%v", err, string(onDisk) == string(payload))
	}
	wantURL := "/api/uploads/" + filepath.Base(b.ImagePath)
	if att.images[0] != wantURL {
		t.Errorf("display URL = %q, want %q", att.images[0], wantURL)
	}
}

// TestParseUserFiles_ImageDataURL_NonVision verifies that when the active model
// does not accept images, an image data URL is persisted to disk and surfaced
// to the model as a path note (so it can read_file the image) rather than an
// image block that would be rejected by the provider.
func TestParseUserFiles_ImageDataURL_NonVision(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	payload := []byte{0xFF, 0xD8, 0xFF, 1, 2, 3}
	att := parseUserFiles([]wsUserFile{
		{Name: "shot.png", MimeType: "image/jpeg", DataURL: jpegDataURL(payload)},
	}, false, false)

	// Non-vision image produces only a path note; the thumbnail is derived from
	// the note by docChipRefs so live and replay use the same source.
	if len(att.blocks) != 0 || len(att.images) != 0 || len(att.notes) != 1 {
		t.Fatalf("blocks/images/notes = %d/%d/%d, want 0/0/1",
			len(att.blocks), len(att.images), len(att.notes))
	}
	path := strings.TrimPrefix(strings.TrimSuffix(att.notes[0], "]"), "[Attached file: ")
	if !strings.Contains(filepath.ToSlash(path), ".octo/uploads") {
		t.Errorf("note should reference the persisted upload path, got %q", att.notes[0])
	}
	onDisk, err := os.ReadFile(path)
	if err != nil || string(onDisk) != string(payload) {
		t.Errorf("persisted copy mismatch: err=%v, match=%v", err, string(onDisk) == string(payload))
	}
}

func TestParseUserFiles_DocPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir, err := ensureUploadsDir()
	if err != nil {
		t.Fatalf("uploads dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "123_report.pdf"), []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	att := parseUserFiles([]wsUserFile{
		{Name: "report.pdf", MimeType: "application/pdf", Path: "/api/uploads/123_report.pdf"},
	}, false, true)

	// A document produces no block and no image ref — only a note. Its display
	// chip is derived from that note (see docChipRefs / the replay test).
	if len(att.blocks) != 0 || len(att.images) != 0 || len(att.notes) != 1 {
		t.Fatalf("blocks/images/notes = %d/%d/%d, want 0/0/1",
			len(att.blocks), len(att.images), len(att.notes))
	}
	wantPath := filepath.Join(dir, "123_report.pdf")
	if !strings.Contains(att.notes[0], wantPath) {
		t.Errorf("note %q does not reference %q", att.notes[0], wantPath)
	}
}

func TestDocChipRefs(t *testing.T) {
	// Document attachment → "pdf:<name>" chip.
	in := "look at this\n\n" + agent.AttachmentNote("/home/u/.octo/uploads/1720000000000000000_report.pdf")
	cleaned, refs := docChipRefs(in)
	if cleaned != "look at this" {
		t.Errorf("cleaned = %q, want %q", cleaned, "look at this")
	}
	if len(refs) != 1 || refs[0] != "pdf:report.pdf" {
		t.Errorf("refs = %v, want [pdf:report.pdf]", refs)
	}

	// Image attachment persisted under uploads → "/api/uploads/" thumbnail URL.
	in = "look at this\n\n" + agent.AttachmentNote("/home/u/.octo/uploads/1720000000000000000_shot.jpg")
	cleaned, refs = docChipRefs(in)
	if cleaned != "look at this" {
		t.Errorf("cleaned = %q, want %q", cleaned, "look at this")
	}
	if len(refs) != 1 || refs[0] != "/api/uploads/1720000000000000000_shot.jpg" {
		t.Errorf("refs = %v, want [/api/uploads/1720000000000000000_shot.jpg]", refs)
	}

	// Non-upload local image path stays a document chip (not served by /api/uploads/).
	in = "look at this\n\n" + agent.AttachmentNote("/tmp/local_shot.jpg")
	cleaned, refs = docChipRefs(in)
	if len(refs) != 1 || refs[0] != "pdf:local_shot.jpg" {
		t.Errorf("refs = %v, want [pdf:local_shot.jpg]", refs)
	}
}

func TestParseUserFiles_SkipsBadEntries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	att := parseUserFiles([]wsUserFile{
		{Name: "bad.jpg", DataURL: "not-a-data-url"},
		{Name: "evil.pdf", Path: "/api/uploads/does-not-exist.pdf"},
		{Name: "text.txt", DataURL: "data:text/plain;base64,aGk="}, // non-image data URL
	}, false, true)
	if len(att.blocks) != 0 || len(att.images) != 0 || len(att.notes) != 0 {
		t.Errorf("bad entries not skipped: %+v", att)
	}
}

// A persisted image attachment note for a non-vision model must replay as a
// thumbnail ref (/api/uploads/<name>) rather than a pdf: document chip.
func TestHandleGetSessionMessages_ImageAttachmentReplay(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := agent.NewSession("deepseek-v4-pro", "")
	sess.Title = "fixed"
	sess.Messages = []agent.Message{{
		Role:    agent.RoleUser,
		Content: "analyze this\n\n" + agent.AttachmentNote("/home/u/.octo/uploads/9_shot.jpg"),
	}}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess.ID+"/messages", nil)
	req.SetPathValue("id", sess.ID)
	rec := httptest.NewRecorder()
	srv.handleGetSessionMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("messages endpoint = %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, ev := range body.Events {
		if ev["type"] != "history_user_message" {
			continue
		}
		found = true
		if got := ev["content"]; got != "analyze this" {
			t.Errorf("content = %q, want %q (note not stripped)", got, "analyze this")
		}
		imgs, _ := ev["images"].([]any)
		if len(imgs) != 1 || imgs[0] != "/api/uploads/9_shot.jpg" {
			t.Errorf("images = %v, want [/api/uploads/9_shot.jpg]", ev["images"])
		}
	}
	if !found {
		t.Fatalf("no history_user_message event: %+v", body.Events)
	}
}

// A persisted document attachment (only an "[Attached file: <abspath>]" note in
// the text, no image block) must replay with the note stripped from the visible
// content and re-derived into a "pdf:<name>" chip ref — so a reloaded transcript
// matches what the live turn showed. Image notes instead become
// "/api/uploads/<name>" thumbnail refs.
func TestHandleGetSessionMessages_DocAttachmentReplay(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed"
	sess.Messages = []agent.Message{{
		Role:    agent.RoleUser,
		Content: "analyze this\n\n" + agent.AttachmentNote("/home/u/.octo/uploads/9_data.csv"),
	}}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess.ID+"/messages", nil)
	req.SetPathValue("id", sess.ID)
	rec := httptest.NewRecorder()
	srv.handleGetSessionMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("messages endpoint = %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, ev := range body.Events {
		if ev["type"] != "history_user_message" {
			continue
		}
		found = true
		if got := ev["content"]; got != "analyze this" {
			t.Errorf("content = %q, want %q (note not stripped)", got, "analyze this")
		}
		imgs, _ := ev["images"].([]any)
		if len(imgs) != 1 || imgs[0] != "pdf:9_data.csv" {
			t.Errorf("images = %v, want [pdf:9_data.csv]", ev["images"])
		}
	}
	if !found {
		t.Fatalf("no history_user_message event: %+v", body.Events)
	}
}

func TestHandleGetUpload(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir, err := ensureUploadsDir()
	if err != nil {
		t.Fatalf("uploads dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1_pic.jpg"), []byte("img-bytes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	ts := httptest.NewServer(srv.http.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/uploads/1_pic.jpg")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// Traversal must be rejected.
	for _, bad := range []string{"..%2F..%2Fsecret", "a%2Fb.jpg"} {
		resp, err := http.Get(ts.URL + "/api/uploads/" + bad)
		if err != nil {
			t.Fatalf("get %s: %v", bad, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("traversal %q served with 200", bad)
		}
	}
}

func TestHandleGetUpload_HTMLAttachmentForcesDownload(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir, err := ensureUploadsDir()
	if err != nil {
		t.Fatalf("uploads dir: %v", err)
	}
	for _, name := range []string{"1_xss.html", "2_xss.htm", "3_xss.js", "4_xss.mjs"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("<script>alert(1)</script>"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Non-executable file should not get Content-Disposition.
	if err := os.WriteFile(filepath.Join(dir, "5_note.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	ts := httptest.NewServer(srv.http.Handler)
	defer ts.Close()

	for _, name := range []string{"1_xss.html", "2_xss.htm", "3_xss.js", "4_xss.mjs"} {
		resp, err := http.Get(ts.URL + "/api/uploads/" + name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Content-Disposition"); got != "attachment" {
			t.Errorf("%s Content-Disposition = %q, want attachment", name, got)
		}
	}

	resp, err := http.Get(ts.URL + "/api/uploads/5_note.txt")
	if err != nil {
		t.Fatalf("get txt: %v", err)
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Disposition") != "" {
		t.Errorf("txt Content-Disposition = %q, want empty", resp.Header.Get("Content-Disposition"))
	}
}

// TestHandleWSUserMessage_ImageOnly is the regression guard for the web-UI
// "sent an image, nothing happened" bug: handleWSUserMessage ignored
// msg.Files entirely and returned early on empty text, silently dropping
// image-only messages.
func TestHandleWSUserMessage_ImageOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title" // suppress async title generation so Windows
	// cleanup doesn't race a fire-and-forget title goroutine writing the file.
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sess.ID)

	payload := []byte{0xFF, 0xD8, 0xFF, 9, 9}
	srv.handleWSUserMessage(conn, &wsMsgUserMessage{
		SessionID: sess.ID,
		Content:   json.RawMessage(`""`),
		Files: []wsUserFile{
			{Name: "shot.jpg", MimeType: "image/jpeg", DataURL: jpegDataURL(payload)},
		},
	})

	// Drain broadcasts until the turn completes; the user-message event must
	// carry the thumbnail URL.
	var gotImages bool
	deadline := time.After(5 * time.Second)
drain:
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			switch ev["type"] {
			case "history_user_message":
				if imgs, ok := ev["images"].([]any); ok && len(imgs) == 1 {
					url, _ := imgs[0].(string)
					if strings.HasPrefix(url, "/api/uploads/") {
						gotImages = true
					}
				}
			case "complete":
				break drain
			}
		case <-deadline:
			t.Fatal("turn did not complete — image-only message still dropped?")
		}
	}
	if !gotImages {
		t.Error("history_user_message carried no /api/uploads/ image ref")
	}

	// The persisted session must carry the image block with its file path,
	// rehydrated from disk. doAgentTurn persists before broadcasting "complete",
	// but the turn runs on a background goroutine and the assert reads a file
	// that goroutine just wrote; poll briefly so a loaded CI runner's scheduling
	// jitter can't flake the read (it succeeds on the first iteration normally).
	hasRehydratableImage := func() bool {
		loaded, err := agent.LoadSession(sess.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		for _, m := range loaded.Messages {
			for _, blk := range m.Blocks {
				if blk.Type == "image" && blk.ImagePath != "" && blk.Image != nil {
					return true
				}
			}
		}
		return false
	}
	foundBlock := hasRehydratableImage()
	for deadline := time.Now().Add(2 * time.Second); !foundBlock && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		foundBlock = hasRehydratableImage()
	}
	if !foundBlock {
		t.Error("no rehydratable image block persisted in session")
	}

	// Wait for the background turn goroutine to fully unwind before returning.
	// It flips turnRunning back to false only after runAgentTurnLoop — and every
	// Save it makes — has returned, so this is the point past which no goroutine
	// still holds a session-file handle. Without the barrier, t.TempDir()'s
	// RemoveAll races that handle on Windows and fails the test with "directory
	// is not empty" even though every assertion above passed.
	turnMu := srv.sessionTurnLock(sess.ID)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		turnMu.Lock()
		running := srv.turnRunning[sess.ID]
		turnMu.Unlock()
		if !running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestHandleWSUserMessage_ImageOnly_NonVision verifies that when the active
// model is text-only, an image-only WS message is not dropped and is surfaced
// to the model as a path note rather than an image block that the provider
// would reject.
func TestHandleWSUserMessage_ImageOnly_NonVision(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("deepseek-v4-pro", "")
	sess.Title = "fixed title"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sess.ID)

	payload := []byte{0xFF, 0xD8, 0xFF, 9, 9}
	srv.handleWSUserMessage(conn, &wsMsgUserMessage{
		SessionID: sess.ID,
		Content:   json.RawMessage(`""`),
		Files: []wsUserFile{
			{Name: "shot.jpg", MimeType: "image/jpeg", DataURL: jpegDataURL(payload)},
		},
	})

	var gotImage bool
	deadline := time.After(5 * time.Second)
drain:
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			switch ev["type"] {
			case "history_user_message":
				if imgs, ok := ev["images"].([]any); ok {
					if len(imgs) != 1 {
						t.Errorf("images = %v, want length 1", imgs)
					}
					for _, img := range imgs {
						ref, _ := img.(string)
						if strings.HasPrefix(ref, "/api/uploads/") {
							gotImage = true
						}
					}
				}
			case "complete":
				break drain
			}
		case <-deadline:
			t.Fatal("turn did not complete — non-vision image-only message still dropped?")
		}
	}
	if !gotImage {
		t.Error("history_user_message carried no /api/uploads/ image ref for the persisted image")
	}

	// The persisted message should carry a path note, not an image block.
	loaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var hasNote, hasBlock bool
	for _, m := range loaded.Messages {
		if m.Role != agent.RoleUser {
			continue
		}
		if strings.Contains(m.Content, "[Attached file:") && strings.Contains(filepath.ToSlash(m.Content), ".octo/uploads") {
			hasNote = true
		}
		for _, blk := range m.Blocks {
			if blk.Type == "image" {
				hasBlock = true
			}
		}
	}
	if !hasNote {
		t.Error("persisted user message missing the image path note")
	}
	if hasBlock {
		t.Error("non-vision model persisted an image block")
	}

	turnMu := srv.sessionTurnLock(sess.ID)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		turnMu.Lock()
		running := srv.turnRunning[sess.ID]
		turnMu.Unlock()
		if !running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestParseUserFilesLocalPath: a real local path is referenced in place only
// for a loopback (same-machine) client; for a remote client it's ignored so it
// can't make the agent read arbitrary server files.
func TestParseUserFilesLocalPath(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "notes.md")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	att := parseUserFiles([]wsUserFile{{Name: "notes.md", LocalPath: f}}, true, true)
	if len(att.notes) != 1 || !strings.Contains(att.notes[0], f) {
		t.Errorf("local path should be referenced in place for loopback, got notes=%+v", att.notes)
	}

	att2 := parseUserFiles([]wsUserFile{{Name: "notes.md", LocalPath: f}}, false, true)
	if len(att2.notes) != 0 {
		t.Errorf("local path must be ignored for a non-loopback client, got notes=%+v", att2.notes)
	}
}
