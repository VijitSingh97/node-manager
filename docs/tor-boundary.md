# The Tor boundary

The rule: the application's own traffic leaves the host only through the Tor
container. Nodes dial peers through Tor's SOCKS port, and clearnet peers are
reached only from a Tor exit. The dashboard fetches prices, release metadata and
notifications over Tor with remote name resolution. No node publishes a P2P port
on the host. LAN RPC is an explicit opt-in listener on the Tor container, which is
inbound, not internet egress. [Tor-only networking](privacy.md) describes the
design; this page lists every egress path found in the repository, how each one
stays on Tor, and which test fails when it stops doing so.

Evidence below is `file:line` at the commit that introduced this page.

## How the boundary is enforced

1. Every service except `tor` joins only `btc_net`, which is `internal: true`
   (`docker-compose.yml:532-535`). Docker gives an internal network no gateway, so
   a direct TCP, UDP or ICMP packet to a public address has no route.
2. `tor` is the only service on `tor_egress`, the only network with an external
   route (`docker-compose.yml:43-48`, `539-540`).
3. Every service except `tor` sets `dns: [127.0.0.1]`, so Docker's embedded
   resolver has no upstream to forward public names to (`docker-compose.yml:119`,
   `157`, `199`, `236`, `273`, `311`, `348`, `385`, `529`; `docker-compose.tari-wallet.yml:48`).
   Names that must resolve (`tari`, for example) are served by Docker's own
   service-name DNS, not the upstream.
4. Each node is configured to dial through `172.29.0.25:9050` (the Tor container)
   rather than relying only on layer 1. If layer 1 failed, the node still has no
   configured direct route.

Layers 1 and 3 are the hard boundary. Layer 4 is why nothing is even attempted.

## Egress path inventory

| Path | Mechanism | Verdict |
|---|---|---|
| Compose networks | Two networks only, `btc_net` internal, `tor_egress` external (`docker-compose.yml:532-540`). | Tor only |
| Published host ports | Base file publishes only the Tor relay to the dashboard on `DASHBOARD_BIND_IP`, loopback by default (`docker-compose.yml:49-52`). Eight overlays add one LAN listener each on `tor`, each with a required bind IP and no default (`docker-compose.rpc.yml:4`, `monero-rpc.yml:4`, `monero-zmq.yml:4`, `tari-grpc.yml:4`, `litecoin-rpc.yml:4`, `dogecoin-rpc.yml:4`, `bitcoincash-rpc.yml:4`, `zcash-rpc.yml:4`). No node publishes a port. | Inbound opt-ins only |
| `extra_hosts`, `network_mode`, `privileged`, host `pid`/`ipc`, Docker socket | None in any compose file. | None |
| Proxy environment variables | None. The only proxy variable is `TOR_PROXY`, always `172.29.0.25:9050` (`docker-compose.yml:141`, `259`, `297`, `344`). | Tor only |
| Tor container | `SocksPort 0.0.0.0:9050`, no `ControlPort`, `ORPort`, `ExitRelay`, `TransPort` or `DNSPort` (`build/tor/torrc:1-3`). Socat relays end at stack addresses or at Tor's own SOCKS port (`build/tor/entrypoint.sh:46-82`). | Tor only |
| Bitcoin Core | `-proxy` and `-onion` both `172.29.0.25:9050`; `-onlynet=onion` when clearnet-over-Tor is off; inbound only through an onion bind with `-listenonion=0` (`docker-compose.yml:98`, `102-104`); `listen=0` by default (`build/bitcoin/bitcoin.conf:14`). Upstream Core resolves DNS seeds through a set proxy (not tested here). | Tor only |
| Litecoin, Dogecoin, Bitcoin Cash | `proxy=` and `onion=` set to Tor, `discover=0`, `listen=0` unless an onion bind exists, `onlynet=onion` plus `dnsseed=0` when onion-only (`build/bitcoin-family/entrypoint.sh:58-79`). | Tor only |
| Dogecoin seed lookup | DNS-over-HTTPS to `cloudflare-dns.com` through `curl --socks5-hostname "$TOR_PROXY"`, HTTPS only, `--noproxy ''` (`build/bitcoin-family/entrypoint.sh:23-48`). | Tor only |
| Monero and its wallet | `proxy=socks5://$TOR_PROXY`, `tx-proxy=tor,...`, `in-peers=0`, `hide-my-port=1`, DNS checkpoints and update checks off (`build/monero/entrypoint.sh:16-23`). The wallet's only daemon is the internal node (`build/monero/wallet-entrypoint.sh:15`). | Tor only (see gap 2) |
| Tari node | Transport `tcp_tor`, or `tor` when onion-only; `proxy_bypass_for_outbound_tcp = false`, empty bypass list, empty `dns_seeds`, onion seeds only, SOCKS override is the Tor container (`build/tari/entrypoint.sh:3-4`; `build/tari/config.toml:4-10`, `34-41`). | Tor only (see gaps 2 and 3) |
| Tari wallet | Transport `tor`, same bypass settings, `use_libtor = false`, `[auto_update] check_interval = 0` (`build/tari-wallet/config.toml:4-5`, `7-13`, `29-38`). | Tor only (see gap 3) |
| Zakura (Zcash) | Peer list is exactly four Tor-container listeners, `cache_dir = false`, P2P listens on loopback (`build/zcash/zakurad.toml:5-12`). Each listener reaches one seed through `SOCKS4A:127.0.0.1:<host>:8233,socksport=9050` (`build/tor/entrypoint.sh:77-82`). Zakura has no Tor transport: any address it learns later is dialled directly, and the internal network drops it. | Tor only, by layer 1 |
| Dashboard outbound HTTP | Telegram (`monitor_io.go:32`), Healthchecks, whose URL the user supplies and `settings.go:221-236` requires to be HTTPS (`monitor_io.go:79-95`), CoinGecko (`prices.go:43`), GitHub release check (`monitor_release.go:100`) all use `newTorHTTPClient(torProxy)` or its isolated variant (`monitor.go:140-141`). The transport has `Proxy: nil`, dials only the SOCKS5 proxy, sends hostnames to Tor (no local DNS) and never follows redirects (`tor_http.go:16-76`). If Tor is down the request fails with no fallback. | Tor only |
| Dashboard to nodes | `rpcClient`, `moneroRPC` and gRPC clients target addresses from `validateInternalHTTPURL`, which accepts only private, loopback and link-local IPs or single-label, `.local` and `.home.arpa` names (`chains.go:118-133`; `main.go:164-197`). Defaults are fixed stack addresses (`chains.go:54-58`, `main.go:70-123`). | Stack-internal (see gap 1) |
| Tari wallet launcher | The in-container supervisor probes its own gRPC port at `127.0.0.1:18143` (`build/tari-wallet/launcher.go:83`, `693`) and starts the pinned wallet binary without a shell (`450`). It dials nothing else. | Loopback |
| Dashboard health check | `GET http://127.0.0.1:<port>/healthz` inside its own container (`main.go:286-287`). | Loopback |
| Host: `stack`, `configure.sh`, `scripts/node-control`, `node-port-control`, `node-resource-profile`, `packaging/` | No curl, wget, ssh or similar. `curl` only runs inside `compose exec` against container loopback addresses (`scripts/node-resource-profile:84`, `139`, `154`). Container starts use `--pull never --no-build` (`stack:1036`, `1219`, `1746`, `1782`) and the one `docker run` uses `--network none` (`stack:1015`). | Local or offline |
| Host: image download | `stack pull` and install-time pulls run `docker compose pull` through the host's Docker daemon (`stack:1046-1047`, `scripts/node-control:581`). | Install-time, host connection |
| Host: package managers | `apt` and `brew` upgrades are printed as instructions, never run (`stack:1483-1500`). | Install-time, user-run |
| Image builds | Dockerfiles `apt-get`, `apk`, `curl` pinned release tarballs with SHA-256 and `go mod download` (`build/*/Dockerfile`). They run in the release pipeline; users pull prebuilt images. | Install-time |
| Maintainer tooling | `scripts/release.sh` (`git push`, `gh`), `scripts/resolve-runtime-images.sh` (registry read), `tools/`, `.github/`. | Not application traffic |

Install-time paths use the host's connection and can reveal the host's public IP
to the registry or package mirror; see
[Downloads and upgrades](privacy.md#downloads-and-upgrades). That exemption is a
policy choice, not a leak: those paths run on the host, never inside a stack
container, and do not give any container a route.

## Gaps found

No direct route out of a running container was found: every item below is
blocked by `internal: true` and the loopback DNS. They are places where the
boundary rests on one layer instead of two. Nothing below was changed by the
change that added this page, and none is covered by a failing test.

1. **Dashboard RPC and gRPC clients use Go's default transport.** Nineteen
   `http.Client` constructions in the dashboard (one is the loopback health check) and two `grpc.NewClient` channels
   (`egress_boundary_test.go` lists them) honour `HTTP_PROXY`-style variables, and
   many follow redirects, unlike the Tor client. The node URL check also runs
   only for enabled nodes: `main.go:164-197` and `chains.go:98`. A node answering
   with a redirect to a public URL, or a `.env` URL override for a disabled node,
   would be a dial attempt to a public host that only layer 1 stops. Suggested
   fix: one shared constructor for internal clients with `Proxy: nil` and
   `CheckRedirect` returning `http.ErrUseLastResponse`, plus `grpc.WithNoProxy()`.
2. **Defaults the images do not pin off.** Monero sets no `no-igd=1`
   (`build/monero/entrypoint.sh`), the Tari node config has no `[auto_update]`
   section while the wallet disables it (`build/tari-wallet/config.toml:4-5`), and
   Bitcoin Core relies on `-proxy` soft-setting `-upnp` and `-natpmp` off instead of
   passing them. All are unverified against the pinned upstream defaults; each
   would be a LAN or clearnet attempt dropped by the internal network. Suggested
   fix: set the three options explicitly.
3. **Tari wallet points at a Tor control port the stack never opens.**
   `build/tari-wallet/config.toml:33` sets `control_address` to
   `172.29.0.25:9051` while `build/tor/torrc` has no `ControlPort` by design
   (`tests/test_compose.sh:111-112`, `tests/test_e2e.sh:308`). Fails closed, so it
   is not a leak, but it may stop the wallet's Tor transport from starting.
   Unverified: `tests/test_tari_wallet_native.sh` is opt-in and was not run.
4. **Runtime probe covers only the services the e2e boots.** `tests/test_e2e.sh`
   starts Bitcoin, Monero (with its wallet) and the dashboard. Tari, Tari wallet,
   Litecoin, Dogecoin, Bitcoin Cash and Zakura are covered by the static compose
   test, which asserts the same network, DNS and port properties, but not by a
   live probe.
5. **Single-label host names count as internal.** `internalNodeHost` accepts any
   name without a dot, so a host-run dashboard with a DNS search suffix could
   resolve such a name to a public address. Inside the stack container there is no
   search suffix. Informational.

## Tests

| Test | Fails when |
|---|---|
| `tests/test_tor_boundary.py`, compose group | A service other than `tor` joins a second network or any non-internal one; a network is added; `btc_net` loses `internal`; `dns` is not loopback-only; any service sets `network_mode`, `extra_hosts`, `privileged`, host `pid`/`ipc`, a Docker socket mount, `NET_ADMIN`/`NET_RAW`/`SYS_ADMIN`, or a proxy variable; `TOR_PROXY` is not the Tor address; a node publishes a port; a LAN overlay lacks a required bind IP or touches another service; a new compose file is not classified. |
| Same file, Tor group | A relay in the Tor entrypoint leaves the stack except through Tor's SOCKS port, a listener appears that is not a published relay or a Zakura bridge, or `torrc` gains a control, relay, transparent or DNS port. |
| Same file, node group | Bitcoin Core, the family nodes (rendered from the real entrypoint for clearnet and onion-only, inbound on and off), Monero, Tari, its wallet or either Zakura config loses its Tor proxy or opt-out, gains a direct peer, DNS seed, port mapping, update check or clearnet listener, or the DoH seed lookup stops using Tor with remote DNS. |
| Same file, host-script group | A script, entrypoint, Dockerfile or compose file gains a network-capable command (`curl`, `wget`, `nc`, `socat`, `ssh`, `git` remote verbs, `gh`, `apt`, `brew`, `pip`, `npm`, `go mod download`, `docker pull/run/build`, `compose pull/up`, `/dev/tcp`, Python sockets) that is not provably loopback-only, Tor-routed or offline and not listed in `tests/tor_boundary_allowlist.txt`. |
| `build/dashboard/egress_boundary_test.go` | A dashboard source file or `build/tari-wallet/launcher.go` starts using `net.Dial`, a resolver lookup, `http.Get`, the default client or transport, `tls.Dial`, `os/exec`, a reverse proxy, a raw `syscall` socket call, or constructs a new `http.Client`, `http.Transport`, `net.Dialer` or gRPC channel (including as a zero-value struct field, slice element or `make`) outside the reviewed list; dot-imports a package; names a new host in a URL literal, a `Host:` field or `net.JoinHostPort`; imports `net/smtp`, `golang.org/x/sys/unix` or similar; names a new URL host; or builds a monitor or price client without the Tor dialer, with a proxy-from-environment transport, or with redirects enabled. Also pins which public names `validateInternalHTTPURL` rejects. |
| `tests/test_e2e.sh` (CI, needs Docker) | Tor cannot reach `1.1.1.1:443` itself (the probe control), or, with Tor stopped, any running non-Tor container connects to a public IPv4 or IPv6 address, resolves a public name, queries a public DNS server, lacks loopback-only DNS, or holds an established connection outside the stack subnet. The service list comes from `docker compose ps`, so new services are probed automatically. This step was added without being run locally (it needs Docker); only `bash -n` and `shellcheck` were run. |

Each checker has mutation tests: they edit the real file text, or feed a synthetic
script or Go source, and assert the violation is reported.

Run them:

```sh
python3 -m unittest tests.test_tor_boundary
cd build/dashboard && go test -run 'Egress|ExternalClients|StackInternal|ExternalHosts' ./...
CI=1 tests/test_e2e.sh   # clean checkout, Docker, port 80
```

The first two need no Docker and no network, and run in `tests/run.sh`.

## Changing something that touches the network

- A new network command in a script or Dockerfile: add a line to
  `tests/tor_boundary_allowlist.txt` with its count, class and reason, or make it
  loopback-only, Tor-routed (`--socks5-hostname "$TOR_PROXY"`) or offline
  (`docker run --network none`, `compose up --pull never`).
- A new dashboard client: route external traffic through `newTorHTTPClient`; for
  an internal node, validate the URL with `validateInternalHTTPURL` and add the
  file, symbol and count to `allowedEgress`.
- A new service: attach it to `btc_net` only, give it `dns: ["127.0.0.1"]`, and
  point its proxy at `172.29.0.25:9050`.
- A new compose file: classify it in `tests/tor_boundary_lib.py`.

The script scanner reads command position heuristically and skips here-document
bodies. It sees through path prefixes (`/usr/bin/curl`), wrappers (`env`,
`timeout`, `nohup`, `sudo`, `busybox`, `docker exec`), `sh -c "..."` strings,
`dig`/`nslookup`/`host`, `openssl s_client`, `tailscale`, and `python -c` with
network modules. A command built from variables (`$cmd`), run through `eval`, or
hidden in an interpreter script other than Python imports is invisible to it.
Review those by hand. The Go scanner is syntactic: it does not follow a client
passed in through an interface, and it cannot tell what an allowed `net.Dialer`
connects to, so the allowlist reasons name the code that restricts the address.
