// octo-relay is a nested module so its transport dependencies — the Noise
// implementation (flynn/noise), and later the APNs/FCM push libraries — never
// enter the main module's go.mod. The relay is a separate deployable that the
// CLI binary never imports, so keeping it out of the parent module preserves
// the CLI's zero-dependency static-binary story. The parent's
// `go build ./...` / `go test ./...` skip this directory entirely.
//
// The relay is self-contained: it does not import the parent's packages, so
// there is no replace directive. It only bridges opaque, end-to-end-encrypted
// frames between a host and its paired devices — it never terminates the agent
// protocol, so it needs nothing from internal/agent or internal/server.
module github.com/open-octo/octo-agent/cmd/octo-relay

go 1.25.0

require (
	github.com/flynn/noise v1.1.0
	github.com/gorilla/websocket v1.5.3
	github.com/sideshow/apns2 v0.25.0
	golang.org/x/oauth2 v0.36.0
)

require (
	cloud.google.com/go/compute/metadata v0.3.0 // indirect
	github.com/golang-jwt/jwt/v4 v4.4.1 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
)
