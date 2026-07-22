# octo-mobile

octo's iOS and Android client — a **thin remote client** of an `octo serve`
instance, not a second agent. It wraps the existing web frontend
(`internal/server/webdist`) in a Capacitor shell and adds the native pieces a
browser can't provide. See
[`dev-docs/mobile-managed-tunnel-design.md`](../dev-docs/mobile-managed-tunnel-design.md).

It lives here as a subdirectory for now; it will move to a sibling repository
(like `octo-vscode` / `octo-obsidian`) once its shape settles.

## Status

This is the **verifiable TypeScript groundwork**, landed before the native app:

- `src/pairing.ts` — parses the `octo-pair://v1` URL a host's QR encodes (the
  contract the host side already produces in `cmd/octo/serve_tunnel.go`). Tested.
- `src/transport.ts` — the `Transport` interface (one protocol, two transports:
  self-tunnel and managed-relay) with stubbed implementations.
- `src/native-bridge.ts` — the `OctoTunnel` Capacitor plugin contract the
  managed-relay transport calls into.

## Deferred (needs the iOS/Android toolchains — build on a Mac)

The parts that cannot be compiled or tested in a plain Go/Node checkout:

- The **Capacitor project** itself (`capacitor.config`, the iOS/Android
  platform folders, bundling `webdist` into the app).
- The **native tunnel plugin** (Swift + Kotlin) that runs the Noise handshake
  and holds the device keypair in the Keychain / Keystore — by design the keys
  and handshake never enter JavaScript, so this cannot be a JS module.
- The other native plugins (biometric unlock, push registration, QR scan,
  local notifications, share).

## Develop

```bash
cd mobile
npm install
npm test        # vitest — the pairing parser
npm run typecheck
```
