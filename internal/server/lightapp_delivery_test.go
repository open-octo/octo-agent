package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func resetDeliveries(t *testing.T) {
	t.Helper()
	lightAppDeliveries.mu.Lock()
	lightAppDeliveries.by = nil
	lightAppDeliveries.mu.Unlock()
}

// lastDeliveryID returns the one ticket the server just minted.
func lastDeliveryID(t *testing.T) string {
	t.Helper()
	lightAppDeliveries.mu.Lock()
	defer lightAppDeliveries.mu.Unlock()
	if len(lightAppDeliveries.by) != 1 {
		t.Fatalf("expected exactly one delivery, got %d", len(lightAppDeliveries.by))
	}
	for id := range lightAppDeliveries.by {
		return id
	}
	return ""
}

func TestDeliverToLightApp_RejectsNonImages(t *testing.T) {
	resetDeliveries(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	dir := t.TempDir()
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := srv.deliverToLightApp("sketch", txt, ""); err == nil {
		t.Fatal("a text file should not be deliverable")
	}
	if err := srv.deliverToLightApp("sketch", filepath.Join(dir, "missing.png"), ""); err == nil {
		t.Fatal("a missing file should not be deliverable")
	}
	if err := srv.deliverToLightApp("sketch", dir, ""); err == nil {
		t.Fatal("a directory should not be deliverable")
	}
}

// TestDelivery_TicketIsSingleUse: the web UI redeems it once; a replay must not
// keep the file readable.
func TestDelivery_TicketIsSingleUse(t *testing.T) {
	resetDeliveries(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	png := pngFixture(t, t.TempDir())

	if err := srv.deliverToLightApp("sketch", png, "a generated image"); err != nil {
		t.Fatal(err)
	}
	id := lastDeliveryID(t)

	w := doJSON(t, srv, "GET", "/api/light-apps/sketch/delivery/"+id, "")
	if w.Code != 200 {
		t.Fatalf("first redeem: expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Errorf("served as %q", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("no bytes came back")
	}

	w = doJSON(t, srv, "GET", "/api/light-apps/sketch/delivery/"+id, "")
	if w.Code == 200 {
		t.Error("the ticket was redeemable twice")
	}
}

// TestDelivery_SlugMustMatch: a delivery aimed at one app must not be readable
// by naming a different one.
func TestDelivery_SlugMustMatch(t *testing.T) {
	resetDeliveries(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	png := pngFixture(t, t.TempDir())

	if err := srv.deliverToLightApp("sketch", png, ""); err != nil {
		t.Fatal(err)
	}
	id := lastDeliveryID(t)

	if w := doJSON(t, srv, "GET", "/api/light-apps/board/delivery/"+id, ""); w.Code == 200 {
		t.Fatal("another app redeemed the ticket")
	}
	// And the real target still can — a failed attempt must not consume it.
	if w := doJSON(t, srv, "GET", "/api/light-apps/sketch/delivery/"+id, ""); w.Code != 200 {
		t.Fatalf("the intended app can no longer redeem it: %d", w.Code)
	}
}

func TestDelivery_ExpiredTicket(t *testing.T) {
	resetDeliveries(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	png := pngFixture(t, t.TempDir())

	if err := srv.deliverToLightApp("sketch", png, ""); err != nil {
		t.Fatal(err)
	}
	id := lastDeliveryID(t)

	lightAppDeliveries.mu.Lock()
	lightAppDeliveries.by[id].created = time.Now().Add(-2 * lightAppDeliveryTTL)
	lightAppDeliveries.mu.Unlock()

	if w := doJSON(t, srv, "GET", "/api/light-apps/sketch/delivery/"+id, ""); w.Code == 200 {
		t.Error("an expired ticket was still redeemable")
	}
}

func TestDelivery_UnknownTicket(t *testing.T) {
	resetDeliveries(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	req := httptest.NewRequest("GET", "/api/light-apps/sketch/delivery/deadbeef", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code == 200 {
		t.Error("an unknown ticket returned content")
	}
}
