package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/open-octo/octo-agent/internal/agent"
)

// ─── insert_into_artifact: the one direction that reaches into a page ───────
//
// The other half of the loop view_artifact opens: look at what the user is
// looking at, make something from it, put it back where they are working.
//
// This is the one direction that reaches *into* a page, and it stays narrow
// on purpose: a file and a note, delivered to an artifact of the current
// session the user has open, which the page is free to ignore. It is not a
// channel — nothing comes back on it, and the page still cannot see the
// conversation.

// ArtifactDeliverer hands a file to an open artifact frame. The tools package
// cannot reach the browser — it cannot even import the server, which imports
// it — so the server installs this on start. Unset (a CLI session, a test),
// the insert tool says so rather than pretending it delivered.
type ArtifactDeliverer func(session, artifactPath, filePath, note string) error

var artifactDeliver struct {
	mu sync.Mutex
	fn ArtifactDeliverer
}

// SetArtifactDeliverer installs the delivery path. Called by the server.
func SetArtifactDeliverer(fn ArtifactDeliverer) {
	artifactDeliver.mu.Lock()
	defer artifactDeliver.mu.Unlock()
	artifactDeliver.fn = fn
}

func artifactDelivererFn() ArtifactDeliverer {
	artifactDeliver.mu.Lock()
	defer artifactDeliver.mu.Unlock()
	return artifactDeliver.fn
}

// ArtifactInsertTool hands a file to an open artifact of the current session.
type ArtifactInsertTool struct{}

func (ArtifactInsertTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "insert_into_artifact",
		Description: "Send an image you produced into an artifact the user has open in the panel — " +
			"for example, drop a generated picture onto a canvas page so they can keep working on " +
			"it. Use after making a file the user will want to use where they are already working, " +
			"rather than only telling them where it was saved. Call artifact_state first: the page " +
			"has to be open and publishing to be reachable here, and it decides what to do with " +
			"what it receives.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"artifact": map[string]any{
					"type":        "string",
					"description": "Which artifact to send to, as artifact_state names it. Optional when exactly one page is open.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path of the image file to send (PNG, JPEG, GIF or WebP).",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "Optional one-line note passed to the page alongside the file, e.g. what it is or where it came from.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (ArtifactInsertTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	path := strings.TrimSpace(stringArg(input, "path"))
	if path == "" {
		return agent.ToolResult{}, fmt.Errorf("insert_into_artifact: path is required")
	}
	deliver := artifactDelivererFn()
	if deliver == nil {
		return agent.ToolResult{
			Text: "Artifacts are only reachable from a running octo Web UI, and this session has " +
				"none attached. Tell the user where the file is instead.",
		}, nil
	}

	sid := SessionIDFrom(ctx)
	target := strings.TrimSpace(stringArg(input, "artifact"))
	pages := artifactSnapshots(sid)
	switch {
	case target != "":
		if artifactSnapshot(sid, target) == nil {
			return agent.ToolResult{
				Text: fmt.Sprintf("No artifact at %q is open. Call artifact_state to see which are.", target),
			}, nil
		}
	case len(pages) == 1:
		target = pages[0].Path
	case len(pages) == 0:
		return agent.ToolResult{
			Text: "No artifact of this session is open, so there is nowhere to put it. Ask the " +
				"user to open the page in the panel, or just tell them the file path.",
		}, nil
	default:
		names := make([]string, 0, len(pages))
		for _, s := range pages {
			names = append(names, s.Path)
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("Several artifacts are open (%s). Pass `artifact` to say which one.", strings.Join(names, ", ")),
		}, nil
	}

	if err := deliver(sid, target, path, strings.TrimSpace(stringArg(input, "note"))); err != nil {
		return agent.ToolResult{}, fmt.Errorf("insert_into_artifact: %w", err)
	}
	return agent.ToolResult{
		Text: fmt.Sprintf("Sent to %s. The page decides what to do with it — check artifact_state "+
			"to see whether it took it, and tell the user to look at the panel.", target),
	}, nil
}
