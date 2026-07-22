// Package client is the PoC's mock endpoint: a WebSocket peer that runs the
// Noise session the relay cannot read. The same type plays both roles — a host
// is the Noise responder and holds one session per paired device; a phone is
// the initiator and holds a single session to its host. The real system will
// grow a host-side tunnel goroutine and a phone-side native shim from exactly
// this shape, which is why the crypto lives here in the client and never in the
// relay.
//
// Handshake: Noise XX over DH25519 / ChaChaPoly / SHA256 — mutual authentication
// and forward secrecy from raw keypairs, no CA. XX has both sides transmit their
// static key during the handshake; production will likely use IK instead (the
// phone already knows the host's static key from the pairing QR, closing even a
// first-message window), but XX keeps the PoC self-contained.
package client

import (
	"fmt"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/wire"
)

// Message is one decrypted application record handed to the caller, tagged with
// the device it belongs to (meaningful on the host, which multiplexes devices).
type Message struct {
	Device string
	Data   []byte
}

// Client is one end of a tunnel. Construct with DialHost or DialPhone.
type Client struct {
	ws     *websocket.Conn
	isHost bool
	selfID string // phone: its relay-assigned device id

	writeM   sync.Mutex
	sessMu   sync.Mutex
	sessions map[string]*session

	recv   chan Message
	paired chan string
	ready  chan string
	errc   chan error
}

func newClient(ws *websocket.Conn, isHost bool) *Client {
	c := &Client{
		ws:       ws,
		isHost:   isHost,
		sessions: map[string]*session{},
		recv:     make(chan Message, 16),
		paired:   make(chan string, 1),
		ready:    make(chan string, 16),
		errc:     make(chan error, 1),
	}
	go c.loop()
	return c
}

// DialHost connects as the host of tunnelID, offering one or more one-time
// pairing tokens for phones to pair against (one per phone).
func DialHost(relayURL, tunnelID string, pairTokens ...string) (*Client, error) {
	q := url.Values{"tunnel": {tunnelID}}
	for _, t := range pairTokens {
		q.Add("pairtoken", t)
	}
	u := fmt.Sprintf("%s/host?%s", relayURL, q.Encode())
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return nil, err
	}
	return newClient(ws, true), nil
}

// DialPhone connects as a new phone presenting a one-time pairing token. The
// relay assigns it a device id (see WaitPaired) and it immediately drives the
// Noise handshake to completion (see WaitReady).
func DialPhone(relayURL, token string) (*Client, error) {
	u := fmt.Sprintf("%s/device?token=%s", relayURL, token)
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return nil, err
	}
	return newClient(ws, false), nil
}

// WaitPaired blocks until the phone learns its assigned device id.
func (c *Client) WaitPaired() string { return <-c.paired }

// WaitReady blocks until the next Noise session finishes its handshake and
// returns that session's device id.
func (c *Client) WaitReady() string { return <-c.ready }

// Recv delivers decrypted application messages.
func (c *Client) Recv() <-chan Message { return c.recv }

// Err delivers the first fatal error from the read loop.
func (c *Client) Err() <-chan error { return c.errc }

// Send encrypts data for the session addressed by device and writes it. On a
// phone, device is ignored (it has a single session).
func (c *Client) Send(device string, data []byte) error {
	if !c.isHost {
		device = c.selfID
	}
	s := c.session(device)
	if s == nil || !s.done {
		return fmt.Errorf("no ready session for device %q", device)
	}
	ct, err := s.encrypt(data)
	if err != nil {
		return err
	}
	return c.sendFrame(wire.Frame{Type: wire.TypeData, Device: device, Payload: ct})
}

// Close tears down the WebSocket.
func (c *Client) Close() error { return c.ws.Close() }

func (c *Client) sendFrame(f wire.Frame) error {
	c.writeM.Lock()
	defer c.writeM.Unlock()
	return f.WriteTo(c.ws)
}

func (c *Client) session(device string) *session {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	return c.sessions[device]
}

func (c *Client) setSession(device string, s *session) {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	c.sessions[device] = s
}

func (c *Client) fail(err error) {
	select {
	case c.errc <- err:
	default:
	}
}

// loop reads frames and drives handshakes and decryption. Both roles share it;
// the frame type says what to do.
func (c *Client) loop() {
	for {
		f, err := wire.Read(c.ws)
		if err != nil {
			c.fail(err)
			close(c.recv)
			return
		}
		switch f.Type {
		case wire.TypePaired:
			// Phone: learn our id and open the initiator handshake.
			c.selfID = f.Device
			s, err := newSession(c.isHost)
			if err != nil {
				c.fail(err)
				return
			}
			c.setSession(f.Device, s)
			msg, err := s.write(nil)
			if err != nil {
				c.fail(err)
				return
			}
			if err := c.sendFrame(wire.Frame{Type: wire.TypeHandshake, Device: f.Device, Payload: msg}); err != nil {
				c.fail(err)
				return
			}
			c.paired <- f.Device

		case wire.TypeDeviceJoined:
			// Host: a phone paired in; stand up the responder session for it.
			s, err := newSession(c.isHost)
			if err != nil {
				c.fail(err)
				return
			}
			c.setSession(f.Device, s)

		case wire.TypeHandshake:
			s := c.session(f.Device)
			if s == nil {
				c.fail(fmt.Errorf("handshake for unknown device %q", f.Device))
				return
			}
			if _, err := s.read(f.Payload); err != nil {
				c.fail(err)
				return
			}
			if !s.done {
				msg, err := s.write(nil)
				if err != nil {
					c.fail(err)
					return
				}
				if err := c.sendFrame(wire.Frame{Type: wire.TypeHandshake, Device: f.Device, Payload: msg}); err != nil {
					c.fail(err)
					return
				}
			}
			if s.done {
				c.ready <- f.Device
			}

		case wire.TypeData:
			s := c.session(f.Device)
			if s == nil || !s.done {
				c.fail(fmt.Errorf("data for device %q with no ready session", f.Device))
				return
			}
			pt, err := s.decrypt(f.Payload)
			if err != nil {
				c.fail(err)
				return
			}
			c.recv <- Message{Device: f.Device, Data: pt}
		}
	}
}
