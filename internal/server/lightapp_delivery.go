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

	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Delivering a file into a running Light App ─────────────────────────────
//
// The other direction from the state mirror, and the only one that reaches
// *into* an app. The agent makes something — a generated image — and wants it
// where the user is already working rather than in a path they have to go and
// find.
//
// The tool cannot hand bytes to an iframe: it runs in this process, the frame
// runs in a browser, and the frame's own origin cannot fetch this API anyway.
// So delivery is a claim ticket:
//
//	insert_into_lightapp → registers (id → file) → WS event {slug, id, note}
//	                     → web UI fetches the bytes → postMessage into the frame
//
// The bytes travel over HTTP, not over the WS event, because a generated image
// is large and the socket carries conversation traffic.
//
// The app receives a file and a note and does what it likes with them. Nothing
// comes back on this path — an app that takes the image says so the way it says
// anything else, by pushing its own state.

const (
	// A ticket is redeemed within a frame or two of the event. The window only
	// needs to outlast a slow render, not a session.
	lightAppDeliveryTTL = 2 * time.Minute
	maxLightAppDelivery = 32 << 20
)

// deliverableImageTypes mirrors what agent.NewImageBlock accepts on the way in,
// so a file the model could look at is exactly a file it can hand back.
var deliverableImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

type lightAppDelivery struct {
	slug    string
	path    string
	ctype   string
	created time.Time
}

var lightAppDeliveries struct {
	mu sync.Mutex
	by map[string]*lightAppDelivery
}

// installLightAppDeliverer wires the tool to this server. Called once per
// server; a CLI session never calls it, and the tool then reports that Light
// Apps are out of reach rather than silently doing nothing.
func (s *Server) installLightAppDeliverer() {
	tools.SetLightAppDeliverer(s.deliverToLightApp)
}

// deliverToLightApp validates the file and announces it to the browser.
func (s *Server) deliverToLightApp(slug, path, note string) error {
	abs, ok := resolveArtifactPath(path)
	if !ok {
		return fmt.Errorf("invalid path %q", path)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", filepath.Base(abs), err)
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory", filepath.Base(abs))
	}
	if fi.Size() > maxLightAppDelivery {
		return fmt.Errorf("%s is too large to deliver (%d bytes)", filepath.Base(abs), fi.Size())
	}
	// Images only, for now: an app receiving an arbitrary file has no way to
	// know what to do with it, and widening this later is easier than
	// narrowing it.
	//
	// The set is the raster formats agent.NewImageBlock can sniff, not every
	// "image/*" the extension table knows — so the two directions agree on what
	// an image is. It also keeps SVG out, which is a document that carries
	// script, not a picture.
	ctype, known := tools.ArtifactContentType(abs)
	if !known || !deliverableImageTypes[ctype] {
		return fmt.Errorf("%s is not an image octo can deliver", filepath.Base(abs))
	}

	id, err := newDeliveryID()
	if err != nil {
		return err
	}
	lightAppDeliveries.mu.Lock()
	if lightAppDeliveries.by == nil {
		lightAppDeliveries.by = map[string]*lightAppDelivery{}
	}
	now := time.Now()
	for k, d := range lightAppDeliveries.by {
		if now.Sub(d.created) > lightAppDeliveryTTL {
			delete(lightAppDeliveries.by, k)
		}
	}
	lightAppDeliveries.by[id] = &lightAppDelivery{slug: slug, path: abs, ctype: ctype, created: now}
	lightAppDeliveries.mu.Unlock()

	s.broadcastGlobal(map[string]any{
		"type": "lightapp_delivery",
		"slug": slug,
		"id":   id,
		"name": filepath.Base(abs),
		"note": note,
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

// handleGetLightAppDelivery redeems a ticket. Single use: the web UI reads it
// once and hands the bytes to the frame, and a ticket left unredeemed expires
// on its own.
func (s *Server) handleGetLightAppDelivery(w http.ResponseWriter, r *http.Request) {
	slug, id := r.PathValue("slug"), r.PathValue("id")

	lightAppDeliveries.mu.Lock()
	d := lightAppDeliveries.by[id]
	// The slug has to match the ticket: a delivery aimed at one app must not
	// be readable by naming another.
	if d != nil && d.slug == slug && time.Since(d.created) <= lightAppDeliveryTTL {
		delete(lightAppDeliveries.by, id)
	} else {
		d = nil
	}
	lightAppDeliveries.mu.Unlock()

	if d == nil {
		writeError(w, http.StatusNotFound, "delivery_not_found")
		return
	}
	data, err := os.ReadFile(d.path)
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
