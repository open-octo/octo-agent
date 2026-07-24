package dev.octo.mobile

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import com.getcapacitor.JSObject
import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin
import com.southernstorm.noise.protocol.CipherState
import com.southernstorm.noise.protocol.DHState
import com.southernstorm.noise.protocol.HandshakeState
import com.southernstorm.noise.protocol.Noise
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

// OctoTunnel is the phone half of octo's managed tunnel — the native piece that
// cannot live in JavaScript. It holds the device's Noise static keypair in the
// Android Keystore, runs the Noise XX handshake with the host (brokered by the
// relay, which sees only ciphertext), and moves encrypted frames over a relay
// WebSocket. JavaScript hands it plaintext frames and receives plaintext frames
// back; no key material ever crosses the bridge.
//
// It mirrors the phone (initiator) side of the Go reference client in
// cmd/octo-relay/internal/client and interoperates with the host in
// internal/tunnel: Noise_XX_25519_ChaChaPoly_SHA256, empty prologue, JSON relay
// frames {t, tn, d, p} with p a base64 payload the relay never inspects.
@CapacitorPlugin(name = "OctoTunnel")
class OctoTunnelPlugin : Plugin() {

    // Noise suite / pattern shared with the host. Must match flynn/noise's
    // NewCipherSuite(DH25519, CipherChaChaPoly, HashSHA256) + HandshakeXX.
    private val protocolName = "Noise_XX_25519_ChaChaPoly_SHA256"

    private val http = OkHttpClient.Builder().build()

    // Session state. The WebSocket listener (one OkHttp thread) drives the
    // handshake and inbound decryption; send() runs on the Capacitor thread.
    // cryptoLock serializes cipher use — a Noise CipherState nonce is stateful.
    private val cryptoLock = Any()
    private var ws: WebSocket? = null
    private var hs: HandshakeState? = null
    private var tx: CipherState? = null
    private var rx: CipherState? = null
    private var deviceId: String? = null
    private var expectedHostKey: ByteArray? = null
    private var pairCall: PluginCall? = null

    // ── JS contract (native-bridge.ts) ──────────────────────────────────────

    @PluginMethod
    fun loadDeviceIdentity(call: PluginCall) {
        try {
            val (_, pub) = deviceKeypair()
            call.resolve(JSObject().put("publicKey", Base64.encodeToString(pub, Base64.NO_WRAP)))
        } catch (e: Exception) {
            call.reject("load identity failed: ${e.message}", e)
        }
    }

    @PluginMethod
    fun pair(call: PluginCall) {
        val relay = call.getString("relay")
        val token = call.getString("token")
        val hostKey = call.getString("hostKey")
        val tunnelId = call.getString("tunnelId")
        if (relay.isNullOrEmpty() || token.isNullOrEmpty() || hostKey.isNullOrEmpty()) {
            call.reject("pair: relay, token and hostKey are required")
            return
        }
        try {
            expectedHostKey = Base64.decode(hostKey, Base64.DEFAULT)

            // Build the initiator handshake with our persistent static keypair.
            val handshake = HandshakeState(protocolName, HandshakeState.INITIATOR)
            val (priv, _) = deviceKeypair()
            handshake.localKeyPair.setPrivateKey(priv, 0)
            handshake.start()
            hs = handshake
            tx = null
            rx = null
            deviceId = null
            pairCall = call

            // Dial the relay's device endpoint. OkHttp performs the WebSocket
            // upgrade over http(s); map ws(s):// accordingly.
            val base = relay
                .replaceFirst("wss://", "https://")
                .replaceFirst("ws://", "http://")
                .trimEnd('/')
            val url = deviceUrl(base, tunnelId, token)
            ws = http.newWebSocket(Request.Builder().url(url).build(), Listener())
        } catch (e: Exception) {
            pairCall = null
            call.reject("pair failed: ${e.message}", e)
        }
    }

    @PluginMethod
    fun send(call: PluginCall) {
        val frame = call.getString("frame")
        if (frame == null) {
            call.reject("send: frame is required")
            return
        }
        try {
            synchronized(cryptoLock) {
                val sender = tx ?: throw IllegalStateException("no active session")
                val pt = frame.toByteArray(Charsets.UTF_8)
                val out = ByteArray(pt.size + sender.macLength)
                val n = sender.encryptWithAd(null, pt, 0, out, 0, pt.size)
                sendFrame("data", out.copyOf(n))
            }
            call.resolve()
        } catch (e: Exception) {
            call.reject("send failed: ${e.message}", e)
        }
    }

    @PluginMethod
    fun disconnect(call: PluginCall) {
        teardown(null)
        call.resolve()
    }

    // ── relay WebSocket ─────────────────────────────────────────────────────

    private inner class Listener : WebSocketListener() {
        override fun onMessage(webSocket: WebSocket, text: String) {
            try {
                val f = JSONObject(text)
                when (f.optString("t")) {
                    "paired" -> {
                        deviceId = f.optString("d")
                        pump() // send handshake message 1
                    }
                    "handshake" -> {
                        val h = hs ?: return
                        val msg = Base64.decode(f.optString("p"), Base64.DEFAULT)
                        val payload = ByteArray(Noise.MAX_PACKET_LEN)
                        h.readMessage(msg, 0, msg.size, payload, 0)
                        pump()
                    }
                    "data" -> deliverData(f.optString("p"))
                }
            } catch (e: Exception) {
                teardown(e)
            }
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            teardown(t)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            teardown(if (tx == null) IllegalStateException("relay closed before handshake") else null)
        }
    }

    // pump drives the initiator handshake: it writes every outgoing handshake
    // message the state machine is ready to send, then completes the session
    // (verify host key + split into transport ciphers) once the pattern is done.
    private fun pump() {
        val h = hs ?: return
        while (h.action == HandshakeState.WRITE_MESSAGE) {
            val buf = ByteArray(Noise.MAX_PACKET_LEN)
            val len = h.writeMessage(buf, 0, ByteArray(0), 0, 0)
            sendFrame("handshake", buf.copyOf(len))
        }
        if (h.action == HandshakeState.SPLIT) {
            completeHandshake(h)
        }
    }

    private fun completeHandshake(h: HandshakeState) {
        // Authenticate the host: its static key, learned during XX, must equal
        // the one the pairing QR carried. This is what the relay can't forge.
        val remote: DHState = h.remotePublicKey
        val hostPub = ByteArray(remote.publicKeyLength)
        remote.getPublicKey(hostPub, 0)
        val expected = expectedHostKey
        if (expected != null && !hostPub.contentEquals(expected)) {
            teardown(SecurityException("host key mismatch — refusing to trust relay peer"))
            return
        }
        val pair = h.split()
        synchronized(cryptoLock) {
            tx = pair.sender
            rx = pair.receiver
        }
        h.destroy()
        hs = null
        pairCall?.resolve()
        pairCall = null
    }

    private fun deliverData(payloadB64: String) {
        val text: String
        synchronized(cryptoLock) {
            val receiver = rx ?: return
            val ct = Base64.decode(payloadB64, Base64.DEFAULT)
            val out = ByteArray(ct.size)
            val n = receiver.decryptWithAd(null, ct, 0, out, 0, ct.size)
            text = String(out, 0, n, Charsets.UTF_8)
        }
        notifyListeners("frame", JSObject().put("frame", text))
    }

    private fun sendFrame(type: String, payload: ByteArray) {
        val o = JSONObject()
        o.put("t", type)
        deviceId?.let { o.put("d", it) }
        o.put("p", Base64.encodeToString(payload, Base64.NO_WRAP))
        ws?.send(o.toString())
    }

    private fun teardown(err: Throwable?) {
        pairCall?.let { call ->
            if (err != null) call.reject("tunnel error: ${err.message}", err as? Exception)
            pairCall = null
        }
        try {
            ws?.close(1000, null)
        } catch (_: Exception) {
        }
        ws = null
        synchronized(cryptoLock) {
            tx = null
            rx = null
        }
        hs?.destroy()
        hs = null
    }

    // ── device static keypair (Keystore-wrapped X25519) ─────────────────────
    //
    // AndroidKeyStore can't hold a raw X25519 scalar we can feed to Noise, so we
    // keep a Keystore-resident AES-GCM master key and store the 32-byte private
    // key wrapped by it. The private key is never persisted in the clear and the
    // master key never leaves the Keystore (hardware-backed where available).

    private val prefsName = "octo.tunnel"
    private val keyPriv = "device_priv" // base64(iv || ciphertext)
    private val keyPub = "device_pub" // base64
    private val masterAlias = "octo_tunnel_master"
    private val gcmTagBits = 128
    private val ivLen = 12

    // deviceUrl builds the relay device endpoint. Against a DNS-named relay it
    // prefixes the tunnel id as a subdomain (<tunnelId>.relay.octo.dev) so a
    // multi-node deployment's L4 balancer can consistent-hash the TLS SNI and
    // land the phone on the same node as its host. IP literals and dotless
    // hosts (local dev) dial unchanged. Mirrors relayDialBase in the host's
    // internal/tunnel/tunnel.go — keep the two rules identical.
    private fun deviceUrl(base: String, tunnelId: String?, token: String): String {
        val plain = "$base/device?token=$token"
        if (tunnelId.isNullOrEmpty()) return plain
        val uri = try { java.net.URI(base) } catch (_: Exception) { return plain }
        val host = uri.host ?: return plain
        val ipLike = host.matches(Regex("^[0-9.]+$")) || host.contains(":")
        if (!host.contains(".") || ipLike) return plain
        val portPart = if (uri.port != -1) ":${uri.port}" else ""
        return "${uri.scheme}://$tunnelId.$host$portPart/device?token=$token"
    }

    @Synchronized
    private fun deviceKeypair(): Pair<ByteArray, ByteArray> {
        val prefs = context.getSharedPreferences(prefsName, 0)
        val storedPriv = prefs.getString(keyPriv, null)
        val storedPub = prefs.getString(keyPub, null)
        if (storedPriv != null && storedPub != null) {
            return unwrap(Base64.decode(storedPriv, Base64.DEFAULT)) to Base64.decode(storedPub, Base64.DEFAULT)
        }

        val dh = Noise.createDH("25519")
        dh.generateKeyPair()
        val priv = ByteArray(dh.privateKeyLength)
        dh.getPrivateKey(priv, 0)
        val pub = ByteArray(dh.publicKeyLength)
        dh.getPublicKey(pub, 0)
        dh.destroy()

        prefs.edit()
            .putString(keyPriv, Base64.encodeToString(wrap(priv), Base64.NO_WRAP))
            .putString(keyPub, Base64.encodeToString(pub, Base64.NO_WRAP))
            .apply()
        return priv to pub
    }

    private fun masterKey(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getKey(masterAlias, null) as? SecretKey)?.let { return it }
        val kg = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        kg.init(
            KeyGenParameterSpec.Builder(
                masterAlias,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .build(),
        )
        return kg.generateKey()
    }

    private fun wrap(plain: ByteArray): ByteArray {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, masterKey())
        val ct = cipher.doFinal(plain)
        return cipher.iv + ct
    }

    private fun unwrap(blob: ByteArray): ByteArray {
        val iv = blob.copyOfRange(0, ivLen)
        val ct = blob.copyOfRange(ivLen, blob.size)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, masterKey(), GCMParameterSpec(gcmTagBits, iv))
        return cipher.doFinal(ct)
    }
}
