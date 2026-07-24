package tunnel

import (
	"crypto/rand"

	"github.com/flynn/noise"
)

// cipherSuite is the Noise suite for the tunnel: DH25519 / ChaCha20-Poly1305 /
// SHA-256 — the WireGuard-style choice the design calls for. It must match the
// phone shim's suite exactly; the two derive a shared session the relay has no
// key for.
var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

// session is one Noise XX exchange and the transport ciphers it yields. The host
// is the responder; the phone shim is the initiator. The type is role-generic so
// the test's mock phone can drive the initiator side with the same code.
type session struct {
	hs        *noise.HandshakeState
	initiator bool
	done      bool
	tx, rx    *noise.CipherState
	// peerStatic is the remote end's static public key, available once the XX
	// handshake completes. It is the only identity of a phone that is stable
	// across reconnects (relay-assigned device ids are per-connection), so the
	// host keys per-device state — like push-token registrations — on it.
	peerStatic []byte
}

// newSession builds a fresh handshake for the given role. Each side supplies its
// static keypair; the host loads a persisted one, a phone holds its own in
// secure storage. (The PoC generates throwaway keys — see GenerateKeypair use in
// callers.)
func newSession(initiator bool, static noise.DHKey) (*session, error) {
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

// generateKeypair mints a static X25519 keypair for the configured suite.
func generateKeypair() (noise.DHKey, error) {
	return cipherSuite.GenerateKeypair(rand.Reader)
}

// write produces the next outbound handshake message, installing transport
// ciphers if it completes the handshake.
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

// read consumes an inbound handshake message, installing transport ciphers if it
// completes the handshake on this side.
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

// install fixes send/receive direction. flynn/noise always returns the
// initiator→responder cipher first, so the responder swaps them.
func (s *session) install(cs1, cs2 *noise.CipherState) {
	if s.initiator {
		s.tx, s.rx = cs1, cs2
	} else {
		s.tx, s.rx = cs2, cs1
	}
	s.peerStatic = s.hs.PeerStatic()
	s.done = true
}

func (s *session) encrypt(pt []byte) ([]byte, error) { return s.tx.Encrypt(nil, nil, pt) }
func (s *session) decrypt(ct []byte) ([]byte, error) { return s.rx.Decrypt(nil, nil, ct) }
