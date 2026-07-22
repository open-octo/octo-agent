import Capacitor
import CryptoKit
import Foundation
import Security

// OctoTunnel is the phone half of octo's managed tunnel — the native piece that
// cannot live in JavaScript. It holds the device's Noise static keypair in the
// iOS Keychain, runs the Noise XX handshake with the host (brokered by the
// relay, which sees only ciphertext), and moves encrypted frames over a relay
// WebSocket. JavaScript hands it plaintext frames and receives plaintext frames
// back; no key material ever crosses the bridge.
//
// It mirrors the phone (initiator) side of the Go reference client in
// cmd/octo-relay/internal/client and the Kotlin plugin, and interoperates with
// the host in internal/tunnel: Noise_XX_25519_ChaChaPoly_SHA256, empty prologue,
// JSON relay frames {t, tn, d, p} with p a base64 payload the relay never reads.
@objc(OctoTunnelPlugin)
public class OctoTunnelPlugin: CAPPlugin, CAPBridgedPlugin {
    // Capacitor plugin identity + method table, declared in Swift (the modern
    // pure-Swift plugin form) so no companion Objective-C CAP_PLUGIN macro file
    // is needed. jsName is the name the JS side calls: registerPlugin('OctoTunnel').
    public let identifier = "OctoTunnelPlugin"
    public let jsName = "OctoTunnel"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "loadDeviceIdentity", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "pair", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "send", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "disconnect", returnType: CAPPluginReturnPromise),
    ]

    private let queue = DispatchQueue(label: "dev.octo.tunnel")
    private var handshake: NoiseHandshakeXX?
    private var tx: NoiseCipherState?
    private var rx: NoiseCipherState?
    private var ws: URLSessionWebSocketTask?
    private var urlSession: URLSession?
    private var deviceId: String?
    private var expectedHostKey: Data?
    private var pairCall: CAPPluginCall?

    // MARK: - JS contract (native-bridge.ts)

    @objc func loadDeviceIdentity(_ call: CAPPluginCall) {
        do {
            let identity = try deviceIdentity()
            call.resolve(["publicKey": identity.publicKey.base64EncodedString()])
        } catch {
            call.reject("load identity failed: \(error.localizedDescription)")
        }
    }

    @objc func pair(_ call: CAPPluginCall) {
        guard let relay = call.getString("relay"), !relay.isEmpty,
              let token = call.getString("token"), !token.isEmpty,
              let hostKey = call.getString("hostKey"), !hostKey.isEmpty else {
            call.reject("pair: relay, token and hostKey are required")
            return
        }
        guard let expected = Data(base64Encoded: hostKey) else {
            call.reject("pair: hostKey is not valid base64")
            return
        }
        queue.async {
            do {
                let identity = try self.deviceIdentity()
                self.handshake = try NoiseHandshakeXX(staticPrivate: identity.privateKey)
                self.tx = nil
                self.rx = nil
                self.deviceId = nil
                self.expectedHostKey = expected
                self.pairCall = call

                // URLSessionWebSocketTask dials ws(s):// directly (unlike OkHttp,
                // which upgrades over http). The PoC relay routes by token;
                // production dials <tunnelId>.<relay-host> for SNI routing.
                let base = relay.hasSuffix("/") ? String(relay.dropLast()) : relay
                guard let url = URL(string: "\(base)/device?token=\(token)") else {
                    throw TunnelError.message("bad relay URL")
                }
                let session = URLSession(configuration: .default)
                let task = session.webSocketTask(with: url)
                self.urlSession = session
                self.ws = task
                task.resume()
                self.receiveLoop()
            } catch {
                self.pairCall = nil
                call.reject("pair failed: \(error.localizedDescription)")
            }
        }
    }

    @objc func send(_ call: CAPPluginCall) {
        guard let frame = call.getString("frame") else {
            call.reject("send: frame is required")
            return
        }
        queue.async {
            guard let tx = self.tx else {
                call.reject("send: no active session")
                return
            }
            do {
                let ct = try tx.encrypt(Data(frame.utf8))
                self.sendFrame(type: "data", payload: ct)
                call.resolve()
            } catch {
                call.reject("send failed: \(error.localizedDescription)")
            }
        }
    }

    @objc func disconnect(_ call: CAPPluginCall) {
        queue.async {
            self.teardown(nil)
            call.resolve()
        }
    }

    // MARK: - relay WebSocket

    private func receiveLoop() {
        ws?.receive { [weak self] result in
            guard let self = self else { return }
            switch result {
            case .failure(let error):
                self.queue.async { self.teardown(error) }
            case .success(let message):
                let text: String
                switch message {
                case .string(let s): text = s
                case .data(let d): text = String(decoding: d, as: UTF8.self)
                @unknown default: text = ""
                }
                self.queue.async { self.onMessage(text) }
                self.receiveLoop()
            }
        }
    }

    private func onMessage(_ text: String) {
        guard let data = text.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let type = obj["t"] as? String else { return }
        do {
            switch type {
            case "paired":
                deviceId = obj["d"] as? String
                // Initiator opens the XX handshake: send message 1 (e).
                if let hs = handshake {
                    let msg1 = try hs.writeMessage1()
                    sendFrame(type: "handshake", payload: msg1)
                }
            case "handshake":
                guard let hs = handshake, let p = obj["p"] as? String,
                      let msg = Data(base64Encoded: p) else { return }
                // The one inbound handshake message in XX is msg2 (e, ee, s, es).
                let msg3 = try hs.readMessage2WriteMessage3(msg)
                // Authenticate the host: its static key, learned in msg2, must
                // equal the one the pairing QR carried — the relay can't forge it.
                if let expected = expectedHostKey, hs.remoteStatic != expected {
                    teardown(TunnelError.message("host key mismatch — refusing to trust relay peer"))
                    return
                }
                sendFrame(type: "handshake", payload: msg3)
                let keys = hs.split()
                tx = NoiseCipherState(key: keys.send)
                rx = NoiseCipherState(key: keys.receive)
                handshake = nil
                pairCall?.resolve()
                pairCall = nil
            case "data":
                guard let rx = rx, let p = obj["p"] as? String,
                      let ct = Data(base64Encoded: p) else { return }
                let pt = try rx.decrypt(ct)
                let frame = String(decoding: pt, as: UTF8.self)
                notifyListeners("frame", data: ["frame": frame])
            default:
                break
            }
        } catch {
            teardown(error)
        }
    }

    private func sendFrame(type: String, payload: Data) {
        var obj: [String: Any] = ["t": type, "p": payload.base64EncodedString()]
        if let d = deviceId { obj["d"] = d }
        guard let data = try? JSONSerialization.data(withJSONObject: obj) else { return }
        ws?.send(.string(String(decoding: data, as: UTF8.self))) { _ in }
    }

    private func teardown(_ error: Error?) {
        if let call = pairCall {
            call.reject("tunnel error: \(error?.localizedDescription ?? "closed")")
            pairCall = nil
        }
        ws?.cancel(with: .normalClosure, reason: nil)
        ws = nil
        urlSession = nil
        tx = nil
        rx = nil
        handshake = nil
    }

    // MARK: - device static keypair (Keychain-held X25519)

    private struct Identity {
        let privateKey: Curve25519.KeyAgreement.PrivateKey
        let publicKey: Data
    }

    private let keychainService = "dev.octo.mobile.tunnel"
    private let keychainAccount = "device_static_key"

    private func deviceIdentity() throws -> Identity {
        if let raw = keychainLoad() {
            let key = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: raw)
            return Identity(privateKey: key, publicKey: key.publicKey.rawRepresentation)
        }
        let key = Curve25519.KeyAgreement.PrivateKey()
        try keychainStore(key.rawRepresentation)
        return Identity(privateKey: key, publicKey: key.publicKey.rawRepresentation)
    }

    private func keychainLoad() -> Data? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: keychainAccount,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess else { return nil }
        return item as? Data
    }

    private func keychainStore(_ data: Data) throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: keychainAccount,
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]
        SecItemDelete(query as CFDictionary)
        let status = SecItemAdd(query as CFDictionary, nil)
        if status != errSecSuccess {
            throw TunnelError.message("keychain store failed (\(status))")
        }
    }
}

private enum TunnelError: Error, LocalizedError {
    case message(String)
    var errorDescription: String? {
        if case .message(let m) = self { return m }
        return nil
    }
}

// MARK: - Noise XX (initiator), Noise_XX_25519_ChaChaPoly_SHA256, empty prologue
//
// A focused implementation of just the initiator's three steps — write e; read
// (e, ee, s, es); write (s, se) — rather than a general token interpreter, built
// on CryptoKit primitives. It is byte-compatible with the Go host's flynn/noise
// and the Android noise-java plugin.

private func hmacSHA256(key: Data, data: Data) -> Data {
    Data(HMAC<SHA256>.authenticationCode(for: data, using: SymmetricKey(data: key)))
}

// Noise HKDF: extract with the chaining key as salt, then expand into `outputs`
// chained blocks. Matches the spec's HKDF used by MixKey and Split.
private func noiseHKDF(chainingKey: Data, ikm: Data, outputs: Int) -> [Data] {
    let tempKey = hmacSHA256(key: chainingKey, data: ikm)
    let o1 = hmacSHA256(key: tempKey, data: Data([0x01]))
    let o2 = hmacSHA256(key: tempKey, data: o1 + Data([0x02]))
    if outputs == 2 { return [o1, o2] }
    let o3 = hmacSHA256(key: tempKey, data: o2 + Data([0x03]))
    return [o1, o2, o3]
}

// NoiseCipherState is one direction's transport cipher: a key plus a 64-bit
// nonce counter. The 96-bit ChaChaPoly nonce is 4 zero bytes then the counter
// little-endian — the Noise convention both other ends use.
private final class NoiseCipherState {
    private let key: SymmetricKey
    private var n: UInt64 = 0

    init(key: Data) { self.key = SymmetricKey(data: key) }

    private func nonce() -> ChaChaPoly.Nonce {
        var bytes = Data(count: 4)
        withUnsafeBytes(of: n.littleEndian) { bytes.append(contentsOf: $0) }
        return try! ChaChaPoly.Nonce(data: bytes)
    }

    func encrypt(_ plaintext: Data, ad: Data = Data()) throws -> Data {
        let box = try ChaChaPoly.seal(plaintext, using: key, nonce: nonce(), authenticating: ad)
        n &+= 1
        return box.ciphertext + box.tag
    }

    func decrypt(_ ciphertext: Data, ad: Data = Data()) throws -> Data {
        let tag = ciphertext.suffix(16)
        let ct = ciphertext.prefix(ciphertext.count - 16)
        let box = try ChaChaPoly.SealedBox(nonce: nonce(), ciphertext: ct, tag: tag)
        let pt = try ChaChaPoly.open(box, using: key, authenticating: ad)
        n &+= 1
        return pt
    }
}

private final class NoiseHandshakeXX {
    private var ck: Data
    private var h: Data
    private var k: SymmetricKey?
    private var n: UInt64 = 0

    private let staticPrivate: Curve25519.KeyAgreement.PrivateKey
    private let ephemeral: Curve25519.KeyAgreement.PrivateKey
    private var remoteEphemeral: Data?
    private(set) var remoteStatic: Data?

    init(staticPrivate: Curve25519.KeyAgreement.PrivateKey) throws {
        self.staticPrivate = staticPrivate
        self.ephemeral = Curve25519.KeyAgreement.PrivateKey()
        let name = Data("Noise_XX_25519_ChaChaPoly_SHA256".utf8) // exactly 32 bytes
        self.h = name.count <= 32 ? name + Data(count: 32 - name.count) : Data(SHA256.hash(data: name))
        self.ck = self.h
        // Noise Initialize() always MixHash(prologue). Ours is empty, but even
        // MixHash([]) advances h (= SHA256(h)); skipping it diverges the running
        // hash from the host and fails the first AEAD tag in msg2.
        mixHash(Data())
    }

    private func mixHash(_ data: Data) { h = Data(SHA256.hash(data: h + data)) }

    private func mixKey(_ ikm: Data) {
        let out = noiseHKDF(chainingKey: ck, ikm: ikm, outputs: 2)
        ck = out[0]
        k = SymmetricKey(data: out[1])
        n = 0
    }

    private func chaNonce() -> ChaChaPoly.Nonce {
        var bytes = Data(count: 4)
        withUnsafeBytes(of: n.littleEndian) { bytes.append(contentsOf: $0) }
        return try! ChaChaPoly.Nonce(data: bytes)
    }

    private func encryptAndHash(_ plaintext: Data) throws -> Data {
        guard let k = k else { mixHash(plaintext); return plaintext }
        let box = try ChaChaPoly.seal(plaintext, using: k, nonce: chaNonce(), authenticating: h)
        n &+= 1
        let ct = box.ciphertext + box.tag
        mixHash(ct)
        return ct
    }

    private func decryptAndHash(_ ciphertext: Data) throws -> Data {
        guard let k = k else { mixHash(ciphertext); return ciphertext }
        let tag = ciphertext.suffix(16)
        let ct = ciphertext.prefix(ciphertext.count - 16)
        let box = try ChaChaPoly.SealedBox(nonce: chaNonce(), ciphertext: ct, tag: tag)
        let pt = try ChaChaPoly.open(box, using: k, authenticating: h)
        n &+= 1
        mixHash(Data(ciphertext))
        return pt
    }

    private func dh(_ priv: Curve25519.KeyAgreement.PrivateKey, _ pubRaw: Data) throws -> Data {
        let pub = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: pubRaw)
        let secret = try priv.sharedSecretFromKeyAgreement(with: pub)
        return secret.withUnsafeBytes { Data($0) }
    }

    // msg1 (initiator → responder): e
    func writeMessage1() throws -> Data {
        let ePub = ephemeral.publicKey.rawRepresentation
        mixHash(ePub)
        let payload = try encryptAndHash(Data()) // no key yet → empty plaintext
        return ePub + payload
    }

    // msg2 read (e, ee, s, es) then msg3 write (s, se). Returns msg3 bytes.
    func readMessage2WriteMessage3(_ msg: Data) throws -> Data {
        var offset = msg.startIndex

        // e
        let re = msg.subdata(in: offset..<msg.index(offset, offsetBy: 32))
        offset = msg.index(offset, offsetBy: 32)
        remoteEphemeral = re
        mixHash(re)
        // ee
        mixKey(try dh(ephemeral, re))
        // s (encrypted static: 32 + 16 tag)
        let encStatic = msg.subdata(in: offset..<msg.index(offset, offsetBy: 48))
        offset = msg.index(offset, offsetBy: 48)
        let rs = try decryptAndHash(encStatic)
        remoteStatic = rs
        // es
        mixKey(try dh(ephemeral, rs))
        // payload (empty; 16-byte tag)
        let encPayload = msg.subdata(in: offset..<msg.endIndex)
        _ = try decryptAndHash(encPayload)

        // msg3: s, se
        let encMyStatic = try encryptAndHash(staticPrivate.publicKey.rawRepresentation)
        mixKey(try dh(staticPrivate, remoteEphemeral!))
        let encMsg3Payload = try encryptAndHash(Data())
        return encMyStatic + encMsg3Payload
    }

    // split derives the two transport keys. flynn/noise returns the
    // initiator→responder cipher first, so for the initiator send=out[0].
    func split() -> (send: Data, receive: Data) {
        let out = noiseHKDF(chainingKey: ck, ikm: Data(), outputs: 2)
        return (out[0], out[1])
    }
}
