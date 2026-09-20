package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// ─── The two model-facing tools over the Light App mirror ───────────────────
//
// Split cheap from expensive on purpose: lightapp_state is a few lines of text
// the model can afford to check whenever the user gestures at "the thing I
// drew", and view_lightapp is the one that spends an image on it.
//
// Both stay registered when nothing is connected. A tool that disappears when
// idle is a tool the model never learns it has — the answer says to ask the
// user to open the app instead.

// LightAppStateTool reports which Light Apps are pushing state, and what they
// say is in them.
type LightAppStateTool struct{}

func (LightAppStateTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "lightapp_state",
		Description: "Inspect what is currently inside the user's open Light Apps — a sketchpad's " +
			"canvas, a board, any mini-app mounted in the UI. Each app reports its own one-line " +
			"digest and whether a screenshot is available. Cheap: call it whenever the user refers " +
			"to something they drew, placed or opened (\"my sketch\", \"the board\", \"这个图\"), and " +
			"before view_lightapp.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (LightAppStateTool) Execute(_ context.Context, _ string, _ map[string]any) (agent.ToolResult, error) {
	now := time.Now()
	apps := lightAppSnapshots()
	if len(apps) == 0 {
		return agent.ToolResult{
			Text: "No Light App is reporting state. Either none is open, or the open one does not " +
				"push state. Ask the user to open the app in octo's Web UI (and to keep that tab " +
				"in view) if they want you to see what is in it.",
		}, nil
	}
	var b strings.Builder
	b.WriteString("Light Apps reporting state:\n")
	for _, s := range apps {
		b.WriteString(renderLightAppLine(s, now))
	}
	b.WriteString("\nUse view_lightapp to look at one that has a screenshot.")
	return agent.ToolResult{Text: b.String()}, nil
}

// LightAppViewTool puts a Light App's screenshot into the conversation.
type LightAppViewTool struct{}

func (LightAppViewTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "view_lightapp",
		Description: "Look at the screenshot a Light App published — a hand-drawn sketch, a " +
			"selection on a canvas, whatever the app chose to show. The image enters the " +
			"conversation, so you can reason about it directly, like any image the user sent. " +
			"Call lightapp_state first to see which apps have one.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug": map[string]any{
					"type": "string",
					"description": "Which Light App to view, as lightapp_state names it. " +
						"Optional when exactly one app is reporting state.",
				},
			},
		},
	}
}

func (LightAppViewTool) Execute(_ context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	slug := strings.TrimSpace(stringArg(input, "slug"))
	apps := lightAppSnapshots()

	var snap *LightAppSnapshot
	switch {
	case slug != "":
		snap = lightAppSnapshot(slug)
		if snap == nil {
			return agent.ToolResult{
				Text: fmt.Sprintf("No Light App named %q is reporting state. Call lightapp_state to see which are.", slug),
			}, nil
		}
	case len(apps) == 1:
		snap = apps[0]
	case len(apps) == 0:
		return agent.ToolResult{
			Text: "No Light App is reporting state, so there is nothing to view. Ask the user to " +
				"open the app in octo's Web UI.",
		}, nil
	default:
		names := make([]string, 0, len(apps))
		for _, s := range apps {
			names = append(names, s.Slug)
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("Several Light Apps are reporting state (%s). Pass `slug` to say which one to view.", strings.Join(names, ", ")),
		}, nil
	}

	if len(snap.Image) == 0 {
		return agent.ToolResult{
			Text: fmt.Sprintf("%s is connected but published no screenshot.\n%s\n\nAsk the user to "+
				"select or show the part they want you to look at — the app publishes an image when "+
				"there is something to see.", snap.Slug, snap.Digest),
		}, nil
	}

	// Only here does a screenshot become an attachment: most are replaced or
	// discarded long before any tool consumes them.
	blk, ok := agent.NewImageBlock(snap.ImageType, snap.Image)
	if !ok {
		return agent.ToolResult{
			Text: fmt.Sprintf("%s published a screenshot in a format the model cannot read (%s). "+
				"Its own description: %s", snap.Slug, snap.ImageType, snap.Digest),
		}, nil
	}

	now := time.Now()
	head := fmt.Sprintf("Screenshot from %s, updated %s.", snap.Slug, describeAge(now.Sub(snap.UpdatedAt)))
	if snap.Stale(now) {
		head += " It is stale — confirm with the user that this is still what they mean."
	}
	if snap.Digest != "" {
		head += "\n" + snap.Digest
	}
	return agent.ToolResult{Text: head, Blocks: []agent.ContentBlock{blk}}, nil
}

// LightAppInsertTool hands a file to a running Light App — the other half of
// the loop view_lightapp opens: look at what the user drew, make something
// from it, put it back where they are working.
//
// This is the one direction that reaches *into* an app, and it stays narrow on
// purpose: a file and a note, delivered to an app the user has open, which the
// app is free to ignore. It is not a channel — nothing comes back on it, and
// the app still cannot see the conversation.
type LightAppInsertTool struct{}

func (LightAppInsertTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "insert_into_lightapp",
		Description: "Send an image you produced into one of the user's open Light Apps — for " +
			"example, put a generated picture onto their sketchpad canvas so they can keep " +
			"working on it. Use after making a file the user will want to use where they are " +
			"already working, rather than only telling them where it was saved. Call " +
			"lightapp_state first: the app has to be open, and it decides what to do with what " +
			"it receives.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug": map[string]any{
					"type":        "string",
					"description": "Which Light App to send to, as lightapp_state names it. Optional when exactly one app is open.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path of the image file to send (PNG, JPEG, WebP, GIF or AVIF).",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "Optional one-line note passed to the app alongside the file, e.g. what it is or where it came from.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (LightAppInsertTool) Execute(_ context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	path := strings.TrimSpace(stringArg(input, "path"))
	if path == "" {
		return agent.ToolResult{}, fmt.Errorf("insert_into_lightapp: path is required")
	}
	deliver := lightAppDelivererFn()
	if deliver == nil {
		return agent.ToolResult{
			Text: "Light Apps are only reachable from a running octo Web UI, and this session has " +
				"none attached. Tell the user where the file is instead.",
		}, nil
	}

	slug := strings.TrimSpace(stringArg(input, "slug"))
	apps := lightAppSnapshots()
	switch {
	case slug != "":
		if lightAppSnapshot(slug) == nil {
			return agent.ToolResult{
				Text: fmt.Sprintf("No Light App named %q is open. Call lightapp_state to see which are.", slug),
			}, nil
		}
	case len(apps) == 1:
		slug = apps[0].Slug
	case len(apps) == 0:
		return agent.ToolResult{
			Text: "No Light App is open, so there is nowhere to put it. Ask the user to open the " +
				"app, or just tell them the file path.",
		}, nil
	default:
		names := make([]string, 0, len(apps))
		for _, s := range apps {
			names = append(names, s.Slug)
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("Several Light Apps are open (%s). Pass `slug` to say which one.", strings.Join(names, ", ")),
		}, nil
	}

	if err := deliver(slug, path, strings.TrimSpace(stringArg(input, "note"))); err != nil {
		return agent.ToolResult{}, fmt.Errorf("insert_into_lightapp: %w", err)
	}
	return agent.ToolResult{
		Text: fmt.Sprintf("Sent to %s. The app decides what to do with it — check lightapp_state "+
			"to see whether it took it, and tell the user to look at the app.", slug),
	}, nil
}
