package tools

import (
	"context"
	"strings"
	"testing"
)

// captureDeliverer installs a recording deliverer for the duration of a test.
func captureDeliverer(t *testing.T) *[]string {
	t.Helper()
	var got []string
	SetLightAppDeliverer(func(slug, path, note string) error {
		got = append(got, slug+"|"+path+"|"+note)
		return nil
	})
	t.Cleanup(func() { SetLightAppDeliverer(nil) })
	return &got
}

func runInsert(t *testing.T, input map[string]any) string {
	t.Helper()
	res, err := (LightAppInsertTool{}).Execute(context.Background(), "insert_into_lightapp", input)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	return res.Text
}

// TestInsert_NoDeliverer: a CLI session has no UI to deliver into, and the tool
// says so rather than reporting a delivery that never happened.
func TestInsert_NoDeliverer(t *testing.T) {
	resetLightAppMirror(t)
	SetLightAppDeliverer(nil)
	PutLightApp(LightAppSnapshot{Slug: "sketch"})

	out := runInsert(t, map[string]any{"path": "/tmp/x.png"})
	if !strings.Contains(out, "no") && !strings.Contains(out, "none attached") {
		t.Fatalf("unexpected answer: %s", out)
	}
	if strings.Contains(out, "Sent to") {
		t.Error("claimed a delivery with no deliverer installed")
	}
}

// TestInsert_NoAppOpen: nowhere to put it, so say that instead of failing.
func TestInsert_NoAppOpen(t *testing.T) {
	resetLightAppMirror(t)
	got := captureDeliverer(t)

	out := runInsert(t, map[string]any{"path": "/tmp/x.png"})
	if !strings.Contains(out, "No Light App is open") {
		t.Fatalf("unexpected answer: %s", out)
	}
	if len(*got) != 0 {
		t.Error("delivered to nobody")
	}
}

// TestInsert_SingleAppNeedsNoSlug: one app open is unambiguous.
func TestInsert_SingleAppNeedsNoSlug(t *testing.T) {
	resetLightAppMirror(t)
	got := captureDeliverer(t)
	PutLightApp(LightAppSnapshot{Slug: "sketch"})

	out := runInsert(t, map[string]any{"path": "/tmp/gen.png", "note": "from your sketch"})
	if !strings.Contains(out, "Sent to sketch") {
		t.Fatalf("unexpected answer: %s", out)
	}
	if len(*got) != 1 || (*got)[0] != "sketch|/tmp/gen.png|from your sketch" {
		t.Fatalf("delivery args: %v", *got)
	}
}

// TestInsert_AmbiguousWithoutSlug: two apps open, so the model has to choose
// rather than have one picked for it.
func TestInsert_AmbiguousWithoutSlug(t *testing.T) {
	resetLightAppMirror(t)
	got := captureDeliverer(t)
	PutLightApp(LightAppSnapshot{Slug: "sketch"})
	PutLightApp(LightAppSnapshot{Slug: "board"})

	out := runInsert(t, map[string]any{"path": "/tmp/x.png"})
	if len(*got) != 0 {
		t.Fatal("picked one of two apps on its own")
	}
	if !strings.Contains(out, "sketch") || !strings.Contains(out, "board") {
		t.Errorf("both candidates should be named: %s", out)
	}

	out = runInsert(t, map[string]any{"path": "/tmp/x.png", "slug": "board"})
	if !strings.Contains(out, "Sent to board") || len(*got) != 1 {
		t.Errorf("naming one should resolve it: %s", out)
	}
}

// TestInsert_UnknownSlug: an app that is not open is an answer, not an error.
func TestInsert_UnknownSlug(t *testing.T) {
	resetLightAppMirror(t)
	got := captureDeliverer(t)
	PutLightApp(LightAppSnapshot{Slug: "sketch"})

	out := runInsert(t, map[string]any{"path": "/tmp/x.png", "slug": "nope"})
	if len(*got) != 0 {
		t.Fatal("delivered to an app that is not open")
	}
	if !strings.Contains(out, "lightapp_state") {
		t.Errorf("should point at lightapp_state: %s", out)
	}
}

func TestInsert_RequiresPath(t *testing.T) {
	resetLightAppMirror(t)
	captureDeliverer(t)
	PutLightApp(LightAppSnapshot{Slug: "sketch"})

	if _, err := (LightAppInsertTool{}).Execute(context.Background(), "insert_into_lightapp", nil); err == nil {
		t.Fatal("expected an error without a path")
	}
}

func TestInsert_Registered(t *testing.T) {
	for _, tl := range allTools {
		if tl.Definition().Name == "insert_into_lightapp" {
			return
		}
	}
	t.Error("insert_into_lightapp is not in allTools")
}
