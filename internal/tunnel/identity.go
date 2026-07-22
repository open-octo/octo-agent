package tunnel

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/flynn/noise"
)

// Identity is a host's stable tunnel identity: its Noise static keypair and the
// tunnel id it presents on the relay. It is generated once and persisted so a
// host keeps the same identity — and its set of paired devices — across
// restarts. The private key never leaves this process; the relay only ever sees
// the tunnel id.
type Identity struct {
	tunnelID string
	static   noise.DHKey
}

// TunnelID is the host's tunnel identity on the relay (its SNI subdomain in
// production).
func (i *Identity) TunnelID() string { return i.tunnelID }

// PublicKeyBase64 is the host's static public key — the value a pairing QR
// carries so a phone can authenticate the host end to end.
func (i *Identity) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(i.static.Public)
}

// identityFile is the on-disk form of an Identity (~/.octo/tunnel.json).
type identityFile struct {
	TunnelID   string `json:"tunnel_id"`
	PrivateKey string `json:"private_key"` // base64
	PublicKey  string `json:"public_key"`  // base64
}

// LoadOrCreateIdentity reads the identity at path, or generates and persists a
// fresh one (0600) when the file does not exist.
func LoadOrCreateIdentity(path string) (*Identity, error) {
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		return decodeIdentity(b)
	case errors.Is(err, os.ErrNotExist):
		return createIdentity(path)
	default:
		return nil, err
	}
}

func decodeIdentity(b []byte) (*Identity, error) {
	var f identityFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("tunnel: parse identity: %w", err)
	}
	priv, err := base64.StdEncoding.DecodeString(f.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("tunnel: decode private key: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(f.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("tunnel: decode public key: %w", err)
	}
	if f.TunnelID == "" || len(priv) == 0 || len(pub) == 0 {
		return nil, fmt.Errorf("tunnel: incomplete identity file")
	}
	return &Identity{tunnelID: f.TunnelID, static: noise.DHKey{Private: priv, Public: pub}}, nil
}

func createIdentity(path string) (*Identity, error) {
	kp, err := generateKeypair()
	if err != nil {
		return nil, err
	}
	tid, err := randomTunnelID()
	if err != nil {
		return nil, err
	}
	f := identityFile{
		TunnelID:   tid,
		PrivateKey: base64.StdEncoding.EncodeToString(kp.Private),
		PublicKey:  base64.StdEncoding.EncodeToString(kp.Public),
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	// Don't rely on ~/.octo already existing: on a fresh machine the access key
	// can come from OCTO_ACCESS_KEY, so server startup may not have created it.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("tunnel: create state dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("tunnel: write identity: %w", err)
	}
	return &Identity{tunnelID: tid, static: kp}, nil
}

// randomTunnelID returns 128 bits of hex — enough to be unguessable as an SNI
// label and collision-free across hosts.
func randomTunnelID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
