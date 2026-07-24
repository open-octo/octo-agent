package push

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// NewFCM builds an FCM v1 pusher from a service-account JSON file: the
// project id comes from the file, OAuth bearer tokens from a JWT token
// source (cached/refreshed by oauth2 internally).
func NewFCM(credFile string) (*FCM, error) {
	project, err := fcmProject(credFile)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(credFile)
	if err != nil {
		return nil, err
	}
	jwtCfg, err := google.JWTConfigFromJSON(raw, fcmScope)
	if err != nil {
		return nil, fmt.Errorf("fcm credentials: %w", err)
	}
	// A bounded client for the token endpoint: TokenSource(nil) would use
	// http.DefaultClient (no timeout), and Token() ignores the per-wake
	// context — a hung Google endpoint would strand wakeup goroutines behind
	// ReuseTokenSource's mutex indefinitely.
	authCtx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: 10 * time.Second})
	src := jwtCfg.TokenSource(authCtx)
	return &FCM{
		Endpoint: "https://fcm.googleapis.com/v1/projects/" + project + "/messages:send",
		Authorize: func(req *http.Request) error {
			tok, err := src.Token()
			if err != nil {
				return fmt.Errorf("fcm auth: %w", err)
			}
			tok.SetAuthHeader(req)
			return nil
		},
	}, nil
}
