FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates=20250419~deb12u2 curl=7.88.1-10+deb12u15 libatomic1=12.2.0-14+deb12u1 && rm -rf /var/lib/apt/lists/* && \
    case "$TARGETARCH" in \
      amd64) archive=x86_64-linux-gnu; sha=23bbe9ff749136a9b832a2a59023ce76a5391322c1ea2f4f24b5b91f035081bc ;; \
      arm64) archive=aarch64-linux-gnu; sha=27e177e5a07dc78c2c0d18798ea2e3925111955509ecdc476e828e7ce8f46383 ;; \
      *) exit 1 ;; \
    esac && \
    curl -fsSLo /tmp/node.tar.gz "https://github.com/bitcoin-cash-node/bitcoin-cash-node/releases/download/v29.2.0/bitcoin-cash-node-29.2.0-${archive}.tar.gz" && \
    printf '%s  %s\n' "$sha" /tmp/node.tar.gz | sha256sum -c - && \
    tar -xzf /tmp/node.tar.gz -C /tmp && \
    install -m 0755 /tmp/bitcoin-cash-node-29.2.0/bin/bitcoind /tmp/bitcoin-cash-node-29.2.0/bin/bitcoin-cli /usr/local/bin/ && \
    rm -rf /tmp/*
COPY --chmod=0755 entrypoint.sh healthcheck.sh /usr/local/bin/
RUN mkdir /data && chown 1000:1000 /data
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
