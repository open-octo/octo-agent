import { registerPlugin } from '@capacitor/core'
import type { OctoTunnelPlugin } from './native-bridge'

// OctoTunnel is the app's custom native plugin. Its Kotlin (android/) and Swift
// (ios/) implementations run the Noise handshake, hold the device keypair in the
// Keystore / Keychain, and move ciphertext over the relay — none of which can
// live in JavaScript. registerPlugin (from @capacitor/core, which augments the
// native-injected bridge) wires the JS-side proxy; Capacitor resolves the native
// implementation at runtime on-device. In a plain browser the calls reject,
// which is why the managed-relay transport is only used inside the app.
//
// We deliberately use @capacitor/core's registerPlugin for every plugin — our
// own OctoTunnel and first-party ones like App — rather than each plugin's own
// JS wrapper (e.g. @capacitor/app). The wrappers layer a second event path that
// misbehaves when this boot bundle loads at document start; the low-level proxy
// from registerPlugin talks straight to the one native bridge.
export { registerPlugin }

export const OctoTunnel = registerPlugin<OctoTunnelPlugin>('OctoTunnel')
