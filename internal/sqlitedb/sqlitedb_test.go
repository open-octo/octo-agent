package sqlitedb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func scratchHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func mustExec(t *testing.T, name string, mode Mode, q string, params ...any) *Result {
	t.Helper()
	res, err := Exec(context.Background(), name, mode, q, params, 100)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return res
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"prices", "a", "hn_top-10", strings.Repeat("x", 64)} {
		if !ValidName(ok) {
			t.Errorf("ValidName(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "Prices", "../x", "a/b", `a\b`, "a.db", "a b", strings.Repeat("x", 65), "价格"} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) = true", bad)
		}
	}
}

func TestExec_CreateWriteRead(t *testing.T) {
	scratchHome(t)
	mustExec(t, "prices", Create, "CREATE TABLE quote(id INTEGER PRIMARY KEY, symbol TEXT, price REAL, raw BLOB)")
	res := mustExec(t, "prices", Create, "INSERT INTO quote(symbol, price, raw) VALUES (?, ?, ?), (?, ?, ?)",
		"AAPL", 231.4, nil, "MSFT", 5.0, "x")
	if res.HasRows || res.Changes != 2 || res.LastInsertID != 2 {
		t.Errorf("insert result = %+v", res)
	}

	path, _ := Path("prices")
	var mode string
	res = mustExec(t, "prices", ReadOnly, "PRAGMA journal_mode")
	if mode, _ = res.Rows[0][0].(string); mode != "wal" {
		t.Errorf("journal_mode = %q, want wal (file %s)", mode, path)
	}

	// An integral JSON number binds as an integer, so it matches an INTEGER key.
	res = mustExec(t, "prices", ReadWrite, "SELECT symbol, price FROM quote WHERE id = ?", float64(2))
	if !res.HasRows || len(res.Rows) != 1 || res.Rows[0][0] != "MSFT" {
		t.Errorf("select by float id = %+v", res)
	}
	if got := strings.Join(res.Columns, ","); got != "symbol,price" {
		t.Errorf("columns = %s", got)
	}

	// A SELECT that matches nothing still has columns and an empty, non-nil row list.
	res = mustExec(t, "prices", ReadOnly, "SELECT symbol FROM quote WHERE id = 99")
	if !res.HasRows || res.Rows == nil || len(res.Rows) != 0 {
		t.Errorf("empty select = %+v", res)
	}

	res = mustExec(t, "prices", Create, "SELECT x'00ff'")
	if res.Rows[0][0] != "AP8=" {
		t.Errorf("blob = %v, want base64 AP8=", res.Rows[0][0])
	}
}

func TestExec_MissingDatabase(t *testing.T) {
	scratchHome(t)
	for _, mode := range []Mode{ReadWrite, ReadOnly} {
		if _, err := Exec(context.Background(), "nope", mode, "SELECT 1", nil, 10); !errors.Is(err, ErrNotFound) {
			t.Errorf("mode %d: err = %v, want ErrNotFound", mode, err)
		}
	}
	path, _ := Path("nope")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a page request created %s", path)
	}
}

func TestExec_InvalidName(t *testing.T) {
	scratchHome(t)
	if _, err := Exec(context.Background(), "../x", Create, "SELECT 1", nil, 10); !errors.Is(err, ErrInvalidName) {
		t.Errorf("err = %v, want ErrInvalidName", err)
	}
}

func TestExec_ReadOnlyRefusesWrites(t *testing.T) {
	scratchHome(t)
	mustExec(t, "prices", Create, "CREATE TABLE t(a)")
	for _, q := range []string{"INSERT INTO t VALUES (1)", "DROP TABLE t", "CREATE TABLE u(a)"} {
		if _, err := Exec(context.Background(), "prices", ReadOnly, q, nil, 10); !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s: err = %v, want ErrReadOnly", q, err)
		}
	}
}

// ATTACH and VACUUM INTO are how a connection reaches a file other than its
// own database — read-only ones included.
func TestExec_NoOtherFiles(t *testing.T) {
	scratchHome(t)
	mustExec(t, "prices", Create, "CREATE TABLE t(a)")
	mustExec(t, "other", Create, "CREATE TABLE secret(a)")
	dir, _ := Dir()
	outside := filepath.ToSlash(filepath.Join(t.TempDir(), "escape.db"))
	otherPath, _ := Path("other")
	for _, mode := range []Mode{Create, ReadWrite, ReadOnly} {
		for _, q := range []string{
			"ATTACH DATABASE '" + outside + "' AS o",
			"ATTACH DATABASE '" + filepath.ToSlash(otherPath) + "' AS o",
			"VACUUM INTO '" + outside + "'",
		} {
			if _, err := Exec(context.Background(), "prices", mode, q, nil, 10); err == nil {
				t.Errorf("mode %d: %q succeeded", mode, q)
			}
		}
	}
	if _, err := os.Stat(filepath.FromSlash(outside)); !os.IsNotExist(err) {
		t.Errorf("a file was written outside %s", dir)
	}
}

func TestExec_MultipleStatementsRefused(t *testing.T) {
	scratchHome(t)
	mustExec(t, "prices", Create, "CREATE TABLE t(a)")
	mustExec(t, "prices", Create, "INSERT INTO t VALUES (1), (2)")
	if _, err := Exec(context.Background(), "prices", Create, "SELECT a FROM t; DELETE FROM t", nil, 10); !errors.Is(err, ErrMultipleStatements) {
		t.Fatalf("err = %v, want ErrMultipleStatements", err)
	}
	if res := mustExec(t, "prices", ReadOnly, "SELECT count(*) FROM t"); res.Rows[0][0] != int64(2) {
		t.Errorf("rows after refused call = %v, want 2", res.Rows[0][0])
	}
}

func TestCheckSingleStatement(t *testing.T) {
	for _, ok := range []string{
		"SELECT 1",
		"SELECT 1;",
		"SELECT 1 ;  ;\n",
		"SELECT ';' AS s",
		`SELECT "a;b" FROM t`,
		"SELECT `a;b`, [c;d] FROM t",
		"SELECT 'it''s; fine'",
		"SELECT 1; -- trailing comment",
		"SELECT 1; /* trailing ; comment */",
		"-- lead\nSELECT 1",
		"SELECT /* ; */ 1",
	} {
		if err := checkSingleStatement(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, multi := range []string{
		"SELECT 1; SELECT 2",
		"SELECT 1;DELETE FROM t",
		"SELECT 'a'; DROP TABLE t",
		"SELECT 1; -- c\nDELETE FROM t",
	} {
		if err := checkSingleStatement(multi); !errors.Is(err, ErrMultipleStatements) {
			t.Errorf("%q: err = %v, want ErrMultipleStatements", multi, err)
		}
	}
	for _, empty := range []string{"", "  ", ";", "-- only a comment", "/* x */ ;"} {
		if err := checkSingleStatement(empty); !errors.Is(err, ErrEmptyStatement) {
			t.Errorf("%q: err = %v, want ErrEmptyStatement", empty, err)
		}
	}
}

func TestExec_RowCap(t *testing.T) {
	scratchHome(t)
	res, err := Exec(context.Background(), "nums", Create,
		"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 250) SELECT i FROM n", nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 100 || res.Total != 250 || !res.Truncated() {
		t.Errorf("rows=%d total=%d truncated=%v", len(res.Rows), res.Total, res.Truncated())
	}
}

func TestExec_UnsupportedParam(t *testing.T) {
	scratchHome(t)
	if _, err := Exec(context.Background(), "p", Create, "SELECT ?", []any{map[string]any{"a": 1}}, 10); err == nil {
		t.Error("an object parameter was accepted")
	}
}

// Two writers on one file stand in for the serve process and a CLI process:
// WAL plus busy_timeout makes the second wait instead of failing.
func TestExec_ConcurrentWriters(t *testing.T) {
	scratchHome(t)
	mustExec(t, "log", Create, "CREATE TABLE e(n INTEGER)")
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if _, err := Exec(context.Background(), "log", Create, "INSERT INTO e VALUES (?)", []any{i}, 10); err != nil {
					errs <- err
				}
				if _, err := Exec(context.Background(), "log", ReadOnly, "SELECT count(*) FROM e", nil, 10); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if res := mustExec(t, "log", ReadOnly, "SELECT count(*) FROM e"); res.Rows[0][0] != int64(100) {
		t.Errorf("count = %v, want 100", res.Rows[0][0])
	}
}
