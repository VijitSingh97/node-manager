# The Tor boundary

The rule: the application's own traffic leaves the host only through the Tor
container. Nodes dial peers through Tor's SOCKS port, and clearnet peers are
reached only from a Tor exit. The dashboard fetches prices, release metadata and
notifications over Tor with remote name resolution. No node publishes a P2P port
on the host. LAN RPC is an explicit opt-in listener on the Tor container, which is
inbound, not internet egress. [Tor-only networking](privacy.md) describes the
design; this page lists every egress path found in the repository, how each one
stays on Tor, and which test fails when it stops doing so.

Evidence below is `file:line` as of the last change to this page; line numbers drift as files change.

## How the boundary is enforced

1. Every service except `tor` joins only `btc_net`, which is `internal: true`
   (`docker-compose.yml:533-536`). Docker gives an internal network no gateway, so
   a direct TCP, UDP or ICMP packet to a public address has no route.
2. `tor` is the only service on `tor_egress`, the only network with an external
   route (`docker-compose.yml:43-48`, `540-541`).
3. Every service except `tor` sets `dns: [127.0.0.1]`, so Docker's embedded
   resolver has no upstream to forward public names to (`docker-compose.yml:119`,
   `157`, `199`, `237`, `274`, `312`, `349`, `386`, `530`; `docker-compose.tari-wallet.yml:48`).
   Names that must resolve (`tari`, for example) are served by Docker's own
   service-name DNS, not the upstream.
4. Each node is configured to dial through `172.29.0.25:9050` (the Tor container)
   rather than relying only on layer 1. If layer 1 failed, the node still has no
   configured direct route.

Layers 1 and 3 are the hard boundary. Layer 4 is why nothing is even attempted.

## Egress path inventory

| Path | Mechanism | Verdict |
|---|---|---|
| Compose networks | Two networks only, `btc_net` internal, `tor_egress` external (`docker-compose.yml:533-541`). | Tor only |
| Published host ports | Base file publishes only the Tor relay to the dashboard on `DASHBOARD_BIND_IP`, loopback by default (`docker-compose.yml:49-52`). Eight overlays add one LAN listener each on `tor`, each with a required bind IP and no default (`docker-compose.rpc.yml:4`, `monero-rpc.yml:4`, `monero-zmq.yml:4`, `tari-grpc.yml:4`, `litecoin-rpc.yml:4`, `dogecoin-rpc.yml:4`, `bitcoincash-rpc.yml:4`, `zcash-rpc.yml:4`). No node publishes a port. | Inbound opt-ins only |
| `extra_hosts`, `network_mode`, `privileged`, host `pid`/`ipc`, Docker socket | None in any compose file. | None |
| Proxy environment variables | None. The only proxy variable is `TOR_PROXY`, always `172.29.0.25:9050` (`docker-compose.yml:141`, `260`, `298`, `345`). | Tor only |
| Tor container | `SocksPort 0.0.0.0:9050`, no `ControlPort`, `ORPort`, `ExitRelay`, `TransPort` or `DNSPort` (`build/tor/torrc:1-3`). The only control channel is `ControlSocket /var/lib/tor/control/sock`, a unix socket in a 0700 directory of the tor-only `tor_data` volume that no port, other container or network reaches; `tor-health` (`build/tor/tor-health.sh`) reads bootstrap, network, relay-connection, directory and guard status from it with the cookie, and `nc -U` there cannot reach a network (`build/tor/torrc:5-7`). Socat relays end at stack addresses or at Tor's own SOCKS port (`build/tor/entrypoint.sh:54-89`). | Tor only |
| Tor health probe | The healthcheck opens one TCP stream through Tor's own SocksPort, `timeout 20 nc -z -w 20 -X 5 -x 127.0.0.1:9050 <host> 443` (`timeout` because `-w` does not bound the SOCKS handshake) (SOCKS5, so the hostname goes to Tor and no local DNS happens), to `www.torproject.org`, then `check.torproject.org`, then `api.github.com` while each fails (`build/tor/tor-health.sh`). The first two are run by the Tor Project, the most stable HTTPS names a Tor client can assume. `api.github.com` is independent of them and is already reached through Tor by the dashboard's release check (`build/dashboard/monitor.go:21`), so one outage or block of the Tor Project hosts cannot fail every install. A probe that fails on all three while Tor still reports relays is the signal that circuits do not work. The probe sends no data and closes at once; the exit relay sees only a connection to the Tor Project. If Tor is down the probe fails with no fallback. | Tor only |
| Bitcoin Core | `-proxy` and `-onion` both `172.29.0.25:9050`; `-onlynet=onion` when clearnet-over-Tor is off; inbound only through an onion bind with `-listenonion=0` (`docker-compose.yml:98`, `102-104`); `listen=0` and `natpmp=0` by default (`build/bitcoin/bitcoin.conf:14`, `16`). Core 32.1 has no `-upnp` option, so none is set. Upstream Core resolves DNS seeds through a set proxy (not tested here). | Tor only |
| Litecoin, Dogecoin, Bitcoin Cash | `proxy=` and `onion=` set to Tor, `discover=0`, `listen=0` unless an onion bind exists, `onlynet=onion` plus `dnsseed=0` when onion-only; `upnp=0` on all three and `natpmp=0` on Bitcoin Cash, the only fork that has the option (`build/bitcoin-family/entrypoint.sh:58-81`). | Tor only |
| Dogecoin seed lookup | DNS-over-HTTPS to `cloudflare-dns.com` through `curl --socks5-hostname "$TOR_PROXY"`, HTTPS only, `--noproxy ''` (`build/bitcoin-family/entrypoint.sh:23-48`). | Tor only |
| Monero and its wallet | `proxy=socks5://$TOR_PROXY`, `tx-proxy=tor,...`, `in-peers=0`, `hide-my-port=1`, `no-igd=1`, DNS checkpoints and update checks off (`build/monero/entrypoint.sh:16-24`). The wallet's only daemon is the internal node (`build/monero/wallet-entrypoint.sh:15`). | Tor only. The pinned release has no UPnP code; `no-igd=1` keeps port mapping off if a later one restores it (`build/monero/entrypoint.sh:22`) |
| Tari node | Transport `tcp_tor`, or `tor` when onion-only; `proxy_bypass_for_outbound_tcp = false`, empty bypass list, empty `dns_seeds`, onion seeds only, SOCKS override is the Tor container (`build/tari/entrypoint.sh:3-4`; `build/tari/config.toml:7-13`, `36-45`); `[auto_update] check_interval = 0` (`build/tari/config.toml:4-5`); `tari_pulse_enabled = false` (`build/tari/config.toml:16`), because the Tari Pulse service looks up `checkpoints.tari.com` TXT records through the system resolver every 2 minutes, which cannot resolve here and only fills Docker's log with resolver errors. Nothing in the stack reads its `failed_checkpoints` flag, which then stays false. | Tor only |
| Tari wallet | Transport `tor`, same bypass settings, `use_libtor = false`, `[auto_update] check_interval = 0` (`build/tari-wallet/config.toml:4-5`, `7-13`, `21`, `27-36`). No `control_address`: the wallet opens no Tor control connection. | Tor only |
| Zakura (Zcash) | Peer list is exactly four Tor-container listeners, `cache_dir = false`, `p2p_stack = "legacy"`, P2P listens on loopback (`build/zcash/zakurad.toml:5-15`). The v1.8.8 workspace crates have no port-mapping or update-check code (dependencies and image digest not audited). Each listener reaches one seed through `SOCKS4A:127.0.0.1:<host>:8233,socksport=9050` (`build/tor/entrypoint.sh:86-89`). Zakura has no Tor transport: any address it learns later is dialled directly, and the internal network drops it. | Tor only, by layer 1 |
| Dashboard outbound HTTP | Telegram (`monitor_io.go:32`), Healthchecks, whose URL the user supplies and `settings.go:221-236` requires to be HTTPS (`monitor_io.go:79-95`), CoinGecko (`prices.go:44`), GitHub release check (`monitor_release.go:130`) all use `newTorHTTPClient(torProxy)` or its isolated variant (`monitor.go:144-145`). The transport has `Proxy: nil`, dials only the SOCKS5 proxy, sends hostnames to Tor (no local DNS) and never follows redirects (`tor_http.go:16-76`). If Tor is down the request fails with no fallback. | Tor only |
| Dashboard to nodes | `rpcClient`, `moneroRPC` and gRPC clients target addresses from `validateInternalHTTPURL`, which accepts only private, loopback and link-local IPs or single-label, `.local` and `.home.arpa` names (`chains.go:118-135`). It runs for every configured node URL whether or not the node is enabled (`chains.go:98`; `main.go:163-197`). Defaults are fixed stack addresses (`chains.go:54-58`, `main.go:70-123`). Every HTTP client comes from `newInternalHTTPClient` (`internal_http.go:41`): `Proxy: nil`, redirects never followed, and a dialer whose `Control` hook refuses any resolved address outside the private, loopback and link-local ranges (`internal_http.go:14-39`). Both gRPC channels pass `grpc.WithNoProxy()` and dial through the same hook (`internalGRPCDialer`; `tari.go:37`, `tari_watch.go:448`). | Stack-internal |
| Tari wallet launcher | The in-container supervisor probes its own gRPC port at `127.0.0.1:18143` (`build/tari-wallet/launcher.go:83`, `698`) and starts the pinned wallet binary without a shell (`455`). It dials nothing else. | Loopback |
| Dashboard health check | `GET http://127.0.0.1:<port>/healthz` inside its own container (`main.go:282-283`). | Loopback |
| Host: `stack`, `configure.sh`, `scripts/node-control`, `node-port-control`, `node-resource-profile`, `packaging/` | No curl, wget, ssh or similar. `curl` only runs inside `compose exec` against container loopback addresses (`scripts/node-resource-profile:101`, `156`, `171`). Container starts use `--pull never --no-build` (`stack:1370`, `1595`, `2139`, `2175`) and the one `docker run` uses `--network none` (`stack:1346`). | Local or offline |
| Host: `node-manager-hostd` (`packaging/systemd/node-manager-host-stats.service`) | Timer-started root helper that reads `/proc`, `/sys` and cgroups and talks to the local Docker socket. `RestrictAddressFamilies=AF_UNIX` and `IPAddressDeny=any` leave it no network at all, and there is no `PrivateNetwork` only because that would blank `/proc/net/dev`. | None |
| Host: Tor watchdog | `tor_watchdog` in `scripts/node-control` reads the tor container's Docker health result and may run `docker restart`, `docker logs --tail`, `docker exec <tor> tor-health snapshot`, and `docker exec <tor> touch` or `rm -f /var/lib/tor/.reset-state`, then writes an event line into the dashboard state and a root-only diagnostic snapshot into the host state directory. It opens no connection, adds no Docker access for the dashboard and gives no container a Tor control port. | Local, Docker daemon only |
| Host: image download | `stack pull`, install-time pulls and the pull before a node activation run `docker compose pull` through the host's Docker daemon (`stack:1380-1381`, `scripts/node-control:1186`, `1672`). A package upgrade and a plain `stack pull` also run `docker pull` for each node whose repository already has an image on the machine (`stack:1409`, `1411`). | Host connection, not Tor-routed; an activation pull runs only when a node's image is missing |
| Host: package managers | `apt` and `brew` upgrades are printed as instructions, never run (`stack:1892-1912`). | Install-time, user-run |
| Image builds | Dockerfiles `apt-get`, `apk`, `curl` pinned release tarballs with SHA-256 and `go mod download` (`build/*/Dockerfile`). They run in the release pipeline; users pull prebuilt images. | Install-time |
| Maintainer tooling | `scripts/release.sh` (`git push`, `gh`), `scripts/resolve-runtime-images.sh` (registry read), `tools/`, `.github/`. | Not application traffic |

Install-time paths use the host's connection and can reveal the host's public IP
to the registry or package mirror; see
[Downloads and upgrades](privacy.md#downloads-and-upgrades). That exemption is a
policy choice, not a leak: those paths run on the host, never inside a stack
container, and do not give any container a route.

## Gaps found

No direct route out of a running container was found: every item below was
blocked by `internal: true` and the loopback DNS. They were places where the
boundary rested on one layer instead of two. Gaps 1 to 5 are fixed and each
has a failing test or a probe.

1. **Fixed: dashboard RPC and gRPC clients used Go's default transport.** The
   clients honoured `HTTP_PROXY`-style variables, and many followed redirects,
   unlike the Tor client. The node URL check also skipped disabled nodes. Fix:
   one constructor, `newInternalHTTPClient` (`internal_http.go:41`), for every
   stack-internal client, with `Proxy: nil`, `http.ErrUseLastResponse` on
   redirects, one shared transport and a dial `Control` hook that rejects any
   resolved address outside private, loopback and link-local ranges
   (`internal_http.go:14-39`); `grpc.WithNoProxy()` on both channels
   and `internalGRPCDialer` (`tari.go:37`, `tari_watch.go:448`); and `validateInternalHTTPURL` for the
   Bitcoin, Monero, Monero wallet and Tari wallet URLs whether or not the node is
   enabled (`main.go:163-197`; the other nodes already were, `chains.go:98`).
   Tests: `TestInternalClientsNeverUseProxiesRedirectsOrPublicAddresses`,
   `TestGRPCChannelsIgnoreProxyVariables`, `TestGRPCDialerRefusesPublicAddresses`,
   `TestEveryNodeURLIsCheckedEvenWhenTheNodeIsDisabled` and the scanner
   allowance, which now fails any `http.Client` or `http.Transport` built outside
   `internal_http.go` and `tor_http.go`. Both gRPC channels also dial through
   `internalGRPCDialer`, the same `Control` hook (`TestGRPCDialerRefusesPublicAddresses`;
   `TestGRPCChannelsIgnoreProxyVariables` fails any channel without it).
2. **Fixed: defaults the images did not pin off.** Checked against the pinned
   sources. Monero 0.18.5.4 still registers `no-igd` (`src/p2p/net_node.cpp`) but
   has removed its UPnP code (`src/p2p/net_node.inl` warns that `--igd` is
   non-functional), so `no-igd=1` changes nothing today and only keeps port
   mapping off if a later release restores it. The Tari node had no
   `[auto_update]` section; it now mirrors the wallet with `check_interval = 0`
   (the v6.2.0 default is no update URIs, so this pins an already-off default).
   Bitcoin Core 32.1 registers `-natpmp` with default on and has no `-upnp`
   (`src/init.cpp`, `src/mapport.h`), so `natpmp=0` is set and `upnp` is
   deliberately not: an unknown option would stop `bitcoind` from starting.
   Litecoin 0.21.5.8, Dogecoin 1.14.9 and Bitcoin Cash Node 29.2.0 accept
   `-upnp` (default off, and soft-set off by `-proxy` and `listen=0`), so
   `upnp=0` is set on all three; only Bitcoin Cash Node also registers
   `-natpmp`, so only it gets `natpmp=0`. Zakura 1.8.8 (tag `v1.8.8`, commit `009b60b`, the pinned image
   version) was read from source: it has no UPnP, NAT-PMP or port-mapping code,
   and no update or version check in any workspace crate (dependencies such as iroh and rocksdb and the image digest were not audited; iroh is reachable only with a non-legacy `p2p_stack`). Its only other
   outbound settings are off by default and now pinned: `p2p_stack = "legacy"`
   (`crates/zakura-network/src/config.rs`; the native stack would start an iroh
   endpoint), `[zcashd_compat]` (`enabled = false`, `crates/zakurad/src/components/zcashd_compat/config.rs`),
   `[metrics] endpoint_addr` (a listener, `None` by default) and
   `opentelemetry_endpoint` (ignored, export removed). Seed hostnames would be
   resolved with `tokio::net::lookup_host`, but the stack lists only IP
   addresses. Tests: `NodeConfigTest` for each setting, including the rendered family
   configs for all three forks.
3. **Fixed: Tari wallet pointed at a Tor control port the stack never opens.**
   In Tari v6.2.0 the console wallet no longer starts a peer-to-peer stack: its
   `p2p` settings are marked deprecated in `base_layer/wallet/src/config.rs`,
   nothing in `applications/minotari_console_wallet` or `base_layer/wallet`
   reads them, and it reaches the node over `http_server_url`. The key was
   inert, and `control_address` and `control_auth` are removed so the file says
   what the stack does. Missing keys fall back to the struct defaults, as they
   already do in the node config. Test: `test_tari_node_and_wallet_dial_only_through_tor`
   and `tests/test_compose.sh`. Still unverified: `tests/test_tari_wallet_native.sh`
   (opt-in, needs Docker) was not run, so the pinned image itself was not started
   with the new file.
4. **Fixed: runtime probe covered only Bitcoin, Monero and the dashboard.**
   `E2E_ALL_NODES=1 CI=1 tests/test_e2e.sh` also boots Tari, Tari wallet,
   Litecoin, Dogecoin, Bitcoin Cash and Zakura (no sync needed) and requires all
   of them in the probe list. It runs weekly and on demand in
   `.github/workflows/e2e-all-nodes.yml`, not on every pull request, because it
   builds and pulls many images. It needs a Linux host CPU with BMI2 and ADX
   (Zakura's image) and fails with a message if they are missing. Not yet run:
   written without Docker, so the first run may need healthcheck or timeout
   tuning.
5. **Fixed: single-label host names counted as internal.** `internalNodeHost`
   accepts any name without a dot, so a host-run dashboard with a DNS search
   suffix could resolve such a name to a public address. The dial check from gap 1
   now tests the resolved address, so that connection is refused. Test:
   `TestInternalClientsNeverUseProxiesRedirectsOrPublicAddresses` and
   `TestGRPCDialerRefusesPublicAddresses`.

## Tests

| Test | Fails when |
|---|---|
| `tests/test_tor_boundary.py`, compose group | A service other than `tor` joins a second network or any non-internal one; a network is added; `btc_net` loses `internal`; `dns` is not loopback-only; any service sets `network_mode`, `extra_hosts`, `privileged`, host `pid`/`ipc`, a Docker socket mount, `NET_ADMIN`/`NET_RAW`/`SYS_ADMIN`, or a proxy variable; `TOR_PROXY` is not the Tor address; a node publishes a port; a LAN overlay lacks a required bind IP or touches another service; a new compose file is not classified. |
| Same file, Tor group | A relay in the Tor entrypoint leaves the stack except through Tor's SOCKS port, a listener appears that is not a published relay or a Zakura bridge, or `torrc` gains a `ControlPort`, a `ControlSocket` outside `/var/lib/tor/control/`, `ControlSocketsGroupWritable`, a volume or tmpfs at the tor service's `/tmp`, another service mounting `tor_data`, or a relay, transparent or DNS port. |
| Same file, node group | Bitcoin Core, the family nodes (rendered from the real entrypoint for clearnet and onion-only, inbound on and off), Monero, Tari, its wallet or either Zakura config loses its Tor proxy, `p2p_stack = "legacy"` or opt-out, or gains a `[zcashd_compat]`, `[metrics]` or `[network.zakura]` section, or the `zcash` service sets any `ZAKURA_` variable beyond the three known ones, gains a direct peer, DNS seed, port mapping, update check or clearnet listener, or the DoH seed lookup stops using Tor with remote DNS; Bitcoin Core loses `natpmp=0` or gains `upnp`; a family node loses `upnp=0` (Bitcoin Cash also `natpmp=0`); Monero loses `no-igd=1`; the Tari node or wallet loses `check_interval = 0` or sets a Tor control address. |
| Same file, Tor health probe | `build/tor/tor-health.sh` gains any `nc` other than the SOCKS5 probe to `127.0.0.1:9050` on port 443 or the `-U` control socket read, or the probe's default hosts change from the three listed here, or a listed host is missing from this page. |
| Same file, host-script group | A script, entrypoint, Dockerfile or compose file gains a network-capable command (`curl`, `wget`, `nc`, `socat`, `ssh`, `git` remote verbs, `gh`, `apt`, `brew`, `pip`, `npm`, `go mod download`, `docker pull/run/build`, `compose pull/up`, `/dev/tcp`, Python sockets) that is not provably loopback-only, Tor-routed or offline and not listed in `tests/tor_boundary_allowlist.txt`. `nc -U` on a unix socket counts as offline, since it takes no host. |
| `build/dashboard/egress_boundary_test.go` | A dashboard source file or `build/tari-wallet/launcher.go` starts using `net.Dial`, a resolver lookup, `http.Get`, the default client or transport, `tls.Dial`, `os/exec`, a reverse proxy, a raw `syscall` socket call, or constructs a new `http.Client`, `http.Transport`, `net.Dialer` or gRPC channel (including as a zero-value struct field, slice element or `make`) outside the reviewed list; dot-imports a package; names a new host in a URL literal, a `Host:` field or `net.JoinHostPort`; imports `net/smtp`, `golang.org/x/sys/unix` or similar; names a new URL host; or builds a monitor or price client without the Tor dialer, with a proxy-from-environment transport, or with redirects enabled. A new internal client must come from `newInternalHTTPClient`. Also pins which public names `validateInternalHTTPURL` rejects, that the shared internal client has no proxy, never follows a redirect and refuses to dial a public address, that every gRPC channel passes `WithNoProxy` and the internal dialer, that the gRPC dialer refuses a public address, and that every node URL is checked even when its node is disabled. |
| `tests/test_e2e.sh` (CI, needs Docker) | Tor cannot reach `1.1.1.1:443` itself (the probe control), or, with Tor stopped, any running non-Tor container connects to a public IPv4 or IPv6 address, resolves a public name, queries a public DNS server, lacks loopback-only DNS, or holds an established connection outside the stack subnet. The service list comes from `docker compose ps`, so new services are probed automatically. With `E2E_ALL_NODES=1` (weekly workflow) every node service must be in that list. This step was added without being run locally (it needs Docker); only `bash -n` and `shellcheck` were run. |

Each checker has mutation tests: they edit the real file text, or feed a synthetic
script or Go source, and assert the violation is reported.

Run them:

```sh
python3 -m unittest tests.test_tor_boundary
cd build/dashboard && go test -run 'Egress|ExternalClients|StackInternal|ExternalHosts' ./...
CI=1 tests/test_e2e.sh   # clean checkout, Docker, port 80
E2E_ALL_NODES=1 CI=1 tests/test_e2e.sh   # every node; Linux with BMI2 and ADX
```

The first two need no Docker and no network, and run in `tests/run.sh`.

## Changing something that touches the network

- A new network command in a script or Dockerfile: add a line to
  `tests/tor_boundary_allowlist.txt` with its count, class and reason, or make it
  loopback-only, Tor-routed (`--socks5-hostname "$TOR_PROXY"`) or offline
  (`docker run --network none`, `compose up --pull never`).
- A new dashboard client: route external traffic through `newTorHTTPClient`; for
  an internal node, validate the URL with `validateInternalHTTPURL` and build the
  client with `newInternalHTTPClient`.
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
