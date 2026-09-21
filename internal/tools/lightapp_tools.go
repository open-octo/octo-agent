package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// ─── insert_into_lightapp: the one direction that reaches into an app ──────
//
// (lightapp_state and view_lightapp are gone — the mirror they read is
// retired in favour of the session-artifact mirror, artifact_mirror.go.)

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
			"lightapp_state first: the app has to be publishing its state to be reachable " +
			"here, and it decides what to do with what it receives.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug": map[string]any{
					"type":        "string",
					"description": "Which Light App to send to, as lightapp_state names it. Optional when exactly one app is open.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path of the image file to send (PNG, JPEG, GIF or WebP).",
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
