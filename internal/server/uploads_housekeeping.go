package server

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/uploads"
)

// channelTempAdapters lists the IM channel names that stage inbound file
// attachments under a namespaced OS-temp subdirectory (uploads.ChannelTempDir)
// instead of the bare OS temp root. Listed explicitly rather than derived
// reflectively, so wiring in a new adapter's cleanup is a one-line addition.
var channelTempAdapters = []string{"telegram", "dingtalk", "feishu", "discord", "wecom", "weixin"}

// StartUploadsHousekeeping ages out old attachment files in the background
// (#2004): ~/.octo/uploads (web uploads + IM inline images) and each IM
// channel adapter's namespaced temp directory (IM document/file attachments).
// Best-effort and safe to call unconditionally at server startup — both
// `octo serve` (cmd/octo, gated behind its own go-test housekeepingDisabled
// guard) and the desktop hub (cmd/octo-desktop, whose startHub only runs
// inside a live GUI event loop, never from `go test`) call this once.
func StartUploadsHousekeeping() {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	retention := cfg.UploadsRetention()
	if retention <= 0 {
		return
	}
	go func() {
		if dir, err := uploadsDirPath(); err == nil {
			_, _, _ = uploads.Sweep(dir, retention)
		}
		for _, adapter := range channelTempAdapters {
			if dir, err := uploads.ChannelTempDir(adapter); err == nil {
				_, _, _ = uploads.Sweep(dir, retention)
			}
		}
	}()
}

// uploadsDirPath returns ~/.octo/uploads without creating it. Unlike
// ensureUploadsDir (used by callers about to write a file there), the startup
// sweep has no reason to spin the directory into existence for an install
// that has never received an upload.
func uploadsDirPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("uploads: home dir: %w", err)
	}
	return filepath.Join(home, ".octo", uploadsDirName), nil
}
