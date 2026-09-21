package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// ─── The two model-facing tools over the artifact mirror ────────────────────
//
// Split cheap from expensive on purpose: artifact_state is a few lines of text
// the model can afford to check whenever the user gestures at "the page" or
// "the chart you just made", and view_artifact is the one that spends an image
// on it.
//
// Both stay registered when nothing is connected. A tool that disappears when
// idle is a tool the model never learns it has — the answer says to ask the
// user to open the artifact in the panel instead.

// ArtifactStateTool reports which artifacts of the current session are pushing
// state, and what they say is in them.
type ArtifactStateTool struct{}

func (ArtifactStateTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "artifact_state",
		Description: "Inspect what is currently inside this session's open artifacts — the HTML " +
			"page, chart or diagram the user has open in the panel. Each reporting page offers its " +
			"own one-line digest and whether a screenshot is available. Cheap: call it whenever the " +
			"user refers to something on the page (\"this chart\", \"what I selected\", \"这里\"), " +
			"and before view_artifact.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (ArtifactStateTool) Execute(ctx context.Context, _ string, _ map[string]any) (agent.ToolResult, error) {
	now := time.Now()
	pages := artifactSnapshots(SessionIDFrom(ctx))
	if len(pages) == 0 {
		return agent.ToolResult{
			Text: "No artifact of this session is reporting state. Either none is open in the " +
				"panel, or the open one does not push state. Ask the user to open the artifact " +
				"in the panel if they want you to see what is in it.",
		}, nil
	}
	var b strings.Builder
	b.WriteString("Artifacts of this session the user has open, each describing itself:\n")
	for _, s := range pages {
		b.WriteString(renderArtifactLine(s, now))
	}
	b.WriteString("\nUse view_artifact to look at one that has a screenshot.")
	return agent.ToolResult{Text: b.String()}, nil
}

// ArtifactViewTool puts an artifact's screenshot into the conversation.
type ArtifactViewTool struct{}

func (ArtifactViewTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "view_artifact",
		Description: "Look at the screenshot an open artifact published — the rendered page, a " +
			"selection on it, whatever the page chose to show. The image enters the conversation, " +
			"so you can reason about it directly, like any image the user sent. Call artifact_state " +
			"first to see which pages have one.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type": "string",
					"description": "Which artifact to view, as artifact_state names it. " +
						"Optional when exactly one page is reporting state.",
				},
			},
		},
	}
}

func (ArtifactViewTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	sid := SessionIDFrom(ctx)
	path := strings.TrimSpace(stringArg(input, "path"))
	pages := artifactSnapshots(sid)

	var snap *ArtifactSnapshot
	switch {
	case path != "":
		snap = artifactSnapshot(sid, path)
		if snap == nil {
			return agent.ToolResult{
				Text: fmt.Sprintf("No artifact at %q is reporting state. Call artifact_state to see which are.", path),
			}, nil
		}
	case len(pages) == 1:
		snap = pages[0]
	case len(pages) == 0:
		return agent.ToolResult{
			Text: "No artifact of this session is reporting state, so there is nothing to view. " +
				"Ask the user to open the artifact in the panel.",
		}, nil
	default:
		names := make([]string, 0, len(pages))
		for _, s := range pages {
			names = append(names, s.Path)
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("Several artifacts are reporting state (%s). Pass `path` to say which one to view.", strings.Join(names, ", ")),
		}, nil
	}

	if len(snap.Image) == 0 {
		return agent.ToolResult{
			Text: fmt.Sprintf("%s is connected but published no screenshot.\n%s\n\nAsk the user to "+
				"select or show the part they want you to look at — the page publishes an image "+
				"when there is something to see.", snap.Path, snap.Digest),
		}, nil
	}

	// Only here does a screenshot become an attachment: most are replaced or
	// discarded long before any tool consumes them.
	blk, ok := agent.NewImageBlock(snap.ImageType, snap.Image)
	if !ok {
		return agent.ToolResult{
			Text: fmt.Sprintf("%s published a screenshot in a format the model cannot read (%s). "+
				"Its own description: %s", snap.Path, snap.ImageType, snap.Digest),
		}, nil
	}

	now := time.Now()
	head := fmt.Sprintf("Screenshot from %s, updated %s.", snap.Path, describeAge(now.Sub(snap.UpdatedAt)))
	if snap.Stale(now) {
		head += " It is stale — confirm with the user that this is still what they mean."
	}
	if snap.Digest != "" {
		head += "\n" + snap.Digest
	}
	return agent.ToolResult{Text: head, Blocks: []agent.ContentBlock{blk}}, nil
}
