package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CaptureDownload directs downloads to dir, runs trigger (which should click
// the thing that starts a download), and waits for the download to finish —
// returning the saved file's path.
//
// This is required for systems where the file is produced client-side: the raw
// HTTP response is not the file, so the only way to obtain it is to let the page
// generate it and capture what lands on disk.
func (b *Browser) CaptureDownload(ctx context.Context, dir string, trigger func() error) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	willBegin, unsub1 := b.cli.subscribe("Browser.downloadWillBegin", "")
	defer unsub1()
	progress, unsub2 := b.cli.subscribe("Browser.downloadProgress", "")
	defer unsub2()

	if _, err := b.cli.call(ctx, "", "Browser.setDownloadBehavior", map[string]any{
		"behavior":      "allow",
		"downloadPath":  dir,
		"eventsEnabled": true,
	}); err != nil {
		return "", fmt.Errorf("set download behavior: %w", err)
	}

	if err := trigger(); err != nil {
		return "", err
	}

	// Identify the download (guid + suggested filename).
	var guid, filename string
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case ev := <-willBegin:
		var w struct {
			GUID              string `json:"guid"`
			SuggestedFilename string `json:"suggestedFilename"`
		}
		if err := json.Unmarshal(ev.Params, &w); err != nil {
			return "", err
		}
		guid, filename = w.GUID, w.SuggestedFilename
	case <-time.After(30 * time.Second):
		return "", fmt.Errorf("download did not begin within 30s")
	}

	// Wait for completion of that download.
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case ev := <-progress:
			var pr struct {
				GUID  string `json:"guid"`
				State string `json:"state"`
			}
			if err := json.Unmarshal(ev.Params, &pr); err != nil {
				return "", err
			}
			if pr.GUID != guid {
				continue
			}
			switch pr.State {
			case "completed":
				return resolveDownloadPath(dir, filename), nil
			case "canceled":
				return "", fmt.Errorf("download %s canceled", filename)
			}
		case <-time.After(60 * time.Second):
			return "", fmt.Errorf("download %s did not complete within 60s", filename)
		}
	}
}

// resolveDownloadPath returns dir/filename when it exists, otherwise the newest
// file in dir (a fallback for Chrome versions that name the file differently).
func resolveDownloadPath(dir, filename string) string {
	candidate := filepath.Join(dir, filename)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return candidate
	}
	var newest string
	var newestMod time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestMod) {
			newestMod = info.ModTime()
			newest = filepath.Join(dir, e.Name())
		}
	}
	if newest != "" {
		return newest
	}
	return candidate
}
