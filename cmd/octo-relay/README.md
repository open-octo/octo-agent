# octo-relay (PoC)

A single-node proof-of-concept of the managed-tunnel relay from
[`dev-docs/mobile-managed-tunnel-design.md`](../../dev-docs/mobile-managed-tunnel-design.md).

The relay is a **dumb pipe**: it brokers pairing between a host and a phone,
then bridges opaque frames between them by `(tunnel id, device id)`. It runs no
Noise session and holds no key, so everything it copies is ciphertext.

## What this PoC proves

- **Pairing broker** — a phone presenting a one-time token is matched to the
  host that offered it and assigned a device id; the token is spent on match.
- **Bidirectional bridge** — after a Noise XX handshake (run entirely in the
  clients), application records flow phone↔host, addressed by device id.
- **End-to-end confidentiality, structurally** — the test instruments the relay
  to capture every payload it forwards and asserts none contains the plaintext.

## What it deliberately omits

- **Push wakeups (APNs/FCM)** — needs real push credentials and a device.
- **Multi-node SNI-hash routing** — a scaling/ops concern, not a correctness one.

Both slot in on top of this transport core; the design doc covers where.

## Layout

| Path | Role |
|---|---|
| `internal/wire` | the `Frame` the clients and relay exchange (payload opaque to the relay) |
| `internal/relay` | the broker + bridge, and the end-to-end test |
| `internal/client` | mock endpoint: WebSocket + Noise, plays host (responder) and phone (initiator) |
| `main.go` | runs the relay on `--addr` |

It is a **nested module** (its own `go.mod`) so the Noise dependency never
enters the CLI's zero-dependency build. The parent `go build ./...` skips it.

## Run

```bash
cd cmd/octo-relay
go test ./...        # the end-to-end proof
go run . --addr :8090
```
