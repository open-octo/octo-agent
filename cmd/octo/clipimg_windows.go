//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// captureClipboardImage reads an image from the Windows clipboard via
// PowerShell, saving it to a temp PNG and reading it back. Prefers pwsh
// (PowerShell 7+) and falls back to the built-in powershell.exe — the same
// selection the terminal tool makes. Returns a friendly error when the
// clipboard holds no image.
func captureClipboardImage() (data []byte, mime string, err error) {
	shell := "powershell"
	if p, e := exec.LookPath("pwsh"); e == nil {
		shell = p
	}

	tmp, err := os.CreateTemp("", "octo-clip-*.png")
	if err != nil {
		return nil, "", fmt.Errorf("clipboard: temp file: %w", err)
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(path)

	// Get-Image returns null when the clipboard has no bitmap; we print a
	// sentinel so the caller can show a friendly notice. The path is injected
	// with doubled backslashes so it's a valid PowerShell single-quoted string.
	psPath := strings.ReplaceAll(path, `\`, `\\`)
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img -eq $null) { Write-Output 'NO_IMAGE' }
else { $img.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'OK' }`, psPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil, "", fmt.Errorf("clipboard: powershell: %w", err)
	}
	if strings.Contains(string(out), "NO_IMAGE") {
		return nil, "", fmt.Errorf("no image in clipboard")
	}

	data, err = os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("clipboard: read: %w", err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("no image in clipboard")
	}
	return data, "image/png", nil
}
