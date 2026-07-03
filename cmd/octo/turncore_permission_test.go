package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPlainAskPermission_ShowsFullCommand(t *testing.T) {
	var out bytes.Buffer
	v := newPlainView(nil, &out, &out, verbosityNormal, true)
	cmd := "git push --force origin main && echo " + strings.Repeat("y", 150)
	resp, err := v.Ask(context.Background(), UserPrompt{
		Kind:      KindPermission,
		ToolName:  "terminal",
		ToolInput: map[string]any{"command": cmd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Allow {
		t.Error("nil reader must deny")
	}
	if !strings.Contains(out.String(), cmd) {
		t.Errorf("plain prompt must show the full command; got:\n%s", out.String())
	}
}
