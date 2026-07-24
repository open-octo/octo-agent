# Security

octo is a **single-user, personal agent**. Its server (`octo serve`) executes
arbitrary shell commands on behalf of its one trusted operator — that is the
product, not a vulnerability. This document states where the security
boundary sits, what protects it, and what is deliberately out of scope.

## Threat model

### The boundary

- **One user.** There are no accounts or roles. Whoever holds the access key
  (or sits at the machine) has full control, including command execution via
  chat.
- **Loopback is trusted.** Requests from `127.0.0.1`/`::1` are exempt from
  key authentication. Anything that can already run code as a local user on
  the machine is outside the boundary.
- **Everything else needs the key.** The server binds to `127.0.0.1:8088` by
  default; binding wider (`-addr :8088`) makes every API and WebSocket
  request from a non-loopback client require the access key.

### What is defended

| Threat | Defense |
|---|---|
| LAN / internet attacker calling an exposed API | Loopback-only default bind; 256-bit access key (constant-time compare) on every non-loopback request |
| Malicious website CSRF-ing `http://localhost:8088` from your own browser | Browser-sent `Origin` must be local or `--cors`-allowlisted (a literal `*` is never honored); the auth cookie is `SameSite=Strict` |
| DNS rebinding (attacker domain resolving to 127.0.0.1) | The loopback exemption requires a local `Host` header |
| Spoofed client IPs | `X-Forwarded-For` is never consulted for the loopback exemption |
| XSSI reads of uploaded files | `X-Content-Type-Options: nosniff` on served uploads |

IM channels (Feishu, DingTalk, Discord, …) authenticate separately via each
platform's bot credentials plus octo's chat/user binding; the adapters hold
outbound connections only and expose no inbound HTTP routes.

### What is not defended (by design)

- **Hostile local processes.** Loopback traffic is trusted; malware running
  as any local user on the same machine can reach the API. If your machine
  is shared or compromised, the access key does not help you.
- **Plaintext transport.** The key travels in cookies/headers over HTTP.
  On untrusted networks, terminate TLS in front (reverse proxy, `tailscale
  serve`) or tunnel.
- **Brute force.** No lockout or rate limiting: the key is 256 bits of
  `crypto/rand`; online guessing is not viable.

## The access key

Resolution precedence: `octo serve -access-key` flag → `OCTO_ACCESS_KEY`
env var → `access_key` in `~/.octo/config.yml` → auto-generated on first
start and persisted to `config.yml` (mode 0600). When the bind address is
not loopback-only, startup prints a ready-to-open URL embedding the key;
the web UI stores it and strips it from the address bar.

Clients present the key as `Authorization: Bearer <key>`, `X-Access-Key`,
or the `octo_access_key` cookie (the `access_key` query parameter is
accepted on `/ws` only). `GET /api/health` and `GET /api/version` are the
only unauthenticated API routes; they carry no secrets.

To rotate the key: edit `access_key` in `~/.octo/config.yml` and restart
(or `POST /api/restart`).

## The update channel

`octo upgrade` (and the web UI's upgrade button) installs the latest GitHub
release after verifying the archive's SHA-256 against the same release's
`checksums.txt`. Both are fetched over GitHub's TLS — the verification
catches transport corruption and mirror tampering, **not** a compromised
GitHub account publishing a malicious release; there is no signature layer
yet. Upgrades run only on explicit user action (the version *check* behind
the web badge is automatic; the install never is), and the web trigger
sits behind the access-key auth like every other mutating endpoint.

## Code signing policy

octo's Windows installer (`octo-setup.exe`) is signed through the free
open-source program of the [SignPath Foundation](https://signpath.org/),
which issues the certificate; the signing key is held by SignPath and never
leaves their infrastructure. The macOS installer (`octo-setup.pkg`) isn't
signed or notarized yet — there's no Apple Developer Program membership behind
this project — so Gatekeeper's unidentified-developer warning is expected for
every release until that changes.

- **What is signed:** the `octo-setup.exe` installer attached to each GitHub
  release. The release archives themselves are integrity-checked via the
  `checksums.txt` published alongside them.
- **How:** the installer is built in CI (`.github/workflows/release.yml`) and
  submitted to SignPath for signing as part of the release. Every signing
  request is approved by a project maintainer before it is signed.
- **Who can approve:** the repository maintainers. Anyone with signing-request
  approval or repository write access uses multi-factor authentication on both
  GitHub and SignPath.
- **Verifying:** right-click the installer → *Properties → Digital Signatures*
  to confirm the publisher.

Signing is being rolled out via the SignPath Foundation; until the certificate
is provisioned, released installers may be **unsigned**, in which case Windows
SmartScreen shows "Windows protected your PC" — click **More info → Run anyway**.

## Reporting a vulnerability

Open a [GitHub security advisory](https://github.com/open-octo/octo-agent/security/advisories/new)
or email the maintainer privately. Please do not file public issues for
exploitable bugs.
