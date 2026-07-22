// Package tunnel implements the host side of the managed tunnel: the goroutine
// that dials octo's relay, runs a Noise session per paired device, and bridges
// each device's decrypted traffic to the local server's loopback /ws as an
// ordinary key-authenticated client. From internal/server's view it is one more
// /ws client — the tunnel adds no endpoint, route, or protocol to the server.
//
// This package holds the CLI's own copy of the wire frame. The relay
// (cmd/octo-relay) is a separate deployable with its own copy: the two agree on
// a JSON wire contract, not on shared Go code, so the relay stays cleanly
// extractable to its own repository. The frame is small; duplicating it across
// the service boundary is the honest cost of that independence.
package tunnel

import (
	"encoding/json"

	"github.com/gorilla/websocket"
)

// Frame type tags. handshake and data are client-to-client (opaque to the
// relay); paired and device_joined are control frames the relay emits.
const (
	frameHandshake    = "handshake"
	frameData         = "data"
	frameDeviceJoined = "device_joined"
	framePaired       = "paired"
)

// frame is one message between the host and the relay. Tunnel/Device address it;
// Payload carries Noise handshake or encrypted application bytes, opaque to the
// relay. It mirrors cmd/octo-relay's wire.Frame field-for-field (same JSON).
type frame struct {
	Type    string `json:"t"`
	Tunnel  string `json:"tn,omitempty"`
	Device  string `json:"d,omitempty"`
	Payload []byte `json:"p,omitempty"`
}

func writeFrame(conn *websocket.Conn, f frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, b)
}

func readFrame(conn *websocket.Conn) (frame, error) {
	_, b, err := conn.ReadMessage()
	if err != nil {
		return frame{}, err
	}
	var f frame
	if err := json.Unmarshal(b, &f); err != nil {
		return frame{}, err
	}
	return f, nil
}
