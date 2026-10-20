# octo-mobile

octo's iOS and Android client — a **thin remote client** of your own
`octo serve`, not a second agent. It wraps the web frontend
(`internal/server/webdist`) in a Capacitor shell; the phone UI itself lives in
`web/src/mobile/`.

## How it connects

The app talks to `octo serve` directly — there is no relay. Getting the phone to
the server is your network's job: the same LAN, Tailscale, cloudflared, frp, a
reverse proxy. Authentication is the server's access key.

The bundled frontend calls same-origin `/api` and `/ws`, as it does when
`octo serve` hosts it. The **local shim** (`src/shim.ts`) monkeypatches `fetch`
and `WebSocket` to send those calls to the server's address with the key
attached (`X-Access-Key` header; `?access_key=` on `/ws`). The server allows the
app's WebView origins (`capacitor://localhost`, `https://localhost`) CORS by
default.

```
webview fetch('/api/…') / new WebSocket('ws://localhost/ws')
        │  (shim rewrites)
        ▼
   http(s)://<your server>/api/…  ·  ws(s)://<your server>/ws?access_key=…
```

Connecting takes the bootstrap link `octo serve` prints on startup
(`http://<host>:8088/?access_key=…`). Settings › Mobile on the desktop/web UI
shows it as a QR, with the server's LAN addresses to pick from or any other
address typed in. On first launch the app scans it (or takes it pasted), checks
it against the server, saves it, and reloads. Settings › "Switch server" in the
app forgets it.

There are no push notifications: the app syncs when it is opened.

| Piece | File |
|---|---|
| Connect link parse + storage | `src/connection.ts` |
| Local shim (fetch + WebSocket rewrite) | `src/shim.ts` |
| App boot: shim or connect screen | `src/boot.ts` |
| QR scan (getUserMedia + jsQR) | `src/qr.ts` |
| Capacitor config + web bundling | `capacitor.config.ts`, `scripts/bundle-web.mjs` |
| Native settings (camera, plain http) | `scripts/wire-native.mjs` |

```bash
cd mobile
npm install
npm test          # vitest: connection + shim
npm run typecheck # tsc
```

## Build the app

Android needs the Android SDK; iOS needs a Mac with Xcode.

```bash
# 1. Build the web frontend (repo root) and bundle it in.
make web-build
cd mobile && npm install && npm run bundle-web

# 2. Add the native platforms (generates ios/ and android/, both gitignored).
npx cap add ios
npx cap add android

# 3. Apply the native settings: camera permission for the QR scan, and plain-http
#    networking (iOS NSAllowsArbitraryLoads, Android usesCleartextTraffic).
#    Idempotent; re-run after any `cap add`.
npm run wire-native

# 4. Build & run. `cap copy` re-syncs the web bundle into the native projects —
#    run it after EVERY web change; xcodebuild / gradle do not sync it themselves.
npx cap copy

#    Android:
(cd android && ./gradlew assembleDebug) && \
  adb install -r android/app/build/outputs/apk/debug/app-debug.apk

#    iOS (or just `npx cap open ios` and Run from Xcode):
xcodebuild -project ios/App/App.xcodeproj -scheme App \
  -destination 'platform=iOS Simulator,name=iPhone 17' build
```

Then start the server where the phone can reach it — `octo serve --addr
0.0.0.0:8088` for the LAN, or keep the default loopback bind behind
`tailscale serve` / cloudflared — and scan the QR from Settings › Mobile.
