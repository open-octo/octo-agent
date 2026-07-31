# dl.octo-agent.dev — release download mirror

A Cloudflare Worker that proxies this repository's GitHub release assets so
downloads and upgrades work from networks where github.com is blocked
(notably mainland China). Cloudflare's edge is reachable there without a
proxy; GitHub is fetched from the edge, never from the client.

`internal/upgrade` and `landing/install.sh` / `install.ps1` try GitHub first
and fall back to this host (then to public gh-proxy mirrors). Keep the three
mirror lists in sync when anything changes here.

## Endpoints

| Path | Behavior |
|---|---|
| `/releases/latest` | 302 passthrough; the release tag is in the `Location` path |
| `/releases/download/<tag>/<asset>` | streams the asset; edge-cached (immutable) |
| `/releases/latest/download/<asset>` | streams the asset; uncached |
| `/install.sh`, `/install.ps1` | installer scripts from `main` — the China-reachable install one-liner |
| anything else | redirect to the landing page |

## Deploy

One-time setup:

1. Add the `octo-agent.dev` zone to the Cloudflare account (change the
   nameservers at the registrar). The rest of the site can stay where it is —
   only the `dl` subdomain is claimed here, via the worker's custom domain.
2. `npx wrangler login`

Then, from this directory:

```sh
npx wrangler deploy
```

`custom_domain = true` in `wrangler.toml` creates the `dl.octo-agent.dev`
DNS record automatically. There is no CI deploy: the worker changes rarely,
and a manual `wrangler deploy` keeps the account credentials out of GitHub.

## Verify

```sh
# tag in the Location header
curl -sI https://dl.octo-agent.dev/releases/latest | grep -i location
# a real asset downloads and matches GitHub's checksum
curl -fsSL https://dl.octo-agent.dev/releases/latest/download/checksums.txt | head
```

## Limits

Workers free plan: 100k requests/day, no egress charge. Asset responses are
cached at the edge (Cloudflare's cache serves up to 512 MB objects), so
repeat downloads of the same release mostly don't re-hit GitHub or count
against upstream bandwidth.
