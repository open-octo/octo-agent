package push

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/payload"
	"github.com/sideshow/apns2/token"
)

// APNS pushes through Apple's HTTP/2 API with .p8 token auth (no per-app
// certificates to rotate). sideshow/apns2 handles the JWT/HTTP2/error-code
// details we'd rather not hand-roll on a security-adjacent path.
type APNS struct {
	client *apns2.Client
	topic  string // the app's bundle id
}

// APNSConfig locates the operator's APNs credentials.
type APNSConfig struct {
	KeyFile string // .p8 signing key
	KeyID   string
	TeamID  string
	Topic   string // bundle id, e.g. dev.octo.mobile
}

// NewAPNS loads the .p8 key and returns a production-environment client.
func NewAPNS(cfg APNSConfig) (*APNS, error) {
	if cfg.KeyFile == "" || cfg.KeyID == "" || cfg.TeamID == "" || cfg.Topic == "" {
		return nil, fmt.Errorf("apns: key file, key id, team id and topic are all required")
	}
	authKey, err := token.AuthKeyFromFile(cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("apns: load key: %w", err)
	}
	tok := &token.Token{AuthKey: authKey, KeyID: cfg.KeyID, TeamID: cfg.TeamID}
	return &APNS{client: apns2.NewTokenClient(tok).Production(), topic: cfg.Topic}, nil
}

func (a *APNS) Wake(ctx context.Context, _ string, deviceToken string) error {
	n := &apns2.Notification{
		DeviceToken: deviceToken,
		Topic:       a.topic,
		Payload:     payload.NewPayload().AlertTitle(notifTitle).AlertBody(notifBody).Sound("default"),
	}
	res, err := a.client.PushWithContext(ctx, n)
	if err != nil {
		// apns2 puts the device token in the request URL, and a transport
		// failure surfaces as *url.Error whose Error() embeds that URL — the
		// caller logs our return value, so strip the URL and keep only the
		// operation and underlying cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("apns: %s: %w", ue.Op, ue.Err)
		}
		return fmt.Errorf("apns: %v", err)
	}
	if !res.Sent() {
		// Reason strings are Apple error codes (BadDeviceToken, …), token-free.
		return fmt.Errorf("apns: status %d reason %s", res.StatusCode, res.Reason)
	}
	return nil
}
