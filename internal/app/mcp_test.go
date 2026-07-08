package app

import (
	"context"
	"io"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/tools"
)

func TestToolSearchConfigFrom(t *testing.T) {
	cases := []struct {
		enabled string
		want    tools.ToolSearchMode
	}{
		{"on", tools.ToolSearchOn},
		{"off", tools.ToolSearchOff},
		{"", tools.ToolSearchAuto},
		{"garbage", tools.ToolSearchAuto},
	}
	for _, c := range cases {
		got := ToolSearchConfigFrom(config.ToolSearchConfig{
			Enabled:      c.enabled,
			ThresholdPct: 42,
		})
		if got.Mode != c.want {
			t.Errorf("enabled=%q: mode = %v, want %v", c.enabled, got.Mode, c.want)
		}
		if got.ThresholdPct != 42 {
			t.Errorf("enabled=%q: numeric field not passed through: %+v", c.enabled, got)
		}
	}
}

// TestConnectMCPNoServers verifies the no-config path: with no MCP config under
// HOME or the project dir, ConnectMCP is a no-op that returns a non-nil cleanup
// and registers nothing.
func TestConnectMCPNoServers(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows home

	cleanup, err := ConnectMCP(context.Background(), t.TempDir(), io.Discard)
	if err != nil {
		t.Fatalf("ConnectMCP: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup must be non-nil even when no servers connect")
	}
	cleanup() // must not panic
}
