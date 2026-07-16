---
title: Security model
description: Where the boundary sits, what protects it, and what's deliberately out of scope.
---

octo is a **single-user, personal agent**. Its server (`octo serve`) executes arbitrary shell
commands on behalf of its one trusted operator — that is the product, not a vulnerability. This
page states where the security boundary sits, what protects it, and what's deliberately out of
scope.

## The boundary

- **One user.** No accounts or roles. Whoever holds the access key (or sits at the machine) has
  full control, including command execution via chat.
- **Loopback is trusted.** Requests from `127.0.0.1`/`::1` are exempt from key authentication —
  anything that can already run code as a local user is outside the boundary anyway.
- **Everything else needs the key.** `octo serve` binds to `127.0.0.1:8088` by default; binding
  wider (`-addr :8088`) makes every API and WebSocket request from a non-loopback client require
  the [access key](#the-access-key).

## What's defended

| Threat | Defense |
|---|---|
| LAN / internet attacker calling an exposed API | Loopback-only default bind; 256-bit access key (constant-time compare) on every non-loopback request |
| Malicious website CSRF-ing `http://localhost:8088` from your browser | `Origin` must be local or `--cors`-allowlisted (a literal `*` is never honored); auth cookie is `SameSite=Strict` |
| DNS rebinding (attacker domain resolving to 127.0.0.1) | The loopback exemption requires a local `Host` header |
| Spoofed client IPs | `X-Forwarded-For` is never consulted for the loopback exemption |
| XSSI reads of uploaded files | `X-Content-Type-Options: nosniff` on served uploads |
| Agent wiping the OS with a destructive command | Permission engine with hardcoded `deny` rules for `rm -rf /`, `dd`, `mkfs`, `fdisk`, `format`, `shutdown`, `reboot`, `kill -9 -1`, system-directory removal (`rm -rf /usr`, `rmdir /s /q C:\Windows` and every other deletion spelling targeting `C:\Windows` / `C:\ProgramData` / `C:\Program Files`, including the PowerShell `ri`/`del`/`erase` aliases), plus `diskpart`, `bcdedit`, `reg delete`, `vssadmin delete`, `wbadmin delete`; these cannot be overridden by `~/.octo/permissions.yml` |
| Agent writing to system directories | `write_file`/`edit_file` hardcoded `deny` for `/bin`, `/sbin`, `/usr`, `/System`, `/boot`, `/lib`, `/Windows`, `C:/Windows`, `C:/Windows/System32`, `C:/Windows/SysWOW64`, `C:/ProgramData`, etc. across Unix, macOS, and Windows |
| Agent exfiltrating data or opening reverse shells | Default `ask` rules for `curl`, `wget`, `ssh`, `scp`, `nc`, `socat`, `nmap`, `systemctl`, `iptables`, `crontab`, etc. |
| Agent reconfiguring the OS via Windows management tools | Default `ask` rules for `wmic`, `sc`, `schtasks`, `netsh`, `icacls`, `takeown`, `robocopy`, `xcopy`, `mklink`, `dism`, `fsutil`, `powershell`, `cmd /c`, and untargeted recursive deletes (`rmdir /s /q`, `del /s /q` — the cmd.exe counterpart of `rm -rf`) |
| After-the-fact investigation | Append-only JSON audit log at `~/.octo/audit.log` recording every deny, ask-denied, user-declined, and user-allowed tool decision |

IM channels (Feishu, DingTalk, Discord, …) authenticate separately via each platform's own bot
credentials plus octo's chat/user binding; adapters hold outbound connections only and expose no
inbound HTTP routes.

## The permission engine

octo evaluates every tool call against a rule-driven permission engine. The default rules live in
the binary and are supplemented by `~/.octo/permissions.yml` if present. User rules can relax or
tighten policy, but **hardcoded OS-destruction guards cannot be overridden**: commands like `rm -rf /`,
`dd if=/dev/zero of=/dev/sda`, `mkfs`, `fdisk`, `shutdown`, `reboot`, and `kill -9 -1` are always
denied, and writing to system directories is always blocked. This prevents a misconfigured
permissions file (or an LLM that persuades you to edit it) from silently disabling the guardrails.

Command guards are anchored to the command word itself — they also fire through `sudo`/`env`/`xargs`
wrappers, shell chaining (`;`, `&&`), and path invocations like `/sbin/reboot`, but they do **not**
fire on incidental text inside arguments: `git commit -m "fix shutdown handling"` and
`docker ps --format json` pass through untouched.

The engine operates in three modes:
- `interactive` (default in CLI): ask-class decisions prompt the user for confirmation.
- `auto`: ask-class decisions are automatically approved — convenient, but use with care.
- `strict`: ask-class decisions are denied with no prompt — safest for unattended/cron/IM use.

## Audit log

Every non-`allow` permission decision is appended to `~/.octo/audit.log` as a single JSON line:

```json
{"ts":"2026-07-16T14:12:00.000000000Z","tool":"terminal","input":{"command":"rm -rf /"},"decision":"deny","reason":"permission_denied: terminal matched deny rule (pattern: \"rm -rf /\"). This operation is blocked by policy."}
```

Recorded decisions include `deny`, `ask-denied` (ask-class verdict in non-interactive mode),
`user-declined`, and `user-allowed` (ask-class verdict where the user clicked yes). String values in
the recorded input are truncated to 1 KiB — the log answers "what was denied and why", it does not
archive file contents or command payloads. The log is created with mode `0600`; on startup it is
rotated once it exceeds 10 MiB (keeping 3 generations), and never truncated mid-run.

## What's not defended, by design

- **Hostile local processes.** Loopback traffic is trusted; malware running as any local user on
  the same machine can reach the API. A shared or compromised machine is outside what the access
  key can help with.
- **Plaintext transport.** The key travels in cookies/headers over HTTP. On untrusted networks,
  terminate TLS in front (a reverse proxy, `tailscale serve`) or tunnel.
- **Brute force.** No lockout or rate limiting — the key is 256 bits of `crypto/rand`, so online
  guessing isn't viable regardless.

## The access key

Resolution order: `octo serve --access-key` flag → `OCTO_ACCESS_KEY` env var → `access_key` in
`~/.octo/config.yml` → auto-generated on first start and persisted (mode `0600`). When the bind
address isn't loopback-only, startup prints a ready-to-open URL with the key embedded; the Web UI
stores it and strips it from the address bar.

Clients present it as `Authorization: Bearer <key>`, `X-Access-Key`, or the `octo_access_key`
cookie (the `access_key` query parameter works on `/ws` only). `GET /api/health` and
`GET /api/version` are the only unauthenticated routes, and carry no secrets.

To rotate: edit `access_key` in `config.yml` and restart (or `POST /api/restart`).

## The update channel

`octo upgrade` (and the Web UI's upgrade button) verifies the downloaded archive's SHA-256 against
the release's `checksums.txt`, both fetched over GitHub's TLS. That catches transport corruption and
mirror tampering — **not** a compromised GitHub account publishing a malicious release; there's no
signature layer yet. The version *check* behind the web badge is automatic; the *install* never is,
and sits behind access-key auth like every other mutating endpoint.

## Reporting a vulnerability

Open a [GitHub security advisory](https://github.com/open-octo/octo-agent/security/advisories/new)
or email the maintainer privately — please don't file public issues for exploitable bugs.
