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
	"time"
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

// TestLightAppState_NothingConnected: the tool stays useful when no app is
// open — it tells the model what to ask for instead of failing.
func TestLightAppState_NothingConnected(t *testing.T) {
	resetLightAppMirror(t)

	out := runTool(t, LightAppStateTool{}, nil)
	if !strings.Contains(out, "No Light App") {
		t.Fatalf("unexpected answer: %s", out)
	}
	if !strings.Contains(out, "Ask the user to open") {
		t.Error("the answer should tell the model how to get a canvas connected")
	}
}

// TestLightAppState_ReportsApps: an app's own digest is what the model reads.
func TestLightAppState_ReportsApps(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{
		Slug:    "sketch",
		Digest:  "3 strokes and one image, 1 shape selected",
		Summary: json.RawMessage(`{"nodes":4}`),
		Image:   tinyPNG(t),
	})
	PutLightApp(LightAppSnapshot{Slug: "board", Digest: "12 cards across 3 columns"})

	out := runTool(t, LightAppStateTool{}, nil)
	for _, want := range []string{
		"sketch", "3 strokes and one image", "has a screenshot",
		"board", "12 cards across 3 columns", `{"nodes":4}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("answer is missing %q:\n%s", want, out)
		}
	}
	// board published no image, so it must not advertise one.
	boardLine := out[strings.Index(out, "- board"):]
	if strings.Contains(boardLine[:strings.Index(boardLine, "\n")], "screenshot") {
		t.Error("board has no screenshot but the line claims one")
	}
}

// TestLightAppState_MarksStale: a snapshot the user has long moved past must
// not read as current.
func TestLightAppState_MarksStale(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{
		Slug:      "sketch",
		Digest:    "an old drawing",
		UpdatedAt: time.Now().Add(-2 * lightAppStaleAfter),
	})

	out := runTool(t, LightAppStateTool{}, nil)
	if !strings.Contains(out, "stale") {
		t.Fatalf("expected a staleness marker:\n%s", out)
	}
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
	if out := runTool(t, LightAppStateTool{}, nil); !strings.Contains(out, "No Light App") {
		t.Errorf("state still reports an app:\n%s", out)
	}
}

// TestViewLightApp_ReturnsAnImageBlock is the point of the whole feature: the
// sketch has to enter the conversation as an image, not as a description.
func TestViewLightApp_ReturnsAnImageBlock(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{
		Slug:      "sketch",
		Digest:    "a hand-drawn layout",
		Image:     tinyPNG(t),
		ImageType: "image/png",
	})

	res, err := (LightAppViewTool{}).Execute(context.Background(), "view_lightapp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 1 {
		t.Fatalf("expected one content block, got %d", len(res.Blocks))
	}
	if res.Blocks[0].Type != "image" {
		t.Errorf("block type is %q", res.Blocks[0].Type)
	}
	if !strings.Contains(res.Text, "a hand-drawn layout") {
		t.Errorf("the app's own description should travel with the image: %s", res.Text)
	}
}

// TestViewLightApp_NoImage: connected but nothing shown — say what to ask for.
func TestViewLightApp_NoImage(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{Slug: "sketch", Digest: "an empty canvas"})

	res, err := (LightAppViewTool{}).Execute(context.Background(), "view_lightapp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 0 {
		t.Error("there is no image to send")
	}
	if !strings.Contains(res.Text, "select") {
		t.Errorf("should ask the user to select something: %s", res.Text)
	}
}

// TestViewLightApp_AmbiguousWithoutSlug: two canvases open, so the model has
// to say which one rather than being handed an arbitrary pick.
func TestViewLightApp_AmbiguousWithoutSlug(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{Slug: "sketch", Image: tinyPNG(t), ImageType: "image/png"})
	PutLightApp(LightAppSnapshot{Slug: "board", Image: tinyPNG(t), ImageType: "image/png"})

	res, err := (LightAppViewTool{}).Execute(context.Background(), "view_lightapp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 0 {
		t.Error("an ambiguous view must not pick one")
	}
	if !strings.Contains(res.Text, "board") || !strings.Contains(res.Text, "sketch") {
		t.Errorf("both candidates should be named: %s", res.Text)
	}

	// Naming one resolves it.
	res, err = (LightAppViewTool{}).Execute(context.Background(), "view_lightapp", map[string]any{"slug": "board"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 1 {
		t.Fatalf("expected the named app's image, got %d blocks", len(res.Blocks))
	}
}

// TestViewLightApp_UnknownSlug: a name nobody is publishing is not an error,
// it is an answer that points at lightapp_state.
func TestViewLightApp_UnknownSlug(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{Slug: "sketch", Image: tinyPNG(t), ImageType: "image/png"})

	res, err := (LightAppViewTool{}).Execute(context.Background(), "view_lightapp", map[string]any{"slug": "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 0 || !strings.Contains(res.Text, "lightapp_state") {
		t.Errorf("unexpected answer: %s", res.Text)
	}
}

// TestLightAppTools_Registered: a tool the model cannot see does not exist.
func TestLightAppTools_Registered(t *testing.T) {
	want := map[string]bool{"lightapp_state": false, "view_lightapp": false}
	for _, tl := range allTools {
		if _, ok := want[tl.Definition().Name]; ok {
			want[tl.Definition().Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s is not in allTools", name)
		}
	}
}
