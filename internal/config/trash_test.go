package config

import (
	"testing"
	"time"
)

func TestTrashBounds_Defaults(t *testing.T) {
	var c Config // zero value = unset
	if got := c.TrashRetention(); got != 14*24*time.Hour {
		t.Errorf("default retention = %v, want 14d", got)
	}
	if got := c.TrashMaxBytes(); got != 10240*1024*1024 {
		t.Errorf("default cap = %d, want 10 GiB", got)
	}
	if !c.OverwriteBackupEnabled() {
		t.Error("overwrite backup should default to enabled")
	}
}

func TestTrashBounds_ExplicitAndDisabled(t *testing.T) {
	c := Config{Trash: TrashConfig{RetentionDays: 3, MaxSizeMB: 500}}
	if got := c.TrashRetention(); got != 3*24*time.Hour {
		t.Errorf("retention = %v, want 3d", got)
	}
	if got := c.TrashMaxBytes(); got != 500*1024*1024 {
		t.Errorf("cap = %d, want 500 MiB", got)
	}

	// Negative disables (returns the zero bound Enforce treats as "off").
	off := Config{Trash: TrashConfig{RetentionDays: -1, MaxSizeMB: -1}}
	if off.TrashRetention() != 0 {
		t.Error("negative retention should disable (0)")
	}
	if off.TrashMaxBytes() != 0 {
		t.Error("negative cap should disable (0)")
	}
}
