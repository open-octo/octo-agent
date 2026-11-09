# octo serve in a container.
#
# The image wraps the published linux release binary rather than building from
# source: that binary already embeds the web UI and ripgrep, so the image runs
# exactly what install.sh would install. The release workflow builds and pushes
# this file to ghcr.io/open-octo/octo-agent for linux/amd64 and linux/arm64.
#
# Build locally (VERSION is a published release, without the leading "v"):
#   docker build --build-arg VERSION=1.17.22 -t octo packaging/docker
#
# See packaging/README.md for running it.

FROM debian:trixie-slim AS fetch

ARG VERSION
# Set by BuildKit for multi-platform builds; the legacy builder leaves it empty,
# in which case the build machine's own architecture is used. Debian's names
# (amd64, arm64) match the release archive names.
ARG TARGETARCH

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /dl
RUN set -eu; \
    test -n "${VERSION:-}" || { echo "VERSION build arg is required (e.g. --build-arg VERSION=1.17.22)" >&2; exit 1; }; \
    arch="${TARGETARCH:-$(dpkg --print-architecture)}"; \
    archive="octo_${VERSION}_linux_${arch}.tar.gz"; \
    base="https://github.com/open-octo/octo-agent/releases/download/v${VERSION}"; \
    curl -fsSLO --retry 3 "${base}/${archive}"; \
    curl -fsSLO --retry 3 "${base}/checksums.txt"; \
    grep " ${archive}\$" checksums.txt | sha256sum -c -; \
    tar -xzf "${archive}" octo

FROM debian:trixie-slim

# ca-certificates for TLS to model providers; git, curl and python3 so the
# agent's terminal tool has the everyday basics inside the container; tini as
# PID 1 reaps the processes the terminal tool leaves behind.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git python3 tini \
 && rm -rf /var/lib/apt/lists/*

COPY --from=fetch /dl/octo /usr/local/bin/octo

RUN useradd --create-home --uid 1000 --shell /bin/bash octo \
 && mkdir -p /home/octo/.octo /workspace \
 && chown octo:octo /home/octo/.octo /workspace

USER octo
ENV HOME=/home/octo
WORKDIR /workspace

# ~/.octo holds config, sessions, memory and the persisted access key.
VOLUME ["/home/octo/.octo"]
EXPOSE 8088

# --no-supervisor: the container runtime is the supervisor. A restart request
# (startup-only config change) exits 42, which a restart policy turns into a
# fresh start. -addr :8088 binds all interfaces so the published port reaches
# it; every request from outside the container then needs the access key.
ENTRYPOINT ["tini", "--", "octo"]
CMD ["serve", "--no-supervisor", "-addr", ":8088"]
