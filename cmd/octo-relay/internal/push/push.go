// Package push sends content-free wakeup notifications. The relay consumes a
// host's wakeup frame and fires a generic "Octo has new activity" push at the
// phone's token — no session content, no token persistence, no token logging.
// APNs and FCM credentials belong to the relay operator (the octo host never
// holds them); tokens belong to the phones and pass through in memory only.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// The notification body is deliberately generic: push payloads transit
// Apple/Google, so the E2E design keeps them content-free.
const (
	notifTitle = "Octo"
	notifBody  = "Octo has new activity"
)

// Pusher fires one content-free wakeup at a platform token.
type Pusher interface {
	Wake(ctx context.Context, platform, token string) error
}

// Multi dispatches by platform name. A missing platform is an error the
// caller may log (without the token).
type Multi map[string]Pusher

func (m Multi) Wake(ctx context.Context, platform, token string) error {
	p, ok := m[platform]
	if !ok {
		return fmt.Errorf("no pusher configured for platform %q", platform)
	}
	return p.Wake(ctx, platform, token)
}

// FCM pushes through Firebase Cloud Messaging's HTTP v1 API using a service
// account. TokenSource handles OAuth; the project id comes from the
// credentials file.
type FCM struct {
	// Endpoint is the messages:send URL; NewFCM derives it from the project id.
	Endpoint string
	// Authorize adds the OAuth bearer token to a request.
	Authorize func(*http.Request) error
	// Client defaults to http.DefaultClient.
	Client *http.Client
}

func (f *FCM) Wake(ctx context.Context, _ string, token string) error {
	body, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"token": token,
			"notification": map[string]string{
				"title": notifTitle,
				"body":  notifBody,
			},
		},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if f.Authorize != nil {
		if err := f.Authorize(req); err != nil {
			return err
		}
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		// Status only — the response body can echo the token.
		return fmt.Errorf("fcm: status %d", res.StatusCode)
	}
	return nil
}

// fcmProject extracts project_id from a service-account JSON file.
func fcmProject(credFile string) (string, error) {
	raw, err := os.ReadFile(credFile)
	if err != nil {
		return "", err
	}
	var sa struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(raw, &sa); err != nil {
		return "", fmt.Errorf("fcm credentials: %w", err)
	}
	if sa.ProjectID == "" {
		return "", fmt.Errorf("fcm credentials: missing project_id")
	}
	return sa.ProjectID, nil
}
