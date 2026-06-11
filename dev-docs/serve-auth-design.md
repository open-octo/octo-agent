# Serve Authentication & Threat Model

`octo serve` exposes a chat API that executes arbitrary commands on the host,
so the HTTP surface is gated: requests from anywhere other than the loopback
interface must present a shared access key, browser cross-site attacks are
blocked by Host/Origin validation even on the exempt loopback path, and the
default bind address is loopback-only. `SECURITY.md` at the repo root is the
public statement of this threat model.

## Goals

- **Zero-friction local default.** `octo serve` + a browser on the same
  machine works exactly as before: no key prompt, no setup.
- **Exposure requires a secret.** Binding beyond loopback (`-addr :8080`,
  LAN, tailscale) makes every API and WebSocket request require the access
  key.
- **Browser attacks blocked without the key.** CSRF simple requests and DNS
  rebinding reach loopback from the user's own browser; the loopback
  exemption alone would let them through. Host and Origin validation closes
  that path.
- **Key distribution without reading docs.** When a key is required, startup
  prints a ready-to-open URL embedding it; the web UI adopts and stores it.

## Non-goals

- **Multi-user.** One server, one user, one key. No accounts, roles, or
  per-session identity.
- **TLS.** Terminate HTTPS in front (reverse proxy, tailscale serve) if the
  transport needs encryption; the key travels in cookies/headers and is only
  as private as the transport.
- **Same-host hostile processes.** Anything that can already run code as a
  local user on the machine is outside the boundary — loopback traffic is
  trusted by design. The threat model documents this explicitly.
- **Brute-force lockout / rate limiting.** The key is 256 bits of
  `crypto/rand`; online guessing is not a viable attack, and the comparison
  is constant-time.

## Threat model

| Actor | Vector | Defense |
|---|---|---|
| LAN / internet attacker | Direct API calls to an exposed bind | Loopback-only default bind; access key required for non-loopback clients |
| Malicious website in the user's browser | CSRF simple request (form POST, no-cors fetch) to `http://localhost:8080` | Origin validation: browser-sent `Origin` must be local or in the `--cors` allowlist |
| Malicious website in the user's browser | DNS rebinding (attacker domain resolves to 127.0.0.1, request is then "same-origin") | Host validation: the loopback exemption requires a local `Host` header |
| Malicious website in the user's browser | No-`Origin` subresource GETs (`<img>`, `<script src>`) to loopback | All mutations are POST/PATCH/DELETE (blocked by the Origin gate); GET responses are JSON with `X-Content-Type-Options: nosniff` — `/api/uploads/{name}` gains the same header to close the XSSI read channel |
| IM platforms (Feishu, DingTalk, …) | Inbound messages driving the agent | Out of scope here — adapters hold outbound connections (no inbound HTTP routes) and are authenticated by bot credentials + the chat/user binding |
| Same-host process (any local user) | Loopback API calls | Trusted, by design (see non-goals) |
| Network eavesdropper | Key capture on plaintext HTTP | Out of scope — documented; use TLS in front for untrusted networks |

## Design

### Request classification (`requireAuth`)

`requireAuth` wraps every `/api/*` route except `/api/health` and
`/api/version`, plus `/ws`. Routes register through the `Server.api` helper,
which applies the wrapper in one place — a new route structurally cannot
forget it; only health, version, and static files register directly on the
mux. Decision order:

1. **Valid key presented → allow.** Sources checked in order:
   `Authorization: Bearer <key>`, `X-Access-Key` header, `octo_access_key`
   cookie — plus, on `/ws` only, the `access_key` query parameter (the
   browser `WebSocket` API cannot set headers; restricting the query source
   to `/ws` keeps the key out of request URLs everywhere else). Comparison
   via `crypto/subtle.ConstantTimeCompare`; an empty configured key matches
   nothing.
2. **Loopback exemption → allow** when all three hold:
   - `RemoteAddr` is loopback, determined by `net.SplitHostPort` +
     `net.ParseIP(...).IsLoopback()` — this covers `127.0.0.0/8`, `::1`,
     and the IPv4-mapped `::ffff:127.0.0.1`; a string-prefix match does
     not. `X-Forwarded-For` is never consulted — a spoofable header must
     not widen the exemption.
   - `Host` is local: `localhost` or a loopback IP literal (any port,
     compared case-insensitively, trailing dot stripped), or a host named
     in the `--cors` allowlist. This is the DNS-rebinding gate: a rebound
     page sends `Host: attacker.com`. `0.0.0.0` is not loopback and does
     not qualify.
   - `Origin`, when the header is present, is local or in the `--cors`
     allowlist. `Origin: null` (sandboxed iframes, some redirects) is
     rejected. Non-browser clients (curl, scripts) send no `Origin` and
     pass. This is the CSRF gate: a cross-site form/fetch sends
     `Origin: https://evil.example`.
3. **Otherwise** → `401` (missing/wrong key from non-loopback) or `403`
   (loopback with a foreign Host/Origin), JSON error body.

A literal `*` in `--cors` is honored only by `corsMiddleware`'s
response-header logic; the Host/Origin **auth** predicates never treat `*`
as match-any — otherwise `--cors '*'` would silently void both browser
gates.

### WebSocket

A valid key wins on `/ws` exactly as on HTTP: `requireAuth` runs before the
upgrade, and the upgrader's `CheckOrigin` is only consulted for requests
that passed via the loopback exemption, where it applies the same Origin
predicate as rule 2. Browser dials need no URL change — the
`SameSite=Strict` cookie rides the same-origin WS handshake automatically;
the `access_key` query parameter exists for non-browser WS clients. When a
dial is rejected, `ws.js` re-runs the auth probe instead of blind
reconnecting, so an expired/cleared cookie funnels back into the key prompt.

### Key resolution

Precedence, first non-empty wins:

1. `-access-key` flag on `octo serve`
2. `OCTO_ACCESS_KEY` environment variable
3. `access_key` in `~/.octo/config.yml` (field already exists)
4. Auto-generated: 32 bytes from `crypto/rand`, hex-encoded, **persisted to
   `config.yml`** via `config.Save()` (0600) on first start so the key is
   stable across restarts — in particular across the self-restart
   supervisor's worker respawns, which would otherwise invalidate every
   logged-in browser on each restart. `server.New` therefore writes
   `~/.octo/config.yml` on a first start with no configured key; if the
   save fails, the generated key is kept for the process lifetime and a
   warning is printed (a fresh key is generated on next start).

Generation happens on any start where no key is configured, regardless of
bind address, so a later switch to a non-loopback bind doesn't change the
key. `resolveAccessKey` returns `(key, generated)`; only `server.New`
persists on `generated` — test constructors resolve keys in-memory and
never write the user's config.

### Startup UX

- Loopback bind: unchanged output (`octo server listening on http://…`).
- Non-loopback bind: additionally prints the key and a ready-to-open URL.
  For a wildcard bind (empty host, `0.0.0.0`, `::`), the URL uses the
  machine's primary non-loopback interface address; when none is found it
  falls back to a `<host>` placeholder:

  ```
  octo server listening on http://0.0.0.0:8080
  access key required for non-localhost clients
  open: http://192.168.1.5:8080/?access_key=<key>
  ```

### Web UI (`auth.js`)

The browser authenticates **via the cookie** — the frontend has ~74 raw
`fetch()` call sites and no wrapper, so per-request headers are not the
transport. `auth.js` provides:

- On load, probe `GET /api/sessions?limit=1`. Loopback browsers get `200`
  and never see a prompt.
- On `401`, prompt for the key (password-type modal, three attempts); on
  success store it in `localStorage` and seed a `SameSite=Strict` cookie
  (`Secure` on https). The cookie then authenticates every fetch and the WS
  handshake; `localStorage` re-seeds the cookie when it's cleared.
- A `?access_key=` query parameter on the page URL is adopted into storage
  and stripped via `history.replaceState`, so the bootstrap link's key does
  not linger in the address bar or history. The server never reads the
  query parameter on static routes — adoption is purely client-side.
- `getHeaders()` returns `Authorization: Bearer` for the few callers that
  want explicit headers; non-browser clients use the same header directly.

### Unauthenticated surface

- `GET /api/health`, `GET /api/version` — liveness/version probes; responses
  carry no secrets.
- Static files (`/`) — the UI shell is public; all data sits behind the API.

Everything else, including `POST /api/chat`, `POST /api/restart`, and
`GET /ws`, is behind `requireAuth`.

### Default bind change

`octo serve -addr` defaults to `127.0.0.1:8080` (was `:8080`, all
interfaces). **Breaking**: LAN/phone access now needs an explicit
`-addr :8080`, which in turn activates the key requirement for those
clients. Changelog calls out both.

The previous exposure gate — `validateBindAddr` requiring
`~/.octo/permissions.yml` to exist before a non-localhost bind — is
retired. It was a weak proxy (file existence, not content) with a hole: the
empty-host form `:8080` bypassed it entirely. The access key replaces it as
the exposure gate.

### Onboarding over a non-loopback bind

The server can start unconfigured (nil sender) for web onboarding. The
access key is resolved and persisted before that, and the onboarding API
routes sit behind `requireAuth` like everything else, so a remote first run
hits the key prompt **before** onboarding. The printed bootstrap URL is the
intended distribution channel for that flow.

### Interaction with `--cors`

The hosts named in `--cors` origins are accepted by the Host/Origin auth
predicates above (never `*`; see request classification), so a deliberate
cross-origin deployment keeps working — its requests still need the key
unless they arrive from loopback. To make that deployment actually
workable, `corsMiddleware` additionally allows the `Authorization` and
`X-Access-Key` request headers and the `PATCH`/`PUT` methods in its
preflight response; the `SameSite=Strict` cookie never crosses origins, so
a separate-origin frontend authenticates with explicit headers.

## Tests

- `requireAuth` matrix (httptest, crafted `RemoteAddr`):
  - loopback, no key, local Host, no Origin → 200
  - loopback, foreign `Origin` → 403 (CSRF)
  - loopback, `Origin: null` → 403
  - loopback, foreign `Host` → 403 (DNS rebinding)
  - non-loopback, no key → 401
  - non-loopback, valid key via each of Bearer / `X-Access-Key` / cookie →
    200; via query parameter → 200 on `/ws`, 401 elsewhere
  - any source, wrong key → 401
  - `X-Forwarded-For: 127.0.0.1` from non-loopback, no key → 401
  - `--cors '*'` configured: foreign Origin from loopback still 403
- Route-coverage assertion: every registered `/api/*` mux entry except
  `/api/health` and `/api/version` returns 401 to a keyless non-loopback
  request — a future route that forgets the `requireAuth` wrapper fails the
  test.
- `resolveAccessKey` precedence; first-start persistence to config.yml;
  save-failure fallback (in-memory key, no crash).
- WS `CheckOrigin` predicate unit tests; key-authenticated WS dial with a
  foreign Origin succeeds (precedence consistency).
- `octo serve` default-addr regression test; `validateBindAddr` retirement.

## Rollout

- `SECURITY.md` (repo root): public threat-model statement — boundary
  (single user; loopback trusted), what the key does and doesn't protect,
  TLS guidance, reporting contact.
- CHANGELOG: two breaking entries (default bind, non-loopback auth) plus
  the permissions.yml bind-gate retirement.
- `docs/` serve page: document `-access-key`, `OCTO_ACCESS_KEY`,
  `access_key`, and the exposure workflow.
