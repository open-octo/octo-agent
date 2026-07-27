package main

import (
	"context"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	ghprovider "github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// releaseAssets mirrors a real release's asset listing: CLI archives whose
// names contain the same platform/arch substrings as the desktop artifacts,
// installers, checksum sidecars, and the swappable desktop artifacts.
var releaseAssets = []ghprovider.ReleaseAsset{
	{Name: "checksums.txt"},
	{Name: "desktop-checksums.txt"},
	{Name: "octo_1.15.0_darwin_all.tar.gz"},
	{Name: "octo_1.15.0_darwin_arm64.tar.gz"},
	{Name: "octo_1.15.0_windows_amd64.zip"},
	{Name: "octo_1.15.0_windows_arm64.zip"},
	{Name: "octo-setup.pkg"},
	{Name: "octo-setup.exe"},
	{Name: "octo-setup-arm64.exe"},
	{Name: "Octo-x86_64.AppImage"},
	{Name: "Octo-aarch64.AppImage"},
	{Name: "Octo-darwin-universal.zip"},
	{Name: "octo-desktop-windows-amd64.exe"},
	{Name: "octo-desktop-windows-arm64.exe"},
}

// The matcher must hit the desktop artifact by exact name — a substring
// heuristic would pick the CLI archive (same platform/arch in the name) and
// swap the GUI with the CLI binary.
func TestDesktopAssetIndex_ExactName(t *testing.T) {
	for _, want := range []string{
		"Octo-darwin-universal.zip",
		"octo-desktop-windows-amd64.exe",
		"octo-desktop-windows-arm64.exe",
	} {
		idx := desktopAssetIndex(releaseAssets, want)
		if idx < 0 {
			t.Fatalf("desktopAssetIndex(%q) = -1, want a match", want)
		}
		if got := releaseAssets[idx].Name; got != want {
			t.Fatalf("desktopAssetIndex(%q) picked %q", want, got)
		}
	}
}

func TestDesktopAssetIndex_AbsentAsset(t *testing.T) {
	// A release predating the desktop artifacts (or a failed packaging job)
	// must yield -1, not a near-miss like the CLI archive.
	cliOnly := releaseAssets[:6]
	if idx := desktopAssetIndex(cliOnly, desktopAssetName()); idx != -1 {
		t.Fatalf("desktopAssetIndex on CLI-only assets = %d (%s), want -1", idx, cliOnly[idx].Name)
	}
}

// stubProvider returns a canned Check result so verifiedOnly's gate can be
// exercised without a network.
type stubProvider struct {
	rel *updater.Release
	err error
}

func (s stubProvider) Name() string { return "stub" }
func (s stubProvider) Check(context.Context, updater.CheckRequest) (*updater.Release, error) {
	return s.rel, s.err
}
func (s stubProvider) Download(context.Context, *updater.Release, io.Writer, func(int64, int64)) error {
	return nil
}

// verifiedOnly must fail closed: a release without a digest (missing or
// incomplete desktop-checksums.txt) would otherwise install on TLS alone.
func TestVerifiedOnly_FailsClosedWithoutDigest(t *testing.T) {
	rel := func(v *updater.Verification) *updater.Release {
		return &updater.Release{Version: "9.9.9", Artifact: updater.Artifact{Filename: "x.zip"}, Verification: v}
	}
	cases := []struct {
		name   string
		rel    *updater.Release
		wantOK bool
	}{
		{"digest present", rel(&updater.Verification{DigestAlgo: "sha256", Digest: []byte{0x1}}), true},
		{"nil verification", rel(nil), false},
		{"empty digest", rel(&updater.Verification{DigestAlgo: "sha256"}), false},
	}
	for _, tc := range cases {
		got, err := verifiedOnly{stubProvider{rel: tc.rel}}.Check(context.Background(), updater.CheckRequest{})
		if tc.wantOK && (err != nil || got == nil) {
			t.Errorf("%s: got (%v, %v), want release through", tc.name, got, err)
		}
		if !tc.wantOK && err == nil {
			t.Errorf("%s: err = nil, want refusal", tc.name)
		}
	}
	// Up-to-date (nil, nil) and provider errors pass through untouched.
	if got, err := (verifiedOnly{stubProvider{}}).Check(context.Background(), updater.CheckRequest{}); got != nil || err != nil {
		t.Errorf("up-to-date: got (%v, %v), want (nil, nil)", got, err)
	}
}

// desktopAssetName must name an asset the release workflow actually attaches,
// on the platforms that in-place update (see canInplaceUpdate).
func TestDesktopAssetName_MatchesReleaseAssets(t *testing.T) {
	name := desktopAssetName()
	switch runtime.GOOS {
	case "darwin", "windows":
		if desktopAssetIndex(releaseAssets, name) < 0 {
			t.Fatalf("desktopAssetName() = %q — not among the assets the release workflow attaches", name)
		}
		if !strings.Contains(strings.ToLower(name), runtime.GOOS) {
			t.Fatalf("desktopAssetName() = %q — missing the %q platform marker", name, runtime.GOOS)
		}
	default:
		if name != "" {
			t.Fatalf("desktopAssetName() = %q on %s, want \"\" (no in-place update)", name, runtime.GOOS)
		}
	}
}
