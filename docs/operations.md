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
| `./stack events` | Event history from `/state/events.log` (node down and recovered, host actions, settings and sign-in changes; see [Event log](#event-log)) — survives restarts and reboots. |
| `./stack apply` | Re-render `.env` from `config.json` and apply the changes. |
| `./stack pull` | Download prebuilt images using the host's Docker settings. |
| `./stack status` | Container + health summary; exits non-zero if anything is down. |
| `./stack doctor` | Read-only health report: deps, config freshness, disk, containers, sync state, tor bootstrap, onion addresses. |
| `./stack backup` / `restore [-y] <archive>` | See [Backup](#backup). |
| `docker compose exec -T bitcoin bitcoin-cli -datadir=/data getblockchaininfo` | Talk to the node directly (cookie auth, no credentials needed). |

## Event log

The **Events** button (three lines, between the person icon and the gear) lists
what happened on this node set, newest first. Filter it by category or node, and
press **Show older events** to page back. It reads `/state/events.log`, the file
`./stack events` prints, so nothing is stored twice. The file keeps about 1 MB;
past that the dashboard drops the oldest half.

| Category | What is listed |
|---|---|
| Nodes | Node down and recovered, monitor online, activate and deactivate results, installs, deletions and data moves, and nodes that fail to start or that the host stopped |
| Sync and blocks | Initial sync complete, and new blocks when that node's block alert is on |
| Host | Thermal throttle and release, low disk, Tor watchdog steps, and dashboard or LAN listener address moves |
| Settings | Saved settings (the names of the changed fields, never their values), LAN port changes, launch at boot, the Tor network switches, and a new configuration applied with the nodes it restarted |
| Sign-in and credentials | Browser sign-ins with the client address, sign-outs, dashboard password changes, and RPC credential reveals and resets |
| Balance index | Turned on or off, seed started, finished, cancelled or failed, and verify results |
| Wallets | Watch-only wallets and addresses added or removed, without the name, key or address |
| Updates and restarts | Each dashboard start with its version, so an upgrade shows as a new version, and new releases |
| Alert delivery | Failed Healthchecks pings |

Events that need attention are labelled **Warning** or **Error**. `Host:` lines
come from the host helpers, so the panel checks them, and any line it does not
recognise, with the helpers' own `redact_last_line` rule: if the text mentions a
password, token, login, seed, key or onion address, or holds a word of 40 or
more characters or a Telegram bot token, the panel shows "Details hidden because
they may include a secret." instead. The Telegram copy of a `Host:` line is not
listed twice. Failed sign-in attempts are not logged, because unlimited attempts
would push real events out of the file, and failed Telegram sends go to the
dashboard's output only (`./stack logs dashboard`).

Results that the host helper finishes while the dashboard restarts are listed
once it is back; `/state/events_seen.json` records which results were already
listed. The panel calls `GET /api/events` with a signed-in session (or Basic
auth) and the `X-Requested-With: fetch` header. It takes `category`, `node`,
`limit` (1–200, default 50) and `before`, the `next` value of the previous page.

## Health checks

Shared services and configured node services define Docker health checks. For
the core Bitcoin deployment:

- `tor` — SOCKS port and the internal relay port accepting connections, and a
  real stream opened through Tor (`tor-health`, every 30 s; 2 min start period
  for bootstrap; see the Tor troubleshooting entry below).
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

With **Node Manager usage** ticked in Settings, the usage panel at the top left
shows the host's temperature, fan speed, uptime and network rate as four tiles
above the disk bars. On APT installs (amd64 and arm64) the small
`node-manager-hostd` helper writes them every 10 seconds. New installs and
upgrades get it as a recommended package. If `dpkg -s node-manager-hostd` says
it is missing, install it with `sudo apt install node-manager-hostd`. Elsewhere, and whenever the helper
is not running, the resource-profile shell helper collects them once a minute.
Either way the dashboard container needs no extra mounts or privileges.

Check the helper with:

```sh
systemctl status node-manager-host-stats.timer
systemctl status node-manager-host-stats.service
journalctl -u node-manager-host-stats.service
```

The timer should be `active (waiting)` and the service `inactive (dead)` between
runs. The service logs only warnings and errors, so a healthy install adds
nothing to the journal. If a run fails, the service shows `failed`, and the
resource-profile helper writes the readings itself from its next run until the
helper succeeds again, but only while `node-manager-resource-profile.timer` is
enabled (it is when a node uses the compact resource profile); otherwise the
readings stop until the helper is fixed. A stopped dashboard is not a failure:
the helper exits quietly and resumes with it. Without the package (or on an architecture it is not
built for) the shell collector runs as before.

The helper writes into the dashboard's state volume, which it finds through
Docker. If Docker's data root is not `/var/lib/docker`, allow that path with a
drop-in, then run `sudo systemctl daemon-reload`:

```ini
# /etc/systemd/system/node-manager-host-stats.service.d/data-root.conf
[Service]
ReadWritePaths=/srv/docker/volumes
```

The CPU and memory pies split the host three ways. A node slice covers the
node's container and its wallet container (Monero, Tari). **Node Manager
(dashboard and Tor)** covers those two containers; Tor carries every node's
peer traffic. **Other processes** is the rest of the host's used CPU and
memory: processes outside the stack, Docker itself, and kernel memory that no
container owns. On Linux each container's CPU comes from its cgroup over the
same second as the host total, and its memory is its anonymous memory plus its
own kernel memory (page tables, unreclaimable slab, socket buffers), not file
cache. Docker Desktop hosts use `docker stats` instead.

The four vitals tiles read:

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
  out). The rate is measured across the helper's CPU sample window: about 10
  seconds with the Go helper, or one second once a minute with the shell
  collector.

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

Open the User dialog (person icon) to change the dashboard login. Enter the current password,
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
Upgrades also install newly recommended packages such as `node-manager-hostd`.
If it is missing (for example after `--no-install-recommends`), run
`sudo apt install node-manager-hostd` (see [Host readings](#host-readings)).
Activation pulls the images of the running services one at a time, through the
host's Docker daemon as before (no new network path). It then also pulls the
new image of every other node that already has an older image on the machine,
whether that node is inactive or deleted (deleting a node never removes its
images), so Activate works afterwards. These extra pulls are tried once, never
start a container, and only warn on failure; activating that node pulls its
image then. A node with no image of its repository is not pulled. Each pull may take up to
10 minutes and is tried up to 3 times, 15 s and then 30 s apart, so a transient
registry or Tor failure costs a retry instead of the activation. The APT output
records every try, for example `Pulled the dashboard image in 42 s (attempt 1 of
3).` or `Pulling the dashboard image failed after 610 s (attempt 2 of 3).` An
image that fails all 3 tries stops activation before any running service is
changed. An image whose pull hangs every time gives up after about 31 minutes.

If an image still fails, or Docker is unavailable, APT keeps the new package
installed and exits successfully: a failing package script would leave the
package half-configured and make every later APT run fail until it is fixed.
Instead, the last line Node Manager prints in the APT output is:

```text
NODE MANAGER ACTIVATION FAILED: 1.39.0 is installed, but the running containers were not updated; retry with: sudo node-manager package-upgrade
```

Until an activation succeeds, `sudo node-manager doctor` repeats this under
`== Config` as `WARN  Node Manager 1.39.0 is installed, but its containers were
not updated; retry with: sudo node-manager package-upgrade`. The warning also
clears when `package-upgrade` finds no service running, since a stopped stack
starts on the new images. After correcting the problem, retry with
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
  - **Login.** Both endpoints use the same login as the dashboard: user
    `admin` and the dashboard password, over HTTP Basic auth, with the same
    wrong-password throttling (429 with `Retry-After`). A dashboard session
    cookie also works, as on the rest of the protected API, and a Basic
    request does not start a session. Without a valid login they answer 401
    with a `WWW-Authenticate: Basic` challenge, and there is no anonymous
    access; if no dashboard password is configured they answer 503 like the
    rest of the API. A switched-off endpoint answers 404 whatever credentials
    are sent. A monitor given this login can also sign in to the dashboard.
    A monitor polling with a wrong password counts against the same
    sign-in limit, so with no `dashboard.public_url` set it can briefly
    throttle dashboard sign-in too. Settings saved before this release with the old single switch on turn
    both endpoints on after the upgrade; the generated `actuator` login from
    earlier releases is gone, so update monitors to the dashboard login.
  - `GET /actuator/health` returns the overall `status` and `components`: HTTP
    503 for `DOWN`, 200 for `UP` or `DEGRADED`.
    - `dashboard`: always `UP`.
    - One entry per active node, with `details.state`:
      - `synced`: `UP`.
      - `syncing`: `DEGRADED`. For Bitcoin, Litecoin, Bitcoin Cash and Dogecoin
        this includes a node whose headers run ahead of its blocks or whose newest
        block is more than 2 hours old (peers lost), even if Core says its initial
        download is over. Tari reports `syncing` while its tip is more than
        30 minutes old. These stale-tip cases show on the actuator only; they do
        not trigger Telegram sync alerts.
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
      fall back to the slower scan meanwhile, and the index counts as
      `catching_up` while its node is itself behind the network.
    - `balance_index_litecoin`, `balance_index_bitcoincash` and
      `balance_index_dogecoin` (each only when that chain's index is turned
      on): the same `UP`/`DEGRADED` rule and `details.state`.

    The overall status is `DOWN` if any component is `DOWN`, `DEGRADED` if any
    is `DEGRADED`, otherwise `UP`.
  - `GET /actuator/info` returns:
    - `app`: `name`, `version`, and `install_kind` (`apt`, `homebrew`,
      `source`, or `unknown` until the host helper has reported the install
      type).
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
  service.** The actuator uses the same login as the dashboard for both
  endpoints, so any client that can poll `/actuator/health` can also read
  `/actuator/info` and those passwords. If monitors must not see them, leave **Serve /actuator/info** off.
  Settings → **Actuator** shows **Copy curl for health** and **Copy curl for
  info** under the switches, for each switch that is saved on. After you type
  the dashboard password (checked by the server, after a recent sign-in), the
  button copies a complete curl command for `<address>/actuator/health`
  (or `/info`), with the admin login and that password filled in, using the
  address the page was opened from. **The copied
  command contains the password and will land in shell history.** For scripts
  and monitors prefer `curl --user admin <url>`, which prompts. On a plain
  `http://` dashboard the browser blocks the clipboard API, so the button
  falls back to a temporary copy, then to a selected field you copy with
  Ctrl+C or Cmd+C.
  Neither endpoint returns wallet data, onion addresses or host paths.
  A configured `dashboard.public_url` still limits which host name they answer
  on, except for Basic-auth clients on the private network, as for `/metrics`.
  For example, `curl -fsS --user admin https://node.example.com/actuator/info`
  prompts for the password; use `/actuator/health` to exit non-zero when
  something is down. Uptime monitors that cannot prompt take the same
  username and password in their own Basic-auth settings.
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
  is valid, idle and within 2 blocks of the node tip, and the node is not itself
  syncing (initial download, its headers more than 2 blocks ahead of its blocks,
  or, for Bitcoin, Litecoin, Bitcoin Cash and Dogecoin, a newest block more than 2 hours
  old), in which case it reports `catching_up` with a `reason` saying why and
  `blocks_behind` counted to the headers;
  a wallet balance comes
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
litecoin`. On mini-1 its automatic seed took 346 s and the index takes 4.52 GB for
53.8 million coins ([measurement](measurements/litecoin-index-mainnet.md)). Only one
balance index seeds at a time; a second request is refused with the `lock`
guard until the first finishes.

The Bitcoin Cash balance index (`bitcoincash.utxo_index`) works on a pruned node.
Bitcoin Cash Node has no coin-set export, so a seed stops the Bitcoin Cash node
while its chainstate is copied, starts it again, and builds the index from the
copy; the index then follows new blocks. Check either kind of seed with
`./stack utxo-index verify --chain bitcoincash`. The copy hardlinks the node's tables, so
it needs free space for three times the chainstate and marks Bitcoin Cash as
restarting while the node is down, so it raises no node-down alert. The dashboard
asks for this itself on APT and Homebrew installs, and `./stack utxo-seed
bitcoincash` does it from a source checkout. The seed reads the copy only after a
clean stop; a copy taken while the node was flushing is refused. On mini-1 the
first mainnet seed took 530 s and built a 5.2 GB index for 58.8 million coins
([measurement](measurements/bitcoincash-index-mainnet.md)).

An archival Bitcoin Cash node with no host helper can instead replay every block
from genesis through the dashboard: `./stack utxo-seed bitcoincash --replay`, or
automatically once the node has synced. No seed pipe is made. A cancel, failure or
restart keeps the blocks already applied, and the next run resumes from there. A
node restart, an RPC timeout or a reorg near the tip is retried within the replay;
progress resets the count, so it waits for the hourly check only after about five
minutes of failures in a row, and storage errors stop it at once. It takes the
shared seed lock and the heat pause, but holds neither node alerts nor the watch
scan. Regtest rates are in [the replay measurement](measurements/utxo-replay.md);
a mainnet replay has not been timed. A pruned node refuses a replay.

With a balance index enabled the index starts empty, and on APT and Homebrew
installs it seeds itself as soon as the node has synced; there is no Seed button.
While an index has no seed (or went invalid), the dashboard checks the seed guards
and, when they pass, asks the host helper (`node-control`) to prepare the seed, the
same preparation `utxo-seed` does. The node card shows a bar for the index: waiting
for the node to sync, building with a percentage, catching up, or ready.

- A seed never starts while the node is still syncing, within 10 minutes of the
  node starting, or beside another index's seed or chainstate copy. These waits are
  re-checked every 30 seconds, cost nothing and are not failures. The host helper
  makes the last two checks itself and refuses before it stops any node.
- A seed also waits while the host has under 512 MiB of memory available or the
  dashboard's memory limit is under 512 MiB, rechecked every 30 seconds.
- A seed that starts and then fails, is cut short by a dashboard restart, or whose
  host preparation fails (for example a chainstate copy that could not be taken)
  is retried after 6 hours, then 24 hours, and not again after a third failure; the
  bar then reads "seeding failed" with the reason. Turning the index off and on in
  Settings clears that count. An index that turns invalid is reseeded the same way,
  but not within a day of a successful seed, so it cannot stop a node again and
  again. Cancelling a seed (`utxo-seed cancel`) stops automatic seeding until you
  start one with `utxo-seed`.
- Settings changes made while a Dogecoin or Bitcoin Cash chainstate copy runs wait
  for it (the node's stop can take up to five minutes) and then go through.
- Turning an index on from Settings or the install dialog is all it takes: the
  next check seeds it.
- An index built before coins paid to a public key (P2PK) were counted shows a note
  and keeps serving. Once its node is synced it rebuilds once, automatically, through
  the same guards, lock and back-off as any seed, one index at a time. The new index is
  built in a `rebuild-*` directory on the same volume while the old index keeps serving
  and following; a pointer file (`ACTIVE`) switches to it only after the seed succeeded,
  and the old files are deleted after the switch. A failed or interrupted rebuild leaves
  the old index serving, and a restart removes its partial copy. The volume needs free
  space for both copies. The card says when an automatic rebuild cannot start (no host
  helper, blocked by a guard, stopped after repeated failures). Run
  `./stack utxo-seed` (with the chain name) to choose the time. Rolling back to an older
  release makes the rebuilt index invalid, and it seeds again.
- The automatic seed holds node-down alerts like a manual one. A source checkout
  has no host helper, so the index card shows the command and you seed it by hand.
  To seed it from the host, with the stack running and the node synced:

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
- **Check it.** After the first seed, once the card reads ready, run
  `./stack utxo-index verify` (with `--chain litecoin` or `--chain bitcoincash`
  for those indexes); see
  [Checking a balance index against the node](#checking-a-balance-index-against-the-node).

To time lookups with and without the index on public test wallets, run
`utxo-index bench`, with `--chain litecoin`, `--chain dogecoin` or `--chain bitcoincash` for those indexes; the steps
are in [the lookup benchmark](measurements/lookup-bench.md).

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

## Seeding the Dogecoin balance index

Dogecoin Core 1.14.9 has no coin-set export, so `dogecoin.utxo_index` seeds from a copy of the node's
chainstate, which a pruned node keeps whole.

- The automatic seed goes through the host helper. It
  checks that the node has finished syncing and that the disk beside its data can hold the chainstate
  twice, stops the Dogecoin node, hardlinks the chainstate's tables into
  `<data_dir>/utxo-seed-chainstate`, copies its other files, and starts the node again. The dashboard
  then builds the index from the copy and follows new blocks.
- From the command line, `./stack utxo-seed dogecoin` (`sudo node-manager utxo-seed dogecoin` on APT)
  does the same and waits for the outcome. `./stack utxo-seed dogecoin cancel` stops the dashboard's
  build.
- **Alerts:** either way, the copy marks Dogecoin as restarting from just before the stop until its RPC
  answers after the start, for at most 10 minutes, so the stop raises no node-down alert. If the node
  does not start again, the mark is dropped and the alert follows.
- The copy keeps the node's older tables on disk until it is removed. The host helper removes it once
  the dashboard reports the seed finished, or when an interrupted copy left it with no seed at all; the
  command line removes it when it ends. An interrupted or hung-up copy starts the node again. If the
  owner stops the node while the index is being built, the seed leaves it stopped.
- The seed reports which of three things happened when it does not start: it refused before the stop
  (nothing changed), the copy failed and the node was started again, or the node did not start again
  (start it with `./stack up`).
- `./stack utxo-index remove dogecoin` deletes the index volume once `dogecoin.utxo_index` is off and
  applied.
- If a pruned node drops blocks the index still needs, the index turns invalid and is seeded again
  automatically (not within a day of a successful seed).
- `./stack utxo-index bench <file> --chain dogecoin` runs the plain lookup bench with `addr(<address>)` wallet keys. `--scan` is refused: Dogecoin Core 1.14.9 has no `scantxoutset`, so `utxo-index verify --chain dogecoin` is the full check. See [the lookup bench](measurements/lookup-bench.md).
- After the first seed, once the card reads ready, run `./stack utxo-index verify --chain dogecoin`; see
  [Checking a balance index against the node](#checking-a-balance-index-against-the-node).

## Checking a balance index against the node

Run this after a seed, once the index card reads ready. It checks the index against the node's own
UTXO set at the same block:

```
./stack utxo-index verify                   # Bitcoin; asks for y, -y skips the question
./stack utxo-index verify --chain dogecoin  # or litecoin, bitcoincash
```

On APT and Homebrew installs the command is `node-manager utxo-index verify`.

- The dashboard waits up to 10 minutes for the index to reach the node's best block. If it does not get
  there, the check is refused.
- It then counts every coin in the index and, at the same time, calls the node's `gettxoutsetinfo`. It
  compares the coin count (`txouts`) and the total amount, and prints both rows. The verdict is `PASS`,
  or `FAIL` with a non-zero exit.
- If the node moves to a new block before its walk starts, nothing is compared. Run it again.
- Only `PASS` exits 0. `FAIL`, every refusal and a command timeout exit 1. Refusals print as `refused:`;
  they include an index that stays behind, a node that moved to another block, and a node call that
  failed or timed out. If the dashboard stops during the wait, the command also exits 1 at once and says
  nothing was compared. Run it again once the dashboard is back.
- Coins a node never keeps are in neither count: OP_RETURN outputs, scripts over 10,000 bytes and the
  genesis coinbase. Litecoin's MWEB outputs are in neither count either.
- **Node load.** `gettxoutsetinfo` reads the node's whole chainstate, which takes minutes of disk and
  CPU on mainnet. Each node gets the cheapest form it accepts, read from its help and source at the
  pinned version and checked on regtest:

  | Node | Call | Notes |
  | --- | --- | --- |
  | Bitcoin Core 32.1 | `gettxoutsetinfo none` | `muhash` and `hash_serialized_3` (the default) both hash the set |
  | Litecoin Core 0.21.5.8 | `gettxoutsetinfo none` | No `muhash`; the default is `hash_serialized_2` |
  | Bitcoin Cash Node 29.2.0 | `gettxoutsetinfo none` | `ecmh`, `muhash_testing` and the default `hash_serialized_3` all hash the set |
  | Dogecoin Core 1.14.9 | `gettxoutsetinfo` | Takes no argument and always hashes the whole set, so it is the slowest |

  On mini-1, Litecoin took 44.5 s for `gettxoutsetinfo` and 68.6 s for the index count, 76 s for the
  whole command ([measurement](measurements/litecoin-index-mainnet.md)). Bitcoin Cash took 60.1 s and
  82.5 s, 87 s in all ([measurement](measurements/bitcoincash-index-mainnet.md)). Dogecoin took 304.6 s and
  305.1 s, 310 s in all ([measurement](measurements/dogecoin-index-mainnet.md)). Bitcoin is not measured yet. For Bitcoin on the same host, with bitcoind capped at 0.5 CPU, expect about 3 minutes, a guess from
  `scantxoutset`, which makes a similar walk there in 164 seconds ([lookup bench](measurements/lookup-bench-mainnet.md)).
- **Alerts.** In all four versions the walk reads a database snapshot without the node's main lock
  (`cs_main`). The lock is taken only for the flush before the walk and for short lookups, so the
  health probe's `getblockchaininfo` keeps answering. Node-down alerts for that chain are still held
  while the call runs, for at most 30 minutes, in case a CPU-capped node answers too slowly. They
  resume when the call ends. The hold is saved with the index's seed state, so a dashboard restarted
  during the walk keeps it until the 30 minutes are up.
- **Index.** The index stays at the compared block until the check ends: its own count is a full read
  of the index, and it then waits for the node. Balances keep answering from that block and catch up
  afterwards.
- After 30 minutes without an answer from the node, the check gives up. The node may still finish its
  walk afterwards.
- The check takes no seed lock, so run it when no other index is seeding, in a window the owner
  approved.

## Self-healing (a deliberate non-feature)

Fresh installs use `on-failure:5` to retry a crashed container up to five times.
With launch at boot/login enabled, `unless-stopped` also restarts crashed
containers and brings them back after Docker restarts.
Docker does not retry a container it could not start. After a power cut the
host can boot before the router hands out its LAN address, so a container that
publishes a port on that address fails with `cannot assign requested address`
and stays down. With launch at boot/login enabled, the host helper
(`node-control`, every 60 s on APT and every 10 s under Homebrew's host
service) recreates any stack container that Docker could not start because it
could not bind a published port. It retries on its first run at least 30 s
later, then less often, at most every 10 minutes, until the container starts, and logs
the first failure and the recovery as `Host:` lines in `./stack events`. Other
start errors, such as a data disk that is not mounted, are left for you, and so
are containers you stopped and deactivated nodes. `./stack up --force-recreate
<svc>` recreates a service by hand.

The router can also hand the host a different LAN address after a power cut, or
a laptop can join another network. Docker cannot bind the old address again, so
recreating Tor alone does not help. With `dashboard.bind_ip` set to `auto`, the
host helper remembers the last LAN address the dashboard was reachable on. Once
that address has been gone from every host interface for a minute, and this
host's detected LAN address has stayed the same different private address for
that minute, it acts. A DHCP renewal or a Wi-Fi reconnect is shorter and changes
nothing. The dashboard always follows `auto` to the new address. It re-renders
the last approved configuration, so a pending `config.json` change stays
pending, and recreates only Tor and the dashboard.

Tor publishes every LAN RPC, ZMQ and gRPC listener and cannot start while one
names a missing address. Listeners follow the host only onto the same network,
because Tari gRPC and Monero ZMQ have no password. The same subnet counts as the
same network:

- If the old address is inside the new address's interface subnet (the router
  just handed out a new lease), listeners on the old address move to the new
  one, identically in `config.json` and the approved snapshot. They move even
  when another start, such as the failed-start recovery above, already
  re-rendered the dashboard. A `/32` or point-to-point address, or a subnet the
  helper cannot read, never counts as the same LAN.
- Otherwise the host is on a network you did not choose, so listeners never
  follow it there; unauthenticated Tari gRPC and Monero ZMQ stay off it. Any
  listener whose address is gone is paused instead: it is listed in
  `.paused-listeners` in the state directory and rendered unpublished, while
  `config.json` and the approved snapshot stay unchanged. Tor then starts, so
  the dashboard, node peers and alerts keep working. Once a paused listener's
  address has been back for a minute, the helper publishes it again. To use the
  new network instead, re-point the listener from Settings.

The helper also moves the dashboard off the `127.0.0.1` fallback that a start
before the network came up leaves behind, and recreates a dashboard that still
holds the old address after the failed-start recovery recreated only Tor. Each
step logs one `Host:` line naming the listeners it moved, paused or published
again. It does nothing while the network has no private address, and it leaves
a stack you stopped alone. With a fixed `dashboard.bind_ip`, it changes nothing
and logs one warning instead. `./stack doctor` lists every published address
the host no longer has and every paused listener.

On Homebrew the same check runs from the host service. Docker Desktop records
no start error when a port cannot be bound, so the failed-start recovery above
never applies there. The host service re-renders and starts the stack at login
instead.
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

**Tor is up but nothing connects (Healthchecks.io alerts, nodes keep their old
peers)** — Tor can stop completing circuits while the container stays up, for
example after the host's link drops and returns. The tor container's
healthcheck runs `tor-health` every 30 seconds. Each run opens a real stream
through Tor's own SocksPort to `www.torproject.org:443`, then
`check.torproject.org:443`, then `api.github.com:443` while each fails (one
success counts as a good probe, no data is sent). It does not count circuits, so
it gives the same answer under light or heavy load. It also reads Tor's
bootstrap phase, network liveness and the number of connected relay channels
over a unix control socket inside the container, only to explain a failure.
`docker inspect` on the tor container shows the last output, one line such as
`tor: ok via www.torproject.org bootstrap=100 liveness=up orconns=3`, `tor:
probe-failed for 130s ... probes_ok=3/9`, `tor: offline for 130s ...` (Tor
reports the network down and holds no relay connection) or `tor: bootstrapping
...`.

The container turns `unhealthy` when either holds. The probes have failed
continuously for 4 minutes (a total stall), or at least 5 probes fall in the last
10 minutes and fewer than half succeeded (a brown-out like the 2026-11-18
incident, where a few streams still got through). One success does not clear
either state: the container turns healthy again only after 3 good probes in a
row. Time spent offline (Tor reports the network down and holds no relay
connection) is not counted: the failure record is dropped while offline, so the
4-minute timer starts when the link returns. Tor notices the network is down
only after about 5-6 minutes, so a host outage can still cost one restart before
that; after it, the outage costs nothing more. Only after an hour of Tor saying offline does it count as stuck. Tor
not yet fully bootstrapped in its first 10 minutes of uptime is neutral. The host helper
(`node-control`, run every 10 seconds by the APT timer and by the
`host-service` loop, which `brew services` runs on Homebrew and which
`./stack up` starts on source checkouts: a login-session process on macOS, a
process plus an `@reboot` cron entry on Linux; a source checkout whose
`host-service` is not running has no watchdog) then handles each incident,
which starts when the container turns unhealthy and ends only when it is healthy
with a good probe, and writes each step to the dashboard events log
(`./stack events`, lines starting `Host:`). The events are local, so they are
readable even while Tor is down; only their forwarding to Telegram waits for Tor
to work again, so during the outage you see only the Healthchecks.io alert and
this log. A container that stays unhealthy for 10 minutes without a probe result
(for example SocksPort not listening) is treated as an incident too.

1. **Save a snapshot and restart Tor** (`docker restart tor`; the nodes keep
   running). The snapshot (relay channels, circuit counts by state, bootstrap
   phase, liveness, `status/circuit-established`, `status/enough-dir-info`,
   `dormant`, the entry guards and the last 40 Tor log lines) goes to
   `node-control/tor-snapshots/` in the state directory, root-only, 64 KiB each,
   newest 5 kept. Onion addresses stay the same, but Bitcoin, Litecoin and
   Bitcoin Cash are unreachable over onion for a few minutes.
2. **Clear Tor's saved state and restart once more**, if the container is
   unhealthy again at least 8 minutes later while Tor holds at least one connected
   relay channel (checked after the restart, so stale connections do not count),
   so the host link works and Tor reaches relays. The state file holds both the
   chosen guards and the learned circuit build timeout; which of the two helps
   is not known, so both are discarded: Tor's whole state file is deleted, and Tor rebuilds it on start. This has a
   small anonymity cost, runs at most once in 7 days, and is recorded only after
   the restart succeeds.
3. **Pause and tell you**, otherwise: the event names the likely cause (host
   offline or blocked from relays, or Tor stuck). A long internet outage costs at
   most one restart, about 6 minutes in, before Tor reports the network down; a Tor that stays broken costs one restart, one reset
   and one pause event, never a loop. The helper does nothing more until the
   probe succeeds, then logs `Tor working again after N min (actions taken)`, with N counted from
   the first failed probe (or the start of the unhealthy state) to the first good
   probe.

Limits: at most 3 automatic restarts in 6 hours across incidents, so a Tor that
flaps is paused, not restarted forever. The helper skips a tick while an install
or upgrade holds the config lock and only acts on a tor container that belongs to
this stack.

Timeline against the recommended Healthchecks.io check (5-minute period,
10-minute grace; the dashboard pings every 60 seconds through Tor). These are
estimates from the settings, not measurements: a total stall is flagged after 4
to 5 minutes of failed probes (probes that fail take up to 60 seconds each) and a
brown-out after roughly 3 to 8 minutes, the helper restarts Tor within about 40
seconds of that, and Tor takes 2 to 4 minutes to bootstrap, so the first ping
after a successful restart lands around 8 to 12 minutes in. If the restart does
not cure it, the checker starts counting again from the restart, so the reset
comes at least 8 minutes after it (17 minutes after the first restart in a
fault-injection run on a real Tor), and the alert can still fire. The events log and the snapshots show what happened.

To disable the automatic recovery, create `node-control/tor-watchdog.disabled`
in the state directory (`touch /var/lib/node-manager/node-control/tor-watchdog.disabled`
on APT installs; the state directory is the checkout for source installs and
`BITCOIN_STACK_STATE_DIR` for Homebrew) and remove it to turn it back on. The
healthcheck keeps running and only reports. Tor's own check needs outbound
access through Tor to the three probe hosts, the same as everything else the
stack does.

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
User dialog takes precedence over that baseline. Use the changed password or the
offline recovery procedure in [Dashboard and node credentials](#dashboard-and-node-credentials).

**A wallet app fails after an RPC credential reset** — reveal the new login in
that node's tower settings and update the app. The previous password is no
longer valid.

**Node syncs but peer count is 0 after hours** — tor container unhealthy or
restarted with a new identity mid-session. `docker compose restart tor bitcoin`.

**Disk filling up** — the dashboard's disk row is the early warning.
Options: a bigger disk, or switch to a pruned node via `prune_mb` in
`config.json` — see [Configuration → Pruned node](configuration.md#pruned-node).
