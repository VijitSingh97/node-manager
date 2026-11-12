FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
WORKDIR /src
COPY --chmod=0644 build/dashboard/go.mod build/dashboard/go.sum ./
RUN go mod download
COPY --chmod=0644 build/dashboard/*.go ./
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /dashboard .

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
WORKDIR /app
COPY --from=build --chmod=0755 /dashboard ./dashboard
COPY --from=build --chmod=0644 /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chmod=0644 build/dashboard/index.html ./index.html
COPY --chmod=0644 build/dashboard/login.html ./login.html
COPY --chmod=0755 build/dashboard/static ./static
COPY --chmod=0644 VERSION ./VERSION
COPY --chmod=0644 build/dashboard/THIRD_PARTY_NOTICES ./THIRD_PARTY_NOTICES
RUN find /app/static -type f -exec chmod 0644 {} + \
    && mkdir -p /state /tari-wallet \
    && chown 1000:1000 /state /tari-wallet
USER 1000:1000
EXPOSE 8000
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/dashboard"]
