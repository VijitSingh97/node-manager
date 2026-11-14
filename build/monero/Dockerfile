FROM debian:13.7-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2

ARG TARGETARCH
ARG MONERO_VERSION=0.18.5.4
ARG MONERO_AMD64_SHA256=ba2928962c101b609434bf8c1b43ad59ebe96c5525977284ca599d8bf0f02d10
ARG MONERO_ARM64_SHA256=d20d71e6522b41eb8e549c3425f7f4622c9c9a448fc7147525393fd0a6b59703

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
# Build tools follow Debian security updates instead of brittle exact versions.
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl bzip2 \
    && case "$TARGETARCH" in \
         amd64) archive="monero-linux-x64-v${MONERO_VERSION}.tar.bz2"; checksum="$MONERO_AMD64_SHA256" ;; \
         arm64) archive="monero-linux-armv8-v${MONERO_VERSION}.tar.bz2"; checksum="$MONERO_ARM64_SHA256" ;; \
         *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
       esac \
    && curl --fail --show-error --location --proto '=https' --tlsv1.2 --output "/tmp/$archive" "https://downloads.getmonero.org/cli/$archive" \
    && printf '%s  %s\n' "$checksum" "/tmp/$archive" | sha256sum --check --strict \
    && mkdir /tmp/monero \
    && tar -xjf "/tmp/$archive" -C /tmp/monero --strip-components=1 \
    && install -m 0755 /tmp/monero/monerod /tmp/monero/monero-wallet-rpc /usr/local/bin/ \
    && rm -rf /tmp/monero "/tmp/$archive" /var/lib/apt/lists/*

COPY --chmod=0755 entrypoint.sh /usr/local/bin/monero-entrypoint
COPY --chmod=0755 healthcheck.sh /usr/local/bin/monero-healthcheck
COPY --chmod=0755 wallet-entrypoint.sh /usr/local/bin/monero-wallet-entrypoint
COPY --chmod=0755 wallet-healthcheck.sh /usr/local/bin/monero-wallet-healthcheck
RUN mkdir -p /data /wallets \
    && chown 1000:1000 /data /wallets \
    && chmod 0700 /wallets

USER 1000:1000
ENTRYPOINT ["/usr/local/bin/monero-entrypoint"]
