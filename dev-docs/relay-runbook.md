# octo-relay runbook (M1a single node · M1b multi-node)

How to provision, operate, and verify a hosted octo-relay node. The relay is a
stateless dumb pipe (see `dev-docs/mobile-managed-tunnel-design.md`): it holds
no persistent data, terminates TLS, and bridges end-to-end-encrypted frames it
cannot read. Everything below is reproducible from scratch; losing the VM
loses nothing but uptime.

## Topology (M1a)

```
DNS: relay.octo.dev ──► 1 VM (octo-relay, systemd, TLS at the relay)
```

Multi-node SNI routing behind an L4 LB is M1b; this runbook covers the single
node it grows out of.

## Provision a node

1. **VM**: any small Linux VM (1 vCPU / 512 MB is plenty — the relay only
   copies frames). Open inbound 443 (and 80 if using certbot's HTTP-01).
2. **DNS**: point `relay.octo.dev` (A/AAAA) at the VM.
3. **User**:
   ```sh
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin octo-relay
   ```
4. **Certificate** (single domain, Let's Encrypt HTTP-01):
   ```sh
   sudo certbot certonly --standalone -d relay.octo.dev
   ```
   Standalone HTTP-01 binds :80 only, and the relay binds :443 only, so
   issuance and renewals never conflict with a running relay. Give the
   service user read access:
   ```sh
   sudo groupadd -f octo-certs
   sudo usermod -aG octo-certs octo-relay
   sudo chgrp -R octo-certs /etc/letsencrypt/live /etc/letsencrypt/archive
   sudo chmod -R g+rX /etc/letsencrypt/live /etc/letsencrypt/archive
   ```
5. **Renewal hook** — restart the relay when the cert renews:
   ```sh
   sudo tee /etc/letsencrypt/renewal-hooks/deploy/octo-relay <<'EOF'
   #!/bin/sh
   systemctl restart octo-relay
   EOF
   sudo chmod +x /etc/letsencrypt/renewal-hooks/deploy/octo-relay
   ```
6. **Config + unit** (files in `cmd/octo-relay/deploy/`):
   ```sh
   sudo mkdir -p /etc/octo-relay
   sudo cp deploy/env.example /etc/octo-relay/env   # then edit paths if needed
   sudo cp deploy/octo-relay.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now octo-relay
   ```
7. **Binary**: from a checkout, `cmd/octo-relay/deploy/deploy.sh <ssh-host>`
   cross-builds linux/amd64, ships it, restarts the service, and curls
   `/healthz`.

## Verify

- **Liveness + version**: `curl https://relay.octo.dev/healthz` → `ok <version>`.
- **End to end**: on any machine, `octo serve --tunnel` (the default relay is
  already `wss://relay.octo.dev`), pair a phone via the QR, send a message.
- **Self-heal**: `sudo systemctl kill octo-relay` — systemd restarts it within
  seconds (`Restart=always`); both tunnel ends reconnect on their own.

## Operate

- **Logs**: `journalctl -u octo-relay -f`. The relay logs connection lifecycle
  only — never payloads, tokens, or anything content-derived. Keep it that way.
- **Upgrade**: rerun `deploy.sh`. In-flight tunnels drop on restart and both
  ends reconnect; no drain needed at this stage.
- **State**: none. Pairing tokens and connections live only in process memory.
  A restart invalidates unredeemed pairing QRs (the host re-offers its tokens
  on reconnect automatically).

## Scaling out (M1b: multi-node SNI routing)

Clients dial `wss://<tunnelid>.relay.octo.dev` (the host's Go tunnel and both
native mobile plugins apply the same rule: DNS-named relay → tunnel-id
subdomain; IP/dotless hosts dial unchanged). An L4 balancer consistent-hashes
the TLS SNI, so both ends of a tunnel always land on the same node and nodes
never talk to each other.

```
DNS: relay.octo.dev + *.relay.octo.dev ─► HAProxy (SNI passthrough + consistent hash)
                                              │
                                     ┌────────┴────────┐
                                  relay VM1         relay VM2   (TLS terminates HERE)
```

1. **Wildcard cert** on every relay node: `*.relay.octo.dev` requires DNS-01
   (HTTP-01 can't issue wildcards):
   ```sh
   sudo certbot certonly --preferred-challenges dns \
     --dns-<provider> -d 'relay.octo.dev' -d '*.relay.octo.dev'
   ```
   Pick the certbot DNS plugin for wherever the zone is hosted; renewal is
   automatic via the same plugin. Update `/etc/octo-relay/env` to the new
   `live/relay.octo.dev*` paths if certbot picked a new lineage name.
2. **Balancer**: `cmd/octo-relay/deploy/haproxy.cfg` — TCP mode, TLS
   passthrough (`req.ssl_sni` from the ClientHello, never decrypted),
   `balance hash req.ssl_sni` + `hash-type consistent` (HAProxy ≥ 2.2). List
   every relay node as a `server` line; adding/removing a node reshuffles
   ~1/N tunnels, and moved tunnels reconnect on their own.
3. **DNS**: move `relay.octo.dev` **and** `*.relay.octo.dev` to the balancer.
4. **Relay nodes**: nothing special — each node is the M1a setup with the
   wildcard cert. Nodes hold only in-memory state, so any assignment works
   as long as both ends of a tunnel agree (that's the SNI hash's job).
5. **Compatibility**: the relay resolves the tunnel from `?tunnel=` first and
   falls back to the Host's first DNS label, so pre-M1b clients (query-only)
   keep working on a single node. They can't span multiple nodes — the LB
   would hash `relay.octo.dev` for all of them onto one node, which is also
   harmless.

Verify: pair a phone, then `systemctl stop octo-relay` on the node serving it
— both ends reconnect and meet on a surviving node within seconds.

## Local development

No flags = plaintext on :8090, same as the PoC. The relay is a nested Go
module, so run it from its own directory (the parent module doesn't contain
it):

```sh
(cd cmd/octo-relay && go run . --addr :8090)
octo serve --tunnel --relay ws://127.0.0.1:8090
```

Setting exactly one of `--tls-cert`/`--tls-key` is a startup error by design —
a typo'd path must not silently downgrade a production node to plaintext.
