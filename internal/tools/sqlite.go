package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/sqlitedb"
)

const (
	sqliteMaxBytes = 16 << 10 // of printed output
	sqliteMaxCell  = 1000     // bytes of one printed value
)

var sqliteLimits = sqlitedb.Limits{MaxRows: 200, MaxBytes: 1 << 20, Timeout: time.Minute}

// SQLiteTool runs one SQL statement against a named database under
// ~/.octo/databases/. It is the only writer that can create a database; pages
// (artifacts and Light Apps) query the same databases by name through
// ./__octo/db/<name> (internal/server/db_pages.go).
type SQLiteTool struct{}

func (SQLiteTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "sqlite",
		Description: "Run one SQL statement against a named SQLite database. Databases live in " +
			"`~/.octo/databases/<db>.db`, are created on first use, and persist across sessions — " +
			"use them for data collected over time (a scheduled task appending results, a log, a " +
			"dataset a page shows). The name is all a reader needs: an HTML artifact or Light App " +
			"page reads the same database with `fetch('./__octo/db/<db>', {method:'POST', body: " +
			"JSON.stringify({sql, params})})` — it answers `{columns, rows}` with each row an array in " +
			"`columns` order, not an object — so a writer never needs to know where a page lives.\n\n" +
			"One statement per call (a second one is refused); use a multi-row VALUES list or " +
			"INSERT … SELECT to write many rows at once. Bind values with `?` placeholders and " +
			"`params` rather than splicing them into the SQL. ATTACH and VACUUM INTO are refused. " +
			"A statement that returns rows prints them tab-separated under a header line (up to " +
			"200 rows, long values cut short; use count(*) or LIMIT/OFFSET for more); any other " +
			"prints the changed-row count and last insert id. List existing databases with glob " +
			"on `~/.octo/databases/*.db`; see a database's tables with `SELECT sql FROM sqlite_master`.\n\n" +
			"In the conversation, read and write these databases with this tool, not `sqlite3` or a " +
			"script through terminal: Windows has no `sqlite3`, and this tool waits for locks and " +
			"refuses a second statement. A script meant to run outside octo (from the OS scheduler, " +
			"say) may write them with its language's SQLite library, opening the file with a lock " +
			"wait (Python: `sqlite3.connect(path, timeout=5)`).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"db": map[string]any{
					"type":        "string",
					"description": "Database name: 1-64 characters of a-z, 0-9, _ and -.",
				},
				"sql": map[string]any{
					"type":        "string",
					"description": "One SQL statement.",
				},
				"params": map[string]any{
					"type":        "array",
					"description": "Values for the `?` placeholders, in order: strings, numbers, booleans or null.",
					"items":       map[string]any{},
				},
			},
			"required": []string{"db", "sql"},
		},
	}
}

func (SQLiteTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	db := stringArg(input, "db")
	query := stringArg(input, "sql")
	if strings.TrimSpace(db) == "" {
		return agent.ToolResult{}, fmt.Errorf("sqlite: db is required")
	}
	var params []any
	if raw, ok := input["params"]; ok && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return agent.ToolResult{}, fmt.Errorf("sqlite: params must be an array")
		}
		params = list
	}
	res, err := sqlitedb.Exec(ctx, db, sqlitedb.Create, query, params, sqliteLimits)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("sqlite: %w", err)
	}
	return agent.ToolResult{Text: formatSQLiteResult(res)}, nil
}

func formatSQLiteResult(res *sqlitedb.Result) string {
	if !res.HasRows {
		return fmt.Sprintf("changes=%d last_insert_id=%d", res.Changes, res.LastInsertID)
	}
	var b strings.Builder
	b.WriteString(strings.Join(res.Columns, "\t"))
	shown := 0
	for _, row := range res.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = sqliteCell(v)
		}
		line := strings.Join(cells, "\t")
		if b.Len()+1+len(line) > sqliteMaxBytes {
			break
		}
		b.WriteByte('\n')
		b.WriteString(line)
		shown++
	}
	switch {
	case len(res.Rows) == 0 && !res.Truncated:
		b.WriteString("\n(0 rows)")
	case shown < len(res.Rows) || res.Truncated:
		fmt.Fprintf(&b, "\n(showing the first %d rows; more not shown)", shown)
	}
	return b.String()
}

// sqliteCell renders one value on a single line so the tab-separated layout
// holds: NULL spelled out, tabs and line breaks escaped, a long value cut
// short so one cell cannot push its whole row out of the output.
func sqliteCell(v any) string {
	if v == nil {
		return "NULL"
	}
	s := fmt.Sprint(v)
	if len(s) > sqliteMaxCell {
		cut := sqliteMaxCell
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = fmt.Sprintf("%s…(%d bytes)", s[:cut], len(s))
	}
	return strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`).Replace(s)
}
