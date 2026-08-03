package config

import (
	"testing"
	"time"
)

func TestUploadsRetention_Default(t *testing.T) {
	var c Config // zero value = unset
	if got := c.UploadsRetention(); got != 30*24*time.Hour {
		t.Errorf("default retention = %v, want 30d", got)
	}
}

func TestUploadsRetention_ExplicitAndDisabled(t *testing.T) {
	c := Config{Uploads: UploadsConfig{RetentionDays: 5}}
	if got := c.UploadsRetention(); got != 5*24*time.Hour {
		t.Errorf("retention = %v, want 5d", got)
	}

	off := Config{Uploads: UploadsConfig{RetentionDays: -1}}
	if off.UploadsRetention() != 0 {
		t.Error("negative retention should disable (0)")
	}
}
