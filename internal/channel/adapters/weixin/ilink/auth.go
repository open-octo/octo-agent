// Package ilink implements the WeChat iLink Bot HTTP protocol.
package ilink

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultCredPath returns ~/.octo/weixin-credentials.json
func DefaultCredPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".octo", "weixin-credentials.json")
}

// LoadCredentials loads stored credentials from disk.
func LoadCredentials(path string) (*Credentials, error) {
	if path == "" {
		path = DefaultCredPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// SaveCredentials persists credentials to disk with 0600 permissions.
func SaveCredentials(creds *Credentials, path string) error {
	if path == "" {
		path = DefaultCredPath()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(creds, "", "  ")
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// ClearCredentials removes stored credentials.
func ClearCredentials(path string) error {
	if path == "" {
		path = DefaultCredPath()
	}
	return os.Remove(path)
}

// LoginOptions configures the login flow.
type LoginOptions struct {
	BaseURL  string
	CredPath string
	Force    bool
	// QRBaseURL overrides the host used for QR issue/poll. Tests point it at
	// a stub; empty means the production fixedQRBaseURL.
	QRBaseURL string
	OnQRURL   func(url string)
	OnScanned func()
	OnExpired func()
}

const (
	maxQRRefreshCount = 3
	fixedQRBaseURL    = "https://ilinkai.weixin.qq.com"
)

// Login performs QR code login, returning credentials.
// If stored credentials exist and Force is false, returns them directly.
// Handles IDC redirect (scaned_but_redirect) and limits QR refreshes.
func Login(ctx context.Context, client *Client, opts LoginOptions) (*Credentials, error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	if !opts.Force {
		creds, err := LoadCredentials(opts.CredPath)
		if err == nil && creds != nil {
			return creds, nil
		}
	}

	qrBaseURL := opts.QRBaseURL
	if qrBaseURL == "" {
		qrBaseURL = fixedQRBaseURL
	}

	qrRefreshCount := 0
	for {
		qrRefreshCount++
		if qrRefreshCount > maxQRRefreshCount {
			return nil, fmt.Errorf("QR code expired %d times — login aborted", maxQRRefreshCount)
		}

		qr, err := client.GetQRCode(ctx, qrBaseURL)
		if err != nil {
			return nil, fmt.Errorf("get QR code: %w", err)
		}

		if opts.OnQRURL != nil {
			opts.OnQRURL(qr.QRCodeImgURL)
		} else {
			fmt.Fprintf(os.Stderr, "[weixin] Scan this URL in WeChat: %s\n", qr.QRCodeImgURL)
		}

		lastStatus := ""
		currentPollBaseURL := qrBaseURL
		for {
			status, err := client.PollQRStatus(ctx, currentPollBaseURL, qr.QRCode)
			if err != nil {
				return nil, fmt.Errorf("poll QR status: %w", err)
			}

			if status.Status != lastStatus {
				lastStatus = status.Status
				switch status.Status {
				case "scaned":
					if opts.OnScanned != nil {
						opts.OnScanned()
					} else {
						fmt.Fprintln(os.Stderr, "[weixin] QR scanned — confirm in WeChat")
					}
				case "expired":
					if opts.OnExpired != nil {
						opts.OnExpired()
					} else {
						fmt.Fprintln(os.Stderr, "[weixin] QR expired — requesting new one")
					}
				case "confirmed":
					fmt.Fprintln(os.Stderr, "[weixin] Login confirmed")
				}
			}

			if status.Status == "confirmed" {
				if status.BotToken == "" || status.BotID == "" || status.UserID == "" {
					return nil, fmt.Errorf("login confirmed but missing credentials")
				}
				resolvedBase := baseURL
				if status.BaseURL != "" {
					resolvedBase = status.BaseURL
				}
				creds := &Credentials{
					Token:     status.BotToken,
					BaseURL:   resolvedBase,
					AccountID: status.BotID,
					UserID:    status.UserID,
					SavedAt:   time.Now().UTC().Format(time.RFC3339),
				}
				if err := SaveCredentials(creds, opts.CredPath); err != nil {
					fmt.Fprintf(os.Stderr, "[weixin] Warning: could not save credentials: %v\n", err)
				}
				return creds, nil
			}

			// Handle IDC redirect
			if status.Status == "scaned_but_redirect" {
				if status.RedirectHost != "" {
					currentPollBaseURL = "https://" + status.RedirectHost
					fmt.Fprintf(os.Stderr, "[weixin] IDC redirect → %s\n", status.RedirectHost)
				}
				time.Sleep(2 * time.Second)
				continue
			}

			if status.Status == "expired" {
				break // Outer loop gets a new QR
			}

			time.Sleep(2 * time.Second)
		}
	}
}
