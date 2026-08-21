package server

import "testing"

func TestParseUnifiedDiff(t *testing.T) {
	out := `diff --git a/web/src/lib/stores.ts b/web/src/lib/stores.ts
index 1111111..2222222 100644
--- a/web/src/lib/stores.ts
+++ b/web/src/lib/stores.ts
@@ -88,6 +88,8 @@ export const artifactSel = writable(0)
 context one
-removed line
+added line
+added two
 context two
diff --git a/old name.txt b/new name.txt
similarity index 90%
rename from old name.txt
rename to new name.txt
--- a/old name.txt
+++ b/new name.txt
@@ -1 +1 @@
-one
+two
diff --git a/logo.png b/logo.png
index 3333333..4444444 100644
Binary files a/logo.png and b/logo.png differ
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 5555555..0000000
--- a/gone.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-bye
-now
`
	patches := parseUnifiedDiff(out)
	if len(patches) != 4 {
		t.Fatalf("patches = %d, want 4", len(patches))
	}

	// Modification: counts and hunk contents.
	p := patches[0]
	if p.newPath != "web/src/lib/stores.ts" || p.oldPath != "web/src/lib/stores.ts" {
		t.Errorf("paths = %q / %q", p.oldPath, p.newPath)
	}
	if p.adds != 2 || p.dels != 1 || p.totalLines != 5 {
		t.Errorf("adds/dels/total = %d/%d/%d, want 2/1/5", p.adds, p.dels, p.totalLines)
	}
	if len(p.hunks) != 1 || p.hunks[0].Header != "@@ -88,6 +88,8 @@ export const artifactSel = writable(0)" {
		t.Fatalf("hunks = %+v", p.hunks)
	}
	want := []diffLine{
		{Kind: "context", Content: "context one"},
		{Kind: "del", Content: "removed line"},
		{Kind: "add", Content: "added line"},
		{Kind: "add", Content: "added two"},
		{Kind: "context", Content: "context two"},
	}
	for i, w := range want {
		if got := p.hunks[0].Lines[i]; got != w {
			t.Errorf("line %d = %+v, want %+v", i, got, w)
		}
	}

	// Rename with spaces in both names: the ---/+++ pair keeps each side whole,
	// which the `diff --git a/x b/y` header could not.
	if patches[1].oldPath != "old name.txt" || patches[1].newPath != "new name.txt" {
		t.Errorf("rename paths = %q / %q", patches[1].oldPath, patches[1].newPath)
	}

	// Binary: flagged, no hunks.
	if !patches[2].binary || len(patches[2].hunks) != 0 {
		t.Errorf("binary patch = %+v", patches[2])
	}
	if patches[2].newPath != "logo.png" {
		t.Errorf("binary path = %q", patches[2].newPath)
	}

	// Deletion: /dev/null on the new side is kept verbatim.
	if patches[3].newPath != "/dev/null" || patches[3].dels != 2 {
		t.Errorf("deletion = %+v", patches[3])
	}
}

func TestParseUnifiedDiffNoNewline(t *testing.T) {
	out := `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-one
\ No newline at end of file
+two
\ No newline at end of file
`
	patches := parseUnifiedDiff(out)
	if len(patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(patches))
	}
	// The annotation rides along as context so nothing is silently dropped, but
	// it must not be counted as an addition or a deletion.
	if patches[0].adds != 1 || patches[0].dels != 1 {
		t.Errorf("adds/dels = %d/%d, want 1/1", patches[0].adds, patches[0].dels)
	}
	if got := len(patches[0].hunks[0].Lines); got != 4 {
		t.Errorf("lines = %d, want 4", got)
	}
}

func TestParseUnifiedDiffEmpty(t *testing.T) {
	if got := parseUnifiedDiff(""); len(got) != 0 {
		t.Errorf("patches = %d, want 0", len(got))
	}
}

func TestTruncatePatch(t *testing.T) {
	mk := func(sizes ...int) *diffPatch {
		p := &diffPatch{}
		for _, n := range sizes {
			h := diffHunk{Header: "@@"}
			for i := 0; i < n; i++ {
				h.Lines = append(h.Lines, diffLine{Kind: "add", Content: "x"})
			}
			p.Hunks = append(p.Hunks, h)
		}
		return p
	}

	// Under the cap: untouched.
	p := mk(3, 4)
	if cut, kept := truncatePatch(p, 10); cut || kept != 7 || len(p.Hunks) != 2 {
		t.Errorf("under cap: cut=%v kept=%d hunks=%d", cut, kept, len(p.Hunks))
	}

	// Cap lands mid-hunk: that hunk is clipped and later ones dropped.
	p = mk(3, 4, 5)
	cut, kept := truncatePatch(p, 5)
	if !cut || kept != 5 {
		t.Errorf("mid-hunk: cut=%v kept=%d, want true/5", cut, kept)
	}
	if len(p.Hunks) != 2 || len(p.Hunks[1].Lines) != 2 {
		t.Errorf("mid-hunk hunks = %d, second len = %d", len(p.Hunks), len(p.Hunks[1].Lines))
	}

	// Cap lands exactly on a hunk boundary: no clipped hunk is kept.
	p = mk(3, 4)
	if cut, kept := truncatePatch(p, 3); !cut || kept != 3 || len(p.Hunks) != 1 {
		t.Errorf("boundary: cut=%v kept=%d hunks=%d", cut, kept, len(p.Hunks))
	}

	// Zero cap: everything goes.
	p = mk(3)
	if cut, kept := truncatePatch(p, 0); !cut || kept != 0 || len(p.Hunks) != 0 {
		t.Errorf("zero cap: cut=%v kept=%d hunks=%d", cut, kept, len(p.Hunks))
	}

	if cut, kept := truncatePatch(nil, 10); cut || kept != 0 {
		t.Errorf("nil patch: cut=%v kept=%d", cut, kept)
	}
}

func TestParseStatusZ(t *testing.T) {
	// Renames span two fields, new path first — the reverse of the human
	// format. Untracked and unmerged records ride alongside.
	out := "M  mod.ts\x00 M unstaged.ts\x00R  new.txt\x00old.txt\x00?? fresh.txt\x00UU conflict.go\x00 D gone.txt\x00"
	entries := parseStatusZ(out)
	if len(entries) != 6 {
		t.Fatalf("entries = %d, want 6\n%+v", len(entries), entries)
	}

	cases := []struct {
		path, orig, status string
		staged, untracked  bool
	}{
		{path: "mod.ts", status: "M", staged: true},
		{path: "unstaged.ts", status: "M"},
		{path: "new.txt", orig: "old.txt", status: "R", staged: true},
		{path: "fresh.txt", status: "?", untracked: true},
		{path: "conflict.go", status: "M", staged: true},
		{path: "gone.txt", status: "D"},
	}
	for i, c := range cases {
		e := entries[i]
		if e.path != c.path || e.origPath != c.orig {
			t.Errorf("entry %d path = %q / %q, want %q / %q", i, e.path, e.origPath, c.path, c.orig)
		}
		if e.status() != c.status {
			t.Errorf("entry %d status = %q, want %q", i, e.status(), c.status)
		}
		if e.staged() != c.staged {
			t.Errorf("entry %d staged = %v, want %v", i, e.staged(), c.staged)
		}
		if e.untracked() != c.untracked {
			t.Errorf("entry %d untracked = %v, want %v", i, e.untracked(), c.untracked)
		}
	}
}

func TestStatusEntryWorktreeDeletionWins(t *testing.T) {
	// Modified in the index, then deleted in the worktree: the net effect
	// against HEAD is a deletion, so that is what the panel shows.
	e := statusEntry{x: 'M', y: 'D', path: "x.txt"}
	if e.status() != "D" {
		t.Errorf("status = %q, want D", e.status())
	}
	if !e.staged() {
		t.Error("staged = false, want true")
	}
}
