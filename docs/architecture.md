# Architecture

Tor, the dashboard, and each selected node share the internal `btc_net` bridge (`172.29.0.0/24`). Only
Tor also joins the external `tor_egress` bridge.

```mermaid
flowchart LR
    You(["👤 You · Browser"])
    Net(["🌐 Tor Network"])

    subgraph stack ["🐳 Node Manager"]
      subgraph internal ["btc_net · internal · 172.29.0.0/24"]
        Dashboard["📊 dashboard<br/>.27 · Go"]
        Bitcoin["🟠 bitcoin<br/>.26 · Bitcoin Core"]
        Monero["🟠 monero<br/>.30 · monerod"]
        MoneroWallet["👁️ monero-wallet<br/>.31 · wallet RPC"]
        Tari["Tari<br/>.32 · mining gRPC + wallet query"]
        TariWallet["👁️ tari-wallet<br/>.37 · wallet gRPC"]
        OtherNodes["Litecoin · Dogecoin · Bitcoin Cash<br/>.33 · .34 · .35 · RPC"]
        Zcash["Zcash / Zakura<br/>.36 · private RPC"]
        Tor["🧅 tor<br/>.25 · SOCKS5"]
      end
    end

    You ==>|"HTTP :80 · loopback or private LAN"| Tor
    Tor ==>|"fixed TCP forward · :8000"| Dashboard
    You -.->|"optional authenticated RPC · private LAN"| Tor
    Tor -.->|"fixed TCP relay · :8332"| Bitcoin
    Dashboard -.->|"RPC :8332 (internal)"| Bitcoin
    Dashboard -.->|"internal Digest RPC :18085"| Monero
    Dashboard -.->|"authenticated wallet RPC :18082"| MoneroWallet
    MoneroWallet -.->|"internal Digest RPC :18085"| Monero
    Bitcoin ==>|"SOCKS5 :9050 (internal)"| Tor
    Monero ==>|"SOCKS5 :9050 (internal)"| Tor
    Tari ==>|"SOCKS5 :9050 (internal)"| Tor
    TariWallet ==>|"SOCKS5 :9050 (internal)"| Tor
    OtherNodes ==>|"SOCKS5 :9050 (internal)"| Tor
    Zcash ==>|"private TCP bridges → SOCKS4A"| Tor
    Dashboard -.->|"gRPC / JSON-RPC (internal)"| Tari
    Dashboard -.->|"authenticated wallet gRPC :18143"| TariWallet
    TariWallet -.->|"HTTP wallet query :9000"| Tari
    Dashboard -.->|"authenticated JSON-RPC (internal)"| OtherNodes
    Dashboard -.->|"native JSON-RPC :8232 (internal)"| Zcash
    Tor -.->|"optional authenticated read-only Zcash proxy :18232"| Dashboard
    Tor <==> Net
```

## The services

| Service | Image | Runs as | Role |
|---|---|---|---|
| `tor` | `alpine:3.24` + tor (built locally) | `tor` user | Outbound SOCKS5 proxy at `172.29.0.25:9050`. Fresh onion identity per volume. |
| `bitcoin` | [`bitcoin/bitcoin`](https://hub.docker.com/r/bitcoin/bitcoin) (official, digest-pinned) | `1000:1000` | Full or pruned node. `-datadir=/data` bind-mounted from the host. Credentials, `dbcache`, and `prune` injected from `.env` at runtime. |
| `monero` | Pinned, SHA-256 verified upstream Monero binary | `1000:1000` | Optional pruned node. Restricted Digest RPC, separately opt-in LAN RPC and ZMQ. |
| `monero-wallet` | Same verified Monero image | `1000:1000` | Internal-only watch wallet RPC. Encrypted wallet files use a dedicated volume; no host port or public route. |
| `tari` | Official digest-pinned `ghcr.io/tari-project/minotari_node` | `1000:1000` | Pruned base node, Tor transport, mining gRPC allowlist, and internal HTTP wallet-query service; no miner. |
| `tari-wallet` | Official digest-pinned `ghcr.io/tari-project/minotari_console_wallet` plus the local supervisor | `1000:1000` | One native view-only wallet. Authenticated internal gRPC, owner-only native database and archives, no host port. Its separate 0.5 CPU / 512 MiB ceiling is not yet qualified under sustained load. |
| `litecoin`, `dogecoin`, `bitcoincash` | Pinned upstream binaries, verified against signed release checksums | Non-root image user | Separate pruned nodes with authenticated RPC and Tor proxies. |
| `zcash` | Official digest-pinned `zakuracore/zakura` | Image entrypoint drops to `10001` | Pruned state, four private Tor bootstrap bridges, and internal native RPC. |
| `dashboard` | `alpine:3.24` + a static Go binary (built locally) | `1000:1000` | Polls the node over RPC every page load; reads the data dir read-only for disk stats. |

Static IPs keep the bitcoin entrypoint's `-proxy=` argument (in
`docker-compose.yml`) and the dashboard's RPC URL simple and deterministic.
Docker Compose 2.33.1 or newer is required for the gateway-priority setting
that makes `tor_egress` Tor's deterministic default route.

## Privacy model

- **`-onlynet=onion` + `-proxy=…`**, set by the bitcoin entrypoint in
  [docker-compose.yml](../docker-compose.yml), plus **`listen=0`** in
  [bitcoin.conf](../build/bitcoin/bitcoin.conf): every P2P connection is
  routed to onion peers through the Tor container. The node never dials
  clearnet; inbound peers use the optional onion service, disabled by default.
- **The application network is internal.** Bitcoin and dashboard have no
  external route and use loopback-only DNS, so a proxy bug fails closed.
  Tor alone joins `tor_egress`; its gateway has explicit priority. The host
  dashboard and optional LAN RPC ports terminate in Tor's container and forward
  only to fixed internal addresses.
- **The additional nodes use the same boundary.** Tari, Litecoin, Dogecoin,
  and Bitcoin Cash use the internal Tor proxy. Zakura has no native SOCKS
  transport here: four fixed TCP bridges send bootstrap peer connections through
  Tor with remote DNS resolution. Direct discovered-peer connections fail closed.
  Native Zakura RPC stays private; optional LAN RPC passes through the Go server's
  separate authenticated, read-only method allowlist. See [network details](privacy.md).
- **Monero uses the same isolated network.** Both P2P sync and transaction relay
  use Tor; DNS checkpoints and update checks are disabled. Restricted RPC and
  ZMQ are separate private-LAN relays owned by the Tor container. The authenticated
  unrestricted RPC listener at internal port 18085 supplies complete dashboard
  metrics; it has no host relay or published port. LAN clients use restricted
  port 18081. ZMQ has no authentication. The separate wallet service at internal
  port 18082 accepts only authenticated dashboard traffic, uses a read-only root
  filesystem, and has neither an external route nor a host port.
- **Tari's wallet stays internal.** The dashboard authenticates to the sidecar at
  `172.29.0.37:18143`; the sidecar queries the Tari node at `tari:9000`. Neither
  listener is published. The mode-600 creation request is removed after use, but
  Tari v6.1 stores the private view key inside its native SQLite database. The
  owner-only volume is not encryption at rest; use encrypted host storage when
  required. No seed or private spend key is accepted.
- **Inbound onion service** (`inbound_onion` in `config.json`, opt-in):
  bitcoind registers an onion address over tor's cookie-authed control
  port and serves blocks to the network — still no clearnet, no host port,
  no IP exposure. The tor data volume is mounted read-only into the
  bitcoin container (whose process joins tor's group) so it can read the
  control-auth cookie.
- **RPC auth is a salted hash.** bitcoind is started with `rpcauth=`; the
  plaintext password exists only in the dashboard container and `.env`.
  The bitcoin health check authenticates with Core's `.cookie` file.
- **The only published port by default is the dashboard's `80`.** It selects an RFC1918 host address automatically, with loopback as a fallback.
  `dashboard.bind_ip` accepts `auto`, loopback, or an explicit RFC1918 host
  address; public and wildcard bindings are rejected. `bitcoin.rpc_expose` can publish
  authenticated RPC on a private host address through Tor's TCP relay;
  `rpcallowip` still restricts Core to Docker's address space. SOCKS (`9050`)
  and the Tor control port (`9051`) are not published to the host. The optional
  dashboard onion service adds reachability through Tor without another host port.
- **Notification egress rides Tor too.** Telegram messages and
  Healthchecks.io pings (both opt-in) go through the tor container's SOCKS
  proxy (`socks5h`, so even DNS resolves through Tor) — enabling alerts
  never turns the stack into a clearnet beacon.
- **All coin prices use the same Tor proxy.** Background CoinGecko requests every five
  minutes cache nine fiat quotes; page loads and currency changes only read the
  cache. Initial failures retry sooner while Tor starts. DNS resolves through Tor,
  TLS is verified, and failures never fall back to
  a direct connection. Quotes become stale after a failed fetch or fifteen minutes.

## Trust boundaries — and non-boundaries

- Fresh installs generate separate authenticated node RPC logins,
  plus Monero wallet RPC, Tari wallet RPC, a Monero wallet-file password, and a
  Tari native wallet startup password. Tari mining gRPC
  and Monero ZMQ rely on the trusted LAN boundary. The dashboard defaults to
  a required password and an automatically selected private LAN address. Trusted
  LAN clients can view personal wallets and manage nodes after authentication.
  Credential reveal/reset and onion access still
  require authentication. See [SECURITY.md](../SECURITY.md).
- Release checks are informational. The dashboard cannot request a host update;
  users install releases themselves through APT, Homebrew, or GitHub.
- LAN port controls use a separate typed request in the same private volume.
  A host helper validates the request, private LAN bind, ports, saved RPC
  credentials, Compose ownership, and host/Docker port availability before any
  restart, then updates only the selected RPC/ZMQ
  listener fields in server config and applies it offline. The dashboard cannot
  publish a public listener, choose host paths, run commands, or access Docker.
  Source installs reuse the host agent; APT installs use a dedicated hardened
  systemd timer. Dashboard changes require authentication.
  A compromised dashboard process can exercise these allowed LAN-listener
  controls; the helper limits its actions rather than independently authenticating the operator.
- Node activation uses another fixed, typed request. The host validates the node
  identity and saved configuration, then starts or gracefully stops only that
  node and its dependent wallet service. Data is retained. Failed changes restore
  the prior configuration and service state; the dashboard waits for the host's
  result before marking a tower inactive.
- Docker host access is **not** a boundary: `docker inspect` reveals RPC
  credentials. Anyone who can run Docker commands owns the box anyway.

## Startup behavior

The bitcoin entrypoint deletes stale `*.lock`/`LOCK` files before starting
bitcoind — recovery from hard power-offs, safe because `container_name`
guarantees a single instance. `stop_grace_period: 5m` gives Bitcoin Core
time to flush its cache on shutdown; don't `docker kill` it.
