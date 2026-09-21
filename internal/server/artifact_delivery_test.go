package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pngFixture writes a real PNG so the content-type gate sees an image.
func pngFixture(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "gen.png")
	// 1×1 transparent PNG.
	data := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
		0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
		0, 0, 0, 0x0a, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1,
		0x0d, 0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// artifactDeliveryFixture: a server, a session that wrote one HTML artifact,
// and a real PNG to deliver.
type artifactDeliveryFixture struct {
	srv       *Server
	sessionID string
	page      string
	png       string
}

func newArtifactDeliveryFixture(t *testing.T) *artifactDeliveryFixture {
	t.Helper()
	dir := t.TempDir()
	page := dir + "/page.html"
	sid := newArtifactSession(t, page)
	return &artifactDeliveryFixture{
		srv:       mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false}),
		sessionID: sid,
		page:      page,
		png:       pngFixture(t, dir),
	}
}

func resetArtifactDeliveries(t *testing.T) {
	t.Helper()
	artifactDeliveries.mu.Lock()
	artifactDeliveries.by = nil
	artifactDeliveries.mu.Unlock()
}

func lastArtifactDeliveryID(t *testing.T) string {
	t.Helper()
	artifactDeliveries.mu.Lock()
	defer artifactDeliveries.mu.Unlock()
	if len(artifactDeliveries.by) != 1 {
		t.Fatalf("expected exactly one delivery, got %d", len(artifactDeliveries.by))
	}
	for id := range artifactDeliveries.by {
		return id
	}
	return ""
}

func (f *artifactDeliveryFixture) redeem(t *testing.T, session, ticket string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/sessions/"+session+"/artifacts/delivery/"+ticket, nil)
	w := httptest.NewRecorder()
	serveLoopback(f.srv.mux, w, req)
	return w
}

func TestArtifactDelivery_RoundTrip(t *testing.T) {
	resetArtifactDeliveries(t)
	f := newArtifactDeliveryFixture(t)

	if err := f.srv.deliverToArtifact(f.sessionID, f.page, f.png, "a generated sketch"); err != nil {
		t.Fatal(err)
	}
	ticket := lastArtifactDeliveryID(t)

	w := f.redeem(t, f.sessionID, ticket)
	if w.Code != 200 {
		t.Fatalf("redeem: %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("content type %q", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Error("delivery bytes must not be openable as a document")
	}
}

// TestArtifactDelivery_SessionMustMatch: a ticket minted for one session's
// page must not be redeemable through another session's URL — the ticket is
// bound to (session, path), not just to the path.
func TestArtifactDelivery_SessionMustMatch(t *testing.T) {
	resetArtifactDeliveries(t)
	f := newArtifactDeliveryFixture(t)
	if err := f.srv.deliverToArtifact(f.sessionID, f.page, f.png, ""); err != nil {
		t.Fatal(err)
	}
	ticket := lastArtifactDeliveryID(t)

	other := newArtifactSession(t, f.page)
	if w := f.redeem(t, other, ticket); w.Code != 404 {
		t.Fatalf("another session redeemed the ticket: %d", w.Code)
	}
}

func TestArtifactDelivery_TicketIsSingleUse(t *testing.T) {
	resetArtifactDeliveries(t)
	f := newArtifactDeliveryFixture(t)
	if err := f.srv.deliverToArtifact(f.sessionID, f.page, f.png, ""); err != nil {
		t.Fatal(err)
	}
	ticket := lastArtifactDeliveryID(t)
	if w := f.redeem(t, f.sessionID, ticket); w.Code != 200 {
		t.Fatalf("first redeem: %d", w.Code)
	}
	if w := f.redeem(t, f.sessionID, ticket); w.Code != 404 {
		t.Fatalf("second redeem: %d", w.Code)
	}
}

func TestArtifactDelivery_ExpiredTicket(t *testing.T) {
	resetArtifactDeliveries(t)
	f := newArtifactDeliveryFixture(t)
	if err := f.srv.deliverToArtifact(f.sessionID, f.page, f.png, ""); err != nil {
		t.Fatal(err)
	}
	ticket := lastArtifactDeliveryID(t)
	artifactDeliveries.mu.Lock()
	artifactDeliveries.by[ticket].created = time.Now().Add(-2 * artifactDeliveryTTL)
	artifactDeliveries.mu.Unlock()

	if w := f.redeem(t, f.sessionID, ticket); w.Code != 404 {
		t.Fatalf("an expired ticket redeemed: %d", w.Code)
	}
}

// TestArtifactDelivery_ForeignPage: the destination must be an artifact this
// session wrote — otherwise a delivery names bytes into a page nobody
// vouched for.
func TestArtifactDelivery_ForeignPage(t *testing.T) {
	resetArtifactDeliveries(t)
	f := newArtifactDeliveryFixture(t)
	err := f.srv.deliverToArtifact(f.sessionID, t.TempDir()+"/not-written.html", f.png, "")
	if err == nil {
		t.Fatal("a page the session never wrote accepted a delivery")
	}
}

func TestArtifactDelivery_RejectsNonImages(t *testing.T) {
	resetArtifactDeliveries(t)
	f := newArtifactDeliveryFixture(t)
	for _, name := range []string{"x.txt", "x.svg", "x.html"} {
		p := t.TempDir() + "/" + name
		if err := os.WriteFile(p, []byte("<x/>"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := f.srv.deliverToArtifact(f.sessionID, f.page, p, ""); err == nil {
			t.Errorf("%s was accepted for delivery", name)
		}
	}
}
