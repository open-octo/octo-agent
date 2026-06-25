package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// archiveChunk writes msgs to a new chunk-NNN.md file under a.ArchiveDir and
// returns its absolute path, so a compaction summary can point the model at the
// verbatim originals it folded away (recall via the read tool). Returns "" when
// archival is disabled (no ArchiveDir) or fails — archival is best-effort and
// must never break a compaction.
//
// The chunk index continues past any files already in the directory, so a
// resumed session doesn't overwrite earlier chunks.
func (a *Agent) archiveChunk(msgs []Message) string {
	if a.ArchiveDir == "" || len(msgs) == 0 {
		return ""
	}
	if err := os.MkdirAll(a.ArchiveDir, 0o755); err != nil {
		return ""
	}
	existing, _ := filepath.Glob(filepath.Join(a.ArchiveDir, "chunk-*.md"))
	path := filepath.Join(a.ArchiveDir, fmt.Sprintf("chunk-%03d.md", len(existing)+1))
	if err := os.WriteFile(path, []byte(renderChunk(msgs)), 0o600); err != nil {
		return ""
	}
	return path
}

// renderChunk turns folded messages into a readable Markdown transcript — what
// the model sees when it reads the chunk back, so it stays plain text rather
// than wire JSON.
func renderChunk(msgs []Message) string {
	var b strings.Builder
	b.WriteString("# Archived conversation turns\n\n")
	b.WriteString("> These turns were folded out of the live context during compaction. Read-only recall.\n")
	for _, m := range msgs {
		role := string(m.Role)
		if len(m.Blocks) == 0 {
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", role, m.Content)
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n", role)
		for _, blk := range m.Blocks {
			switch blk.Type {
			case "text":
				if strings.TrimSpace(blk.Text) != "" {
					fmt.Fprintf(&b, "\n%s\n", blk.Text)
				}
			case "tool_use":
				args := ""
				if len(blk.Input) > 0 {
					if j, err := json.Marshal(blk.Input); err == nil {
						args = " " + string(j)
					}
				}
				fmt.Fprintf(&b, "\n**→ tool: %s**%s\n", blk.Name, args)
			case "tool_result":
				tag := "tool result"
				if blk.IsError {
					tag = "tool error"
				}
				fmt.Fprintf(&b, "\n**%s:**\n\n```\n%s\n```\n", tag, blk.Result)
			case "thinking":
				// reasoning trace is not part of the user-visible transcript
			}
		}
	}
	return b.String()
}
