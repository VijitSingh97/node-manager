FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc AS base
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates=20250419~deb12u2 curl=7.88.1-10+deb12u15 libatomic1=12.2.0-14+deb12u1 && rm -rf /var/lib/apt/lists/*
COPY --chmod=0755 entrypoint.sh healthcheck.sh /usr/local/bin/
RUN mkdir /data && chown 1000:1000 /data
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

FROM base AS litecoin
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      amd64) archive=x86_64-linux-gnu; sha=43200c9f9d65ebc126ea5833ca9429e144c4b3273da6bb9f4e89fd7450ab1be9 ;; \
      arm64) archive=aarch64-linux-gnu; sha=129f55defb9045d5635566382c7b9f6fd227d4106a6fc07847a38b25ff9b29a1 ;; \
      *) exit 1 ;; \
    esac && \
    curl -fsSLo /tmp/node.tar.gz "https://github.com/litecoin-project/litecoin/releases/download/v0.21.5.8/litecoin-0.21.5.8-${archive}.tar.gz" && \
    printf '%s  %s\n' "$sha" /tmp/node.tar.gz >/tmp/node.sha256 && sha256sum -c /tmp/node.sha256 && \
    tar -xzf /tmp/node.tar.gz -C /tmp && install -m 0755 /tmp/litecoin-0.21.5.8/bin/litecoind /tmp/litecoin-0.21.5.8/bin/litecoin-cli /usr/local/bin/ && rm -rf /tmp/*
ENV NODE_BIN=litecoin NODE_ENV_PREFIX=LITECOIN
USER 1000:1000

FROM base AS dogecoin
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      amd64) archive=x86_64-linux-gnu; sha=4f227117b411a7c98622c970986e27bcfc3f547a72bef65e7d9e82989175d4f8 ;; \
      arm64) archive=aarch64-linux-gnu; sha=6928c895a20d0bcb6d5c7dcec753d35c884a471aaf8ad4242a89a96acb4f2985 ;; \
      *) exit 1 ;; \
    esac && \
    curl -fsSLo /tmp/node.tar.gz "https://github.com/dogecoin/dogecoin/releases/download/v1.14.9/dogecoin-1.14.9-${archive}.tar.gz" && \
    printf '%s  %s\n' "$sha" /tmp/node.tar.gz >/tmp/node.sha256 && sha256sum -c /tmp/node.sha256 && \
    tar -xzf /tmp/node.tar.gz -C /tmp && install -m 0755 /tmp/dogecoin-1.14.9/bin/dogecoind /tmp/dogecoin-1.14.9/bin/dogecoin-cli /usr/local/bin/ && rm -rf /tmp/*
ENV NODE_BIN=dogecoin NODE_ENV_PREFIX=DOGECOIN
USER 1000:1000

FROM base AS bitcoincash
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      amd64) archive=x86_64-linux-gnu; sha=23bbe9ff749136a9b832a2a59023ce76a5391322c1ea2f4f24b5b91f035081bc ;; \
      arm64) archive=aarch64-linux-gnu; sha=27e177e5a07dc78c2c0d18798ea2e3925111955509ecdc476e828e7ce8f46383 ;; \
      *) exit 1 ;; \
    esac && \
    curl -fsSLo /tmp/node.tar.gz "https://github.com/bitcoin-cash-node/bitcoin-cash-node/releases/download/v29.2.0/bitcoin-cash-node-29.2.0-${archive}.tar.gz" && \
    printf '%s  %s\n' "$sha" /tmp/node.tar.gz >/tmp/node.sha256 && sha256sum -c /tmp/node.sha256 && \
    tar -xzf /tmp/node.tar.gz -C /tmp && install -m 0755 /tmp/bitcoin-cash-node-29.2.0/bin/bitcoind /tmp/bitcoin-cash-node-29.2.0/bin/bitcoin-cli /usr/local/bin/ && rm -rf /tmp/*
ENV NODE_BIN=bitcoin NODE_ENV_PREFIX=BITCOINCASH
USER 1000:1000
