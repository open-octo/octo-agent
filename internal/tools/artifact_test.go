package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShowArtifact_HappyPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bundle.html")
	if err := os.WriteFile(p, []byte("<h1>x</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := (ShowArtifactTool{}).Execute(context.Background(), "show_artifact", map[string]any{"path": p})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	ui, ok := res.UI.(map[string]any)
	if !ok {
		t.Fatalf("UI payload type %T", res.UI)
	}
	if ui["type"] != "artifact" || ui["path"] != p {
		t.Errorf("ui = %+v", ui)
	}
	if !strings.Contains(res.Text, p) {
		t.Errorf("Text = %q, want it to carry the path", res.Text)
	}
}

func TestShowArtifact_Errors(t *testing.T) {
	dir := t.TempDir()
	binFile := filepath.Join(dir, "tool.exe")
	if err := os.WriteFile(binFile, []byte{0x4d, 0x5a}, 0o644); err != nil {
		t.Fatal(err)
	}
	// A source file: readable text, but not an artifact kind the panel lists.
	srcFile := filepath.Join(dir, "script.py")
	if err := os.WriteFile(srcFile, []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		input map[string]any
	}{
		{"missing path", map[string]any{}},
		{"nonexistent file", map[string]any{"path": filepath.Join(dir, "nope.html")}},
		{"non-previewable extension", map[string]any{"path": binFile}},
		{"source file", map[string]any{"path": srcFile}},
		{"directory", map[string]any{"path": dir + string(filepath.Separator) + "sub.html"}},
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub.html"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if _, err := (ShowArtifactTool{}).Execute(context.Background(), "show_artifact", c.input); err == nil {
			t.Errorf("%s: want error", c.name)
		}
	}
}

func TestArtifactContentType(t *testing.T) {
	if ct, ok := ArtifactContentType("/x/y/Report.MD"); !ok || !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("case-insensitive ext: ct=%q ok=%v", ct, ok)
	}
	if _, ok := ArtifactContentType("/x/y/binary.exe"); ok {
		t.Error("exe should not be previewable")
	}
	// Source, config, and data files are not artifacts: the panel only lists
	// what it can render, and code changes belong to its Git Diff mode.
	for _, p := range []string{"/x/script.py", "/x/main.go", "/x/app.js", "/x/data.csv", "/x/notes.TXT", "/x/config.json"} {
		if ct, ok := ArtifactContentType(p); ok {
			t.Errorf("%s: ct=%q, want not previewable", p, ct)
		}
	}
}
