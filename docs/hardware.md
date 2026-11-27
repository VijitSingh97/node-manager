# Hardware Requirements

Reference deployment and test host: a mini PC with 4 logical CPUs (2 physical
cores), 8 GB RAM, and an NVMe SSD running Ubuntu Server 24.04.5.

## Sync versus normal operation

Use these **per-node planning estimates** when choosing a server. They are
headroom budgets, not measured average consumption or hard minimums:

| Node and phase | CPU estimate / budget | RAM estimate / budget | Default behavior |
|---|---|---|---|
| Bitcoin initial sync | Plan for 2 cores; validation can keep available cores busy. | Allow 4 GiB for headroom with the default 128 MiB cache. | No container CPU or RAM cap. |
| Monero initial sync | Plan for 2 cores; additional cores can help validation. | Allow 4 GiB for headroom. | No container CPU or RAM cap. |
| Tari, Litecoin, Dogecoin, Bitcoin Cash, or Zcash initial sync | Plan for 2 cores per syncing node. | Allow 4 GiB per node as a starting budget. | No container CPU or RAM cap. Sync one at a time on a small host. |
| Bitcoin after sync | 1 core of capacity; usually idle between validation and RPC work. | 2 GiB container budget. | Fresh APT/Homebrew default installs apply the cap after healthy sync checks. |
| Monero after sync | 1 core of capacity; wallet/RPC activity adds work. | 2 GiB container budget, still experimental. | Fresh APT/Homebrew default installs apply the cap after healthy sync checks. |
| Tari, Litecoin, Dogecoin, Bitcoin Cash, or Zcash after sync | 1 core per node. | 4 GiB container budget for Tari and Zcash, 2 GiB for Litecoin, Dogecoin and Bitcoin Cash. | Fresh APT/Homebrew default installs apply the cap after healthy sync checks; sustained operation remains unqualified. |

Smaller machines can work, with slower sync and less room for bursts. Uncapped
sync can use more than the planning estimate; it does not reserve memory or
protect the host from memory pressure. For two nodes syncing together, add
their budgets, or sync one at a time. Add space for the operating system,
Docker, Tor, dashboard, filesystem cache, and any view-only wallet scans.

The Monero RAM estimate follows its
[upstream recommendation of 4 GB or more](https://docs.getmonero.org/running-node/monerod-systemd/#assumptions).
The CPU and Bitcoin RAM figures are this project's planning allowances, not
upstream guarantees. Bitcoin's
[memory guide](https://github.com/bitcoin/bitcoin/blob/v32.1/doc/reduce-memory.md)
explains why lowering caches saves RAM but can slow initial sync.

The default post-sync caps are explicit and editable. Bitcoin, Monero,
Litecoin, Dogecoin, and Bitcoin Cash use `steady_cpus: 1` and
`steady_mem_limit_mb: 2048`; Tari and Zcash use `steady_cpus: 1` and
`steady_mem_limit_mb: 4096`.
See [resource caps](configuration.md#resource-caps) for changing them. A cap is
a ceiling, not an allocation: actual usage can be lower. Source installs and
existing profiles set to `off` do not enable automatic caps.

All seven base nodes therefore have configured ceilings totaling 7 CPUs and 18
GiB. The Monero and Tari wallet sidecars add 0.5 CPU and 512 MiB each, bringing
the container ceilings to 8 CPUs and 19 GiB. Add Tor, dashboard, Docker,
filesystem cache, and operating-system headroom. This is cap arithmetic, not a
measurement of actual simultaneous use or a qualified minimum host.

The dashboard warns before Activate or Start when the host's free memory or idle
CPU is below that node's budget (the sync budget until it has synced, then its
steady caps). The warning is advisory and never blocks the action.

## What we have measured

A short container-capped Bitcoin trial supports a small-host target: during
a 10-minute sample after warmup, the stack advanced 1,200 blocks with 15/15 RPC
checks succeeding, no HTTP failures, and no application-container swap, OOM
kills, or restarts. This trial did not cover a full initial sync, sustained full
mempool, or wallet-scan load. See the
[resource-cap experiment](https://github.com/VijitSingh97/node-manager/blob/5dc960cb91873bcc09f599805cdc5bf1d6ec103a/experiments/go-dashboard-results/mini-1-resource-caps-2026-10-29/README.md)
for the method, exact temporary caps, and the 2 CPU/4 GB profile results.

The trial's settings were caps, not recorded average CPU/RAM measurements.
The **1 CPU and 2 GiB per-node** profile remains experimental until a sustained
post-sync test passes for each daemon. The Bitcoin trial does not qualify Monero.
Two selected nodes need separate budgets, plus shared Tor/dashboard and host
overhead; a 2 GiB VPS cannot provide a full 2 GiB node allowance and operating
system headroom. See [Nodes](nodes.md) for qualification status.

The Monero wallet service is additional to the daemon budget. In a local idle
smoke test it used about **13.2 MiB RAM and 0.03% CPU**; Compose caps it at
**512 MiB and 0.5 CPU** so a large wallet scan cannot consume the daemon's full
allowance. This single local measurement does not qualify the whole stack on a
2 GiB VPS, and wallet size and scan activity can raise usage. Tari's wallet
sidecar has the same 0.5 CPU / 512 MiB ceiling, but no sustained-load
qualification.

## Disk

The default keeps 10,000 MiB of recent block files after validating the whole chain.
That target does not include chainstate, indexes, wallet data, logs, or headroom.

- **Default pruned node:** plan for 28–56 GiB total. The live chainstate is
  currently about 11 GiB, in addition to the 10,000 MiB block-retention target.
- **Archival node:** roughly 840 GiB today; 1 TiB is tight and 2 TiB provides
  practical growth room. Enable it manually with `prune_mb: 0`.
- **SSD required.** Chainstate access is random-I/O heavy; initial sync on a
  spinning disk takes weeks instead of days.
- **Pruned Monero:** about 105 GiB according to the
  [official documentation](https://docs.getmonero.org/interacting/remote-nodes/)
  as of October 5, 2026. Allow 120–150 GiB for working room and growth.
- **Litecoin, Dogecoin, and Bitcoin Cash:** each keeps 10,000 MiB of block files
  by default. Until a complete current sync is measured, allow a provisional
  28–56 GiB total on an expandable volume for each chain.
- **Tari:** the default pruning horizon is 10,000 blocks. There is no current
  measured total for this stack; use an expandable volume and measure the first
  completed sync rather than treating the horizon as a byte estimate.
- **Zcash / Zakura:** the pruned configuration retains 10,000 blocks.
  The published snapshot was roughly 10 GiB when checked; allow 28–56 GiB until
  this stack records a complete sync and compaction cycle.

The six chains with byte estimates total roughly 260–430 GiB before growth,
temporary compaction space, Docker images, and host headroom. Tari is additional
and unknown; do not convert its 10,000-block pruning horizon into a byte estimate.

The dashboard shows chain size vs. disk capacity so you can watch the
headroom shrink.

## Balance indexes

The balance index is on by default for Bitcoin, Litecoin, Dogecoin and Bitcoin Cash
installed from the dashboard (untick it in the install dialog to opt out). Each index
has its own dashboard volume, and the dashboard runs with 0.5 CPU however many indexes
are on. Existing installs keep their setting: nothing is turned on, seeded or restarted
on upgrade.

| Index | Disk | Dashboard memory limit | While seeding |
|---|---|---|---|
| Bitcoin | about 7.5 GiB, up to about 17 GiB during the seed | 1 GiB | one `dumptxoutset` seed; 27 GB free |
| Litecoin | about 4.5 GB (53.8 million coins on mini-1; [measurement](measurements/litecoin-index-mainnet.md)) | 1 GiB | one `dumptxoutset` seed; 27 GB free |
| Dogecoin | about 18.4 GB (202.1 million coins on mini-1; [measurement](measurements/dogecoin-index-mainnet.md)) | 1 GiB | node stopped briefly; free space for 3 times its chainstate |
| Bitcoin Cash | about 5.2 GB (58.8 million coins on mini-1; [measurement](measurements/bitcoincash-index-mainnet.md)) | 1 GiB | node stopped briefly; free space for 3 times its chainstate |

- Every index sets the same 1 GiB dashboard limit, whichever are on. Bitcoin Cash's
  32 MB blocks fit because a large block is applied in pieces. Seeds run one at a
  time, so the peak is one seed plus the followers of the other indexes. The
  followers are light, but dashboard
  memory with all four indexes ready and one seeding has not been measured; measure it
  on a host before relying on it.
- By cap arithmetic, four chains with their indexes on an 8 GB host are
  over-committed: Litecoin, Dogecoin and Bitcoin Cash each have a 2 GiB steady cap,
  Bitcoin 2 GiB, and the dashboard 1 GiB. Keep the chains you do not need
  deactivated, or leave their indexes off.
- A Dogecoin or Bitcoin Cash seed hardlinks the node's chainstate tables into
  `<data_dir>/utxo-seed-chainstate`; the copy is small, but the seed needs free space
  for three times the chainstate and the copy is removed once the seed finishes.
- The copy also holds the node's database log. A Dogecoin node that stops with a full
  cache leaves its whole cache in that log, up to a few hundred MB. The seed streams the
  log to a scratch store on the index volume, removed when the seed ends, so the log
  costs disk there rather than dashboard memory. In a test with a 256 MiB log, a whole
  seed peaked at about 270 MiB of Go heap, most of it the import's 128 MiB of sort
  buffers; reading the log into memory, as 1.42.2 and earlier did, peaked at about
  1.5 GiB.

## RAM

Fresh installs use a conservative **128 MiB** Bitcoin Core UTXO cache, controlled
by `dbcache_mb` in `config.json`. Existing installs keep their configured or
previously rendered value. Larger caches are optional tuning that can speed up
initial sync and reindexing:

| System RAM | `dbcache_mb` guidance |
|---|---|
| 2 GiB small-host target | Use 128 MiB. Existing users moving to 2 GiB must set `bitcoin.dbcache_mb` to `128` explicitly because upgrades preserve older, larger cache settings. |
| 4 GiB recommended target | Start with 128 MiB; raise it only if faster initial sync is worth the extra memory. |
| 8 GiB reference host | 128 MiB remains the default; 3000 MiB is an optional initial-sync tuning value. |
| 16 GiB+ | Larger values such as 6000 MiB are optional initial-sync tuning. |

Don't set the cache near total RAM. Bitcoin Core also uses memory outside the
UTXO cache, and usage varies by workload; leave room for Tor, the dashboard,
Docker, and the host operating system. The OOM killer is not a graceful
shutdown. Bitcoin Core's pinned
[memory-reduction guide](https://github.com/bitcoin/bitcoin/blob/v32.1/doc/reduce-memory.md)
explains the cache tradeoff and other memory controls. The optional container
memory setting is preserved for existing installations. Fresh package setup
installs use the experimental `compact` resource profile: sync without limits,
then apply the configured container limits (default 1 CPU / 2048 MiB) after
repeated fresh, healthy sync checks. Explicit operator limits take precedence. See
[configuration](configuration.md) for disabling that profile and its safety gates.

## CPU

Two CPUs per syncing node are a planning estimate, not a minimum. The short
capped trial supports a smaller target within the scope described above. Signature validation
parallelizes across CPUs during initial sync; after that the node mostly idles.

## Network

Initial sync still pulls the whole historical chain, currently roughly 900 GB,
through Tor and can take days. Steady state is a few GB per day. Bitcoin P2P
needs no inbound host port; incoming peers arrive through an onion service,
which is on by default and adds outbound bandwidth for the blocks served. The dashboard uses port 80 for APT/source installs and 8080 for
Homebrew installs. It chooses a private LAN address automatically, or accepts an explicit private
address for LAN access. Change `dashboard.port` to select another host port.
