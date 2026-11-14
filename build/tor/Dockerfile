FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
# netcat is only used by the compose healthcheck
RUN apk add --no-cache tor=0.4.9.17-r0 netcat-openbsd=1.234.1-r0 socat=1.8.1.3-r0
COPY --chmod=0644 torrc /etc/tor/torrc
COPY --chmod=0755 entrypoint.sh /entrypoint.sh
# chown to tor:tor — the GROUP matters: the bitcoin container joins gid 101
# to read the control-auth cookie for the inbound onion service
RUN mkdir -p /var/lib/tor && chown -R tor:tor /var/lib/tor
USER 100:101
ENTRYPOINT ["/entrypoint.sh"]
