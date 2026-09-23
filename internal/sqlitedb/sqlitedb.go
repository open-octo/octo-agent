// Package sqlitedb opens the named SQLite databases under
// ~/.octo/databases/ that the sqlite tool writes and pages (session artifacts
// and Light Apps) query. Both sides meet at a database name only; see
// dev-docs/named-databases-design.md.
package sqlitedb

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	sqlite "modernc.org/sqlite"
	lib "modernc.org/sqlite/lib"

	"github.com/open-octo/octo-agent/internal/datahome"
)

// Mode is how a caller may touch a database.
type Mode int

const (
	// Create opens read-write and creates the file when missing: the sqlite
	// tool, the only thing that brings a database into being.
	Create Mode = iota
	// ReadWrite opens an existing database read-write: a non-public page.
	ReadWrite
	// ReadOnly opens an existing database read-only: a public Light App.
	ReadOnly
)

var (
	ErrInvalidName        = errors.New("invalid database name: use 1-64 of a-z, 0-9, _ and -")
	ErrNotFound           = errors.New("database not found")
	ErrEmptyStatement     = errors.New("no SQL statement given")
	ErrMultipleStatements = errors.New("only one SQL statement per call")
	ErrBusy               = errors.New("database is busy")
	ErrReadOnly           = errors.New("database is read-only here")
)

var nameRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// ValidName reports whether name can be a database name. The name becomes the
// file name as is, so the character set rules out path traversal and two
// names that collide on a case-insensitive file system.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Dir is the directory holding every database; it follows --profile.
func Dir() (string, error) { return datahome.Path("databases") }

// Path is the file of the named database.
func Path(name string) (string, error) {
	if !ValidName(name) {
		return "", ErrInvalidName
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".db"), nil
}

// Result is one statement's outcome. A statement that yields columns fills
// Columns/Rows/Total; one that yields none fills Changes/LastInsertID.
type Result struct {
	HasRows      bool
	Columns      []string
	Rows         [][]any
	Total        int // rows the statement produced; > len(Rows) when capped
	Changes      int64
	LastInsertID int64
}

// Truncated reports whether Rows stops short of what the statement produced.
func (r *Result) Truncated() bool { return r.Total > len(r.Rows) }

// Exec runs one SQL statement against the named database. At most maxRows
// rows are kept; the rest are counted into Total.
func Exec(ctx context.Context, name string, mode Mode, query string, params []any, maxRows int) (*Result, error) {
	if err := checkSingleStatement(query); err != nil {
		return nil, err
	}
	args, err := normalizeParams(params)
	if err != nil {
		return nil, err
	}
	path, err := Path(name)
	if err != nil {
		return nil, err
	}
	if mode == Create {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	} else if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return nil, ErrNotFound
	}

	// Opened per call rather than pooled: calls are rare next to the cost of
	// an open, and nothing is left holding the file — Windows cannot delete
	// or replace a file that is still open.
	db, err := sql.Open("sqlite", dsn(path, mode))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	defer conn.Close()
	// A read-only connection still lets ATTACH create and write another file,
	// and VACUUM INTO write one anywhere. The driver exposes no authorizer; a
	// zero attach limit refuses both. The limit belongs to this connection,
	// so it is set on every one.
	if _, err := sqlite.Limit(conn, lib.SQLITE_LIMIT_ATTACHED, 0); err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	res, err := collect(rows, maxRows)
	if err != nil {
		return nil, mapErr(err)
	}
	if !res.HasRows {
		if err := conn.QueryRowContext(ctx, "SELECT changes(), last_insert_rowid()").Scan(&res.Changes, &res.LastInsertID); err != nil {
			return nil, mapErr(err)
		}
	}
	return res, nil
}

func dsn(path string, mode Mode) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	switch mode {
	case Create:
		q.Set("mode", "rwc")
		// Set when the file is born and kept by the file; every later
		// connection, read-only ones included, reads it in WAL.
		q.Add("_pragma", "journal_mode(WAL)")
	case ReadWrite:
		q.Set("mode", "rw")
	default:
		q.Set("mode", "ro")
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: q.Encode()}
	if filepath.VolumeName(path) != "" {
		// SQLite spells a drive path file:///C:/x.db; without the slash the
		// drive would parse as part of a relative path.
		u.Path = "/" + u.Path
	}
	return u.String()
}

func collect(rows *sql.Rows, maxRows int) (*Result, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &Result{HasRows: len(cols) > 0, Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		res.Total++
		if res.Total > maxRows {
			continue
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			vals[i] = jsonValue(v)
		}
		res.Rows = append(res.Rows, vals)
	}
	return res, rows.Err()
}

// jsonValue maps a scanned value onto what JSON carries: a BLOB becomes
// standard base64, a time its RFC 3339 text.
func jsonValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return base64.StdEncoding.EncodeToString(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return v
}

// normalizeParams turns decoded JSON into bind values. JSON has one number
// type, so an integral number binds as an integer — otherwise `WHERE id = ?`
// would compare against 5.0 and an INTEGER column would store a REAL.
func normalizeParams(params []any) ([]any, error) {
	out := make([]any, len(params))
	for i, p := range params {
		switch x := p.(type) {
		case nil, string, bool, int, int64:
			out[i] = x
		case float64:
			if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
				out[i] = int64(x)
			} else {
				out[i] = x
			}
		case json.Number:
			if n, err := x.Int64(); err == nil {
				out[i] = n
			} else if f, err := x.Float64(); err == nil {
				out[i] = f
			} else {
				return nil, fmt.Errorf("parameter %d: invalid number %q", i+1, x)
			}
		default:
			return nil, fmt.Errorf("parameter %d: only strings, numbers, booleans and null can be bound", i+1)
		}
	}
	return out, nil
}

func mapErr(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case lib.SQLITE_BUSY:
			return ErrBusy
		case lib.SQLITE_READONLY:
			return ErrReadOnly
		}
	}
	return err
}

// checkSingleStatement refuses input holding more than one statement. The
// driver runs every statement it is given and reports only the last, so
// "SELECT …; DELETE …" would return nothing and empty the table. A trigger
// body (BEGIN … ; … END) reads as several statements and is refused too.
func checkSingleStatement(s string) error {
	seen, ended := false, false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i < len(s) && !(s[i] == '*' && i+1 < len(s) && s[i+1] == '/') {
				i++
			}
			i += 2
			continue
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
			continue
		case c == ';':
			if seen {
				ended = true
			}
			i++
			continue
		}
		if ended {
			return ErrMultipleStatements
		}
		seen = true
		switch c {
		case '\'', '"', '`':
			i = skipQuoted(s, i, c)
		case '[':
			for i++; i < len(s) && s[i] != ']'; i++ {
			}
			i++
		default:
			i++
		}
	}
	if !seen {
		return ErrEmptyStatement
	}
	return nil
}

// skipQuoted returns the index after the quoted run opening at s[i]; a doubled
// quote inside it is an escaped quote.
func skipQuoted(s string, i int, q byte) int {
	for i++; i < len(s); i++ {
		if s[i] != q {
			continue
		}
		if i+1 < len(s) && s[i+1] == q {
			i++
			continue
		}
		return i + 1
	}
	return i
}
