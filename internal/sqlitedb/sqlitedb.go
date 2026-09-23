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
	"strings"
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
	ErrTimeout            = errors.New("query ran past its time limit")
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

// Limits bound one call. A public page's query comes from anyone who has the
// link, so every bound is on by default for the caller to size: without them
// one request can spin a core forever (an unbounded recursive CTE) or ask for
// gigabytes (zeroblob in a loop).
type Limits struct {
	MaxRows  int
	MaxBytes int // value bytes kept across all rows
	// MaxValueLen caps any one string or blob (SQLITE_LIMIT_LENGTH); 0
	// keeps SQLite's default.
	MaxValueLen int
	Timeout     time.Duration
}

// Result is one statement's outcome. A statement that yields columns fills
// Columns/Rows/Truncated; one that yields none fills Changes/LastInsertID.
type Result struct {
	HasRows bool
	Columns []string
	Rows    [][]any
	// Truncated means the statement had more rows than Rows holds. Reading
	// stops at the cap rather than counting on, so the query stops too.
	Truncated    bool
	Changes      int64
	LastInsertID int64
}

// Exec runs one SQL statement against the named database within lim.
func Exec(ctx context.Context, name string, mode Mode, query string, params []any, lim Limits) (*Result, error) {
	verb, err := checkSingleStatement(query)
	if err != nil {
		return nil, err
	}
	// A read-only connection refuses writes, but not a PRAGMA that sets
	// process-wide state (soft_heap_limit, hard_heap_limit). A reader needs
	// none: table info is SELECT … FROM pragma_table_info(…).
	if mode == ReadOnly && verb != "SELECT" && verb != "WITH" && verb != "VALUES" {
		return nil, ErrReadOnly
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

	if lim.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lim.Timeout)
		defer cancel()
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
		return nil, mapErr(ctx, err)
	}
	defer conn.Close()
	// A read-only connection still lets ATTACH create and write another file,
	// and VACUUM INTO write one anywhere. The driver exposes no authorizer; a
	// zero attach limit refuses both. The limit belongs to this connection,
	// so it is set on every one.
	if _, err := sqlite.Limit(conn, lib.SQLITE_LIMIT_ATTACHED, 0); err != nil {
		return nil, err
	}
	if lim.MaxValueLen > 0 {
		if _, err := sqlite.Limit(conn, lib.SQLITE_LIMIT_LENGTH, lim.MaxValueLen); err != nil {
			return nil, err
		}
	}

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapErr(ctx, err)
	}
	res, err := collect(rows, lim)
	if err != nil {
		return nil, mapErr(ctx, err)
	}
	if !res.HasRows {
		if err := conn.QueryRowContext(ctx, "SELECT changes(), last_insert_rowid()").Scan(&res.Changes, &res.LastInsertID); err != nil {
			return nil, mapErr(ctx, err)
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

func collect(rows *sql.Rows, lim Limits) (*Result, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &Result{HasRows: len(cols) > 0, Columns: cols, Rows: [][]any{}}
	size := 0
	for rows.Next() {
		if len(res.Rows) == lim.MaxRows {
			res.Truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		rowSize := 0
		for i, v := range vals {
			vals[i] = jsonValue(v)
			rowSize += valueSize(vals[i])
		}
		if size+rowSize > lim.MaxBytes {
			res.Truncated = true
			break
		}
		size += rowSize
		res.Rows = append(res.Rows, vals)
	}
	return res, rows.Err()
}

func valueSize(v any) int {
	if s, ok := v.(string); ok {
		return len(s)
	}
	return 8
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

func mapErr(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
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
//
// It also returns the statement's first keyword, upper-cased.
func checkSingleStatement(s string) (string, error) {
	seen, ended := false, false
	verb := ""
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
			return "", ErrMultipleStatements
		}
		if !seen {
			j := i
			for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
				j++
			}
			verb = strings.ToUpper(s[i:j])
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
		return "", ErrEmptyStatement
	}
	return verb, nil
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
