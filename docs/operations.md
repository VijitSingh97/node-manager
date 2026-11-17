# Operations

## Day-to-day commands

Use `./stack` for operations; it prevents implicit image downloads and builds.
The explicit `pull` command uses the host's existing Docker settings. Package
installs use `node-manager` instead of `./stack` (with `sudo` for APT). Direct
`docker compose` examples run from a configured source checkout.

| Command | What it does |
|---|---|
| `./stack up` / `down` / `restart [svc]` | Start / stop / restart. Bitcoin Core gets up to 5 minutes to flush on stop — let it. |
| `./stack logs [svc]` | Follow logs (`tor`, `bitcoin`, or `dashboard`). |
| `./stack events` | Incident history (down/recovered, failed Healthchecks pings, monitor restarts) from `/state/events.log` — survives restarts and reboots. |
| `./stack apply` | Re-render `.env` from `config.json` and apply the changes. |
| `./stack pull` | Download prebuilt images using the host's Docker settings. |
| `./stack status` | Container + health summary; exits non-zero if anything is down. |
| `./stack doctor` | Read-only health report: deps, config freshness, disk, containers, sync state, tor bootstrap, onion addresses. |
| `./stack backup` / `restore [-y] <archive>` | See [Backup](#backup). |
| `docker compose exec -T bitcoin bitcoin-cli -datadir=/data getblockchaininfo` | Talk to the node directly (cookie auth, no credentials needed). |

## Health checks

Shared services and configured node services define Docker health checks. For
the core Bitcoin deployment:

- `tor` — SOCKS port accepting connections (5 min grace for bootstrap).
- `bitcoin` — RPC answers `getblockchaininfo`, authenticated with Core's
  `.cookie` file (30 min grace: RPC returns "warming up" during startup
  block verification).
- `dashboard` — unauthenticated `/healthz` on port 8000 inside the container
  must return HTTP 200. It reports application liveness without node or wallet
  details; node health is checked separately.

`docker ps` shows the state. Run `./stack doctor` or `node-manager doctor` for
a health report. From a source checkout,
`docker inspect --format '{{json .State.Health}}' "$(docker compose ps -q bitcoin)"`
shows recent probe output when diagnosing.

## Host readings

With **Node manager usage** ticked in Settings, the usage panel at the top left
lists the host's temperature, fan speed, uptime and network rate under the disk
bars and CPU and memory pies. The resource-profile host helper collects them
once a minute with the other host figures, so the dashboard container needs no
extra mounts or privileges:

- **Temperature** — the same `/sys/class/thermal` zones the thermal throttle
  reads. `CPU` is the hottest CPU package zone (`x86_pkg_temp`, `cpu-thermal`,
  `soc-thermal` or `TCPU`) and `Board` the hottest `acpitz` zone; other zones
  are not shown but still count for the throttle.
- **Fan** — every `fan*_input` under `/sys/class/hwmon`, including drivers such
  as `applesmc` that keep their readings in the hwmon `device/` directory.
  Headers reading 0 rpm are hidden while any fan spins, because unconnected
  headers read 0; when every fan reads 0 the panel shows `0 rpm`.
- **Uptime** — `/proc/uptime` on the host.
- **Network** — received and sent bytes per second, summed over the host's
  physical interfaces (those with a device in `/sys/class/net`, so loopback,
  Docker bridges, `veth` pairs and VPN tunnels such as `tailscale0` are left
  out). The rate is measured across the helper's existing one-second CPU
  sample, so it is a snapshot taken once a minute, not an average.

Each reading the host cannot provide shows `not available`: on Homebrew for
macOS (Docker runs in a VM, and the helper does not read the Mac's sensors),
in virtual machines without thermal zones, on hosts without hwmon fans, and on
CPUs that report their temperature only through hwmon (for example AMD's
`k10temp`), which show just the board reading when there is one. Only numbers
reach the dashboard: no interface names, addresses or sensor names.

## Startup and external configuration changes

The application starts only when you run `init` or `up`, unless **Launch at
boot/login** is enabled in global Settings. APT uses Docker's startup policy;
Homebrew uses its user service after login and waits for Docker. The host
management helper remains available independently of the application startup
preference. Turning the preference off leaves running nodes alone. Fresh
containers use `on-failure:5` while it is off and `unless-stopped` while on.
Existing installations preserve their previous behavior until explicitly changed.

Private `.approved-config.json` and `.approved.env` snapshots retain the last
approved host configuration. If `config.json` changes while stopped, a manual
start asks before applying it. A background start keeps the approved version;
global Settings offers the pending file for approval. Approval is tied to that
file revision, so a second edit requires a fresh review. `apply` is explicit
approval. On an older installation with no approval snapshot, the existing
rendered runtime is retained until the file is explicitly approved once.

## Dashboard and node credentials

Open the gear menu to change the dashboard login. Enter the current password,
then the new password twice. It must contain 12–128 characters and no control
characters. The old password stops working immediately; reload and sign in
with the new one. The dashboard stores only a salted PBKDF2 hash in
`/state/dashboard_auth.json` inside the `dashboard_state` named volume. That
saved login takes precedence over the baseline in `config.json` and `.env`.
Authentication is required for all dashboard sessions. Existing logins require
their current password before a change.
Changing the dashboard password signs out every browser session. RPC credential
reveal and reset require a dashboard login from the last 15 minutes; older
sessions are sent back to the login page. Browser sessions expire after 30
minutes without an authenticated request or 24 hours after sign-in, whichever
comes first. A "Remember me" login lasts 30 days and survives dashboard restarts
and upgrades until the password changes; other sessions are held in memory, so
a dashboard restart logs them out.

If the dashboard password is forgotten, set a new baseline
`dashboard.password` in the installation's `config.json`, then stop the
dashboard before touching its state. While it is stopped, remove only
`/state/dashboard_auth.json` from the correct deployment's `dashboard_state`
named volume. Keep every other file and never delete the volume. Run the
installation's normal apply/start flow to render `.env` and recreate the
dashboard; it then uses the new baseline. Compose project names determine the
actual volume name, so inspect the deployment before making this offline change
or ask the installer administrator to perform it.

Node credentials are in the selected tower's settings, after its wallet
section. Authenticate before revealing or resetting RPC credentials.
For an enabled authenticated LAN RPC listener, **Show password**
reveals that node's username and password only after an explicit click. To
rotate the password, type `reset`. The host helper creates a new 48-character
hex password, updates the private node configuration and `.env`, and restarts
the affected services. Reveal the new login and update every external client.
Reset is unavailable for an inactive node. Monero ZMQ and Tari gRPC have no
application authentication and therefore no password to reveal or reset.

## Upgrading

The dashboard checks for new Node Manager releases daily through Tor and shows
an informational notice. It cannot install or apply an update. Choose the same
installation method you used originally and back up before updating.

**Ubuntu / APT:**

```bash
sudo node-manager backup
sudo apt update
sudo apt upgrade
```

APT upgrades automatically download the new release images and recreate the
services that were running. Changed services may restart. A stopped deployment
stays stopped, and configuration edits still awaiting approval are not applied.
If Docker or the registry is unavailable, APT keeps the package installed and
reports activation as pending. After correcting the problem, retry with
`sudo node-manager package-upgrade`. Inspect `sudo node-manager status` and
`sudo node-manager doctor` if activation reports a failure. If activation partly
started and left a service stopped, use the specific `node-manager up` command
printed in the error before retrying. Recovery starts are explicit so later
upgrades cannot override a service you deliberately stopped. No volumes or old
images are deleted, and a failed partial activation never automatically downgrades
a node database.

**macOS / Homebrew:**

```bash
node-manager backup
brew update
brew upgrade node-manager
node-manager pull
node-manager apply
```

**GitHub download / source:** download the runtime archive from
[the latest release](https://github.com/VijitSingh97/node-manager/releases/latest).
Back up and stop the existing stack, replace its runtime files while preserving
`config.json`, `.env`, data directories, and named volumes, then run
`./stack pull && ./stack apply`. For a Git checkout, select the published release
tag yourself before pulling images and applying. Review release notes for data
migration requirements; node databases may not support downgrades.

`node-manager upgrade` (or `./stack upgrade`) only prints instructions. It does
not fetch, check out, download, or apply a release. Installation and container
downloads use the host connection; see [Privacy](privacy.md#downloads-and-upgrades).

The first package release is for fresh installations. Do not run
`sudo node-manager up` beside an existing source checkout: the package refuses to
replace containers whose Compose labels point at another installation. Keep an
existing production node source-managed until a manual migration is planned.
Package mode uses its own `node-manager-apt` Compose project, so stopped
source installations' named volumes are not adopted by a fresh package install.
That transition must stop and back up the old deployment, set
`bitcoin.data_dir` to the existing absolute chain path, and preserve the Compose
project's dashboard and Tor volumes. Package installation, upgrade, removal,
and purge never perform that migration or change those data paths.

Node images and dependencies remain pinned to reviewed versions and digests.
Dependabot proposes updates in pull requests. The daily dashboard check reports
Node Manager releases, rather than individual Bitcoin Core or other node releases.
Local development builds require a separately controlled build environment.

Older source installs may have an `@reboot` entry or a manually created
`node-manager-upgrade-agent.service`. The compatibility agent now handles only
validated node controls; it cannot update the application. The removed
`dashboard.control` setting is ignored. Keep the host helper running if you use
dashboard node management.

## Backup

```bash
./stack backup                          # writes backups/stack-backup-<ts>.tar.gz
./stack restore backups/stack-backup-<ts>.tar.gz
./stack apply
./stack restart dashboard               # reload restored dashboard state
```

APT installations use `sudo node-manager backup` and write archives under
`/var/lib/node-manager/backups`; restore and apply with `sudo` as well.

A backup holds the stack configuration and dashboard state, but excludes chain
data and encrypted Monero wallet files:

- `config.json` and `.env` (settings + credentials)
- the onion keys from the tor volume: the dashboard's (if enabled) and each
  Bitcoin, Litecoin and Bitcoin Cash onion service (`/var/lib/tor/<node>_onion`).
  Restore puts them back, restarts Tor, and restarts those nodes so they
  advertise the restored addresses. An `onion_v3_private_key` in an older
  archive is skipped, because bitcoind no longer creates its own onion service.
- the full `dashboard_state` volume: watch-only wallet roster and balance
  history (including entries that still need a rescan), event log, and
  dashboard-saved notification settings
  (`notification_settings.json`), plus the saved dashboard password hash
  (`dashboard_auth.json`)

The `monero_wallets` volume is intentionally excluded because its encrypted
wallet files contain private view-key material. After restoring onto a new
machine, remove each unavailable Monero row and add it again with its address
and private view key. Release archives and Debian packages never contain this
runtime volume.

Losing the onion keys means new `.onion` addresses, so back up after
enabling the dashboard onion or incoming connections. The chain itself is re-derivable from the
network — backing up roughly 900 GB is only worth it if a multi-day Tor resync
hurts more than the storage does. If you do copy it: `./stack down`
first — a live copy of `chainstate/` is corrupt by construction.

## Monitoring integrations

- **Prometheus:** the dashboard serves `/metrics` (text format) — block
  height, headers, verification progress, peers in/out, disk, uptime,
  pruned flag, `bitcoin_node_up`, and (once synced) mempool size/bytes and
  `bitcoin_fee_sat_vb` for 1/3/6-block targets. It sits behind the same
  required dashboard authentication. Machine clients must send preemptive
  Basic credentials with username `admin` and the current dashboard password;
  the server does not send a Basic-auth challenge. For example,
  `curl --user admin https://node.example.com/metrics` prompts for the password
  locally. Browsers use session sign-in; use a cookie-free command-line client
  for Basic authentication.
- **Health and info endpoints:** off by default. Settings → **Actuator** has
  two independent switches, **Serve /actuator/health** and **Serve
  /actuator/info**; press **Save** to apply. They are JSON endpoints in the
  style of Spring Boot Actuator and read state the dashboard already keeps
  (the node probe that runs about once a minute, the balance index status and
  the last Tor price fetch), so a request never calls a node.
  - **Login.** The dashboard creates an actuator username and a random
    password (256 bits) once and keeps them in the dashboard state volume
    (`actuator-credentials.json`, mode 600). Settings → Actuator shows both
    with a **Copy** button, to signed-in administrators only. Both endpoints
    always require HTTP Basic auth with exactly this login when switched on.
    Without it, or with the wrong login, they answer 401 with a
    `WWW-Authenticate: Basic` challenge. The dashboard `admin` login and a
    dashboard session do not open them, and there is no anonymous access. A
    switched-off endpoint answers 404 whatever credentials are sent. Behind
    `dashboard.public_url`, repeated wrong logins from one client are
    throttled with a 429 and `Retry-After`, separately from dashboard
    sign-in. If the file is deleted, unreadable, or readable by group or
    others, a new password is generated on the next start; update your
    monitors then. Settings saved before this release with the old single
    switch on turn both endpoints on after the upgrade.
  - `GET /actuator/health` returns the overall `status` and `components`: HTTP
    503 for `DOWN`, 200 for `UP` or `DEGRADED`.
    - `dashboard`: always `UP`.
    - One entry per active node, with `details.state`:
      - `synced`: `UP`.
      - `syncing`: `DEGRADED`.
      - `busy`: `DEGRADED`. The node is being changed from the dashboard, or
        a balance index seed has paused Bitcoin RPC. This matches the window
        in which node-down alerts are held.
      - `down`: `DOWN`. Its RPC has not answered in the last 2 minutes.

      Deactivated nodes are left out.
    - `tor`: `UP` while the dashboard's Tor price fetch (every 5 minutes)
      succeeds, `DEGRADED` after it fails. This is indirect, since the price
      service can fail on its own, so it never reports `DOWN`.
    - `balance_index` (only when turned on): `UP` when `ready`, otherwise
      `DEGRADED`, with the index state in `details.state`. Wallet balances
      fall back to the slower scan meanwhile.

    The overall status is `DOWN` if any component is `DOWN`, `DEGRADED` if any
    is `DEGRADED`, otherwise `UP`.
  - `GET /actuator/info` returns:
    - `app`: `name`, `version`, and `install_kind` (`apt`, `homebrew`,
      `source`, or `unknown` without the host helper).
    - `chains`: one entry per installed node, with `name`, `active` and
      `status` (`UP`, `DEGRADED`, `DOWN`, or `OFF` when deactivated). While
      the node answers, the entry also carries `network`, `height`,
      `sync_progress` (0 to 1), `peers` and `version` where already known.
      Bitcoin, Litecoin, Dogecoin and Bitcoin Cash report `peers` and
      `version` once their tower has been opened. `network` is the node's own
      word (`main` for the Bitcoin family, `mainnet` for Monero); Tari and
      Zcash report none. Each entry also has `services`: every
      [private-LAN endpoint](nodes.md#private-lan-endpoints) the node can
      offer (Bitcoin, Litecoin, Dogecoin, Bitcoin Cash and Zcash RPC, Monero
      RPC and ZMQ, Tari gRPC), whether or not it is switched on:
      - `id`, `name`, `protocol` (`json-rpc`, `zmq` or `grpc`), `auth`
        (`basic`, `digest` or `none`) and `exposed`.
      - When `exposed` is true: `bind_address`, `port` and a ready-to-use
        `uri`, for example `http://192.168.1.117:18081` for Monero RPC,
        `tcp://192.168.1.117:18083` for Monero ZMQ and `192.168.1.117:18142`
        (host and port) for Tari gRPC. They come from the exposure the
        dashboard started with, the same values as the LAN ports in Settings.
      - When `auth` is `basic` or `digest` and the node is active: the
        `username` and `password` a LAN client sends, the same login Settings
        shows for that node's RPC. A deactivated node shows none.
        Zcash's is the login to the dashboard's read-only proxy. Monero ZMQ
        and Tari gRPC have no login, so they show `auth: none` and no
        credentials. A service that is not exposed carries no address and no
        credentials.

  **The info response contains the RPC passwords of every exposed LAN
  service.** There is one actuator login for both endpoints, so any client
  that can poll `/actuator/health` can also read `/actuator/info` and those
  passwords. If monitors must not see them, leave **Serve /actuator/info** off.
  Neither endpoint returns wallet data, onion addresses or host paths.
  A configured `dashboard.public_url` still limits which host name they answer
  on, except for Basic-auth clients on the private network, as for `/metrics`.
  For example, `curl -fsS --user actuator:PASSWORD
  https://node.example.com/actuator/health` exits non-zero when something is
  down, and `curl --user actuator https://node.example.com/actuator/info`
  prompts for the password.
- **Telegram / Healthchecks.io:** see [Notifications](notifications.md).
- **Fee sparkline:** the dashboard samples the next-block fee once a minute
  into an in-memory 24-hour series (reset on restart) and draws it under the
  fee row. Served as JSON at `/api/fees` behind the same authentication.
- **Bitcoin balance index:** with `bitcoin.utxo_index` enabled,
  `/api/utxo-index` returns JSON behind the same authentication: `enabled`,
  `state` (`off`, `not_seeded`, `seeding`, `catching_up`, `ready`, `degraded`
  or `invalid`), `reason`, `index_tip_height`, `node_tip_height`,
  `blocks_behind` and `seed_progress` (0 to 1). Heights and `blocks_behind`
  are `null` until the index has a tip and the node has answered once;
  `seed_progress` is `null` unless a seed is running. `ready` means the index
  is valid, idle and within 2 blocks of the node tip; a wallet balance comes
  from the index only at `blocks_behind` 0, otherwise the scan answers. Seed status adds `phase`
  (`waiting`, `importing`, `merging`, `paused` or `cancelling`; empty when no
  seed runs), `started_at`, `refusals` (a list of `guard` and `detail` for each
  failed seed precondition), `last_seed` (`outcome`, `finished_at`,
  `duration_seconds`) and `alerts_suppressed_until`, which is set only while
  node-down alerts are suppressed for a seed, for at most 15 minutes.

## Seeding the Bitcoin balance index

The Litecoin balance index (`litecoin.utxo_index`) seeds the same way, through
the same host helper, with `litecoin` after the command: `./stack utxo-seed
litecoin`, `./stack utxo-seed litecoin cancel` and `./stack utxo-index remove
litecoin`. Its seed time and size on mainnet are not measured yet. Only one
balance index seeds at a time; a second request is refused with the `lock`
guard until the first finishes.

With `bitcoin.utxo_index` enabled the index starts empty. On APT and Homebrew
installs it seeds itself: while the index has no seed (or its last seed failed),
the dashboard checks the seed guards once an hour, and when they pass it asks the
host helper (`node-control`) to create the seed pipe and request, the same
preparation `utxo-seed` does. A refused check stays in the index status with its
reason. A seed that starts and then fails, or is cut short by a dashboard
restart, is retried after 6 hours, then 24 hours, and not again after a third
failure until you start one; a guard refusal costs nothing and is re-checked
hourly. Cancelling a seed (`utxo-seed cancel`) stops automatic seeding until you
start one with **Seed now** or `utxo-seed`. The automatic seed holds node-down alerts
like a manual one. **Seed now** on the index card or in Settings starts a seed
through the same host helper. A source checkout has no host helper, so seed it by
hand. To seed it from the host, with the stack running and the node synced:

```
./stack utxo-seed        # asks for y; -y skips the question
./stack utxo-seed cancel # from another terminal
```

What happens:

- **Warning, first.** The seed takes about 20 minutes, longer if heat pauses
  it. Bitcoin RPC stops answering for about 5 minutes, then answers slowly
  until the seed ends. The index takes about 7.5 GiB afterwards and up to about
  17 GiB while it runs; the volume needs 27 GB free. Node-down alerts are held
  from when the dashboard starts the dump, for up to 15 minutes, and then
  resume normally; a successful seed ends the hold early, a failed or cancelled
  one keeps it to the cap because Core may still be hashing. Pause other heavy
  chains (for example Tari) first.
- **Checks.** The command refuses if `getrpcinfo` shows a `dumptxoutset` or
  `scantxoutset` already running. The dashboard then lists every failed
  precondition in `refusals` on `/api/utxo-index` and on the dashboard card:
  index state, hottest thermal zone at or below 75 C (an unreadable sensor
  refuses), a 0.5 CPU cap on the dashboard, 27 GB free, a synced and fresh
  node, no other dump or scan, no running watch scan, and a bitcoin data
  directory owned by the dashboard user (both containers run as uid 1000). The
  seed runs on Linux only.
- **No watchdog.** Before you start, make sure nothing on the host restarts
  unhealthy containers on its own, such as an autoheal-style watchdog. Compose
  marks bitcoind unhealthy for about 2.5 minutes during the dump, and a restart
  ends the seed. The stack itself never does this (see
  [Self-healing](#self-healing-a-deliberate-non-feature)).
- **Dump.** The command removes any stale `utxo-seed.dat*` in the bitcoin data
  directory, creates the FIFO `utxo-seed.dat.incomplete` there, and writes a
  single-use request into the dashboard state volume (stale after 120
  seconds). The dashboard acknowledges it within 60 seconds or the command
  removes the request and the FIFO and exits non-zero. The dashboard reads
  `dumptxoutset` straight from the FIFO, so no 9 GB dump file lands on disk.
  The command prints the phase (`waiting`, `importing`, `merging`, `paused`,
  `cancelling`) and progress until the seed ends, then removes the FIFO names
  (opening a still-parked dump once first so bitcoind is not left blocked).
  Ctrl-C only detaches; the seed keeps running and the FIFO names stay until
  the next `./stack utxo-seed`.
- **Heat and disk.** The dashboard pauses reading at 85 C and resumes at 72 C,
  and aborts below 2 GB free. Pausing stalls only the dump write.
- **Result.** On success the index sits at the dump's base block and follows
  new blocks from there. A cancel, an error, a dump that does not match the
  `dumptxoutset` result, or a dashboard restart during the seed discards the
  partial index and records `last_seed`; start again with the same command.

To time lookups with and without the index on public test wallets, run
`utxo-index bench`; the steps are in
[the lookup benchmark](measurements/lookup-bench.md).

Measured budget, from the first mainnet seed on a 2018 Mac mini with bitcoind
capped at 0.5 CPU and the Tari base node stopped (see
[the measurement](measurements/utxo-seed-mainnet.md)):

- **Time.** 1179 s (19.7 minutes), including 8 heat pauses during the write
  phase; each resumed. The peak was 86 C and the 88 C stack throttle never
  fired. The index was ready 10 seconds after the seed.
- **Bitcoin RPC.** Failed for about 306 s, then answered every sample at a mean
  of 106 ms against about 70 ms before the seed.
- **Disk.** 7.5 GiB (7,635 MiB) at the end and at least 16.7 GiB (17,117 MiB)
  at the peak, during the merge. The 27 GB free check is unchanged.
- **Dashboard memory.** Anonymous memory peaked at 289 MiB. The page cache
  fills the 1 GiB limit during the import and is reclaimed; that is expected
  and nothing is killed. While following blocks the dashboard used 13 MiB
  anonymous and 31 MiB in total.
- **Not measured.** A seed with every chain running. With every chain running
  that host read 73 C, above the 72 C resume point. After its thermal paste
  was renewed it reads a median of 66 C, with short spikes to 87 C. On a host
  that stays above 72 C, a paused seed may not resume until other chains are
  stopped.

When the card says "Needs a new seed" (`invalid`), balances come from the scan
and nothing re-seeds on its own. Run `./stack utxo-seed`; it resets the store
first. The reason is on the card and in `reason` on `/api/utxo-index`. The
causes are:

- the index format version changed (schema or Pebble format), or its metadata
  or tip cannot be read;
- the index was built for a different chain;
- a block does not fit the index, a spent coin is missing, or another integrity
  or follower error;
- a reorg deeper than the undo journal;
- the write failed because the disk is full while following; below 2 GB free
  the index only pauses and reports `degraded`;
- the index fell behind the blocks the pruned node still retains;
- the index directory cannot be created or the store cannot be opened.

Upgrades:

- With the option off, nothing changes at runtime.
- Turning it on adds the volume and the dashboard limits, and the index starts
  `not_seeded`.
- A later release that changes the index format makes the index invalid on
  start. There is no silent migration; re-seed.
- Rolling the dashboard back to an older image may find an index it cannot
  read. The dashboard then reports `invalid` ("cannot open the index store" or
  a format version reason), the scan answers, and a new seed resets the store.

Removing the index:

1. Set `bitcoin.utxo_index` to false in `config.json`.
2. Apply it: `./stack apply` from source, `node-manager apply` on Homebrew,
   `sudo node-manager apply` on APT. This recreates the dashboard without the
   volume; bitcoind is not restarted.
3. Remove the volume: `./stack utxo-index remove` from source,
   `node-manager utxo-index remove` on Homebrew,
   `sudo node-manager utxo-index remove` on APT. It prints the volume name and
   asks for `y`; pass `-y` to skip the prompt. It refuses while the option is
   still on or a container still mounts the volume, and says so when there is
   nothing to remove. On Linux, `df -h /var/lib/docker` before and after shows
   the space it frees.

By hand, the volume is `docker volume rm <project>_utxo_index`, where the
project is `COMPOSE_PROJECT_NAME` in the state directory's `.env`
(`/var/lib/node-manager/.env` on APT).

Removing the volume cannot be undone. If you turn the option on again over a
kept volume, the index resumes following from its stored tip, or becomes
invalid if it fell behind the node's retained blocks. After the volume is
removed it starts `not_seeded`.

Recovery:

- **Cancel** closes the dashboard's end of the FIFO. Core ends the dump with
  an RPC error (`AutoFile::write: write failed`) and keeps running. On mainnet
  Core first hashes the coin set for about 5 minutes before it writes
  anything; a cancel in that window ends the seed in the dashboard at once,
  but Core only stops when the hash pass is done, so RPC stays slow until
  then.
- **`./stack stop`, `down` and `restart`** of bitcoind, when `getrpcinfo`
  shows a dump, first cancel a running seed (waiting up to 15 seconds for the
  dashboard to let go of the FIFO) and then open the FIFO once (`timeout 5
  cat`). Without that, `bitcoin-cli stop` hangs while Core waits for a reader
  or is blocked writing to a paused one. If you stop bitcoind another way
  and it will not exit, run `docker compose exec -T bitcoin sh -c 'timeout 5
  cat /data/utxo-seed.dat.incomplete >/dev/null'` once.
- If the FIFO is replaced or removed before Core opens it, Core writes a
  regular file instead. The dashboard aborts the seed, but Core keeps writing
  that file (about 9 GB, within the 27 GB free check) until the dump ends.
- If a seed ever leaves `utxo-seed.dat` or `utxo-seed.dat.incomplete` behind,
  the next `./stack utxo-seed` removes both. The status file
  (`utxo-seed.json` in the state volume) and logs carry phases, counts and
  error classes only, never addresses, descriptors or script hashes.

## Self-healing (a deliberate non-feature)

Fresh installs use `on-failure:5` to retry a crashed container up to five times.
With launch at boot/login enabled, `unless-stopped` also restarts crashed
containers and brings them back after Docker restarts.
The remaining case — running but unhealthy — would need something with
Docker-socket access to act on, and a socket proxy is a bigger attack
surface than the failure it heals on a single-node stack. So: unhealthy
states are surfaced (`./stack status`, `docker ps`, Telegram node-down
alerts) and recovery stays a human decision — `./stack restart <svc>`.

## Troubleshooting

**Compose says `run ./configure.sh first`** — exactly that. `.env` is
missing or incomplete.

**Tor never reaches "Bootstrapped 100%"** — your network may block Tor.
Check `docker compose logs tor` for connection failures; a restrictive
firewall needs outbound 443/9001 open.

**bitcoind exits immediately** — almost always data-dir permissions. The
container runs as uid 1000; `ls -ld <data_dir>` should show your user (uid
1000) as owner. Also check `docker compose logs bitcoin` for the actual
error.

**Dashboard stuck on "Initializing"** — the dashboard can't reach RPC.
During startup that's normal (block verification can take minutes). If it
persists: `docker compose logs bitcoin` — and if you recently changed
credentials, make sure you re-ran `./configure.sh` *and*
`./stack apply` so both containers got the new values.

**The dashboard rejects the password from `.env`** — a password changed in the
gear menu takes precedence over that baseline. Use the changed password or the
offline recovery procedure in [Dashboard and node credentials](#dashboard-and-node-credentials).

**A wallet app fails after an RPC credential reset** — reveal the new login in
that node's tower settings and update the app. The previous password is no
longer valid.

**Node syncs but peer count is 0 after hours** — tor container unhealthy or
restarted with a new identity mid-session. `docker compose restart tor bitcoin`.

**Disk filling up** — the dashboard's disk row is the early warning.
Options: a bigger disk, or switch to a pruned node via `prune_mb` in
`config.json` — see [Configuration → Pruned node](configuration.md#pruned-node).
