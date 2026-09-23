package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/sqlitedb"
)

func runSQLite(t *testing.T, input map[string]any) (string, error) {
	t.Helper()
	res, err := SQLiteTool{}.Execute(context.Background(), "sqlite", input)
	return res.Text, err
}

func TestSQLiteTool_CreatesAndQueries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := runSQLite(t, map[string]any{"db": "prices", "sql": "CREATE TABLE q(sym TEXT, note TEXT, px REAL)"}); err != nil {
		t.Fatal(err)
	}
	path, _ := sqlitedb.Path("prices")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file not created: %v", err)
	}
	out, err := runSQLite(t, map[string]any{
		"db":     "prices",
		"sql":    "INSERT INTO q VALUES (?, ?, ?), (?, ?, ?)",
		"params": []any{"AAPL", "a\tb\nc", 231.4, "MSFT", nil, 5.0},
	})
	if err != nil || out != "changes=2 last_insert_id=2" {
		t.Fatalf("insert: %q, %v", out, err)
	}
	out, err = runSQLite(t, map[string]any{"db": "prices", "sql": "SELECT sym, note, px FROM q ORDER BY sym"})
	if err != nil {
		t.Fatal(err)
	}
	want := "sym\tnote\tpx\nAAPL\ta\\tb\\nc\t231.4\nMSFT\tNULL\t5"
	if out != want {
		t.Errorf("select:\n%q\nwant\n%q", out, want)
	}
	out, _ = runSQLite(t, map[string]any{"db": "prices", "sql": "SELECT sym FROM q WHERE 0"})
	if out != "sym\n(0 rows)" {
		t.Errorf("empty select = %q", out)
	}
}

func TestSQLiteTool_TruncatesRowsAndBytes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	out, err := runSQLite(t, map[string]any{"db": "n", "sql": "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 500) SELECT i FROM n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "(showing 200 of 500 rows)") {
		t.Errorf("row cap tail = %q", out[len(out)-40:])
	}
	out, err = runSQLite(t, map[string]any{"db": "n", "sql": "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 50) SELECT i, printf('%.1000c', 'x') FROM n"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > sqliteMaxBytes+64 || !strings.Contains(out, "of 50 rows)") {
		t.Errorf("byte cap: len=%d tail=%q", len(out), out[len(out)-40:])
	}
}

func TestSQLiteTool_Errors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	for name, input := range map[string]map[string]any{
		"missing db":      {"sql": "SELECT 1"},
		"bad name":        {"db": "../etc", "sql": "SELECT 1"},
		"two statements":  {"db": "x", "sql": "SELECT 1; SELECT 2"},
		"params not list": {"db": "x", "sql": "SELECT ?", "params": "a"},
		"sql error":       {"db": "x", "sql": "SELECT FROM"},
	} {
		if _, err := runSQLite(t, input); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
