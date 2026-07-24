// Package wire defines the framing that a host and a device exchange with the
// relay. A frame is the smallest routable unit: the relay reads its addressing
// header and copies it to the peer, but never looks inside Payload — that field
// carries either a Noise handshake message or a Noise-encrypted application
// record, both opaque to the relay by construction.
package wire

import (
	"encoding/json"

	"github.com/gorilla/websocket"
)

// Frame type tags. These describe the payload's meaning to the *clients*; the
// relay treats data and handshake frames identically (opaque payload, routed by
// address). Only the control tags below are ever produced or consumed by the
// relay itself.
const (
	// TypeHandshake carries one Noise handshake message. Client-to-client,
	// forwarded verbatim by the relay.
	TypeHandshake = "handshake"
	// TypeData carries one Noise-encrypted application record. Client-to-client,
	// forwarded verbatim.
	TypeData = "data"

	// TypePaired is sent by the relay to a device once its pairing token has
	// matched a host; Device carries the id the relay assigned it.
	TypePaired = "paired"
	// TypeDeviceJoined is sent by the relay to a host when a new device pairs
	// into its tunnel; Device carries that device's id.
	TypeDeviceJoined = "device_joined"
	// TypeWakeup is sent by a host and CONSUMED by the relay (never forwarded):
	// a request to fire a content-free push at an offline phone. Device names
	// the target so the relay can skip the push when that phone is currently
	// connected; Payload is a WakeupPayload.
	TypeWakeup = "wakeup"
)

// WakeupPayload rides a TypeWakeup frame. Unlike every data/handshake payload
// it is NOT end-to-end ciphertext — it is host→relay operational metadata,
// deliberately limited to exactly what a push needs: the token and which push
// service it belongs to. No session content ever goes here, and the relay
// must never persist or log the token.
type WakeupPayload struct {
	PushToken string `json:"push_token"`
	Platform  string `json:"platform"` // "apns" | "fcm"
}

// DecodeWakeup parses a TypeWakeup frame's payload.
func DecodeWakeup(payload []byte) (WakeupPayload, error) {
	var p WakeupPayload
	err := json.Unmarshal(payload, &p)
	return p, err
}

// Frame is one message on the wire between a client and the relay.
//
// Tunnel and Device address the frame. On a host→device frame the host sets
// Device to pick the recipient; on a device→host frame the relay stamps Device
// so the host learns which paired phone sent it (a device never needs to name
// its own tunnel — the relay derives both from the connection). Payload is
// end-to-end material the relay must not, and cannot, interpret.
type Frame struct {
	Type    string `json:"t"`
	Tunnel  string `json:"tn,omitempty"`
	Device  string `json:"d,omitempty"`
	Payload []byte `json:"p,omitempty"` // base64 in JSON; opaque to the relay
}

// WriteTo marshals f and writes it as a single WebSocket text message.
func (f Frame) WriteTo(conn *websocket.Conn) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, b)
}

// Read reads and unmarshals the next frame from conn.
func Read(conn *websocket.Conn) (Frame, error) {
	_, b, err := conn.ReadMessage()
	if err != nil {
		return Frame{}, err
	}
	var f Frame
	if err := json.Unmarshal(b, &f); err != nil {
		return Frame{}, err
	}
	return f, nil
}
