package tools

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

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

// runToolInSession is runTool with a session stamped, the way the server's
// turn entries stamp every real turn (see WithSessionID).
func runToolInSession(t *testing.T, tl tool, sid string, input map[string]any) string {
	t.Helper()
	res, err := tl.Execute(WithSessionID(context.Background(), sid), tl.Definition().Name, input)
	if err != nil {
		t.Fatalf("%s: %v", tl.Definition().Name, err)
	}
	return res.Text
}

// TestArtifactState_ListsCurrentSessionOnly: the whole point of the re-key —
// an agent reasoning about its own conversation's panel must never be shown
// another session's pages.
func TestArtifactState_ListsCurrentSessionOnly(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/mine.html", Digest: "my page"})
	PutArtifact(ArtifactSnapshot{Session: "s2", Path: "/tmp/theirs.html", Digest: "someone else's page"})

	out := runToolInSession(t, ArtifactStateTool{}, "s1", nil)
	if !strings.Contains(out, "/tmp/mine.html") {
		t.Errorf("own artifact missing:\n%s", out)
	}
	if strings.Contains(out, "/tmp/theirs.html") {
		t.Errorf("another session's artifact leaked:\n%s", out)
	}
}

// TestArtifactState_NothingOpen keeps the tool registered and honest when the
// panel shows no reporting page — the answer teaches the next step rather
// than pretending emptiness is an error.
func TestArtifactState_NothingOpen(t *testing.T) {
	resetArtifactMirror(t)
	out := runToolInSession(t, ArtifactStateTool{}, "s1", nil)
	if !strings.Contains(out, "No artifact") {
		t.Errorf("expected the nothing-open guidance, got:\n%s", out)
	}
}

// TestArtifactView_SingleDefault: one reporting page needs no path argument —
// the common case is the user looking at exactly one artifact.
func TestArtifactView_SingleDefault(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/a.html", Digest: "a chart", Image: tinyPNG(t), ImageType: "image/png"})

	res, err := ArtifactViewTool{}.Execute(WithSessionID(context.Background(), "s1"), "view_artifact", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 1 {
		t.Fatalf("expected the screenshot as an image block, got %+v", res.Blocks)
	}
	if !strings.Contains(res.Text, "/tmp/a.html") {
		t.Errorf("the answer should name what it shows:\n%s", res.Text)
	}
}

// TestArtifactView_UnknownPath: a path the session has no snapshot for gets a
// named refusal, not another session's page and not a crash.
func TestArtifactView_UnknownPath(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/a.html", Digest: "a chart", Image: tinyPNG(t), ImageType: "image/png"})

	out := runToolInSession(t, ArtifactViewTool{}, "s1", map[string]any{"artifact": "/tmp/nope.html"})
	if !strings.Contains(out, "/tmp/nope.html") || !strings.Contains(out, "artifact_state") {
		t.Errorf("expected a named refusal pointing at artifact_state:\n%s", out)
	}
}

// TestArtifactView_NoScreenshot: a page that pushes only text is connected
// but has nothing to show — say so, with its own digest as the hint.
func TestArtifactView_NoScreenshot(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/a.html", Digest: "a form, halfway filled"})

	res, err := ArtifactViewTool{}.Execute(WithSessionID(context.Background(), "s1"), "view_artifact", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 0 {
		t.Fatal("no screenshot, no image block")
	}
	if !strings.Contains(res.Text, "a form, halfway filled") {
		t.Errorf("expected the page's digest as the hint:\n%s", res.Text)
	}
}

// TestArtifactView_Ambiguous: several reporting pages and no path — list the
// choices rather than guessing.
func TestArtifactView_Ambiguous(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/a.html", Digest: "one"})
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/b.html", Digest: "two"})

	out := runToolInSession(t, ArtifactViewTool{}, "s1", nil)
	if !strings.Contains(out, "/tmp/a.html") || !strings.Contains(out, "/tmp/b.html") {
		t.Errorf("expected both paths listed:\n%s", out)
	}
}
