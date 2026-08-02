package channel

import "testing"

// TestSetPlatform_EnabledKeyWins is the regression test for the UI toggle that
// could never switch a channel off: SetPlatform used to force Enabled=true on
// every call, discarding an explicit enabled:false.
func TestSetPlatform_EnabledKeyWins(t *testing.T) {
	var c Config

	// Explicit enabled:false must persist.
	c.SetPlatform("feishu", map[string]any{"enabled": false, "app_id": "a"})
	if c.IsEnabled("feishu") {
		t.Fatal("explicit enabled:false was overridden")
	}

	// Explicit enabled:true must persist.
	c.SetPlatform("feishu", map[string]any{"enabled": true})
	if !c.IsEnabled("feishu") {
		t.Fatal("explicit enabled:true was not honored")
	}

	// Absent enabled key keeps the legacy "configure ⇒ enable" behavior.
	c.SetPlatform("wecom", map[string]any{"bot_id": "b"})
	if !c.IsEnabled("wecom") {
		t.Fatal("credentials-only SetPlatform should auto-enable")
	}

	// The enabled key must be promoted to the struct field, never linger in
	// the raw config map.
	if _, ok := c.Channels["feishu"][0].Config["enabled"]; ok {
		t.Fatal("enabled key leaked into the platform config map")
	}
}
