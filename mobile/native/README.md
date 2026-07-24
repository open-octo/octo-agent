# Native plugin reference source

`OctoTunnel` is octo-mobile's custom Capacitor plugin — the phone half of the
managed tunnel. The Noise handshake and the device keypair live here, in native
code, and never enter JavaScript (see `../src/native-bridge.ts` for the contract
and `../src/transport.ts` for the caller).

These files are the **reference source**, committed here because the generated
`ios/` and `android/` folders (from `npx cap add`) are gitignored.

**You normally don't apply any of this by hand** — `../scripts/wire-native.mjs`
does all of it (copy the plugin, register it, patch Gradle / Manifest /
Info.plist / the Xcode project) idempotently. Run it after every `npx cap add`:

```bash
npm run wire-native -- --local   # drop --local for a release build
```

The rest of this document is the reference for **what wire-native does and
why** — read it to understand or debug the wiring, not to perform it.

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

## Android wiring (what wire-native does)

Applied automatically after `npx cap add android`:

1. **Kotlin plugin** — the project template is Java-only. In the root
   `android/build.gradle` buildscript dependencies add
   `classpath 'org.jetbrains.kotlin:kotlin-gradle-plugin:1.9.22'`; in
   `android/app/build.gradle` add `apply plugin: 'kotlin-android'`, a
   `kotlinOptions { jvmTarget = "21" }` block (must match javac's target — the
   Android Studio JBR is 21, and AGP fails the build on a mismatch), and
   `implementation "org.jetbrains.kotlin:kotlin-stdlib:1.9.22"`.
2. **Dependencies** (`android/app/build.gradle`, resolved from Maven Central):
   - `implementation "com.github.auties00:noise-java:1.2"` — Noise XX; its
     `com.southernstorm.noise` classes are pure Java (no NDK).
   - `implementation "com.squareup.okhttp3:okhttp:4.12.0"` — relay WebSocket.
3. **Register** the plugin in `MainActivity` **before** `super.onCreate`:
   `registerPlugin(OctoTunnelPlugin.class);`. Capacitor 7's template is a
   bodyless `class MainActivity extends BridgeActivity {}`, so wire-native
   expands it with an `onCreate` that registers the plugin. The pairing screen
   scans a QR with `getUserMedia`, so `CAMERA` is requested at runtime there.
4. **AndroidManifest.xml** — add `android.permission.CAMERA` (plus a non-required
   `android.hardware.camera` feature) and, on `MainActivity`, an
   `android.intent.action.VIEW` intent-filter for the `octo-pair` scheme so a
   scanned QR opens the app as a deep link.
5. **Local testing only** (`--local`) — to reach a plaintext `ws://` relay from
   the emulator (`10.0.2.2`), set `android:usesCleartextTraffic="true"` on
   `<application>`. Production uses `wss://`; do not ship cleartext.

## iOS wiring (what wire-native does)

Applied automatically after `npx cap add ios`. Capacitor 7 uses Swift Package
Manager, so there is no `pod install`.

1. **Plugin source** — `OctoTunnelPlugin.swift` is copied into the `App` target
   (`ios/App/App/`) **and added to `project.pbxproj`** (build file + file ref +
   group + Sources phase). `cap add ios` copies nothing into the project, so
   without the pbxproj entry the file wouldn't compile. It is pure Swift,
   conforms to `CAPBridgedPlugin` (no `.m` file), and uses CryptoKit
   (Curve25519 / ChaChaPoly / SHA256 / HMAC-HKDF) — no third-party dependency.
2. **Register the plugin.** Capacitor's ObjC-runtime auto-scan doesn't reliably
   surface an app-target Swift plugin, and `registerPluginType` is a no-op while
   `autoRegisterPlugins` is on. So wire-native generates
   `OctoBridgeViewController.swift` — a `CAPBridgeViewController` subclass that
   registers the **instance** in `capacitorDidLoad()`
   (`bridge?.registerPluginInstance(OctoTunnelPlugin())`) — and rewrites
   `AppDelegate` to build it as the window's root view controller in code.
   Building the root VC programmatically also drops `Main.storyboard` (and
   `UIMainStoryboardFile` from Info.plist): under Xcode 26's SDK with an older
   simulator runtime, compiling the storyboard fails "Platform Not Installed",
   and the programmatic root sidesteps it. This is always done now (not a
   per-machine workaround).
3. **Info.plist** — add `NSCameraUsageDescription` (getUserMedia QR scan) and a
   `CFBundleURLTypes` entry for the `octo-pair` scheme (the deep link Capacitor's
   `App` plugin delivers as `appUrlOpen`).
4. **Local testing only** (`--local`) — add `NSAppTransportSecurity →
   NSAllowsLocalNetworking = true` so the app can reach a plaintext
   `ws://127.0.0.1` relay on the simulator (the simulator shares the Mac's
   network; no `10.0.2.2` needed). Production uses `wss://`.

The JS side (`../src/plugin.ts`) resolves every plugin — `OctoTunnel` and
first-party ones like `App` — through `@capacitor/core`'s `registerPlugin`, not
each plugin's own JS wrapper. The boot bundle loads at document start; a second
`@capacitor/core` copy or a wrapper's event layer races Capacitor's native
bridge injection.
