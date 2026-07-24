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

echo "==> building octo-relay ${VERSION} (linux/amd64)"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /tmp/octo-relay .

echo "==> shipping to ${HOST}"
scp /tmp/octo-relay "${HOST}:/tmp/octo-relay"
ssh "${HOST}" 'sudo install -m 0755 /tmp/octo-relay /usr/local/bin/octo-relay && sudo systemctl restart octo-relay && rm /tmp/octo-relay'

echo "==> verifying /healthz"
# -k: self-connecting via 127.0.0.1, so the domain cert won't match the SNI.
ssh "${HOST}" 'sleep 1; curl -fsSk https://127.0.0.1/healthz'
echo "==> done"
