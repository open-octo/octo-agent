package trash

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// backdate rewrites an entry's sidecar so it looks like it was deleted `age`
// ago, letting the age-out path be exercised deterministically.
func backdate(t *testing.T, e Entry, age time.Duration) {
	t.Helper()
	m := meta{
		Original:  e.Original,
		DeletedAt: time.Now().Add(-age).UTC().Format(time.RFC3339),
		Project:   e.Project,
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(e.TrashPath+".meta.json", data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stageSized(t *testing.T, project, name string, size int) string {
	t.Helper()
	p := filepath.Join(project, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(p, project); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnforce_AgeOut(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	old := stageSized(t, project, "old.txt", 10)
	recent := stageSized(t, project, "recent.txt", 10)
	backdate(t, find(t, old), 2*time.Hour)
	backdate(t, find(t, recent), 30*time.Minute)

	removed, _, err := Enforce(0, 90*time.Minute) // cap disabled, retention 90m
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 aged-out, got %d", removed)
	}
	entries, _ := List()
	if len(entries) != 1 || entries[0].Original != recent {
		t.Fatalf("only the recent entry should survive, have %+v", entries)
	}
}

func TestEnforce_OrphanSurvivesAgeOut(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := stageSized(t, project, "old.txt", 10)
	backdate(t, find(t, f), 2*time.Hour)
	os.RemoveAll(project) // project gone → entry is an orphan

	removed, _, err := Enforce(0, 90*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("an orphan must survive age-out, but %d were removed", removed)
	}
}

func TestEnforce_SizeCapEvictsOldestFirst(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	a := stageSized(t, project, "a.txt", 100) // will be oldest
	b := stageSized(t, project, "b.txt", 100)
	c := stageSized(t, project, "c.txt", 100) // newest
	backdate(t, find(t, a), 3*time.Hour)
	backdate(t, find(t, b), 2*time.Hour)
	backdate(t, find(t, c), 1*time.Hour)

	// total 300, cap 250 → evict the single oldest (a), leaving 200.
	removed, freed, err := Enforce(250, 0) // retention disabled
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || freed != 100 {
		t.Fatalf("expected to evict 1 (100 bytes), got removed=%d freed=%d", removed, freed)
	}
	entries, _ := List()
	for _, e := range entries {
		if e.Original == a {
			t.Fatal("the oldest entry should have been evicted")
		}
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 survivors (b, c), got %d", len(entries))
	}
	_ = b
	_ = c
}

func TestEnforce_BoundsDisabledIsNoOp(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := stageSized(t, project, "keep.txt", 10)
	backdate(t, find(t, f), 100*24*time.Hour) // ancient

	removed, _, err := Enforce(0, 0) // both disabled
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("both bounds disabled must remove nothing, removed %d", removed)
	}
}
