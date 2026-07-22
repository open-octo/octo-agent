package client

import (
	"crypto/rand"

	"github.com/flynn/noise"
)

// cipherSuite is the Noise suite the PoC uses: DH25519 for key agreement,
// ChaCha20-Poly1305 for the AEAD, SHA-256 for hashing — the WireGuard-style
// choice the design calls for.
var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

// session is one Noise XX exchange and the transport ciphers it yields. A phone
// has exactly one; a host has one per paired device.
type session struct {
	hs        *noise.HandshakeState
	initiator bool
	done      bool
	tx, rx    *noise.CipherState
}

// newSession builds a fresh handshake. The host is the responder; the phone is
// the initiator. Each generates a throwaway static keypair for the PoC — a real
// endpoint loads a persisted one from secure storage.
func newSession(isHost bool) (*session, error) {
	initiator := !isHost
	static, err := cipherSuite.GenerateKeypair(rand.Reader)
	if err != nil {
		return nil, err
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeXX,
		Initiator:     initiator,
		StaticKeypair: static,
	})
	if err != nil {
		return nil, err
	}
	return &session{hs: hs, initiator: initiator}, nil
}

// write produces the next outbound handshake message. When it completes the
// handshake, the transport ciphers are installed.
func (s *session) write(payload []byte) ([]byte, error) {
	msg, cs1, cs2, err := s.hs.WriteMessage(nil, payload)
	if err != nil {
		return nil, err
	}
	if cs1 != nil {
		s.install(cs1, cs2)
	}
	return msg, nil
}

// read consumes an inbound handshake message, installing the transport ciphers
// if it completes the handshake on this side.
func (s *session) read(msg []byte) ([]byte, error) {
	payload, cs1, cs2, err := s.hs.ReadMessage(nil, msg)
	if err != nil {
		return nil, err
	}
	if cs1 != nil {
		s.install(cs1, cs2)
	}
	return payload, nil
}

// install fixes the send/receive direction. flynn/noise always returns the
// initiator→responder cipher first, so the responder swaps them.
func (s *session) install(cs1, cs2 *noise.CipherState) {
	if s.initiator {
		s.tx, s.rx = cs1, cs2
	} else {
		s.tx, s.rx = cs2, cs1
	}
	s.done = true
}

func (s *session) encrypt(pt []byte) ([]byte, error) { return s.tx.Encrypt(nil, nil, pt) }
func (s *session) decrypt(ct []byte) ([]byte, error) { return s.rx.Decrypt(nil, nil, ct) }
