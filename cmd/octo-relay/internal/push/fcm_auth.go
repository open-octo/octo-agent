package push

import (
	"fmt"
	"net/http"
	"os"

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
	src := jwtCfg.TokenSource(nil)
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
