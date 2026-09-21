package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// resetLightAppMirror clears the process-wide mirror between cases.
func resetLightAppMirror(t *testing.T) {
	t.Helper()
	lightAppMirror.mu.Lock()
	lightAppMirror.apps = nil
	lightAppMirror.mu.Unlock()
}

// tinyPNG is a real image, because NewImageBlock sniffs the bytes and refuses
// anything it cannot hand to a provider.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := range 4 {
		for y := range 4 {
			img.Set(x, y, color.RGBA{R: uint8(40 * x), G: uint8(40 * y), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func runTool(t *testing.T, tl tool, input map[string]any) string {
	t.Helper()
	res, err := tl.Execute(context.Background(), tl.Definition().Name, input)
	if err != nil {
		t.Fatalf("%s: %v", tl.Definition().Name, err)
	}
	return res.Text
}

// TestPutLightApp_TrimsOversized: a snapshot is a courtesy from the app, so
// oversized parts are dropped rather than rejecting the whole push.
func TestPutLightApp_TrimsOversized(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{
		Slug:    "sketch",
		Digest:  strings.Repeat("x", maxLightAppDigest+500),
		Summary: json.RawMessage(strings.Repeat("y", maxLightAppSummary+10)),
		Image:   bytes.Repeat([]byte{0}, maxLightAppImage+10),
	})

	snap := lightAppSnapshot("sketch")
	if snap == nil {
		t.Fatal("snapshot was rejected outright")
	}
	if len(snap.Digest) > maxLightAppDigest+4 {
		t.Errorf("digest not truncated: %d", len(snap.Digest))
	}
	if snap.Summary != nil {
		t.Error("oversized summary should be dropped")
	}
	if snap.Image != nil {
		t.Error("oversized image should be dropped")
	}
}

// TestDropLightApp: closing the app removes it from what the model sees.
func TestDropLightApp(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{Slug: "sketch", Digest: "something"})
	DropLightApp("sketch")

	if lightAppSnapshot("sketch") != nil {
		t.Error("snapshot survived the drop")
	}
	if len(lightAppSnapshots()) != 0 {
		t.Error("snapshot still listed")
	}
}
