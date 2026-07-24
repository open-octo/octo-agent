#!/usr/bin/env sh
# Build octo-relay for linux/amd64 and push it to a relay VM over ssh.
#
#   ./deploy.sh <ssh-host> [version]
#
# <ssh-host> is any ssh destination (e.g. relay1.octo.dev or an alias from
# ~/.ssh/config with the right user/key). [version] defaults to the current
# git describe. First-time VM provisioning is in dev-docs/relay-runbook.md;
# this script only ships a new binary and restarts the service.
set -eu

HOST=${1:?usage: deploy.sh <ssh-host> [version]}
VERSION=${2:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}

cd "$(dirname "$0")/.."

OUT=$(mktemp -t octo-relay.XXXXXX)
trap 'rm -f "${OUT}"' EXIT

echo "==> building octo-relay ${VERSION} (linux/amd64)"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "${OUT}" .

# Upload into the ssh user's home, not a world-writable /tmp path — a fixed
# /tmp name could be pre-created by another local user and swapped between
# the scp and the root install.
echo "==> shipping to ${HOST}"
scp "${OUT}" "${HOST}:octo-relay.upload"
ssh "${HOST}" 'sudo install -m 0755 ~/octo-relay.upload /usr/local/bin/octo-relay && sudo systemctl restart octo-relay && rm ~/octo-relay.upload'

# Probe assumes the production shape (RELAY_ADDR=:443 + TLS); -k because we
# self-connect via 127.0.0.1 so the domain cert won't match. Poll a few
# seconds: systemd restart + TLS load isn't instant.
echo "==> verifying /healthz"
ssh "${HOST}" 'for i in 1 2 3 4 5; do curl -fsSk https://127.0.0.1/healthz && exit 0; sleep 1; done; echo "healthz never came up" >&2; exit 1'
echo "==> done"
