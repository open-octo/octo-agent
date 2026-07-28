// In-place desktop updates via the Wails v3 updater (app.Updater): download
// the release artifact, verify it against the desktop-checksums.txt sidecar,
// stage it, and — after the user confirms in the updater window — swap the
// installed app and relaunch. Platforms whose install layout can't be swapped
// (see canInplaceUpdate) keep the notify-and-open flow: the tray item and the
// update toast send the user to the download page instead.
//
// Detection stays on internal/upgrade's releases/latest redirect (no API rate
// limit, mirror fallback); app.Updater only runs when the user asks to install.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/upgrade"
	"github.com/open-octo/octo-agent/internal/version"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	ghprovider "github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// releaseRepo is the GitHub repository the updater pulls desktop artifacts
// from — the "owner/repo" form of upgrade.BaseURL.
const releaseRepo = "open-octo/octo-agent"

// desktopChecksumAsset is the release sidecar carrying SHA-256 sums for the
// desktop artifacts (published by the release workflow's desktop-checksums
// job). goreleaser's checksums.txt covers only the CLI archives.
const desktopChecksumAsset = "desktop-checksums.txt"

// desktopAssetName returns the release asset the updater must download on
// this platform, or "" when the platform has no swappable desktop artifact.
// Names are matched exactly — the release also carries CLI archives whose
// names contain the same platform/arch substrings, so a heuristic matcher
// could pick the CLI binary and swap it over the GUI. Must stay in sync with
// the asset names the release workflow attaches.
func desktopAssetName() string {
	switch runtime.GOOS {
	case "darwin":
		// One universal (amd64+arm64) bundle serves both architectures.
		return "Octo-darwin-universal.zip"
	case "windows":
		return "octo-desktop-windows-" + runtime.GOARCH + ".exe"
	}
	return ""
}

// desktopAssetIndex returns the index of the asset whose name equals want, or
// -1 when absent (the provider then reports "no suitable asset" and the flow
// falls back to the download page).
func desktopAssetIndex(assets []ghprovider.ReleaseAsset, want string) int {
	for i, a := range assets {
		if a.Name == want {
			return i
		}
	}
	return -1
}

// updateHTTPClient replaces the provider's default client, whose 30-second
// http.Client.Timeout caps the WHOLE request — including streaming the
// response body — and so truncates a ~100 MB artifact download on any link
// slower than ~27 Mbps. Timeouts move to the connection phases instead; the
// body stream is unbounded (the updater window surfaces progress, and a lost
// connection still fails through the transport).
func updateHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: tr}
}

// verifiedOnly wraps a Provider to fail closed when a release carries no
// digest. The GitHub provider treats a missing checksum sidecar (say the
// desktop-checksums release job never ran) as "nothing to verify" and the
// updater would then install on TLS alone — silently weaker than the SHA-256
// verification this flow promises. An unverifiable release is an error here,
// which lands on the notify-and-open fallback like any other check failure.
type verifiedOnly struct {
	updater.Provider
}

func (p verifiedOnly) Check(ctx context.Context, req updater.CheckRequest) (*updater.Release, error) {
	rel, err := p.Provider.Check(ctx, req)
	if err != nil || rel == nil {
		return rel, err
	}
	if rel.Verification == nil || len(rel.Verification.Digest) == 0 {
		return nil, fmt.Errorf("release %s carries no digest for %s (missing %s?); refusing an unverified install",
			rel.Version, rel.Artifact.Filename, desktopChecksumAsset)
	}
	return rel, nil
}

// canInplaceUpdate reports whether this build may replace itself in place:
// a release build (upgrade.Eligible — a dev build must not be silently
// replaced by an older release), on a platform whose installed form the
// updater can actually swap. macOS swaps the whole .app bundle, so the
// binary must be running from one; the Windows exe is self-contained (web
// UI and ripgrep are go:embed'd), so replacing it alone is a complete
// upgrade. Linux ships as an AppImage, which runs from a read-only squashfs
// mount the updater would try to write through — notify-and-open stays.
func canInplaceUpdate() bool {
	if upgrade.Eligible() != nil {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		return isBundled()
	case "windows":
		return true
	}
	return false
}

// updaterWindowCSS patches the hover state of the built-in updater window's
// primary buttons ("Install Update", "Restart & Apply", "Try Again").
//
// Upstream (wails v3 alpha2.118, pkg/updater/assets/window.html) has
// `.u__btn:hover:not(:disabled) { background: var(--surface-2) }` at specificity
// 0,3,0, which outranks `.u__btn--primary { background: var(--accent) }` at
// 0,1,0 — and `.u__btn--primary:hover` only layers on a brightness filter, never
// re-asserting the accent background. So hovering a primary button swaps it to
// --surface-2 while its text stays --accent-fg (#ffffff): white on #f0f0f3 in
// the light palette, i.e. the button vanishes. (Dark mode hides the bug, where
// --surface-2 is #2c2c30.) Injected CSS lands last in <head>, so re-asserting
// both at equal specificity wins.
const updaterWindowCSS = `.u__btn--primary:hover:not(:disabled) {
  background: var(--accent);
  color: var(--accent-fg);
}`

// initInplaceUpdater configures app.Updater against the GitHub release feed.
// Returns false (logging why) when configuration fails; the caller then keeps
// the notify-and-open flow, so an updater problem never blocks updates
// entirely. Verification is digest-only (no signing key yet): the sidecar's
// SHA-256 over GitHub's TLS — the same trust anchor as the CLI's `octo
// upgrade`.
func initInplaceUpdater(app *application.App) bool {
	asset := desktopAssetName()
	if asset == "" {
		return false
	}
	gh, err := ghprovider.New(ghprovider.Config{
		Repository:    releaseRepo,
		ChecksumAsset: desktopChecksumAsset,
		HTTPClient:    updateHTTPClient(),
		AssetMatcher: func(_ updater.CheckRequest, assets []ghprovider.ReleaseAsset) int {
			return desktopAssetIndex(assets, asset)
		},
	})
	if err != nil {
		log.Printf("octo-desktop: in-place updater disabled (provider): %v", err)
		return false
	}
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: strings.TrimPrefix(version.Version, "v"),
		Providers:      []updater.Provider{verifiedOnly{gh}},
		Window:         &updater.BuiltinWindow{CSS: updaterWindowCSS},
	}); err != nil {
		log.Printf("octo-desktop: in-place updater disabled (init): %v", err)
		return false
	}
	return true
}

// startUpdateFlow routes an update request — the tray's "Update to vX" item,
// a tap on the update toast, or the web badge's "Update Now" — to the in-place
// updater when this build supports it, else to the download page. Runs on a
// background goroutine: CheckAndInstall blocks for the whole download.
// Single-flighted across those entry points: a second trigger while a flow is
// running would tear down the first flow's window and then trip over its
// still-running download — the running flow's window is already the surface.
func startUpdateFlow(bridge *nativeBridge) {
	if !bridge.inplaceUpdate.Load() {
		_ = bridge.OpenExternal(upgrade.DownloadPageURL)
		return
	}
	if !bridge.updateFlowBusy.CompareAndSwap(false, true) {
		return
	}
	defer bridge.updateFlowBusy.Store(false)
	// CheckAndInstall opens the updater window and walks check → download →
	// verify → stage; the window then prompts for the restart that performs
	// the swap. User choices (skip / remind / cancel) are not errors and end
	// the flow quietly. A genuine failure falls back to the download page —
	// the user asked for an update, so never leave them at a dead end.
	if err := bridge.app.Updater.CheckAndInstall(context.Background()); err != nil {
		if errors.Is(err, updater.ErrDownloadInProgress) {
			// Belt over the CAS: an earlier flow's download still owns the
			// updater window; this attempt has nothing to add.
			return
		}
		log.Printf("octo-desktop: in-place update: %v", err)
		bridge.Notify(L().updTitle, L().updInplaceFailed)
		_ = bridge.OpenExternal(upgrade.DownloadPageURL)
	}
}
