# Native plugin reference source

`OctoTunnel` is octo-mobile's custom Capacitor plugin — the phone half of the
managed tunnel. The Noise handshake and the device keypair live here, in native
code, and never enter JavaScript (see `../src/native-bridge.ts` for the contract
and `../src/transport.ts` for the caller).

These files are the **reference source**, committed here because the generated
`ios/` and `android/` folders (from `npx cap add`) are gitignored. On a Mac,
after `npx cap add ios` / `add android`, place them into the platform projects:

- **iOS** — `OctoTunnelPlugin.swift` → place at `ios/App/App/OctoTunnelPlugin.swift`.
  It is a pure-Swift Capacitor plugin (conforms to `CAPBridgedPlugin`), so no
  companion Objective-C `CAP_PLUGIN` macro file is needed. See the wiring below.
- **Android** — `OctoTunnelPlugin.kt` → place at
  `android/app/src/main/java/dev/octo/mobile/OctoTunnelPlugin.kt`. See the wiring
  below.

## Status

Both plugins are implemented and **verified end to end** on a simulator/emulator:
paired to a local `octo serve --tunnel` + `octo-relay` over a real Noise XX
session, and the bundled web frontend drove `/api` + `/ws` through the tunnel.
They interoperate with the Go host (`internal/tunnel`) and relay
(`cmd/octo-relay`). Android uses noise-java; iOS implements Noise XX on CryptoKit.

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

## iOS wiring (after `npx cap add ios` + `pod install`)

The generated `ios/` is gitignored, so redo these each time it is regenerated:

1. **Plugin source** — add `OctoTunnelPlugin.swift` to the `App` target
   (`ios/App/App/`). It is pure Swift and conforms to `CAPBridgedPlugin`; no
   `.m` file is needed. It uses CryptoKit (Curve25519 / ChaChaPoly / SHA256 /
   HMAC-HKDF) — no third-party dependency.
2. **Register the plugin.** Capacitor's ObjC-runtime auto-scan does not reliably
   surface an app-target Swift plugin, and `registerPluginType` is a no-op while
   `autoRegisterPlugins` is on. Register an instance explicitly: subclass
   `CAPBridgeViewController` and override `capacitorDidLoad()` with
   `bridge?.registerPluginInstance(OctoTunnelPlugin())`, then use that subclass
   as the bridge view controller (Main.storyboard's custom class, or the
   programmatic root VC).
3. **Info.plist** — add `NSCameraUsageDescription` (getUserMedia QR scan) and a
   `CFBundleURLTypes` entry for the `octo-pair` scheme (the deep link Capacitor's
   `App` plugin delivers as `appUrlOpen`).
4. **Local testing only** — add `NSAppTransportSecurity → NSAllowsLocalNetworking
   = true` so the app can reach a plaintext `ws://127.0.0.1` relay on the
   simulator (the simulator shares the Mac's network; no `10.0.2.2` needed).
   Production uses `wss://`.

> Note: on a machine whose installed simulator runtime matches the Xcode SDK,
> keep the generated storyboards. This environment only had an older iOS runtime,
> so the local `ios/` project was patched to drop the storyboards and build the
> bridge view controller programmatically — a workaround for the toolchain
> mismatch, not part of the plugin.

The JS side (`../src/plugin.ts`) resolves every plugin — `OctoTunnel` and
first-party ones like `App` — through `@capacitor/core`'s `registerPlugin`, not
each plugin's own JS wrapper. The boot bundle loads at document start; a second
`@capacitor/core` copy or a wrapper's event layer races Capacitor's native
bridge injection.
