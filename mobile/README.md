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
| Native wiring (both platforms, turnkey) | `scripts/wire-native.mjs` | emulator + simulator E2E |
| Mobile UI shell (feed / chat / approvals / tasks / config / settings) | `web/src/mobile/` | emulator + simulator E2E |

```bash
cd mobile
npm install
npm test          # vitest: pairing + frames + shim + buffering-transport
npm run typecheck # tsc
```

Both the **Android** and **iOS** apps are verified end to end, from a
**from-scratch build** (`cap add` → `wire-native` → build): paired to a local
`octo serve --tunnel` + `octo-relay` over a real Noise XX session, and the
mobile UI shell drove `/api` + `/ws` through the tunnel. `wire-native` automates
the whole native setup for both platforms — see `native/README.md` for what it
does per platform.

## Not done yet

- **Push wakeups** — the host + relay side ship (`internal/tunnel` wakeup frame,
  `cmd/octo-relay/internal/push`), but the app-side Capacitor push-token wiring
  is deferred (a killed app can't be woken yet).
- The other native niceties (biometric unlock, share).
- **Self-tunnel transport** — `SelfTunnelTransport` dials a `/tunnel` endpoint
  the server doesn't expose yet; only the managed-relay path is wired end to end.

## Build the app

Android needs the Android SDK; iOS needs a Mac with Xcode. Both build from the
same steps — `wire-native` does the entire native setup, so no hand-editing of
the generated projects is required.

```bash
# 1. Build the web frontend (repo root) and bundle it in.
make web-build
cd mobile && npm install && npm run bundle-web

# 2. Add the native platforms (generates ios/ and android/, both gitignored).
#    Capacitor 7 uses Swift Package Manager on iOS — there is no Podfile and no
#    `pod install`; Xcode resolves the packages on open.
npx cap add ios
npx cap add android

# 3. Wire the native plugin into the generated projects. Fully automated and
#    idempotent (re-run after any `cap add`): copies the plugin, registers it
#    (Android MainActivity / iOS a generated bridge view controller), patches
#    Gradle / Manifest / Info.plist / the Xcode project. Add --local to inject
#    the simulator/emulator cleartext switches (NEVER for release).
npm run wire-native -- --local

# 4. Build & run. `cap copy` re-syncs the web bundle into the native projects —
#    run it after EVERY web change; xcodebuild / gradle do not sync it themselves.
npx cap copy

#    Android:
(cd android && ./gradlew assembleDebug) && \
  adb install -r android/app/build/outputs/apk/debug/app-debug.apk

#    iOS (or just `npx cap open ios` and Run from Xcode):
xcodebuild -project ios/App/App.xcodeproj -scheme App \
  -destination 'platform=iOS Simulator,name=iPhone 15 Pro' build
```

Then pair: run `octo serve --tunnel` on the host, and scan the printed QR (or,
on a simulator/emulator, open the `octo-pair://` deep link directly —
`xcrun simctl openurl booted '<url>'` / `adb shell am start -a android.intent.action.VIEW -d '<url>'`).

> **Xcode 26 note:** building for iOS requires the iOS platform component
> installed (Xcode → Settings → Components, or `xcodebuild -downloadPlatform iOS`).
> It's a large one-time download; without it the build reports "iOS … not
> installed" and offers no simulator destination.
