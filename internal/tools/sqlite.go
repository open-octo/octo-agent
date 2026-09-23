package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/sqlitedb"
)

const (
	sqliteMaxRows  = 200
	sqliteMaxBytes = 16 << 10
)

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
			"JSON.stringify({sql, params})})`, so a writer never needs to know where a page lives.\n\n" +
			"One statement per call (a second one is refused); use a multi-row VALUES list or " +
			"INSERT … SELECT to write many rows at once. Bind values with `?` placeholders and " +
			"`params` rather than splicing them into the SQL. ATTACH and VACUUM INTO are refused. " +
			"A statement that returns rows prints them tab-separated under a header line (up to " +
			"200 rows); any other prints the changed-row count and last insert id. List existing " +
			"databases with glob on `~/.octo/databases/*.db`; see a database's tables with " +
			"`SELECT sql FROM sqlite_master`.",
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
	res, err := sqlitedb.Exec(ctx, db, sqlitedb.Create, query, params, sqliteMaxRows)
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
	case res.Total == 0:
		b.WriteString("\n(0 rows)")
	case shown < res.Total:
		fmt.Fprintf(&b, "\n(showing %d of %d rows)", shown, res.Total)
	}
	return b.String()
}

// sqliteCell renders one value on a single line so the tab-separated layout
// holds: NULL spelled out, tabs and line breaks escaped.
func sqliteCell(v any) string {
	if v == nil {
		return "NULL"
	}
	s := fmt.Sprint(v)
	return strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`).Replace(s)
}
