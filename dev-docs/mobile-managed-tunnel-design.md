# Mobile Clients & the Managed Tunnel

octo's iOS and Android apps are octo's seventh and eighth interfaces — the eighth leg of the octopus, alongside CLI, TUI, web, the IM bridges, the desktop shell, and the editor plugins. They are **thin remote clients** of an existing `octo serve` instance, not a second agent that runs on the phone. The agent, its tools, and the user's codebase stay on the machine that has the real filesystem and dev environment; the phone drives that machine over the network.

Reaching a `serve` instance from a phone means crossing NAT. Two transports carry the same protocol:

- **Self-hosted tunnel (free).** The user stands up their own reachability — Tailscale, Cloudflare Tunnel, `frp`, a reverse proxy — and points the app at the resulting URL. This is the honest self-host path and preserves octo's zero-runtime, self-hostable positioning.
- **Managed tunnel (paid, this document).** octo runs a relay that brokers the connection so the user configures nothing. The relay is an **end-to-end-encrypted dumb pipe**: it routes ciphertext and wakes the phone, and by construction can never read a prompt, a file, or a reply. This is octo's equivalent of Obsidian Sync — a hosted convenience that sits on top of a local-first core without weakening it.

Billing and accounts are out of scope here. This document specifies only the transport: how a phone reaches a `serve` instance through octo's relay, end-to-end encrypted, with push notifications, while the existing server learns nothing new.

## Goals

- **Full conversational parity on mobile.** Read streamed output, send and steer prompts, approve permission asks, answer `ask_user_question`, retry/rollback, switch sessions — the whole `/ws` surface, on a phone.
- **The relay is a dumb pipe.** It routes end-to-end-encrypted frames by an opaque `(tunnel id, device id)` and pushes content-free wakeups. It holds no plaintext, no key material, and no durable per-device data. This is a structural guarantee, not a policy promise.
- **`octo serve` is barely touched.** The transport is a `--tunnel` flag that starts one goroutine; the flag aside, no handler, route, or protocol changes. The tunnel connects to the server the same way a browser does.
- **One protocol, swappable transport.** The mobile client speaks the existing WebSocket protocol over a transport interface with two implementations — self-tunnel and managed relay. The UI and protocol code do not know which is in use.
- **Push survives a killed app.** Permission asks and turn-complete events wake the phone through APNs/FCM even when the app is not running, without the wakeup carrying any user content.
- **Two platforms from one codebase.** iOS and Android ship from a single Capacitor project that reuses the existing Svelte frontend, mirroring how the Wails desktop shell reuses it.

## Non-goals

- **No on-device agent.** The Go core does not run on the phone. A coding agent with no filesystem, no shell, and an App Store sandbox is not the product; the phone steers a real environment elsewhere. (`gomobile` is therefore not used.)
- **No accounts or billing.** Device pairing authenticates the tunnel with a one-time token; there is no login, subscription, or multi-user identity yet. Those are a separate follow-up that this transport is designed not to preclude.
- **The relay never terminates the agent protocol.** It does not parse `/ws` JSON, hold sessions, or run any agent logic. It moves bytes and pushes wakeups.
- **Not a replacement for the self-tunnel.** The free bring-your-own-tunnel path stays fully supported. The managed tunnel is additive.
- **No relay-side history or storage.** The relay does not persist conversation frames or a device registry. Offline delivery is a wakeup, not a stored plaintext queue.

## Architecture: two symmetric bridges around a dumb relay

The design rests on one move applied at both ends: **the tunnel and its encryption live in a thin bridge, so the real app code — the server on one side, the Svelte frontend on the other — stays untouched and same-origin.**

On the host, a machine behind NAT cannot accept an inbound connection — the only reason a relay exists — so it dials out. `octo serve --tunnel` starts a **tunnel goroutine** in the serve process that opens a persistent outbound connection to the relay and, for each paired phone, bridges decrypted frames to `ws://127.0.0.1:8088/ws` as an ordinary key-authenticated client. The server sees exactly what it sees from the desktop webview or a browser tab.

On the phone, the bundled Svelte frontend still speaks same-origin `/api` and `/ws` — to `capacitor://localhost`. A **local shim** in the app terminates those requests and bridges the bytes, encrypted, out to the relay. The frontend never learns it is remote.

```
     mobile app                          relay                  octo serve --tunnel  (one process)
┌──────────────────┐                  ┌──────────┐              ┌────────────────────────────────────┐
│ webview: Svelte  │  E2E over TLS    │  routes  │  E2E over TLS │  tunnel goroutine                   │
│  UI (unchanged)  │◀────────────────▶│    by    │◀─────────────▶│   self-dials loopback /ws as a      │
│    ▲             │                  │(tunnelid,│              │   normal, key-auth'd ws client      │
│    │ plain /api  │                  │ deviceid)│              │            │                        │
│    │   + /ws     │                  │    +     │              │            ▼                        │
│  local shim  ────┘  Noise + keys    │  push    │              │   /ws :8088  (server unchanged)     │
│  (native; keys   in Keychain/       │ APNs/FCM │              │                                     │
│   Keystore)      Keystore)          └──────────┘              └────────────────────────────────────┘
└──────────────────┘
```

- **The relay sees only ciphertext.** Both bridges run a Noise handshake keyed to device identities the relay never holds. The relay matches a phone to a host by `(tunnel id, device id)` and copies encrypted frames between them. It cannot decrypt.
- **`internal/server` is untouched by the tunnel.** No new endpoints, no relay awareness. The `--tunnel` flag starts a goroutine that is, from the server's view, one more loopback `/ws` client — so the tunnel path exercises the identical contract a browser does and cannot silently drift from it.
- **Symmetry.** Host: goroutine bridging `relay ↔ loopback /ws`. Phone: local shim bridging `webview localhost ↔ relay`. Encryption and tunnelling are confined to these two bridges; keys stay in native secure storage on the phone and in `~/.octo` on the host, never in the Svelte layer or the server.

## The moving parts

### The tunnel goroutine (`octo serve --tunnel`)

Not a second process — a goroutine in the existing serve process, gated by a flag so there is nothing extra to install and no second daemon to fight over the `~/.octo/serve.pid` / port lifecycle the [desktop hub](desktop-hub-design.md) already arbitrates. It:

- Holds the host side of the device keypair in `~/.octo`, alongside the access key.
- Dials the relay, authenticates the host side of the tunnel, and maintains the connection with reconnect/backoff — the same infinite-retry reconnection the editor plugins already do against `serve`.
- For each paired phone, runs a Noise session; decrypts inbound frames and forwards them to the local `/ws` (presenting the local access key); encrypts server events back to that phone.
- Emits content-free wakeup signals to the relay when a paired-but-offline phone has pending activity, driven by the `wsEventSessionActivity` signal (`question_pending` / `turn_complete`) the server already broadcasts.

### Relay (`octo-relay`)

A small Go service, hosted by octo, running as **multiple nodes** — not a single box, and not stateless-per-frame. Its entire job:

- **Bridge two live connections.** A host connection (from a tunnel goroutine) and one or more device connections (phones) belonging to the same tunnel. It copies opaque encrypted frames between them, addressed by `(tunnel id, device id)`, and does nothing else with their contents. Frame routing is in-node memory: the two connections of a tunnel are pinned to the same node (see routing below), so a frame never leaves the node it arrived on until it reaches its peer.
- **Push content-free wakeups.** When a tunnel goroutine has activity for a currently-disconnected phone, it sends the relay a wakeup carrying that phone's push token and nothing more; the relay forwards it to APNs/FCM. The push says "you have activity," never what. The phone wakes, reconnects through the tunnel, and pulls the real (encrypted) frames.
- **Broker pairing.** A transient, in-memory match on a one-time pairing token lets a new phone and its host find each other to run the Noise handshake. Nothing about the pairing is persisted by the relay.

The relay holds APNs/FCM app credentials — per-app secrets that cannot live on each user's machine — and nothing else of value: no keys that decrypt traffic, no conversation data, no persisted device registry. Compromising it yields ciphertext and the ability to disrupt routing, never the ability to read.

#### Routing across nodes

Two connections of the same tunnel must land on the same node for the in-memory copy to work. The tunnel id is encoded in the TLS **SNI subdomain** (`<tunnelid>.relay.octo.dev`), so an L4/L7 load balancer routes on it by **consistent hashing before any decryption** — both the host and the phone of a tunnel hash to the same node. When a node dies, both ends reconnect and the ring re-pins their tunnel to a surviving node together; consistent hashing keeps that churn to `1/N` of tunnels when the fleet changes size. Because frames are E2E ciphertext, exposing the tunnel id in cleartext SNI leaks only "some tunnel is connecting," never content.

A shared message bus (every frame host→node→bus→node→phone) was rejected: the agent stream is chatty and streamed token-by-token, so a per-frame bus hop taxes latency and throughput continuously and adds a stateful hot-path dependency, in exchange for a flexibility (any node serves any connection) that sticky routing does not need.

### Mobile client (`octo-mobile`)

A Capacitor project — a sibling repository to `octo-vscode` and `octo-obsidian` — that wraps the existing Svelte frontend (`internal/server/webdist`) in a native shell and adds the native bridges a browser can't provide. This mirrors the Wails desktop shell (see [wails-desktop-design.md](wails-desktop-design.md)): reuse the web stack wholesale, add a thin native layer, write no second UI.

The frontend is **bundled into the app** and served to the webview from `capacitor://localhost`, so it starts instantly and — crucially — keeps its same-origin assumptions. It does **not** connect cross-origin to the relay; the local shim (above) presents the `/api` and `/ws` surface locally and tunnels it out. Keys and the Noise handshake stay in the native layer and secure storage, never in JavaScript.

Because the app is bundled and connects out to a *remote* host, its nativeness is a client-side fact, not a server-reported one — the desktop shell's model, where a same-machine server reports `native:true` and the page carries `?shell=octo-desktop`, does not apply here. The native layer and how it re-points the frontend's existing native branches are detailed next.

## The native layer: what the app implements

Mobile's native layer is not the desktop shell's. Desktop native capabilities are **server-side** — `/api/native/*` routes on a server that runs on the same machine as the OS dialogs, dock, and notifications it drives. A phone's native capabilities are on the phone, and the phone connects to a **remote** `octo serve --tunnel` that runs plain: `cfg.Native` is nil, so `/api/version` reports `native:false` and no `/api/native/*` routes exist. The app therefore reaches native capability through **Capacitor's JS↔native plugin bridge**, not HTTP to the remote. The layer is three parts: new Capacitor plugins, a re-pointing of the frontend's existing native branches, and a set of desktop-only capabilities that don't apply.

### Client-side nativeness

The frontend today sets `nativeShell` as `native:true` (from `/api/version`) *and* `?shell=octo-desktop` (from the URL). Neither holds on mobile — the remote is plain serve, and the bundled app has no shell query. The app introduces a separate `mobileShell` signal set from `Capacitor.isNativePlatform()`, and the frontend's `if (nativeShell)` branches become `if (nativeShell || mobileShell)`, dispatching to the desktop `/api/native/*` path or the Capacitor path by whichever is set. Mobile nativeness depends on the client, not on anything the server reports.

### Capacitor plugins (new; JS↔native, never the remote)

| Capability | Plugin | Role |
|---|---|---|
| Local tunnel shim | custom | The transport: terminate the webview's `/api` + `/ws` at `capacitor://localhost`, run Noise, tunnel to the relay. The desktop had no equivalent — it connected same-origin. |
| Secure storage | Keychain / Keystore | Hold the device keypair and tunnel credentials (the host keeps these in `~/.octo`). |
| Biometric unlock | biometric | Gate release of the stored credential behind Face ID / fingerprint. |
| Push registration + receipt | `@capacitor/push-notifications` | Obtain the APNs/FCM token and hand it to the host at pairing; on a wakeup, reconnect and pull state. |
| Local notification | `@capacitor/local-notifications` | Foreground/attended notifications, in place of the browser Notification API the webview lacks. |
| Deep link | `@capacitor/app` (`appUrlOpen`) | A push tap opens the app on the exact session that raised the event. |
| QR scan | barcode-scanner / camera | Scan the host's pairing QR. |
| Open external URL | `@capacitor/browser` | Open links in the system browser. |
| Save / share file | `@capacitor/filesystem` + share | Land an artifact download on the phone. |

### Re-pointing the frontend's native branches

These points already branch on `nativeShell`; mobile adds a `mobileShell` arm that targets Capacitor — **never** the remote `/api/native/*`, which is both absent and semantically wrong (opening a URL or saving a file on the remote host is useless to a phone user).

| Frontend point | Desktop | Mobile |
|---|---|---|
| `externalLinks.ts` (open URL / anchor intercept) | `/api/native/open-external` | Capacitor Browser |
| `notifications.ts` | `/api/native/notify` | foreground → local notification; background → push |
| Pick working directory (`nativePickFolder`) | remote OS dialog | **the existing remote web folder picker** (`GET /api/fs/list` → `PATCH …/working_dir`) — the target directory is on the remote host, not the phone, so this reuses the same path plain web uses, not a phone file dialog |
| Artifact download (`nativeSaveFile`) | remote save dialog | Capacitor filesystem + share (the file belongs on the phone) |

### Desktop-only, dropped

- **`ToggleMaximise`** — no window chrome to zoom.
- **`AutostartEnabled` / `SetAutostart`** — no launch-at-login concept.
- **`PersistChannelsEnabled` / channels toggle** — a host concern; the remote plain serve exposes no `NativeBridge` for it anyway.

### Server side

Nothing new. The remote is plain `octo serve --tunnel` with `cfg.Native` nil; the entire native layer is client-side Capacitor code. The one server-side change the mobile experience needs — dropping the fixed timeout on a permission ask so an attended session can approve after a nap — is described under [Reconnect and durability](#reconnect-and-durability-a-wakeup-is-a-tab-refresh) and is orthogonal to this layer.

## End-to-end encryption and pairing

**Crypto: the Noise Protocol Framework** (the handshake pattern behind WireGuard and Tailscale). It gives mutual authentication and forward secrecy from raw X25519 keypairs with no certificate authority or PKI to operate — a good fit for a self-host tool where the endpoints are a user's own phone and a user's own machine. Each device (phone, host) generates a static keypair on first run and stores it in secure storage; the relay never sees a private key.

**A tunnel is a host's presence on the relay, joined by one or more of the user's devices.** The host owns the tunnel id; N phones can pair into it, each with its own device keypair. The host stores every paired device's public key, and the relay routes frames by `(tunnel id, device id)`. This models "my devices ↔ my dev machine" directly and keeps the host to a single outbound relay connection regardless of how many phones are paired.

**Pairing is a QR handshake**, brokered by the relay but not trusted by it:

1. On the host, the pairing QR encodes the tunnel id, the host's static public key, and a short-lived, single-use pairing token.
2. The phone scans it, contacts the relay with the pairing token, and the two sides complete a Noise handshake — exchanging and confirming static public keys over the relay's already-TLS connection. The phone also hands the host its APNs/FCM push token over this E2E channel.
3. Both sides persist the peer's public key; the host adds the phone to its paired set. The pairing token is now spent; later connections authenticate by the established keys.

Because keys are confirmed end to end during pairing, a malicious or compromised relay cannot man-in-the-middle a paired tunnel — it would need a private key it never holds. Its worst case is disrupting routing: a denial of service, not a disclosure.

**Revocation** is dropping a phone's public key from the host's paired set; that device is cut off without affecting the others.

## Push without breaking E2E

The tension: push needs a server-to-phone path that works when the app is dead, but the relay must not read content. The resolution is that **the push carries no content** — it is a wakeup, not a message — and **the relay stores no push tokens**.

1. A turn on the host raises `wsEventSessionActivity` (`question_pending` or `turn_complete`) — already broadcast by the server today.
2. The tunnel goroutine sees it (it is a `/ws` client) and, if the target phone is not currently connected, sends the relay a wakeup carrying that phone's push token (which the host holds from pairing) and the device id — nothing else.
3. The relay pushes through APNs/FCM: "octo has activity." No session content, no prompt text, no tool output. The relay uses only its own APNs/FCM app credentials plus the token handed to it in this call; it persists neither.
4. The phone wakes, reconnects through the tunnel, runs the Noise session, and pulls the real encrypted frames — the permission ask, the completed turn — decrypting and rendering them on-device.

Push tokens rotate. The phone re-delivers its current token to the host over E2E whenever it changes (on app open is enough); a host holding a stale token simply fails to wake that phone until the next refresh, which is self-correcting.

Android delivery goes through FCM and iOS through APNs — both OS-mandated, so they are the only third parties in the push path by construction. The relay talks to them directly with its own credentials via thin Go client libraries (raw HTTP/2 to APNs with `.p8` token auth, FCM HTTP v1), rather than layering a managed push vendor on top: an extra vendor would see device tokens and wakeup metadata for no gain, and contradicts the self-hosted, no-lock-in positioning. Because the relay (`octo-relay`) is a separate deployable, these libraries never touch the CLI binary's zero-dependency story.

The most valuable mobile capability — approving a permission ask while away from the desk — is delivered without any plaintext ever reaching octo's servers.

## Reconnect and durability: a wakeup is a tab refresh

The mobile client is a live `/ws` subscriber only while connected. Backgrounded or killed, it misses the stream — but the host's tunnel goroutine stays connected, so nothing about the turn is lost on the host. Durability comes entirely from the server's on-disk session and its existing resubscribe replay; **the relay buffers nothing, and neither does the host**.

On reconnect — whether from a background resume or a push-driven cold start — the phone does exactly what a reloaded browser tab does:

1. Re-fetches the session's persisted history (`GET /api/sessions/{id}/messages`), recovering every finalized message it missed. Lost streaming deltas don't matter; the on-disk session is the single source of truth.
2. Re-subscribes over `/ws`, and the server replays the pending interactive prompts it already tracks (`pendingQuestions` / `pendingConfirms`) so an in-flight permission ask or question is delivered, not orphaned.

Buffering missed frames — on the relay (rejected: breaks the dumb-pipe guarantee) or on the host (rejected: unbounded growth, and redundant with persisted history) — buys only the lost streaming deltas, which the history re-fetch already supersedes.

**One server-side change follows from this — the only change the tunnel needs outside its own components.** For remote approval to work, a permission confirmation must not resolve to *deny* while the phone sleeps, otherwise the turn is rejected before the user ever sees the ask. Today it does: `requestConfirmation` (`internal/server/ws_handlers.go`) waits on a hardcoded `time.After(5 * time.Minute)` and, on timeout, returns an error that `permissionAskFrom` maps to `allow=false` — a silent deny five minutes into a nap. The IM confirmation (`channelPermissionAsk`, `internal/server/channel_ask.go`) has the same `channelAskTimeout` deadline. The change is to **delete both timeouts**, so an attended ask waits indefinitely for a real answer — exactly what `ask_user_question` already does on both surfaces (`wsAsker.Ask` and `chatAsker.Ask` have no timeout branch; PR #1275).

Deleting the timeout is safe because *attended versus unattended is already a per-session property* — the session's `permission.Mode` — decided at session creation, not something `requestConfirmation` has to infer at wait time from who is subscribed. The engine's `applyMode` resolves an ask-class verdict before the gate ever calls the confirmation function:

- **Interactive** (the default for user-created and IM sessions) passes `Ask` through, so it reaches the wait — and now blocks until the user answers from any client or the turn is interrupted. This is the attended path: a browser tab, a desktop webview, or a phone that a push will summon.
- **Strict** converts `Ask → Deny` inside the engine, so the confirmation function is *never reached*. An unattended posture (evals, scripted runs, a cron session configured strict) fails fast without any timer — the timeout was only ever a redundant second guess of a decision the mode already made.
- **AutoApprove** converts `Ask → Allow`, so it never reaches the wait either.

The unattended default stays **AutoApprove**, not Strict: an unconfigured background session approves rather than blocks, keeping the convenience posture the codebase already chose (write tools no longer blanket-allow `$CWD`, so an implicit interactive ask would otherwise strand every write). A deploy that wants "deny unless pre-approved" opts into `permission_mode: strict`; the mobile transport does not change this posture in either direction.

The only cost of an indefinite wait is that an attended ask nobody answers holds the session's turn lock until the turn is interrupted — `ctx` cancellation, which is `/stop` over IM and the interrupt control in the web UI. This matches `ask_user_question`'s existing behavior exactly; a lost or ignored phone is released by interrupting the turn, not by a silent deny.

## Transport abstraction: free and paid share one protocol

The mobile client talks to its backend through a single transport interface with two implementations:

- **Self-tunnel transport.** Opens a WebSocket straight to the user-provided URL and presents the access key, exactly as the editor plugins do. No relay, no Noise — the user's own tunnel provides the encrypted transport.
- **Managed-relay transport.** Runs the Noise session and speaks to the relay, framing the same `/ws` JSON inside encrypted frames.

Above this interface, the protocol layer and the entire Svelte UI are identical. Moving a user from free to paid, or letting a technical user opt into their own tunnel, is a transport swap — not a code fork. This is the same additive discipline the desktop shell follows: the paid path adds a transport, it does not rewrite the client.

## Relationship to the existing access key

The tunnel does not replace `serve`'s access-key auth ([serve-auth-design.md](serve-auth-design.md)); it layers on top. The tunnel goroutine authenticates to the *local* server with the same access key any client uses, over loopback. The Noise device keys authenticate *phone ↔ host* independently. A phone therefore needs two things to reach a session: a completed pairing (device keys) and, transitively, the tunnel goroutine's local access to the server. Revoking a phone drops its key from the host's paired set; disabling all remote access stops `--tunnel` — the server and its loopback clients are untouched either way.

## Configuration

- **Opt-in.** The managed tunnel is off until `octo serve --tunnel` (or the desktop/web UI equivalent) starts it. Plain `octo serve` is unchanged.
- **Relay endpoint.** A built-in default (`relay.octo.dev`) that `~/.octo/config.yml` can override, for self-hosting the relay or pointing at staging.
- **Pairing.** Driven by a QR the host renders; the pairing token is short-lived and single-use. Two surfaces render it: the CLI prints an ASCII QR (so a headless server — a prime remote-control target — can pair with no GUI), and the web/desktop UI offers a Settings QR panel for workstation users. Both drive the same pairing endpoint; the panel is only a friendlier renderer.

## Distribution and App Store review

The app is positioned as **a remote client to the user's own machine** — the same category as the SSH, terminal, and remote-desktop clients Apple already approves (Termius, Blink, Jump Desktop). The distinctions App Review cares about hold here: the host is the user's own, command execution happens on that host and never inside the app, and no executable code is downloaded into the app (App Review guideline 2.5.2 is not triggered — the app is a viewer/controller, not a code loader). Both the App Store and Google Play are launch targets, and the app keeps its full conversational capability rather than trimming to monitor-only to appease review.

That category positioning is necessary but not sufficient; the connection model has four further consequences for review, each resolved by the transport design above rather than by a review-time workaround.

### A reviewer can exercise the app end to end (Guideline 2.1)

A remote-control app opens onto a pairing screen and does nothing until it reaches a backend — the classic 2.1 rejection is a reviewer who has no host to pair with. octo operates a **demo host** (`octo serve --tunnel` against a throwaway workspace) for exactly this, and the App Review notes carry a **pre-generated pairing code** for it. The reviewer scans it, lands in a live session, sends a prompt, and approves a permission ask — the whole flow, including the mobile-critical remote-approval path, without standing up their own machine. The demo host runs the same build as any user's, so what the reviewer exercises is the real product, not a stub.

### No in-app purchase surface at launch (Guideline 3.1.1)

The managed tunnel is a paid service, but v1 ships **no purchase or subscription UI inside the app**, and no link that steers the user to buy. Paid relay access is provisioned entirely on the host side — the CLI or desktop shell that already renders the pairing QR is where a user turns the managed tunnel on. The app only *consumes* an already-provisioned pairing, so it qualifies as a multiplatform service rather than a digital purchase consumed in-app, and 3.1.1's IAP requirement is not triggered. This also matches the transport's existing shape: pairing authenticates with a one-time token and carries no account or billing identity (see [Deferred](#deferred)).

### Privacy declarations follow the transport, honestly (Guidelines 5.1.1, 5.1.2)

The two transports declare differently, and neither overclaims:

- **Self-tunnel** collects nothing for octo — traffic flows only between the user's phone and the user's host — so it declares **Data Not Collected**.
- **Managed relay** is end-to-end encrypted, so session content is never readable by octo and is not reported as collected; the privacy policy states plainly that the relay is a ciphertext-only dumb pipe that persists no conversation data. The one datum the relay transiently handles — the **push token / device id** used to wake a sleeping phone — *is* declared, under App Functionality, not linked to the user's identity and not used for tracking. The design's structural guarantees (relay holds no key material, persists no token or frame — see [Push without breaking E2E](#push-without-breaking-e2e)) are the factual basis for these declarations. The app does **not** claim zero data collection on the managed path, because the push path demonstrably touches a token.

### Encryption and push are declared, not incidental

The Noise handshake (X25519) makes the app a user of non-exempt encryption: it sets `ITSAppUsesNonExemptEncryption` and answers the export-compliance questionnaire, claiming the standard exemption for encryption that only protects the user's own data (with the additional French declaration where required). Push uses APNs directly with the relay's own credentials, so the app declares the `remote-notification` background mode and the wakeup-only purpose — content-free by construction, as the push section specifies.

## Deferred

- **Accounts, login, and billing.** Pairing uses a one-time token, not an identity. A later account layer can gate relay access and metering; the tunnel is designed so that layer slots in at the relay's authentication step without touching E2E or the client protocol. Whether that layer eventually needs In-App Purchase turns entirely on whether a purchase surface appears *inside the app*: the multiplatform-service posture (provisioning on the host side, no in-app buy flow) that keeps 3.1.1 untriggered at launch is the one to preserve as billing lands.
- **One phone, many hosts.** A phone paired to several hosts (work laptop, home desktop) is a natural extension — the phone simply stores multiple pairings — but v1 ships the many-devices-to-one-host direction and a single active host per phone; the host-switching UI comes later.
- **Managed tunnel for the desktop/web clients.** The transport abstraction is general, but the first target is mobile, where the reachability gap is real. Bringing other clients onto the relay is a later, orthogonal step.
