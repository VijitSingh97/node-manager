# Operations

## Day-to-day commands

Use `./stack` for operations; it prevents implicit image downloads and builds.
The explicit `pull` command uses the host's existing Docker settings. Package
installs use `bitcoin-stack` instead of `./stack` (with `sudo` for APT). Direct
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

`docker ps` shows the state. Run `./stack doctor` or `bitcoin-stack doctor` for
a health report. From a source checkout,
`docker inspect --format '{{json .State.Health}}' "$(docker compose ps -q bitcoin)"`
shows recent probe output when diagnosing.

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
sessions are sent back to the login page. Sessions are held in memory, so a
dashboard restart logs out all browsers.

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
sudo bitcoin-stack backup
sudo apt update
sudo apt upgrade
```

APT upgrades automatically download the new release images and recreate the
services that were running. Changed services may restart. A stopped deployment
stays stopped, and configuration edits still awaiting approval are not applied.
If Docker or the registry is unavailable, APT keeps the package installed and
reports activation as pending. After correcting the problem, retry with
`sudo bitcoin-stack package-upgrade`. Inspect `sudo bitcoin-stack status` and
`sudo bitcoin-stack doctor` if activation reports a failure. If activation partly
started and left a service stopped, use the specific `bitcoin-stack up` command
printed in the error before retrying. Recovery starts are explicit so later
upgrades cannot override a service you deliberately stopped. No volumes or old
images are deleted, and a failed partial activation never automatically downgrades
a node database.

**macOS / Homebrew:**

```bash
bitcoin-stack backup
brew update
brew upgrade bitcoin-starter-stack
bitcoin-stack pull
bitcoin-stack apply
```

**GitHub download / source:** download the runtime archive from
[the latest release](https://github.com/VijitSingh97/node-manager/releases/latest).
Back up and stop the existing stack, replace its runtime files while preserving
`config.json`, `.env`, data directories, and named volumes, then run
`./stack pull && ./stack apply`. For a Git checkout, select the published release
tag yourself before pulling images and applying. Review release notes for data
migration requirements; node databases may not support downgrades.

`bitcoin-stack upgrade` (or `./stack upgrade`) only prints instructions. It does
not fetch, check out, download, or apply a release. Installation and container
downloads use the host connection; see [Privacy](privacy.md#downloads-and-upgrades).

The first package release is for fresh installations. Do not run
`sudo bitcoin-stack up` beside an existing source checkout: the package refuses to
replace containers whose Compose labels point at another installation. Keep an
existing production node source-managed until a manual migration is planned.
Package mode uses its own `bitcoin-starter-stack-apt` Compose project, so stopped
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
`bitcoin-stack-upgrade-agent.service`. The compatibility agent now handles only
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

APT installations use `sudo bitcoin-stack backup` and write archives under
`/var/lib/bitcoin-starter-stack/backups`; restore and apply with `sudo` as well.

A backup holds the stack configuration and dashboard state, but excludes chain
data and encrypted Monero wallet files:

- `config.json` and `.env` (settings + credentials)
- the node's inbound-onion key (`onion_v3_private_key`, if enabled)
- the dashboard onion keys (from the tor volume, if enabled)
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
enabling either onion feature. The chain itself is re-derivable from the
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
- **Telegram / Healthchecks.io:** see [Notifications](notifications.md).
- **Fee sparkline:** the dashboard samples the next-block fee once a minute
  into an in-memory 24-hour series (reset on restart) and draws it under the
  fee row. Served as JSON at `/api/fees` behind the same authentication.

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
