import { registerPlugin } from '@capacitor/core'
import type { OctoTunnelPlugin } from './native-bridge'

// OctoTunnel is the app's custom native plugin. Its Swift (ios/) and Kotlin
// (android/) implementations run the Noise handshake, hold the device keypair in
// the Keychain / Keystore, and move ciphertext over the relay — none of which
// can live in JavaScript. registerPlugin wires the JS-side proxy; Capacitor
// resolves the native implementation at runtime on-device. In a plain browser
// (no native host) the calls reject, which is why the managed-relay transport is
// only selected inside the app (mobileShell).
export const OctoTunnel = registerPlugin<OctoTunnelPlugin>('OctoTunnel')
