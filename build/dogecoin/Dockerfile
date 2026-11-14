FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates=20250419~deb12u2 curl=7.88.1-10+deb12u15 && rm -rf /var/lib/apt/lists/* && \
    case "$TARGETARCH" in \
      amd64) archive=x86_64-linux-gnu; sha=4f227117b411a7c98622c970986e27bcfc3f547a72bef65e7d9e82989175d4f8 ;; \
      arm64) archive=aarch64-linux-gnu; sha=6928c895a20d0bcb6d5c7dcec753d35c884a471aaf8ad4242a89a96acb4f2985 ;; \
      *) exit 1 ;; \
    esac && \
    curl -fsSLo /tmp/node.tar.gz "https://github.com/dogecoin/dogecoin/releases/download/v1.14.9/dogecoin-1.14.9-${archive}.tar.gz" && \
    printf '%s  %s\n' "$sha" /tmp/node.tar.gz | sha256sum -c - && \
    tar -xzf /tmp/node.tar.gz -C /tmp && install -m 0755 /tmp/dogecoin-1.14.9/bin/dogecoind /tmp/dogecoin-1.14.9/bin/dogecoin-cli /usr/local/bin/ && rm -rf /tmp/*
COPY --chmod=0755 entrypoint.sh healthcheck.sh /usr/local/bin/
RUN mkdir /data && chown 1000:1000 /data
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
