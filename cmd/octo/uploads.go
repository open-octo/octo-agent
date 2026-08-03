package main

import (
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/server"
	"github.com/open-octo/octo-agent/internal/uploads"
)

// channelTempAdapters lists the IM channel names that stage inbound file
// attachments under a namespaced OS-temp subdirectory (uploads.ChannelTempDir)
// instead of the bare OS temp root. Listed explicitly rather than derived
// reflectively, so wiring in a new adapter's cleanup is a one-line addition.
var channelTempAdapters = []string{"telegram", "dingtalk", "feishu", "discord", "wecom", "weixin"}

// startUploadsHousekeeping ages out old attachment files in the background at
// startup: ~/.octo/uploads (web uploads + IM inline images, #2004) and each IM
// channel adapter's namespaced temp directory (IM document/file attachments).
// Mirrors startTrashHousekeeping's shape and reuses its housekeepingDisabled
// guard — best-effort, and disabled under `go test` for the same reason (many
// cmd/octo tests drive runServe against a developer's real ~/.octo).
func startUploadsHousekeeping() {
	if housekeepingDisabled {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		return
	}
	retention := cfg.UploadsRetention()
	if retention <= 0 {
		return
	}
	go func() {
		if dir, err := server.UploadsDir(); err == nil {
			_, _, _ = uploads.Sweep(dir, retention)
		}
		for _, adapter := range channelTempAdapters {
			if dir, err := uploads.ChannelTempDir(adapter); err == nil {
				_, _, _ = uploads.Sweep(dir, retention)
			}
		}
	}()
}
