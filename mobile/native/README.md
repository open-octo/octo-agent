# Native plugin reference source

`OctoTunnel` is octo-mobile's custom Capacitor plugin — the phone half of the
managed tunnel. The Noise handshake and the device keypair live here, in native
code, and never enter JavaScript (see `../src/native-bridge.ts` for the contract
and `../src/transport.ts` for the caller).

These files are the **reference source**, committed here because the generated
`ios/` and `android/` folders (from `npx cap add`) are gitignored. On a Mac,
after `npx cap add ios` / `add android`, place them into the platform projects:

- **iOS** — `ios/App/App/OctoTunnelPlugin.swift`, plus a companion
  `OctoTunnelPlugin.m` with the `CAP_PLUGIN` macro (or export via Package.swift)
  so Capacitor discovers it. **Not written yet** — the iOS toolchain hasn't been
  available; mirror the Android plugin against the same JS contract.
- **Android** — `OctoTunnelPlugin.kt` → place at
  `android/app/src/main/java/dev/octo/mobile/OctoTunnelPlugin.kt`. See the wiring
  below.

## Status

- **Android (`OctoTunnelPlugin.kt`)** — implemented and **verified end to end**
  on an emulator: paired to a local `octo serve --tunnel` + `octo-relay` over a
  real Noise XX session, and the bundled web frontend drove `/api` + `/ws`
  through the tunnel. It interoperates with the Go host (`internal/tunnel`) and
  relay (`cmd/octo-relay`).
- **iOS** — not started.

## What it implements

- A device static keypair (X25519) created on first run and kept in secure
  storage. On Android the 32-byte private key is wrapped by an AES-GCM key held
  in the **AndroidKeyStore** and stored in `SharedPreferences`; iOS should hold
  its key in the **Keychain**.
- The **Noise XX** handshake (`Noise_XX_25519_ChaChaPoly_SHA256`, empty
  prologue) with the host, brokered by the relay, authenticating the host by
  comparing its transmitted static key against the scanned `hostKey`.
- A **relay WebSocket** dialing `<relay>/device?token=<token>` (OkHttp on
  Android; `URLSessionWebSocketTask` on iOS). Production dials
  `<tunnelId>.<relay-host>` so the relay routes by SNI; the single-node PoC relay
  routes by token.
- Encrypt outbound plaintext frames, decrypt inbound ones, and emit them as the
  `"frame"` listener event. Cipher use is serialized — a Noise CipherState nonce
  is stateful.

## Android wiring (after `npx cap add android`)

The generated `android/` is gitignored, so redo these each time it is
regenerated:

1. **Kotlin plugin** — the project template is Java-only. In the root
   `android/build.gradle` buildscript dependencies add
   `classpath 'org.jetbrains.kotlin:kotlin-gradle-plugin:1.9.22'`; in
   `android/app/build.gradle` add `apply plugin: 'kotlin-android'`, a
   `kotlinOptions { jvmTarget = "17" }` block, and
   `implementation "org.jetbrains.kotlin:kotlin-stdlib:1.9.22"`.
2. **Dependencies** (`android/app/build.gradle`, resolved from Maven Central):
   - `implementation "com.github.auties00:noise-java:1.2"` — Noise XX; its
     `com.southernstorm.noise` classes are pure Java (no NDK).
   - `implementation "com.squareup.okhttp3:okhttp:4.12.0"` — relay WebSocket.
3. **Register** the plugin in `MainActivity` **before** `super.onCreate`:
   `registerPlugin(OctoTunnelPlugin.class);`. The pairing screen scans a QR with
   `getUserMedia`, so also request `CAMERA` at runtime there.
4. **AndroidManifest.xml** — add `android.permission.CAMERA` (plus a non-required
   `android.hardware.camera` feature) and, on `MainActivity`, an
   `android.intent.action.VIEW` intent-filter for the `octo-pair` scheme so a
   scanned QR opens the app as a deep link.
5. **Local testing only** — to reach a plaintext `ws://` relay from the emulator
   (`10.0.2.2`), set `android:usesCleartextTraffic="true"` on `<application>`.
   Production uses `wss://`; do not ship cleartext.

The JS side (`../src/plugin.ts`) resolves every plugin — `OctoTunnel` and
first-party ones like `App` — through `@capacitor/core`'s `registerPlugin`, not
each plugin's own JS wrapper. The boot bundle loads at document start; a second
`@capacitor/core` copy or a wrapper's event layer races Capacitor's native
bridge injection.
