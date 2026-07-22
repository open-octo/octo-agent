import type { PairingInfo } from './pairing'

// OctoTunnel is the native Capacitor plugin contract the ManagedRelayTransport
// calls into. By design the Noise handshake and the device's static keypair live
// in the native layer (Keychain / Keystore) and never enter JavaScript — so this
// interface only ever moves opaque, already-encrypted frames and pairing
// metadata across the JS↔native boundary. The Swift and Kotlin implementations
// are built separately (they need the iOS/Android toolchains); this is the
// JS-facing contract they satisfy.
export interface OctoTunnelPlugin {
  /**
   * Load — or generate on first run — the device's static keypair from secure
   * storage, returning its public key (base64) to hand the host at pairing.
   */
  loadDeviceIdentity(): Promise<{ publicKey: string }>

  /**
   * Complete the Noise handshake with the host over the relay, using the
   * scanned pairing material. Resolves once the encrypted session is ready.
   */
  pair(info: PairingInfo): Promise<void>

  /**
   * Send one plaintext /api or /ws frame; the native layer encrypts it and
   * forwards it to the host through the relay. `frame` is the raw message text.
   */
  send(options: { frame: string }): Promise<void>

  /** Register for decrypted frames arriving from the host. */
  addListener(
    eventName: 'frame',
    listener: (data: { frame: string }) => void,
  ): Promise<void>

  /** Drop the pairing (revoke this device) or just close the session. */
  disconnect(): Promise<void>
}
