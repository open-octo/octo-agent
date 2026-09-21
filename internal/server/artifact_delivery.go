package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Delivering a file into an open artifact ────────────────────────────────
//
// The other direction from the state mirror, and the only one that reaches
// *into* a page. The agent makes something — a generated image — and wants it
// where the user is already working rather than in a path they have to go and
// find.
//
// The tool cannot hand bytes to an iframe: it runs in this process, the frame
// runs in a browser, and the frame's own origin cannot fetch this API anyway.
// So delivery is a claim ticket:
//
//	insert_into_artifact → registers (id → file) → WS event {session, path, id, note}
//	                     → web UI fetches the bytes → postMessage into the frame
//
// The bytes travel over HTTP, not over the WS event, because a generated image
// is large and the socket carries conversation traffic.
//
// The page receives a file and a note and does what it likes with them.
// Nothing comes back on this path — a page that takes the image says so the
// way it says anything else, by pushing its own state.

const (
	// A ticket is redeemed within a frame or two of the event. The window only
	// needs to outlast a slow render, not a session.
	artifactDeliveryTTL = 2 * time.Minute
	maxArtifactDelivery = 32 << 20
)

// deliverableImageTypes mirrors what agent.NewImageBlock accepts on the way
// in, so a file the model could look at is exactly a file it can hand back.
var deliverableImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

type artifactDelivery struct {
	session      string
	artifactPath string
	filePath     string
	ctype        string
	created      time.Time
}

var artifactDeliveries struct {
	mu sync.Mutex
	by map[string]*artifactDelivery
}

// installArtifactDeliverer wires the tool to this server. Called once per
// server; a CLI session never calls it, and the tool then reports that the
// panel is out of reach rather than silently doing nothing.
func (s *Server) installArtifactDeliverer() {
	tools.SetArtifactDeliverer(s.deliverToArtifact)
}

// deliverToArtifact validates the file and announces it to the browser. The
// destination must be an artifact this session wrote — the same whitelist the
// preview and the state relay enforce — because the ticket and the broadcast
// both name it.
func (s *Server) deliverToArtifact(session, artifactPath, filePath, note string) error {
	sess, err := agent.LoadSession(session)
	if err != nil {
		return fmt.Errorf("session not found")
	}
	if _, ok := sessionWrotePath(sess, artifactPath); !ok {
		return fmt.Errorf("%s is not an artifact of this session", filepath.Base(artifactPath))
	}

	abs, ok := resolveArtifactPath(filePath)
	if !ok {
		return fmt.Errorf("invalid path %q", filePath)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", filepath.Base(abs), err)
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory", filepath.Base(abs))
	}
	if fi.Size() > maxArtifactDelivery {
		return fmt.Errorf("%s is too large to deliver (%d bytes)", filepath.Base(abs), fi.Size())
	}
	// Images only, for now: a page receiving an arbitrary file has no way to
	// know what to do with it, and widening this later is easier than
	// narrowing it.
	//
	// The set is the raster formats agent.NewImageBlock can sniff, not every
	// "image/*" the extension table knows — so the two directions agree on
	// what an image is. It also keeps SVG out, which is a document that
	// carries script, not a picture.
	ctype, known := tools.ArtifactContentType(abs)
	if !known || !deliverableImageTypes[ctype] {
		return fmt.Errorf("%s is not an image octo can deliver", filepath.Base(abs))
	}

	id, err := newDeliveryID()
	if err != nil {
		return err
	}
	artifactDeliveries.mu.Lock()
	if artifactDeliveries.by == nil {
		artifactDeliveries.by = map[string]*artifactDelivery{}
	}
	now := time.Now()
	for k, d := range artifactDeliveries.by {
		if now.Sub(d.created) > artifactDeliveryTTL {
			delete(artifactDeliveries.by, k)
		}
	}
	artifactDeliveries.by[id] = &artifactDelivery{session: session, artifactPath: artifactPath, filePath: abs, ctype: ctype, created: now}
	artifactDeliveries.mu.Unlock()

	s.broadcastGlobal(map[string]any{
		"type":    "artifact_delivery",
		"session": session,
		"path":    artifactPath,
		"id":      id,
		"name":    filepath.Base(abs),
		"note":    note,
	})
	return nil
}

func newDeliveryID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// handleGetArtifactDelivery redeems a ticket. Single use: the web UI reads it
// once and hands the bytes to the frame, and a ticket left unredeemed expires
// on its own. The session in the URL must be the ticket's: a delivery aimed
// at one session's page must not be readable through another's.
func (s *Server) handleGetArtifactDelivery(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("id"), r.PathValue("ticket")

	artifactDeliveries.mu.Lock()
	d := artifactDeliveries.by[id]
	if d != nil && d.session == session && time.Since(d.created) <= artifactDeliveryTTL {
		delete(artifactDeliveries.by, id)
	} else {
		d = nil
	}
	artifactDeliveries.mu.Unlock()

	if d == nil {
		writeError(w, http.StatusNotFound, "delivery_not_found")
		return
	}
	data, err := os.ReadFile(d.filePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "delivery_unreadable")
		return
	}
	w.Header().Set("Content-Type", d.ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	// Defense in depth for a URL opened directly in a tab, the same reasoning
	// (and header) as the artifact endpoint: the bytes are only ever meant to
	// be read by fetch and handed to a frame.
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
