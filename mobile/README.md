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
| Buffering transport (holds traffic until paired) | `src/buffering-transport.ts` | vitest |
| Native plugin JS contract | `src/native-bridge.ts` | tsc |
| Plugin registration | `src/plugin.ts` | tsc |
| App boot: install shim, pair, connect | `src/boot.ts` | on-device |
| QR scan (getUserMedia + jsQR) | `src/qr.ts` | on-device |
| Android native plugin (Noise XX) | `native/OctoTunnelPlugin.kt` | emulator E2E |
| iOS native plugin (Noise XX) | `native/OctoTunnelPlugin.swift` | simulator E2E |
| Capacitor config + web bundling | `capacitor.config.ts`, `scripts/bundle-web.mjs` | tsc |

```bash
cd mobile
npm install
npm test          # vitest: pairing + frames + shim + buffering-transport
npm run typecheck # tsc
```

Both the **Android** and **iOS** apps are verified end to end: paired to a local
`octo serve --tunnel` + `octo-relay` over a real Noise XX session, and the
bundled frontend drove `/api` + `/ws` through the tunnel. See `native/README.md`
for the per-platform wiring.

## Not done yet

- The other native plugins (biometric, push, notifications, share) and the
  `mobileShell` branch re-pointing in the frontend.
- **Self-tunnel transport** — `SelfTunnelTransport` dials a `/tunnel` endpoint
  the server doesn't expose yet; only the managed-relay path is wired end to end.

## Build the app (on a Mac)

```bash
# 1. Build the web frontend (repo root) and bundle it in.
make web-build
cd mobile && npm install && npm run bundle-web

# 2. Add the native platforms (generates ios/ and android/, both gitignored).
npx cap add ios
npx cap add android

# 3. Wire the native plugin into the generated projects (copies the reference
#    source + patches Gradle / Manifest / Info.plist). Idempotent; re-run after
#    any `cap add`. Add --local to inject the simulator/emulator cleartext
#    switches (never for release). See native/README.md for what it does and the
#    one manual step it leaves (iOS plugin-instance registration).
npm run wire-native -- --local

# 4. Sync and open.
npx cap sync
npx cap open ios       # Xcode
npx cap open android   # Android Studio
```
