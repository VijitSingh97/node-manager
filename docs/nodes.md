# Supported nodes

Node Manager supports Bitcoin Core, Monero, Tari, Litecoin, Dogecoin,
Bitcoin Cash Node, and Zcash through Zakura. Select any non-empty combination in
setup. Each selected chain gets a separate data directory and resource profile.
RPC credentials are separate for each authenticated endpoint; the dashboard has
its own password.

Upgrades from `bitcoin-starter-stack` move the managed directories to the
`node-manager` paths and leave compatibility symlinks behind; chain data is
never copied or relocated across filesystems.

## Storage and resource planning

Pruning limits retained history, but every node still downloads and validates
its chain. A block-retention setting does not include chainstate, indexes, logs,
temporary compaction space, or future growth.

| Node | Default retained history | Disk planning allowance | Sync budget | Post-sync compact budget | Qualification |
|---|---:|---:|---:|---:|---|
| Bitcoin Core | 10,000 MiB of block files | 28–56 GiB total | Uncapped; plan around 2 CPU / 4 GiB RAM | 1 CPU / 2 GiB | Short capped trial passed; 24-hour qualification pending |
| Monero | Native pruned mode | 120–150 GiB; upstream reported about 105 GiB pruned on October 5, 2026 | Uncapped; plan around 2 CPU / 4 GiB RAM | 1 CPU / 2 GiB, plus its wallet service | Not yet qualified |
| Tari | 10,000-block pruning horizon | No current measured total; use an expandable volume and measure after first sync | Uncapped; plan around 2 CPU / 4 GiB RAM | 1 CPU / 4 GiB for the node, plus a 0.5 CPU / 512 MiB view-only wallet sidecar | Node startup and native wallet lifecycle/authentication proven; funded pruned full-sync balance and sustained caps not qualified |
| Litecoin | 10,000 MiB of block files | Provisional 28–56 GiB; current full-sync total is not measured | Uncapped; plan around 2 CPU / 4 GiB RAM | 1 CPU / 4 GiB | Binary and runtime startup proven; full sync not qualified |
| Dogecoin | 10,000 MiB of block files | Provisional 28–56 GiB; current full-sync total is not measured | Uncapped; plan around 2 CPU / 4 GiB RAM | 1 CPU / 4 GiB | Binary and runtime startup proven; full sync not qualified |
| Bitcoin Cash Node | 10,000 MiB of block files | Provisional 28–56 GiB; current full-sync total is not measured | Uncapped; plan around 2 CPU / 4 GiB RAM | 1 CPU / 4 GiB | Signed upstream binary and runtime startup proven; full sync not qualified |
| Zcash / Zakura | Pruned state with 10,000 retained blocks | Upstream's published snapshot was roughly 10 GiB when checked; plan 28–56 GiB on an expandable volume until a full sync is measured | Uncapped; plan around 2 CPU / 4 GiB RAM | 1 CPU / 4 GiB | Pruning, Tor bootstrap, and RPC proven; full sync not qualified |

All six non-Bitcoin adapters have passed native-daemon startup and dashboard
checks on an Apple Silicon Mac using Docker Desktop. These checks cover Tor
routing and published endpoint access; they do not establish full-sync disk
usage or sustained operation within the compact budgets. Linux runs the same
checks separately for every chain in CI.

The Litecoin, Dogecoin, Bitcoin Cash, and Zcash figures are planning allowances,
not measured usage. Tari has no defensible total-size estimate yet, so do not put
it on a fixed small volume. The dashboard shows live chain and free-disk values;
record the final size after the first complete sync and leave growth headroom.
The combined planning range for the six chains with byte estimates is roughly
260–430 GiB before growth, temporary compaction space, Docker images, and host
headroom. Tari is additional and unknown; a pruning horizon is a block count,
not a disk-size estimate.

Monero's base figure comes from its
[official remote-node guide](https://docs.getmonero.org/interacting/remote-nodes/).
Tari pruning follows its
[syncing and seeding specification](https://rfc.tari.com/RFC-0140_Syncing_and_seeding).
Bitcoin-family pruning retains a configured amount of recent block files while
keeping the live chainstate.

Budgets apply **per node**. Bitcoin plus Monero in compact mode therefore needs
up to 4 GiB for the daemons. Three of the newer nodes need up to 12 GiB. Add
memory and CPU for Tor, the Go dashboard, Docker, filesystem cache, the operating
system, and wallet sidecars. Tari's sidecar has its own 0.5 CPU / 512 MiB ceiling;
it is not included in the Tari base node's 1 CPU / 4 GiB budget and is not yet
qualified under sustained load. Sync one chain at a time on a small
host. A container cap on a larger test machine does not qualify a real VPS with
the same total RAM.

Selecting all seven nodes sets base-node ceilings totaling 7 CPUs and 24 GiB.
The Monero and Tari wallet sidecars add 0.5 CPU and 512 MiB each, making 8 CPUs
and 25 GiB of container ceilings before Tor, dashboard, Docker, filesystem cache,
and the host. This is cap arithmetic, not measured simultaneous use or a
qualified minimum machine.

Tari's native wallet database is owner-only but not encrypted by its wallet
password: Tari v6.1 stores the private view key in plaintext SQLite data. Use an
encrypted host disk or encrypted volume backing store when encryption at rest is
required. The key cannot spend, but it reveals wallet funds and history.

## Dashboard data

Panels share sync progress, block height, peers, disk space,
endpoint status, and price fields. A value the native daemon cannot supply is
shown as unavailable rather than estimated or copied from another chain. Tari
does not report an uptime or fee estimate through its status API; a zero pruned
height leaves its pruning status unknown until pruning has occurred.

Fee estimates retain their native units: Litecoin uses litoshi/vB, Dogecoin uses
DOGE/kB, and Bitcoin Cash uses sat/B. Bitcoin Cash reports one general estimate;
Litecoin and Dogecoin report 1-, 3-, and 6-block targets when enough observations
are available. An insufficient-history response is shown as unavailable.
Zcash displays its standard ZIP-317 fee separately from an estimate.

Wallet capabilities differ by chain:

| Node | Dashboard wallet view |
|---|---|
| Bitcoin | Public addresses, descriptors, and extended public keys; confirmed UTXOs come from the local node. |
| Monero | Primary address plus private view key, with optional signed key images. Without complete key images, the amount is received-only rather than a verified current balance. |
| Litecoin | Address-only confirmed UTXO snapshots from the local node. |
| Bitcoin Cash | Address-only confirmed UTXO snapshots from the local node. |
| Dogecoin | **Wallet balance not available** by default: the pruned daemon cannot discover an arbitrary address balance. With the opt-in `dogecoin.watch_wallet` (archival node, fresh unpruned sync, hours-long rescans, disk not yet measured here), public addresses are read from the local node's watch-only wallet and the Dogecoin Foundation address is shown as an example. |
| Zcash / Zakura | **Wallet balance not available** by default: Zakura's checkpoint-pruned mode does not retain the address index. With the opt-in `zcash.address_index` (fresh resync, slower and less secure upstream, disk not yet measured), transparent `t1`/`t3` addresses are read from the local node and the Zcash Community Grants address is shown as an example. |
| Tari | One native view-only wallet using a public spend key, private view key, and optional `YYYY-MM-DD` birthday. Blank birthday scans from genesis. No seed or private spend key is accepted. |

On fresh dashboard state, Monero includes the sourced Monero General Fund as a
received-only example, Litecoin includes Linux Mint, and Bitcoin Cash includes
Bitcoin Cash Node. Source installs may also seed a public Binance Bitcoin
address; pruned APT and Homebrew installs default to no Bitcoin example. Public
examples are excluded from personal totals. Tari has no example, because no organisation publishes a Tari view key, and accepts one
imported view-only wallet. Zcash adds the Zcash Community Grants example only with
`zcash.address_index`, and Dogecoin adds the Dogecoin Foundation example only with
`dogecoin.watch_wallet`.

Litecoin and Bitcoin Cash use their native `scantxoutset` RPC after sync. The
dashboard caches the confirmed result for an hour and records balance snapshots;
it does not reconstruct transaction history, spent outputs, or mempool activity.
New entries stay **Pending** until a scan completes. A failed refresh keeps the
last confirmed result and marks it **Stale** or **Error** rather than replacing it
with zero. Litecoin and Bitcoin Cash public examples are excluded from personal totals. No chain falls
back to an external block explorer.

## Default setup and adding nodes

Package installation does not start services or prompt. Run
`sudo node-manager init` for APT, `node-manager init` for Homebrew, or
`./stack init` for source. A fresh setup starts Bitcoin only, downloads missing
images, uses default data directories, and leaves node LAN interfaces closed.
The dashboard gets one generated 32-character password on a new install,
chooses a private LAN address automatically, and prints its URL and login once.
Use it on a trusted LAN. Configuration rejects missing or empty dashboard
passwords before applying changes.

Use **Settings → Node set → Install** to add a chain later. A confirmation dialog
shows the node's estimated post-sync disk use and fills in its default absolute
host data folder. It also lists the interfaces that node can expose—RPC, ZMQ, or
gRPC as applicable—with their default ports. Every interface starts closed;
opening one is an explicit choice in this dialog.

Choose a new or empty data folder. The host rejects symlinked folders and
locations that overlap application settings or other sensitive application paths;
it never changes ownership recursively on existing files. To reuse an existing
chain database, set its path in `config.json` and apply that configuration.

APT management helpers can write only inside the package’s managed directories.
Use the default `/var/lib/node-manager/data/<node>` path, or mount
another disk at that path. An arbitrary external path such as `/mnt/...` or
`/home/nodes/...` needs an administrator to grant that exact directory to the
node-control and port-control systemd services first. Homebrew uses the current
user’s filesystem permissions.

After confirmation, the host validates the folder and checks each enabled port
for conflicts before it stops or recreates anything. A rejected request leaves
the current stack running and reports the folder or port problem in Settings.
An accepted installation uses versioned project images and the normal host
Docker connection; it does not route the image download through a node
container. It retains existing nodes and data. The node appears in the overview
once the host confirms installation, and progress or errors remain visible in
Settings.

For another combination or storage location on first start, create
`config.json` before `init`; see [first-start customization](getting-started.md#customize-before-first-start).
Package data defaults to `/var/lib/node-manager/data/<node>`; Homebrew
uses user-owned Application Support/XDG data, and source uses `./data/<node>`.

Fresh managed installs use the `compact` resource profile: sync is uncapped,
then repeated healthy, current sync checks activate the table's per-node
ceiling with no extra swap. Source first-start selections and existing profiles
remain off unless configured; nodes installed later from the dashboard select
`compact`. These are editable budgets, not claims that every chain has passed
sustained operation within them.

View-only wallets are optional steps in their dashboard cards. Keep endpoint
credentials separate from personal view-only material; never enter a seed
phrase or private spend key. **Node controls**, after wallets, contains separate
**Alerts** and **LAN access** sections followed by deactivation. Credential reveal
and reset require a dashboard sign-in from the last 15 minutes.
Monero ZMQ and Tari gRPC have no password controls.

Global Settings also contains launch at boot/login, Appearance, Dashboard
password, Telegram, and Healthchecks. Launch at boot/login is managed by APT
and Homebrew installations; source installs show it as unavailable. Turning it
off does not stop running nodes; it controls future starts. The read-only
**Config** section immediately below Healthchecks shows the exact host
`config.json` path reported by the installed helper.

## Private-LAN endpoints

All node endpoints are off by default and accept only an IPv4 address assigned
to the host's private LAN. The host helper rejects public, wildcard, loopback,
malformed, conflicting, and already-used bindings before applying a change.

| Node | Default host port | Authentication and intended use |
|---|---:|---|
| Bitcoin Core | 8332 | Generated Bitcoin RPC login; trusted wallet apps |
| Monero restricted RPC | 18081 | Generated Digest-auth login; wallets and Pithead/P2Pool |
| Monero ZMQ | 18083 | No application authentication; trusted P2Pool host only |
| Tari gRPC | 18142 | No application authentication; Pithead remote Tari base node on trusted LAN/WireGuard |
| Litecoin | 9332 | Generated Litecoin RPC login |
| Dogecoin | 22555 | Generated Dogecoin RPC login |
| Bitcoin Cash Node | 8332 | Generated Bitcoin Cash RPC login |
| Zcash | 8232 | Generated login to the dashboard's authenticated read-only proxy; native Zakura RPC is never published |

The public P2P runtime stays behind Tor whether or not a LAN endpoint is
enabled. These LAN services are plaintext inside the trusted network. Never
forward them to the public internet.

The dashboard shows credentials only for the authenticated endpoints in this
table. Monero ZMQ and Tari gRPC are intentionally unauthenticated and never show
a username or password.

## Pithead remote nodes

For Monero, enable restricted RPC and, when P2Pool needs it, ZMQ. Enter the
server's private address, the configured ports, and the **Monero RPC** login in
Pithead. The dashboard login is unrelated. ZMQ has no password.

For Tari, enable gRPC on port 18142 and configure Pithead with `tari.mode:
remote`, this host, and the gRPC port. Pithead's current mining client uses
plaintext unauthenticated gRPC, so use only a trusted LAN or WireGuard link.
The allowlist supports node status, mining-template creation, and block
submission. The stack runs no miner or payout key. Its separate internal
view-only wallet service has no published port and is not part of the mining
endpoint.

Pithead's current settings are documented in its
[remote-node configuration](https://github.com/p2pool-starter-stack/pithead/blob/develop/docs/configuration.md#remote-tari-node).

## Deactivation

An authenticated dashboard user can deactivate one configured node. The host
helper gracefully stops only that daemon and its dependent wallet service,
keeps its data and configuration, and leaves its tower visible but inactive.
Activating it starts the same configured service again. At least one node must
remain configured; deactivation is different from deleting a chain or its data.
Reactivate the node before attempting an RPC credential reset.
