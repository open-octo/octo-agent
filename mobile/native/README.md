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
  so Capacitor discovers it.
- **Android** — `android/app/src/main/java/dev/octo/mobile/OctoTunnelPlugin.kt`,
  and register it in `MainActivity` (`registerPlugin(OctoTunnelPlugin::class.java)`).

## ⚠️ Unverified

Both files were written without the iOS/Android toolchains and have **not been
compiled**. They match the JS contract and sketch the structure; every method
body is a TODO. Expect to iterate on them once they build.

## What each must implement

- A device static keypair (X25519) in the **Keychain** (iOS) / **Keystore**
  (Android), created on first run.
- The **Noise XX** handshake (DH25519 / ChaChaPoly / SHA256) with the host,
  brokered by the relay, authenticating the host against the scanned `hostKey`.
  Pick a maintained native Noise library, or bind libsodium/noise-c.
- A **relay WebSocket** (`URLSessionWebSocketTask` / OkHttp), dialing
  `<tunnelId>.<relay-host>` so the relay routes by SNI.
- Encrypt outbound plaintext frames, decrypt inbound ones and emit them as the
  `"frame"` listener event.
