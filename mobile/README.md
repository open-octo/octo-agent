# octo-mobile

octo's iOS and Android client — a **thin remote client** of an `octo serve`
instance, not a second agent. It wraps the existing web frontend
(`internal/server/webdist`) in a Capacitor shell and adds the native pieces a
browser can't provide. See
[`dev-docs/mobile-managed-tunnel-design.md`](../dev-docs/mobile-managed-tunnel-design.md).

It lives here as a subdirectory for now; it will move to a sibling repository
(like `octo-vscode` / `octo-obsidian`) once its shape settles.

## Architecture

The bundled frontend keeps its same-origin assumptions — it just calls `/api`
and `/ws`. The **local shim** (`src/shim.ts`) monkeypatches `fetch` and
`WebSocket`, multiplexes those calls into **tunnel frames** (`src/frames.ts`),
and carries them over a **Transport** (`src/transport.ts`):

- **managed-relay** — delegates to the native `OctoTunnel` plugin, which runs
  Noise and moves ciphertext over octo's relay. Keys never enter JS.
- **self-tunnel** — a plain WebSocket to the user's own tunnel URL.

```
webview fetch('/api/…') / new WebSocket('/ws')
        │  (shim intercepts, same-origin)
        ▼
   frames.ts  ──►  Transport  ──►  { native OctoTunnel + relay │ self-tunnel ws }  ──►  octo serve
```

## Status — what's landed (verifiable here)

| Piece | File | Verified by |
|---|---|---|
| Pairing URL parse | `src/pairing.ts` | vitest |
| Tunnel frame protocol | `src/frames.ts` | vitest |
| Local shim (fetch + WebSocket) | `src/shim.ts` | vitest |
| Transport interface + two impls | `src/transport.ts` | tsc |
| Native plugin JS contract | `src/native-bridge.ts` | tsc |
| Plugin registration | `src/plugin.ts` | tsc |
| Capacitor config + web bundling | `capacitor.config.ts`, `scripts/bundle-web.mjs` | tsc |

```bash
cd mobile
npm install
npm test          # vitest: pairing + frames + shim
npm run typecheck # tsc
```

## Not done yet

- **Native plugin (`native/`)** — `OctoTunnelPlugin.swift` / `.kt` are
  **unverified skeletons** (no iOS/Android toolchain here). They run the Noise
  handshake + hold keys in Keychain/Keystore. See `native/README.md`.
- **Host-side `/api` tunneling** — the shim frames both HTTP (`/api`) and
  WebSocket (`/ws`), but the host tunnel (`internal/tunnel`) currently bridges a
  raw `/ws` only. A follow-up must teach it the frame protocol: replay
  `http-req` frames as loopback HTTP calls and open a loopback `/ws` per `ws-*`
  stream.
- The other native plugins (biometric, push, QR scan, notifications, share) and
  the `mobileShell` branch re-pointing in the frontend.

## Build the app (on a Mac)

```bash
# 1. Build the web frontend (repo root) and bundle it in.
make web-build
cd mobile && npm install && npm run bundle-web

# 2. Add the native platforms (generates ios/ and android/, both gitignored).
npx cap add ios
npx cap add android

# 3. Copy the native plugin reference source into the platform projects
#    (see native/README.md), then sync and open.
npx cap sync
npx cap open ios       # Xcode
npx cap open android   # Android Studio
```
