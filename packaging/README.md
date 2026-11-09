# Packaging

Service templates and OS packaging assets for running `octo serve` as a
long-lived daemon.

## Running octo serve as a service

`octo serve` writes structured logs (Go `slog`, text format) to **stderr**;
the level is set by `OCTO_LOG_LEVEL` (`debug` | `info` | `warn` | `error`,
default `info`). It does not manage log files itself — the service manager
captures stderr (systemd → the journal, launchd → `StandardErrorPath`).

Run it under your init system with `--no-supervisor`: the init system is the
supervisor. octo's own restart path (binary upgrade, startup-only config
change) exits the worker with code **42**; `Restart=always` (systemd) /
`KeepAlive` (launchd) respawn it from the on-disk binary, so the upgrade takes
effect on restart.

### Linux — systemd (user service)

[`systemd/octo.service`](systemd/octo.service):

```sh
cp packaging/systemd/octo.service ~/.config/systemd/user/octo.service
$EDITOR ~/.config/systemd/user/octo.service     # fix the ExecStart path
$EDITOR ~/.octo/serve.env && chmod 600 ~/.octo/serve.env   # API key, access key
systemctl --user daemon-reload
systemctl --user enable --now octo
journalctl --user -u octo -f
```

### macOS — launchd (user agent)

[`launchd/dev.octo-agent.serve.plist`](launchd/dev.octo-agent.serve.plist):

```sh
cp packaging/launchd/dev.octo-agent.serve.plist ~/Library/LaunchAgents/
$EDITOR ~/Library/LaunchAgents/dev.octo-agent.serve.plist   # fix path + env
launchctl load ~/Library/LaunchAgents/dev.octo-agent.serve.plist
tail -f /tmp/octo-serve.log
```

### Docker

[`docker/Dockerfile`](docker/Dockerfile) wraps a published linux release binary
(downloaded and checked against `checksums.txt`); the release workflow pushes it
to `ghcr.io/open-octo/octo-agent` for amd64 and arm64.
[`docker/compose.yaml`](docker/compose.yaml) runs it with the data root on a
volume:

```sh
cp packaging/docker/compose.yaml . && $EDITOR .env   # OCTO_PROVIDER, API key
docker compose up -d
docker compose logs octo     # access key is in the "open:" URL
```

The container binds `:8088`, so every request — even from the host's own
browser, which arrives via the Docker bridge — needs the access key. See the
self-host guide's "Running in Docker" section for volumes and upgrades.

> The systemd and launchd templates default to a loopback bind. Exposing the server on a LAN/public
> address requires `-addr :8088` (or similar) **and** an access key — see
> [SECURITY.md](../SECURITY.md).

## macOS installer

The double-click `octo-setup.pkg` is built from [`macos/`](macos/) by the
release workflow (`macos/build.sh` drives `pkgbuild` + `productbuild`). It
installs to `~/Library/Application Support/octo` and writes its own
`~/Library/LaunchAgents/dev.octo-agent.serve.plist` — if you already set up
the manual launchd template above under the same label, the installer
overwrites it. See the README's Install section.

## Windows installer

The double-click `octo-setup.exe` is built from
[`windows/octo.iss`](windows/octo.iss) by the release workflow. See the README's
Install section.
