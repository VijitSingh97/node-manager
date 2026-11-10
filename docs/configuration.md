# Configuration

Run `./stack init` for the default installation. It creates private
`config.json`, starts Bitcoin only, chooses a private LAN address, and generates
a dashboard password shown once. Later starts use `./stack up` and your saved
login. `configure.sh` renders configuration into a gitignored `.env` for Docker
Compose; both files are kept private with mode `600`.

For custom first-boot settings, copy `config.example.json` to `config.json`,
set `dashboard.password` to 12–128 letters or numbers, and edit the other values
before running `./stack init`. Missing or empty dashboard passwords are rejected.
Existing installations with a missing or empty baseline must set it before
applying or upgrading, including installations with a saved dashboard password.
A password changed in the dashboard remains authoritative over
that baseline; see [credential recovery](operations.md#dashboard-and-node-credentials).
Restore never prints credentials.

APT installations use `node-manager` in place of `./stack`. Their config is
`/etc/node-manager/config.json`; generated credentials and backups live
under `/var/lib/node-manager`, and chain data defaults to the dedicated
`/var/lib/node-manager/data/bitcoin` and
`/var/lib/node-manager/data/monero` directories. Run package commands
with `sudo`. A fresh package installation with no config or prior `.env` defaults
to `prune_mb: 10000`; an explicit config or existing generated value always wins.

Homebrew installations also use `node-manager`, without `sudo`. On macOS,
configuration lives at
`~/Library/Application Support/node-manager/config/config.json`, with
generated state and chain data in the sibling `state/` and `data/<node>/`
directories. Linuxbrew follows XDG: configuration defaults to
`${XDG_CONFIG_HOME:-~/.config}/node-manager/config.json`, while state
and data default below
`${XDG_DATA_HOME:-~/.local/share}/node-manager/`. Homebrew-managed
upgrades use `brew upgrade` explicitly.
Linuxbrew safely initializes empty default node directories with the container
UIDs after images are present. It does not take ownership of non-empty or custom
paths; correct those deliberately when the startup error identifies a mismatch.

The removed `dashboard.control` key is ignored in existing files. The dashboard
only reports new releases. APT upgrades automatically activate new release images
for running services using approved settings; Homebrew and GitHub updates remain
manual. See [Operations](operations.md#upgrading).

Use the command and paths for your installation:

| Install | Configuration to edit | Generated private state | Apply and check |
|---|---|---|---|
| Source | `config.json` in the extracted directory | `.env` and `backups/` beside it | `./stack apply && ./stack status` |
| Homebrew on macOS | `~/Library/Application Support/node-manager/config/config.json` | sibling `state/` directory | `node-manager apply && node-manager status` |
| Linuxbrew | `${XDG_CONFIG_HOME:-~/.config}/node-manager/config.json` | `${XDG_DATA_HOME:-~/.local/share}/node-manager/state/` | `node-manager apply && node-manager status` |
| APT | `/etc/node-manager/config.json` | `/var/lib/node-manager/.env` and `backups/` | `sudo node-manager apply && sudo node-manager status` |

The dashboard repeats the active host path in **Settings → Config**, directly
below Healthchecks. This is a read-only display from the installed host helper;
edit that file and use the apply command in the table when a setting is not
available in the dashboard.

Installation never runs setup. Run `./stack init` for source,
`node-manager init` for Homebrew, or `sudo node-manager init` for APT. This
noninteractive command downloads missing images and starts the default stack,
or preserves and starts a configuration you created beforehand. Save printed
node credentials in a password manager. Set the required dashboard password
before applying a hand-written configuration. Use global Settings to install another
node or enable launch at boot/login on APT and
Homebrew installations.
Internal Monero and Tari wallet-service passwords live only in `.env`; they are
not dashboard logins or personal wallet keys.

## config.json reference

Every key is optional. The template ([config.example.json](../config.example.json))
shows the overridable settings with their defaults:

```json
{
    "bitcoin": {
        "enabled": true,
        "active": true,
        "node_username": "admin",
        "data_dir": "./data/bitcoin",
        "dbcache_mb": 128,
        "prune_mb": 10000,
        "inbound_onion": false,
        "blockfilterindex": false,
        "mem_limit_mb": 0,
        "cpus": 0,
        "resource_profile": "off",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 2048,
        "rpc_expose": false,
        "rpc_bind_ip": "",
        "rpc_port": 8332
    },
    "monero": {
        "enabled": false,
        "active": true,
        "node_username": "admin",
        "data_dir": "./data/monero",
        "mem_limit_mb": 0,
        "cpus": 0,
        "resource_profile": "off",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 2048,
        "rpc_expose": false,
        "rpc_bind_ip": "",
        "rpc_port": 18081,
        "zmq_expose": false,
        "zmq_bind_ip": "",
        "zmq_port": 18083
    },
    "tari": {
        "enabled": false,
        "active": true,
        "data_dir": "./data/tari",
        "pruning_horizon": 10000,
        "resource_profile": "off",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 4096,
        "cpus": 0,
        "grpc_expose": false,
        "grpc_bind_ip": "",
        "grpc_port": 18142
    },
    "litecoin": {
        "enabled": false,
        "active": true,
        "node_username": "admin",
        "data_dir": "./data/litecoin",
        "prune_mb": 10000,
        "resource_profile": "off",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 2048,
        "cpus": 0,
        "rpc_expose": false,
        "rpc_bind_ip": "",
        "rpc_port": 9332
    },
    "dogecoin": {
        "enabled": false,
        "active": true,
        "node_username": "admin",
        "data_dir": "./data/dogecoin",
        "prune_mb": 10000,
        "resource_profile": "off",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 2048,
        "cpus": 0,
        "rpc_expose": false,
        "rpc_bind_ip": "",
        "rpc_port": 22555
    },
    "bitcoincash": {
        "enabled": false,
        "active": true,
        "node_username": "admin",
        "data_dir": "./data/bitcoincash",
        "prune_mb": 10000,
        "resource_profile": "off",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 2048,
        "cpus": 0,
        "rpc_expose": false,
        "rpc_bind_ip": "",
        "rpc_port": 8332
    },
    "zcash": {
        "enabled": false,
        "active": true,
        "node_username": "admin",
        "data_dir": "./data/zcash",
        "resource_profile": "off",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 4096,
        "cpus": 0,
        "rpc_expose": false,
        "rpc_bind_ip": "",
        "rpc_port": 8232
    },
    "dashboard": {
        "password": "",
        "onion": false,
        "bind_ip": "auto",
        "port": 80,
        "launch_at_boot": false
    },
    "host": {
        "thermal_limit_c": 0,
        "thermal_resume_c": 0
    },
    "notifications": {
        "telegram_bot_token": "",
        "telegram_chat_id": "",
        "healthchecks_url": "",
        "alert_new_block": false,
        "alert_new_block_monero": false,
        "alert_new_block_tari": false,
        "alert_new_block_litecoin": false,
        "alert_new_block_dogecoin": false,
        "alert_new_block_bitcoincash": false,
        "alert_new_block_zcash": false
    },
    "wallets": [
        {
            "name": "Binance (public)",
            "key": "34xp4vRoCGJym3xR7yCVPFHoCNxv4Twseo",
            "birthday": ""
        }
    ]
}
```

| Key | Default | What it does |
|---|---|---|
| `bitcoin.enabled` | `true` | Runs the Bitcoin node. Set false for a Monero-only installation. |
| `bitcoin.node_username` | `admin` | RPC username used by the dashboard and optional LAN wallet connections. Letters and numbers only. |
| `bitcoin.node_password` | auto-generated | Separate Bitcoin RPC password. Existing explicit or rendered credentials remain unchanged. bitcoind receives only a salted hash (`rpcauth`). Letters and numbers only; LAN exposure requires at least 24 characters. |
| `bitcoin.data_dir` | `./data/bitcoin` (source), managed data directory for APT/Homebrew | Where the blockchain lives on the host. APT uses `/var/lib/node-manager/data/bitcoin`; Homebrew uses the platform data path above. Relative source paths resolve from the repo root. |
| `bitcoin.dbcache_mb` | `128` | Bitcoin Core's UTXO cache size in MB. Size it to your RAM — see [Hardware](hardware.md#ram). Existing installs keep their rendered cache setting when this key is omitted; the Compose fallback remains 3000 for older `.env` files that predate this setting. |
| `bitcoin.prune_mb` | `10000` | Keeps 10,000 MiB of recent block files after full validation. Existing explicit configuration and rendered state remain unchanged. `0` remains an advanced manual archival setting; any nonzero value must be ≥ `550`. See [Pruned node](#pruned-node). |
| `bitcoin.inbound_onion` | `false` | `true` publishes a Tor onion service so your node accepts inbound connections and serves available blocks with no IP exposure. See [Inbound onion service](#inbound-onion-service). |
| `bitcoin.blockfilterindex` | `false` | Advanced archival-node compatibility setting. It is incompatible with pruning and is not needed for the default pruned watch-only balance scan. |
| `bitcoin.mem_limit_mb` | `0` (unlimited) | Optional memory cap on the bitcoin container, in MB. `0` = no limit (Docker's default). A cap turns a bitcoind memory blowup into a contained container restart instead of a whole-host OOM. **Size it above `dbcache_mb`** and leave additional room for Core's workload-dependent memory outside the cache. There is no universal fixed margin; monitor actual usage because a low cap can make bitcoind restart-loop. `configure.sh` warns if the cap is not above `dbcache_mb`. |
| `bitcoin.cpus` | `0` (unlimited) | Optional hard CPU cap on the bitcoin container, applied from container start and kept across upgrades, unlike a manual `docker update`. Same number format as `steady_cpus`; `0` = no limit. Applies during initial sync too, so a thermally or otherwise constrained host is never left uncapped while catching up. The resource profiler leaves operator-set limits alone. |
| `bitcoin.rpc_expose` | `false` | `true` exposes authenticated Bitcoin RPC to a trusted private LAN through the Tor container's TCP relay. See [LAN wallet RPC](#lan-wallet-rpc-opt-in). |
| `bitcoin.rpc_bind_ip` | `""` | Private IPv4 address assigned to this host for the opt-in RPC listener. Required when `rpc_expose` is true. Public, wildcard, loopback, malformed, and IPv6 addresses are rejected. |
| `bitcoin.rpc_port` | `8332` | Host port for the opt-in RPC listener (`1`–`65535`). The internal Core RPC port remains `8332`. |
| `bitcoin.resource_profile` | `off`; `compact` for a node selected by APT/Homebrew setup or installed from the dashboard | Initial sync is uncapped, then a host helper applies the configured steady caps after three spaced, healthy, current sync checks. Source first-start selections and existing nodes remain off unless configured. Explicit operator limits take precedence. |
| `bitcoin.steady_cpus` | `1` | Post-sync CPU ceiling when `resource_profile` is `compact`. A JSON number from `0.01` to `256` with at most two decimal places; fractional values such as `0.5` work. |
| `bitcoin.steady_mem_limit_mb` | `2048` | Post-sync memory ceiling in MiB when `resource_profile` is `compact`. A whole JSON number from `257` to `1048576`. Memory plus swap is capped to this same value, so extra swap cannot hide an undersized budget. |
| `monero.enabled` | `false` | Runs the pruned Monero node and adds its health card to the dashboard. |
| `monero.node_username` | `admin` | Digest-auth username for restricted Monero RPC. |
| `monero.node_password` | auto-generated | Separate Digest-auth password for restricted Monero RPC. LAN exposure requires at least 24 letters or numbers. |
| `monero.data_dir` | `./data/monero` (source), managed data directory for APT/Homebrew | Monero blockchain data on the host. APT and Homebrew use their platform data paths. The node always uses pruned storage. |
| `monero.resource_profile` | `off`; `compact` for a node selected by APT/Homebrew setup or installed from the dashboard | The same experimental per-node limit as Bitcoin. Sustained Monero operation at 2 GiB remains unqualified. |
| `monero.steady_cpus` | `1` | Monero's independent post-sync CPU ceiling; same format and range as Bitcoin. |
| `monero.steady_mem_limit_mb` | `2048` | Monero's independent post-sync memory ceiling in MiB; same format and range as Bitcoin. The view-only wallet service is additional. |
| `monero.cpus` | `0` (unlimited) | Monero's independent hard CPU cap; same semantics as `bitcoin.cpus`, including during catch-up sync. |
| `monero.mem_limit_mb` | `0` (unlimited) | Optional memory cap on the monero container, in MB; same semantics as `bitcoin.mem_limit_mb` without the dbcache sizing concern. |
| `monero.rpc_expose` | `false` | Publishes restricted Digest-auth RPC to one trusted private-LAN address. |
| `monero.rpc_bind_ip` | `""` | Private IPv4 address assigned to this host. Required when Monero RPC exposure is enabled. |
| `monero.rpc_port` | `18081` | Host port for the opt-in restricted RPC relay. |
| `monero.zmq_expose` | `false` | Publishes the Monero ZMQ feed to one trusted private-LAN address for software such as P2Pool. ZMQ has no application authentication. |
| `monero.zmq_bind_ip` | `""` | Private IPv4 address assigned to this host. Required when ZMQ exposure is enabled. |
| `monero.zmq_port` | `18083` | Host port for the opt-in ZMQ relay. |
| `<node>.enabled` | `false` for Tari, Litecoin, Dogecoin, Bitcoin Cash, and Zcash | Adds that node to the configured stack and dashboard. At least one node must be enabled. |
| `<node>.active` | `true` | Starts the configured node. Dashboard deactivation changes this to `false` while preserving data and configuration. |
| `<node>.data_dir` | `./data/<node>` (source), managed data directory for APT/Homebrew | Host directory for that chain's data. APT uses `/var/lib/node-manager/data/<node>`; Homebrew uses its platform data path above. |
| `<node>.resource_profile` | `off`; `compact` for APT/Homebrew default setup selections or dashboard installs | Syncs uncapped, then applies that node's steady ceiling after repeated healthy sync checks. |
| `<node>.cpus` | `0` (unlimited) | Hard CPU cap for Tari, Litecoin, Dogecoin, Bitcoin Cash, or Zcash; same semantics as `bitcoin.cpus`. |
| `<node>.steady_cpus` | `1` | Independent post-sync CPU ceiling for Tari, Litecoin, Dogecoin, Bitcoin Cash, or Zcash. |
| `<node>.steady_mem_limit_mb` | `2048` for Litecoin, Dogecoin, and Bitcoin Cash; `4096` for Tari and Zcash | Independent post-sync memory-plus-swap ceiling for those base nodes. Tari's separate wallet sidecar is not included. This is a budget, not a qualified minimum. |
| `tari.pruning_horizon` | `10000` | Positive retained-block horizon for Minotari's pruned state. |
| `tari.grpc_expose`, `grpc_bind_ip`, `grpc_port` | `false`, `""`, `18142` | Optional unauthenticated Tari mining gRPC for Pithead on a trusted private LAN or WireGuard link. It is separate from the internal authenticated view-only wallet service. |
| `litecoin.prune_mb`, `dogecoin.prune_mb`, `bitcoincash.prune_mb` | `10000` | Recent block-file target in MiB. Chainstate and other state are additional. |
| `litecoin.rpc_port`, `dogecoin.rpc_port`, `bitcoincash.rpc_port` | `9332`, `22555`, `8332` | Default host port for each opt-in authenticated private-LAN RPC proxy. Each also has `rpc_expose` and `rpc_bind_ip`. |
| `zcash.rpc_expose`, `rpc_bind_ip`, `rpc_port` | `false`, `""`, `8232` | Optional authenticated read-only dashboard proxy. Native cookie-authenticated Zakura RPC stays internal and is never published. |
| `litecoin.node_password`, `dogecoin.node_password`, `bitcoincash.node_password`, `zcash.node_password` | auto-generated | Separate RPC or proxy passwords. LAN exposure requires at least 24 letters or numbers. |
| `dashboard.password` | generated on new `init` | Required dashboard password, 12–128 letters or numbers. New installs generate one 32-character value, persist it in mode-600 `config.json`, and print it once during `init`; configure rejects missing or empty values. Existing saved dashboard hashes remain authoritative until deliberately recovered; see [Operations](operations.md#dashboard-and-node-credentials). |
| `dashboard.public_url` | `""` | Optional exact HTTPS origin for a host-managed reverse proxy, with no path, query, fragment, or credentials. When set, `dashboard.password` must be 12–128 letters or numbers and `dashboard.bind_ip` must be `127.0.0.1` or `::1`. This does not configure TLS, ACME, VPN, or the host firewall. |
| `dashboard.onion` | `false` | `true` publishes the dashboard as a Tor onion service. It cannot be combined with `dashboard.public_url` — see [Notifications & Remote Access](notifications.md#dashboard-over-tor). |
| `dashboard.bind_ip` | `auto` | Select the default network interface's RFC1918 IPv4 address each time configuration is rendered, falling back to `127.0.0.1` when no supported private interface is available. Set loopback for access only from this machine, or an explicit private host address. Public and wildcard binds are rejected; `::1` is accepted only with `dashboard.public_url`. |
| `dashboard.port` | `80`; `8080` for a fresh Homebrew install | Host TCP port for the dashboard (`1`–`65535`). Edit this key before setup or apply it later when the default conflicts with another service. |
| `dashboard.launch_at_boot` | `false` | Start the configured application after reboot for APT, or after login for user Homebrew services. Global Settings applies this through the host helper, including container restart policy; disabling it leaves the current application running and uses up to five retries for crashes without startup after a Docker restart. Source installs without a managed service report this control as unavailable. |
| `host.thermal_limit_c` | `0` (off) | Opt-in host thermal throttle (Linux, `/sys/class/thermal`). At or above this temperature the resource-profile timer halves running node containers' CPU caps each pass toward a 0.25-CPU floor, instead of anything stopping them. `50`–`110`. Actions are logged to `/state/events.log` (`./stack events`). |
| `host.thermal_resume_c` | `0` (limit − 10) | Temperature at or below which the pre-throttle CPU caps are restored exactly, including unlimited. Must be below `thermal_limit_c`. |
| `notifications.telegram_bot_token` | `""` (off) | With `telegram_chat_id`, enables Telegram alerts (node down/recovered, sync complete, disk low) — see [Notifications](notifications.md#telegram-alerts). |
| `notifications.telegram_chat_id` | `""` (off) | The chat that receives alerts. |
| `notifications.healthchecks_url` | `""` (off) | Healthchecks.io ping URL for a dead-man's switch — see [Notifications](notifications.md#healthchecksio-dead-mans-switch). |
| `notifications.alert_new_block` | `false` | `true` sends a Telegram alert on each new block once synced (~144/day). |
| `notifications.alert_new_block_monero` | `false` | Monero's independent new-block Telegram alerts once synced (~720/day). The legacy `alert_new_block` key continues to mean Bitcoin. |
| `notifications.alert_new_block_<node>` | `false` | Independent new-block alerts for `tari`, `litecoin`, `dogecoin`, `bitcoincash`, or `zcash`, sent only after that node is synced. |
| `wallets` | Binance public example for source; empty for a pruned APT/Homebrew install | First-start **seed** for watch-only balances — each `{ name, key, birthday? }`, where `key` is a public extended key, public descriptor, or address. The example is clearly marked and excluded from personal totals. Set `[]` to opt out. After first start the saved dashboard list is authoritative. Personal wallet views and changes require dashboard authentication. See [Watch-only](watch-only.md). |

### Custom data directories on APT installs

The dashboard's host helpers run inside a strict systemd sandbox that can
write only to `/etc/node-manager` and `/var/lib/node-manager`.
Installing a node from the dashboard with a data folder outside those paths
(for example under `/home`) is rejected until you extend the sandbox for that
path on the host — an explicit operator decision:

```bash
sudo systemctl edit node-manager-node-control node-manager-port-control
```

```ini
[Service]
ProtectHome=false
ReadWritePaths=/home/nodes
```

`ProtectSystem=strict` stays in effect, so everything else remains read-only;
`ProtectHome=false` is needed only for paths under `/home`. Nodes already
running from such a directory keep working either way — only dashboard-driven
installs and applies touch the host paths.

`configure.sh` applies the defaults above for any omitted key, and generates
strong random RPC credentials on first run (reused on later runs).
Starting and applying use local images only. See [Tor-only networking](privacy.md)
for the boundary between host downloads and Tor-only runtime traffic.

Fresh installs generate independent Bitcoin RPC, Monero daemon RPC,
Monero wallet RPC, Tari wallet RPC, Monero wallet-file, and Tari native wallet
startup passwords. Read them locally from the mode-600 `.env`; upgrades preserve
existing credentials instead of forcing a rotation. `TARI_WALLET_RPC_USER` and `TARI_WALLET_RPC_PASSWORD`
authenticate the dashboard to the internal wallet gRPC service, while
`TARI_WALLET_PASSWORD` protects native wallet startup. None is a LAN connection
credential, and the wallet port is not published. Tari v6.1 still stores the
private view key in its native SQLite database, so the wallet password does not
provide encryption at rest; use encrypted host storage when that is required.

## Resource caps

Fresh APT and Homebrew default installs write these values into each selected
node's section. Existing nodes and source first-start selections keep
`resource_profile: "off"` unless explicitly configured. Installing a new node
from the dashboard selects `compact` in every installation mode. Automatic
handoff is provided by the APT host timer or the optional Homebrew background
service. The `steady_*` values have
no effect while the profile is `off`.

```json
{
    "bitcoin": {
        "resource_profile": "compact",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 2048
    },
    "monero": {
        "resource_profile": "compact",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 2048
    },
    "litecoin": {
        "resource_profile": "compact",
        "steady_cpus": 1,
        "steady_mem_limit_mb": 4096
    }
}
```

Edit the installation's existing `config.json` to change those fields; keep its
other settings. Then run:

```bash
node-manager apply  # use sudo for an APT install
```

Tari, Litecoin, Dogecoin, Bitcoin Cash, and Zcash base nodes use 4096 MiB by default;
Bitcoin and Monero use 2048 MiB. For example, `steady_cpus: 2` and
`steady_mem_limit_mb: 3072` give that node a
2-core, 3-GiB post-sync ceiling. Changing a cap recreates the affected container
and rechecks sync before applying the new values. The chain data stays in its
volume. Set `resource_profile` to `"off"` and apply to stop automatic capping.
Tari's view-only wallet sidecar has a separate fixed 0.5 CPU / 512 MiB container
ceiling. It is additional to the Tari node budget and has not completed sustained
resource qualification.

With all seven nodes selected, the base-node ceilings total 7 CPUs and 24 GiB.
The Monero and Tari wallet sidecars add 1 CPU and 1 GiB together, for container
ceilings totaling 8 CPUs and 25 GiB before Tor, dashboard, Docker, filesystem
cache, and host overhead. These figures add configured ceilings; they are not
measurements of actual use or a qualified host minimum.

APT uses `node-manager-resource-profile.timer`; Homebrew's user service
runs the same helper. It never runs inside the dashboard or mounts the Docker
socket there. Initial sync remains
uncapped by this profile. Explicit manual Docker limits and the legacy
`bitcoin.mem_limit_mb` cap take precedence, including during sync.

After three spaced, healthy, current sync checks, the helper leaves 256 MiB of
headroom below the configured memory ceiling. It may cleanly restart a synced
node once to release sync memory before rechecking. If usage stays above that
threshold (1792 MiB at the default 2048 MiB ceiling), it leaves the node
uncapped and records why in the system journal. A Bitcoin cache that leaves
less than 256 MiB below the configured memory ceiling also blocks handoff;
lower `bitcoin.dbcache_mb` explicitly before retrying.
Recreated containers are qualified again, so an upgrade can briefly return
them to uncapped operation. The profile is a budget, not a guarantee that a
workload fits; see [hardware estimates and test evidence](hardware.md).

## Dashboard settings

The gear menu contains **Nodes**, **Launch at boot/login**, **Appearance**,
**Dashboard password**, **Telegram**, and **Healthchecks** settings. **Nodes**
installs an available chain through the host helper using versioned project images and
default storage; LAN interfaces remain closed. It shows installation progress
and errors. Pending external configuration changes appear here for explicit
approval; an automatic boot continues with the last approved configuration. Appearance and notifications use
**Save**. Block alerts and LAN endpoints belong to each node instead: open a
tower and find separate **Alerts** and **LAN access** sections under **Node
controls**, after the wallet section and before deactivation.
Block alerts are off by default.

Under **Telegram → Alert preferences**, choose whether to notify when the monitor
starts, a node goes offline or recovers, synchronization completes, disk space
is low, or a dashboard update is available. These alerts default to enabled.
The offline delay defaults to 3 minutes (1–60); the free-space threshold defaults
to 50 GiB (1–1,048,576). Changes are saved in the dashboard volume and survive
reboots. Initial sync remains quiet until completion. Healthchecks and dashboard
update discovery continue independently of these Telegram preferences.

New-block options include approximate daily notification volumes: Bitcoin and
Bitcoin Cash 144, Monero and Tari 720, Litecoin 576, Dogecoin 1,440, and Zcash
1,152. These use each chain's target block interval; several blocks found in a
single check share one alert, so actual message totals can be lower.

Changing the dashboard password requires the current value and the new password
twice. The new value must contain 12–128 characters and no
control characters. The dashboard stores only a salted PBKDF2 password hash in
`/state/dashboard_auth.json` inside the private `dashboard_state` volume. This
saved login takes precedence over `dashboard.password` and the rendered `.env`;
the old password stops working immediately. Reload and sign in with the new
password, and all browser sessions are signed out. RPC credential reveal and
reset require a login from the last 15 minutes. Browser sessions expire after
30 minutes without an authenticated request or 24 hours after sign-in,
whichever comes first. Sessions are held in memory and are cleared on restart.
Recovery is covered in
[Operations](operations.md#dashboard-and-node-credentials).

Each node's settings let you enable or disable its LAN RPC listener and, for
Monero, its ZMQ listener, and choose the port. It reuses the node's configured
private LAN address, or the dashboard's configured private LAN address for a
first enable. Addresses stay in server configuration. If neither address is
private, set one there before enabling a port. Public, wildcard, loopback, and
IPv6 listener addresses are rejected. RPC exposure also requires a strong saved
node password. Monero ZMQ and Tari gRPC have no application authentication, so
enable them only on a trusted LAN or WireGuard link.

Changes stay in the node form until saved. Alert-only changes use **Save**. Port
changes use **Save & restart**, with affected services shown above the button.
The dashboard briefly reconnects while the host helper validates the bind,
updates `config.json` and `.env`, and restarts affected services. Chain data and
sync state are preserved. If the port is occupied, the panel names the listener
and retains the edits for correction.

After setting a dashboard password, an enabled authenticated LAN RPC endpoint
offers **Show password**, which explicitly
reveals that node's username and password. The general status response never
contains the secret. To rotate it, type `reset`; the host creates a new
48-character hexadecimal password, writes it to the private host configuration and `.env`, and
restarts the affected services. Reveal the new login and update every external
client. Reset is unavailable when the node is inactive. Tari gRPC and Monero
ZMQ do not display made-up credentials.

For APT installs, `node-manager init`, `up`, and `apply` enable
`node-manager-port-control.timer`. Source installs reuse the host
agent for authenticated trusted-LAN controls. On
macOS source installs, setup starts a session helper with a private
`host-service.log`; Homebrew registers the user service automatically. No host
control runs inside the dashboard, and it has no Docker socket. Homebrew uses
`node-manager host-service` through `brew services` for these requests. Port settings persist across reboot because the host
configuration is updated.

The Appearance section includes **Label towers**, off by default. It saves the
appearance preference in the same private dashboard override file, so it
survives restarts. Empty nodes keep a clickable name until their first block appears.
With labels enabled, each tower shows its coin price and
the next block number above it. Installed towers arrange themselves to fit the
screen and build upward in cube layers with each node's sync progress.
Shorter syncing towers take foreground positions to keep each node visible. The
bases continue below the screen; a temporary connection failure keeps the last
known height. Deactivated nodes remain visible in grey.
**Default currency** starts at USD and applies to those prices and to a panel
when opened. Panel currency selectors can still change the current view.
Resetting to server configuration hides the names and restores USD.

Node cards display configured host RPC/ZMQ endpoints under **Open ports**.
Disabled services have no endpoint, and disabling a node disables its relays.
This view describes configuration; it is not a live port scan. Internal Monero
administration RPC is never shown as a wallet connection endpoint.

## Applying changes

The gear menu installs nodes and changes launch at boot/login where supported,
Appearance, Telegram, Healthchecks, and the dashboard password. A node's own
settings change its block alert and LAN listeners. Dashboard-saved notifications use a persistent JSON override; see
[Dashboard settings](notifications.md#dashboard-settings). Personal wallets are
managed by the watch-only card. Dashboard mutations require authentication.
Authenticated changes also work from the onion dashboard.

### Optional advanced wallet scans

Monero view-only wallets are managed in the dashboard, not in `config.json`.
Use the Monero card to submit a mainnet primary address and private view key, with
an optional restore height and signed key-image JSON. Never place a view key,
private spend key, seed phrase, or key images in this tracked configuration.
See [Watch-only balances](watch-only.md#monero-view-only-wallets).

The Tari card manages one native view-only wallet outside `config.json`. Enter a
name, 64-character public spend key, 64-character private view key, and optional
`YYYY-MM-DD` birthday; blank scans from genesis. A seed and private spend key are
rejected. The sidecar-only `tari_wallet_data` volume contains its owner-only native
database and archived removals or failed imports. The shared `tari_wallet` volume
contains only control files and public metadata. The dashboard keeps roster metadata in
`/state/tari_watch.json` and removes the mode-600 key handoff after native wallet
creation. Do not put either key in `config.json`, Git, a URL, or a shell command.

Litecoin and Bitcoin Cash watch lists are also dashboard state rather than
`config.json` settings. They accept one public address per row and persist their
rosters, hourly confirmed-UTXO cache, and saved balance snapshots in the private
dashboard state volume. Their sourced public examples are created automatically
on fresh state: Linux Mint for Litecoin and Bitcoin Cash Node for Bitcoin Cash.
The Monero General Fund is a received-only example because it has no signed key
images. All examples are excluded from personal totals. Dogecoin and Zcash show
**Wallet balance not available** and no public reference address. Tari imports
one view-only wallet and has no bundled example.

Keep the following in the server file: Bitcoin storage paths, pruning, indexes,
cache and memory limits, RPC credentials and LAN exposure, `inbound_onion`, and dashboard
password, bind address, and onion publication. They change storage, process
limits, access, or host privileges and require an explicit server-side apply.

```bash
nano config.json
./stack apply    # re-renders .env and recreates only the containers that changed
```

## Pruned node

The default `prune_mb` runs with a fraction of archival disk. The setting limits
retained block files, not the whole data directory. The node still downloads and
**fully validates** every block before discarding old block files:

```json
"prune_mb": 10000
```

keeps 10,000 MiB of recent blocks. With the live chainstate currently around
11 GiB plus indexes, wallet data, logs, and working headroom, plan for 28–56 GiB
total. Initial sync still transfers the whole historical chain, currently
hundreds of GB (roughly 900 GB), and can take days over Tor.
`configure.sh` rejects values between 1 and 549 because bitcoind does.

Know the trade-offs before enabling:

- **Enabling is one-way-ish.** Turning pruning on for an existing full
  datadir prunes it down in place. Going back to `prune_mb: 0` later
  requires re-downloading the entire chain.
- A pruned node can't serve historical blocks to other software. Software that
  requires archival blocks or `txindex` needs a separately planned archival node.
- The dashboard's "Node Data Size" will show the pruned footprint.

Apply like any other change: edit `config.json`, `./configure.sh`,
`./stack apply`.

## Inbound onion service

The node is outbound-only by default. Set `inbound_onion: true` to accept
inbound connections over a Tor onion service. bitcoind
registers an onion service over Tor's control port, so other nodes
can fetch blocks from you **without your IP ever being visible** — you
contribute to the network from behind Tor. The cost is extra outbound bandwidth
serving the blocks retained by the node.

Mechanics (already wired, just flip the flag): tor exposes a cookie-authed
control port on the internal network; the tor data volume is mounted
read-only into the bitcoin container, whose process joins tor's group to
read the cookie; bitcoind gets `-listen=1 -listenonion=1 -torcontrol=...`.
Confirm it worked with:

```bash
./stack logs bitcoin 2>&1 | grep "tor: Got service ID"
```

No host port is opened — inbound arrives through the Tor network only.

## LAN wallet RPC (opt-in)

To connect a wallet application on a trusted private LAN, set an address assigned
to the node host and optionally choose a host port:

```json
"bitcoin": {
    "node_username": "wallet",
    "node_password": "usealongrandomvalue123456",
    "rpc_expose": true,
    "rpc_bind_ip": "192.168.1.117",
    "rpc_port": 8332
}
```

Run `./stack apply`, then point the wallet at `http://192.168.1.117:8332`
with `node_username` and `node_password`. If credentials were auto-generated,
read `BITCOIN_RPC_USER` and `BITCOIN_RPC_PASSWORD` from the mode-600 `.env`.

The listener belongs to the Tor container, which forwards TCP to Core on the
internal Docker network. Core keeps its existing `rpcauth` authentication and
Docker-only `rpcallowip`; the Bitcoin container gains no host port or external
route. RPC is unencrypted HTTP. Use it only on a trusted LAN and do not forward
the port through a router. Disable it by setting `rpc_expose` to `false` and
running `./stack apply`.

## Reusing an existing node

Already have a synced chain? Point `data_dir` at it and skip the
week-long sync:

```json
"data_dir": "/home/you/.bitcoin"
```

Two requirements:

- The directory must be **owned by uid 1000** (the first user on most
  Ubuntu installs) — the bitcoin container runs as `1000:1000`.
- The chain must come from a compatible Bitcoin Core version — the datadir
  upgrades in place from older releases; this stack currently ships Core
  32.1. Downgrading to an earlier major version on this datadir is one-way
  and not supported.

The dashboard mounts the same directory read-only for its disk-usage
stats.

## What's deliberately not configurable

Static node settings apply to every deployment. Internal RPC bind/allow ranges live in
[build/bitcoin/bitcoin.conf](../build/bitcoin/bitcoin.conf). Tor-only
networking is enforced by an internal application network with only the Tor
container attached to an external bridge. There is no clearnet bypass.
