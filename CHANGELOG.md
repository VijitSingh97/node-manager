# Changelog

All notable changes to this project are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/); versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.33.16] - 2026-11-12

### Fixed

- Watch-only Monero wallets that are far behind the chain scan in the wallet
  RPC's background auto-refresh and show live progress, instead of a blocking
  refresh that outlived its 30-minute budget, poisoned the wallet service
  until the next restart, and left the card on a permanent error. Scan
  progress is also stored every cycle, so restarts no longer discard it.

## [1.33.15] - 2026-11-12

### Fixed

- The "Move data…" buttons were always disabled: the host-control API response
  left out `migrate_supported`, so the settings page never saw the capability
  the host published.

## [1.33.14] - 2026-11-12

### Added

- Cross-disk data migrations report live progress: the host helper samples the
  copy every few seconds and the dashboard's status line shows percent and
  GiB copied until the node restarts from its new folder.
- Each installed node's data folder is shown in Settings next to a
  "Move data…" control that migrates it to another path or disk from the
  dashboard: the host helper stops the node, renames within a disk or
  copies-and-verifies across disks, updates and applies the configuration,
  restarts the node, and rolls everything back (data included) on any
  failure or interruption. Destinations are validated like install
  folders, including a free-space check.

### Changed

- Per-container memory on the usage pies is the container's anonymous memory
  from its cgroup when available (Docker's working-set figure counts active
  file cache), putting the node slices, the grey "Other processes" slice, and
  the free remainder on one cache-free scale; hosts without cgroup v2 keep
  the previous Docker figure.

## [1.33.13] - 2026-11-12

### Fixed

- The Monero RPC client sends an incrementing nonce count when monerod reuses
  a Digest nonce across challenges, instead of a hardcoded nc=00000001 that
  the server rejects as a replay once any concurrent caller has used it. This
  was the remaining source of false "Monero node down / recovered" flaps
  after v1.33.11's stale-nonce retry, which re-handshook into the same nonce
  and replayed again.

## [1.33.12] - 2026-11-11

### Changed

- The usage panel's CPU and memory pies show a grey "Other processes" slice
  for host usage outside Node Manager, from new host-wide CPU and memory
  figures the collector gathers on Linux; the light track remains truly idle
  or free capacity.
- Per-node disk slices prefer the collector's measured directory sizes over
  the nodes' self-reported database sizes (bitcoind's size_on_disk covers only
  the blocks directory), so ~9 GiB of chainstate and indexes no longer land
  in grey "Other data".

## [1.33.11] - 2026-11-11

### Fixed

- Healthchecks.io pings are a pure dead-man's switch again, as documented: the
  monitor pings success on schedule whenever it is alive, and never signals
  `/fail` because a node is down. Node health alerts stay on Telegram, so a
  monero flap no longer marks the Healthchecks check down.
- The dashboard's Monero RPC client re-handshakes when monerod serves a stale
  Digest nonce instead of failing the request. Concurrent dashboard pollers
  churn monerod's bounded nonce table, so single-handshake probes failed in
  bursts for minutes — the source of day-long false "Monero node down /
  recovered" Telegram flaps while monerod answered every outside probe in
  milliseconds.

- Disk usage figures exclude the filesystem's reserved blocks (ext4 reserves
  5% for root), matching df's percentages. The reservation previously counted
  as used space and inflated the usage panel's grey "Other data" by ~90 GiB
  on a 2 TB disk.

### Changed

- The dashboard and tari wallet sidecar build with Go 1.27.4, picking up the
  net/http fix for GO-2026-6859.

## [1.33.10] - 2026-11-11

### Fixed

- Upgrade cleanup also removes this project's dangling (untagged) image
  leftovers by image id and prunes stranded anonymous volumes; the previous
  compose-project label filter matched nothing because Compose never labels
  anonymous volumes. Other projects' images, dangling included, are untouched.

### Changed

- build(deps): bump `zakuracore/zakura` from 1.8.4 to 1.8.5.

## [1.33.9] - 2026-11-10

### Added

- Node Manager cleans up after itself (#184): a successful `package-upgrade`
  removes this project's superseded image tags (keeping the current and
  previous versions; other projects' images are never touched) and prunes the
  project's stranded anonymous volumes, and `stack doctor` warns about
  orphaned directories under the state dir's `data/` that no node's
  configured data dir points at, with their sizes — reporting only, never
  deleting chain data.

### Changed

- The usage panel shows one bar per physical disk: volumes and partitions on
  the same drive (LVM included) are merged, with capacities summed and node
  attributions combined.
- The stack's own CPU and memory pie slices are now the same teal
  "Node Manager itself" as its disk slice, instead of grey "Stack other";
  grey remains for data and usage that is not Node Manager's.
- The "Node Manager itself" disk figure measures only what the package
  exclusively owns — its install and state directories (node data excluded) —
  and no longer includes host-wide Docker storage, which also counts images
  and volumes that are not Node Manager's.

## [1.33.8] - 2026-11-10

### Fixed

- The resource-profile service can read node data directories under `/home`
  (`ProtectHome=read-only`), so such nodes get disk figures in the usage
  panel; `ProtectHome=true` hid them entirely.
- The usage panel's zoomed disk view no longer lists nodes whose data
  directory is empty scaffolding: the collector skips node dirs under 1 MiB,
  so only installed nodes with real data appear.
- The usage panel now shows Node Manager's own footprint (install and state
  directories plus Docker images, containers, and volumes) as a teal
  "Node Manager itself" slice on the disk bars and in the zoomed view.
- Disk bars are labelled with the physical disk's model and kind
  (for example "Samsung SSD 990 PRO NVMe") instead of device-mapper paths,
  when the host can resolve them, and disks holding no node-manager data are
  no longer shown.

## [1.33.7] - 2026-11-10

### Added

- Hard per-node resource caps in configuration: `<node>.cpus` for all seven
  nodes and `monero.mem_limit_mb` render into `.env` and docker-compose, so
  caps apply from container creation and survive upgrades, unlike manual
  `docker update` limits, which container recreation silently wipes. The
  resource profiler defers to these operator caps.
- A host thermal throttle (#177), switched by a "Thermal throttle" checkbox
  in the dashboard settings panel (off by default). While the host runs at or
  above `host.thermal_limit_c` (default 85°C), the resource-profile pass
  halves running node containers' CPU caps toward a 0.25-CPU floor instead of
  anything stopping them, and restores the original caps exactly once it
  cools to `host.thermal_resume_c` (default limit − 10), when the sensor
  disappears, or when the checkbox is turned off. Actions land in
  `./stack events` and are forwarded to Telegram when configured.

### Fixed

- The usage panel keeps per-node disk slices while a node is down: the host
  collector now measures each node's data directory, with live RPC sizes
  refining the figures when the node answers.
- "Other data" on the usage panel's bars and pies uses a darker grey that is
  distinct from free space.

## [1.33.6] - 2026-11-10

### Added

- A "Node manager usage" panel in tower view (enabled in Settings →
  Appearance): a liquid-glass corner card with one stacked bar per disk that
  holds node or stack data (labelled with its device name when there are
  several), and CPU and memory pies for the stack's containers. Bars and pies
  zoom independently into node-manager-only usage on click, and every slice,
  caption, and bar reports exact sizes on hover. Per-container CPU/memory and
  host disks are collected by the resource-profile timer's minute pass and
  served to the dashboard only while fresh.

### Changed

- Node panels now share one wording: the bitcoin panel's connection rows
  collapse to `Connections: x in · y out` like every other panel, monero's
  `Database Size` label becomes `Node Data Size`, bitcoin's data size reads
  GiB (which its value always was), and the per-panel disk rows move into
  the usage panel.
- build(deps): bump `zakuracore/zakura` from 1.8.3 to 1.8.4.

### Fixed

- The 1.33.4 rename migration now rewrites `STACK_ROOT` and
  `STACK_ASSET_ROOT` in `.env` and `.approved.env`, so bitcoin containers on
  migrated APT installs can start again after a stop; already-affected boxes
  self-heal on this upgrade (#170).

## [1.33.5] - 2026-11-09

### Added

- The dashboard settings dialog shows the running node-manager version
  under its heading (#163).

## [1.33.4] - 2026-11-09

### Changed

- Default post-sync memory ceilings for Litecoin, Dogecoin, and Bitcoin Cash
  drop from 4096 to 2048 MiB. These pruned Core-fork nodes run default
  database caches and stay well under 2 GiB at steady state; the resource
  profile still refuses to cap a node whose measured usage does not fit,
  so an undersized budget leaves the node unlimited rather than restarting
  it. Tari and Zcash keep 4096 MiB (Zakura is a Zebra fork with a 4 GiB
  stated minimum). Explicit `steady_mem_limit_mb` settings are unaffected.
- Renamed every published artifact from `bitcoin-starter-stack` to
  `node-manager` (#156): the APT package (with a transitional
  `bitcoin-starter-stack` package for automatic upgrades), the GHCR runtime
  images, the Homebrew formula (mapped via `formula_renames.json`), the systemd
  units, the release tarball, and the `node-manager` command
  (`bitcoin-stack` remains a compatibility symlink). Upgrades move
  `/etc/bitcoin-starter-stack` and `/var/lib/bitcoin-starter-stack` to the
  `node-manager` paths, leave compatibility symlinks behind, migrate enabled
  timers with their drop-in overrides, and preserve node data, containers, and
  Compose project names. The APT feed keeps its original `Origin`/`Label` and
  still publishes the signing key under the old `bitcoin-starter-stack.asc`
  name so existing hosts keep updating without manual steps.

## [1.33.3] - 2026-11-09

### Changed

- build(deps): bump `zakuracore/zakura` from 1.8.0 to 1.8.3.

### Fixed

- Healthchecks pings now retry up to twice over fresh Tor circuits (one-off
  SOCKS credentials, so Tor's stream isolation builds a new path) before a
  failure is recorded. A single bad circuit — roughly one in eight fresh
  circuit builds fails on a busy daemon — no longer counts as a missed ping,
  which was flapping Healthchecks while the node was healthy. Pings stay
  Tor-only.

## [1.33.2] - 2026-11-08

### Fixed

- Keep dashboard-driven config applies working on APT hosts whose data
  directories live outside the control helpers' systemd sandbox (for
  example under `/home`). `configure.sh` no longer fails when it cannot
  recreate such a directory; the directory already exists on the host.
  The sandbox itself stays strict by default — installing a node to a
  custom data folder requires a per-host override, now documented in the
  configuration guide.

## [1.33.1] - 2026-11-08

### Added

- "Remember me for 30 days" checkbox on the dashboard sign-in page. A
  remembered session skips the idle timeout; password changes still revoke
  all sessions, and RPC credential management still requires a recent
  sign-in.

### Changed

- Update Zakura (Zcash) to 1.8.0.

### Fixed

- Install nodes from the dashboard on APT installations. The systemd control
  helpers ran without `BITCOIN_STACK_DATA_DIR`, so install and config-apply
  requests crashed `configure.sh` and the dashboard sat on "Applying host
  change…" indefinitely while recovery retried forever.

## [1.33.0] - 2026-11-02

### Changed

- Require dashboard sign-in on every installation. New installs generate a
  password; existing installations must set a valid `dashboard.password`
  before applying or upgrading to this version.
- Replace the browser login popup with a sign-in page, expiring sessions, and
  sign-out. Password changes revoke all sessions, and managing RPC credentials
  requires a recent sign-in. Machine clients must send preemptive Basic
  credentials with username `admin`.
- Add an optional HTTPS reverse-proxy profile and Caddy example, with a
  loopback-only dashboard backend, canonical origin validation, and secure
  session cookies. VPN access remains recommended for VPS administration.
- Bound password verification work, strengthen browser security headers, and
  add a dependency vulnerability check to CI.

### Fixed

- Refresh node status in place without replaying tower growth on routine updates.
- Keep the next block's flashing slot visible on full-height towers in both
  the overview and focused views.

## [1.32.0] - 2026-10-31

### Added

- Node Manager supports Bitcoin Core, Monero, Tari, Litecoin, Dogecoin,
  Bitcoin Cash Node, and Zcash through Zakura, with a responsive tower overview
  and individual status panels.
- Install additional nodes from the dashboard, review their storage paths and
  estimated disk requirements, and opt into LAN interfaces. Port conflicts are
  checked before changes; affected nodes restart only when needed.
- View-only wallets for Bitcoin, Monero, Tari, Litecoin, and Bitcoin Cash, with
  pending and stale states and saved balance snapshots. Dogecoin and Zcash
  explicitly report that wallet balances are unavailable in their pruned modes.
- Ubuntu APT and macOS Homebrew installation, an explicit `bitcoin-stack init`,
  persistent configuration, and dashboard controls for launch at boot or login.
- Shared Telegram and Healthchecks settings, per-node block alerts, optional
  dashboard authentication, and separate node-interface credentials.
- Cached prices for every supported chain in nine fiat currencies, fetched
  through Tor, with unavailable and stale indicators.
- Experimental post-sync resource profiles: 1 CPU / 2 GiB for Bitcoin and
  Monero, and 1 CPU / 4 GiB for other nodes. Wallet and shared-service overhead
  is additional; full-chain resource qualification remains pending.
- Automatic activation of new container images after APT upgrades, with pinned
  image digests, approved configuration, and preservation of stopped services.
- Signed APT feed with primary fingerprint
  `72FD656754BD7534E274360A37070C2730C0B4EA`.

### Changed

- Rebranded the project and repository as Node Manager. Existing package names,
  `bitcoin-stack` commands, configuration paths, and data volumes are preserved.
- Replaced the Python dashboard runtime with Go and retained functionality-focused
  Python and frontend regression tests.
- Fresh installations start pruned Bitcoin only, select a private LAN dashboard
  address, leave its password optional, and keep node interfaces and automatic
  launch disabled. Initial sync is uncapped by default.
- Updated Bitcoin Core to 32.1. Runtime internet traffic is isolated behind Tor;
  package and container downloads use the host connection.
- Updated Tari node and wallet to 6.2, dashboard gRPC/protobuf libraries, and
  the Monero image base.
- Release archives contain runtime code, required notices, and user guides;
  tests, parity references, benchmarks, screenshots, and maintainer tools remain
  in the repository.
- Removed the application self-updater. Release notices remain informational;
  users update through APT, Homebrew, or a GitHub download. Legacy `upgrade`
  commands only print instructions.
- Simplified the README around Ubuntu and macOS installation and refreshed its
  hero with a closer light-mode preview.

### Fixed

- Launch-at-login settings remain synchronized after saving or reopening.
- Price fetching retries while Tor initializes and uses the correct Tari feed.
- Monero wallets use private writable ring-database storage and remain pending
  while the daemon syncs.
- Disk capacity, free space, and low-disk alerts use the correct allocation units
  on Docker Desktop bind mounts.
- Compose resources are scoped per installation, and unselected Tari wallet
  services and volumes are not created.
- Settings and node installation validate inputs, preserve private state, and
  roll back interrupted host changes.

## [1.31.1] - 2026-10-27

### Changed

- The dashboard now uses Waitress instead of Flask's development server.
- Dashboard containers stop cleanly through Waitress's supported SIGINT handler.
- Refreshed the Alpine 3.24 and Python 3.14 slim image pins and updated
  `actions/setup-python` to 7.1.0 (#121).
- Updated the digest-pinned Gitleaks, Hadolint, and QEMU CI images.

## [1.31.0] - 2026-08-01

### Added

- **Persistent incident log.** The monitor now appends every alert-worthy
  event (node down/recovered, failed Healthchecks pings, disk-low, monitor
  restarts) to `/state/events.log` on the `dashboard_state` volume, so
  incident history survives container restarts and host reboots — `docker
  logs` doesn't. `./stack events` prints it. Naive size cap: over 1 MB the
  newest half is kept.

### Fixed

- **Healthchecks pings no longer fail silently on a bad Tor exit.** The
  dead-man ping rides Tor; an exit answering with an HTTP error page counted
  as delivered, so the 5-minute ping clock advanced and Healthchecks.io
  missed pings with nothing in the logs — showing up as random "down" flaps
  while the node was healthy. Non-200 responses now count as failures: the
  monitor retries every minute (on a fresh circuit) until one lands, and
  logs the failure to the incident log.

## [1.30.0] - 2026-07-18

### Added

- **The Upgrade button now sets itself up.** Enabling `dashboard.control` is all
  it takes: `./stack up`/`apply` start the host-side `upgrade-agent` detached
  (log: `~/upgrade-agent.log`) and install a `@reboot` cron entry so it survives
  reboots — no sudo, no unit file. Disabling `control` and re-applying stops the
  agent and removes the cron entry. If the documented systemd unit is enabled,
  the stack defers to it. `./stack init` asks one new question ("One-click
  upgrades from the dashboard?", default no), and `./stack doctor` reports
  whether the agent is running.
- **Sandboxed lifecycle tests for the agent manager.** `test_cli.sh` stubs
  `crontab`/`docker`/`pgrep`/`pkill`/`setsid`/`systemctl` onto PATH and drives
  enable → re-apply (idempotence) → disable → systemd-deference without
  touching the host. The suite immediately caught a `pipefail` crash in the
  disable path when the agent's entry was the only crontab line.

### Changed

- README: the feature list now covers one-command upgrades, the daily update
  check, and the Prometheus `/metrics` endpoint; the Testing section describes
  the full suite (including the e2e Tor-only egress audit and the gitleaks /
  hadolint CI jobs).

### Fixed

- CHANGELOG version headings 1.23.0–1.29.1 had no reference links, so they
  rendered as literal brackets on GitHub.

## [1.29.1] - 2026-07-18

### Fixed

- **`./stack apply` / `./stack upgrade` no longer needlessly recreate bitcoind.**
  `configure.sh` regenerated a fresh rpcauth salt on every run, which rotated the
  derived hash and changed the bitcoin container's env — so `docker compose up -d`
  recreated bitcoind (triggering its 5-minute flush grace) on **every** apply or
  upgrade, even for an unrelated change like a dashboard-only edit. The salt is
  now preserved across runs (like the RPC password already was), so an unchanged
  password re-renders byte-identical and only the containers that actually changed
  are recreated. A changed password still rotates the hash and correctly recreates
  bitcoind.
- **The upgrade-agent systemd example now runs as the repo owner, not root.** The
  1.29.0 unit in `docs/operations.md` omitted `User=`, so it defaulted to root and
  the upgrade's `git fetch`/`checkout` failed with "dubious ownership" on a
  user-owned checkout. Added `User=YOU` (must be in the `docker` group) and
  `RestartSec`.

## [1.29.0] - 2026-07-17

### Added

- **Opt-in dashboard Upgrade button.** When `dashboard.control` is enabled and a
  newer release is available, the dashboard shows a one-click **Upgrade now**
  button. It preserves the non-root dashboard's security model: the dashboard
  has no host or Docker access and only writes a request marker to its state
  volume. A new host-side agent, `./stack upgrade-agent`, watches for the marker
  and runs `./stack upgrade` with the right privileges (run it under systemd —
  see [docs/operations.md](docs/operations.md#upgrade-button-opt-in)). Off by
  default; without the agent running, the button does nothing. This is the
  dashboard button the 1.28.0 notes deferred — now possible **without** giving
  the dashboard host/docker control. Covered by unit tests (endpoint gating,
  CSRF) and an end-to-end CI test (the agent consumes a button-written request
  and upgrades).

## [1.28.0] - 2026-07-17

### Added

- **One-command upgrade: `./stack upgrade`.** Fetches the latest release tag,
  **backs up first**, checks it out, and re-applies — pulling the new prebuilt
  images and recreating the changed containers. It only ever moves forward
  (never downgrades), prints a rollback line, and points a tarball (non-git)
  install at the release page. Pairs with the dashboard's existing "update
  available" badge: the badge tells you, `./stack upgrade` does it. (CLI only by
  design — no dashboard button, which would require giving the non-root
  dashboard host/docker control.) Covered end-to-end by a new `test_upgrade.sh`
  that boots one release and upgrades to the next in CI.

## [1.27.0] - 2026-07-17

### Changed

- **The node now accepts inbound connections by default.** `inbound_onion` flips
  from opt-in to **on by default** — the node publishes a Tor onion service and
  serves blocks back to the network, still with **no IP exposure and no host
  port** (it's all over Tor). This is the encouraged posture and makes the
  network healthier; the only cost is some extra upload bandwidth. To run
  outbound-only, set `"inbound_onion": false` in `config.json` (or answer *n* to
  the `./stack init` prompt, now `[Y/n]`). Existing deployments keep whatever
  their `config.json` says; a bare `./stack up`/`./stack apply` will enable it.

## [1.26.2] - 2026-07-17

### Fixed

- **Adding a watch-only wallet on a pruned node no longer silently fails.** It
  used to accept the wallet, show "scanning…", then sit at `—` forever (a full
  rescan can't run on a pruned node). The dashboard now rejects it up front with
  a clear "a full node is required" message and doesn't add it to the list.
- **`./stack backup` is more robust and honest:** it no longer aborts if it
  can't read the node onion key, warns (instead of silently skipping) when a
  container is down so an incomplete archive isn't mistaken for a complete one,
  and captures the watch-only roster (from 1.26.1).
- **`./stack restore` with no argument** prints a clean usage line instead of a
  raw bash error; **`./stack init`** checks for `jq`/`openssl` up front.
- **`configure.sh` validation errors name the offending field** instead of
  blaming all three at once.
- **Loading page** no longer shows an internal Docker IP and points you at
  `./stack doctor` if it's stuck; add-form errors are now visually distinct from
  the "scanning" message.
- Documentation consistency pass: corrected the stale "images aren't published"
  note, matched the two doc indexes, and completed the config.json example.

### Changed

- **CI now measures code coverage** (pytest-cov, floor 85%; currently ~93%) and
  cancels superseded runs; new tests pin the inbound-onion entrypoint branch and
  cover previously-dark error paths (wallet RPC, alert channels, version/update
  logic).

## [1.26.1] - 2026-07-17

### Fixed

- **`./stack backup` now captures the watch-only roster.** It previously saved
  config, credentials, and onion keys but not the `dashboard_state` volume — so a
  lost volume (or a restore onto a fresh box) dropped your curated wallet list
  (labels, keys, birthdays) and balance history. Backup and restore now include
  it (verified against a live stack). No action needed; just re-run `./stack backup`.
- **Accessibility:** the watch-only add-form inputs (label / key / birthday) and
  the remove button now carry `aria-label`s, so screen readers announce them.

### Changed

- **CI robustness:** workflows now cancel superseded in-progress runs
  (`concurrency`, keyed by ref) and carry `timeout-minutes`, so a redundant or
  hung run no longer wastes CI minutes.

## [1.26.0] - 2026-07-17

### Changed

- **Container hardening (2026-07 best-practices sweep).** The `dashboard` now runs
  **non-root** (uid 1000, like `bitcoin`) — it's the only service with a host port,
  and it needs no root. All three services also get `security_opt:
  no-new-privileges:true` and `cap_drop: [ALL]`. Verified end-to-end: the full stack
  boots healthy under the new restrictions and the non-root dashboard writes its
  state volume on a fresh install.
  - **Upgrade note:** the existing `dashboard_state` volume was written as root, so
    chown it once before upgrading (the dashboard degrades gracefully without this
    — it just can't persist the wallet roster until done):
    `docker run --rm -v bitcoin-starter-stack_dashboard_state:/state alpine chown -R 1000:1000 /state`

### Added

- **Optional `bitcoin.mem_limit_mb`** (default `0` = unlimited). Caps the bitcoin
  container's memory so a bitcoind blowup is a contained restart instead of a
  whole-host OOM — worth setting on a RAM-tight box. `configure.sh` validates it and
  warns if it isn't above `dbcache_mb`; see [Configuration](docs/configuration.md) for
  sizing.

### Fixed

- A slash-named git branch (`feat/x`) made `STACK_VERSION` contain `/`, which Docker
  rejects as an image tag — `docker compose` failed with "invalid reference format"
  on any feature branch. Sanitized (`/`→`-`).
- CI's `GITHUB_TOKEN` is now least-privilege (`permissions: contents: read`).

## [1.25.1] - 2026-07-17

### Fixed

- **`inbound_onion` silently killed all outbound Tor connections.** With
  `-torcontrol` active, bitcoind asks tor for its SOCKS listener, gets
  `0.0.0.0:9050`, rewrites it to `127.0.0.1:9050` (dead inside the bitcoin
  container), and — because `-onion` wasn't explicitly set — used it for every
  onion destination: the onion service registered fine, but the node never made
  an outbound connection. `-onion=172.29.0.25:9050` is now pinned explicitly.
  Found by the new e2e sync-start check on real hardware (0 peers in 20 min
  before; 10 onion peers within minutes after).

### Added

- **E2e now covers setup and sync-start.** The e2e boots through the documented
  setup path (wizard → `config.json` → `configure.sh` → `.env`), and with
  `E2E_SYNC=1` proves sync starts (onion-only peers, headers advancing) for
  both the wizard and zero-config paths. New `tests/test_postsync.sh` runs
  read-only post-sync assertions against an already-synced node.

## [1.25.0] - 2026-07-17

### Changed

- **`./stack init` now generates a dashboard password by default.** Pressing
  Enter at the wizard's dashboard-password prompt generates a strong random one
  (shown once — save it) instead of leaving the dashboard open, so guided setups
  get basic auth out of the box. Type your own to use it instead. A bare
  `./stack up` is unchanged: zero-config, open on the LAN only.

## [1.24.2] - 2026-07-17

### Fixed

- **Docs corrected against the code** (2026-07 repo audit): Tor-only routing
  (`-onlynet=onion`, `-proxy=`) is documented where it actually lives — the
  `docker-compose.yml` bitcoin entrypoint — not `bitcoin.conf` (it moved in
  v1.21.0); stale versions updated (alpine 3.24, python 3.14, Core 31.1); the
  RPC username is documented as defaulting to `bitcoin` (only the password is
  random); the dashboard healthcheck accepts any HTTP response (401 under auth
  is healthy); the config snippet now includes `sync_over_clearnet`.

### Added

- **Audit-driven test hardening.** The `SYNC_OVER_CLEARNET` routing branch line
  is pinned verbatim in the compose contract test (a value typo or a then/else
  swap — clearnet args leaking into the default branch — both fail);
  `DELETE /api/watch` is asserted to reject requests without the CSRF header;
  the watch-only cannot-spend guarantee (`disable_private_keys=True`) is pinned
  in the fast unit suite instead of only the e2e.

## [1.24.1] - 2026-07-14

### Fixed

- **A tagged release deploy now shows the release number, not `HEAD-<commit>`.**
  Release tags are annotated, so `git rev-parse v<version>` returns the tag
  object rather than its commit; the 1.24.0 check compared that against HEAD's
  commit, never matched, and every deploy looked like a dev build. Dereference
  the tag with `^{commit}`, ignore untracked runtime files when judging a clean
  tree, and render a detached-HEAD dev build as the bare short commit instead of
  `HEAD-<commit>`.

## [1.24.0] - 2026-07-14

### Changed

- **Version display distinguishes releases from dev builds.** A clean checkout of
  a release tag (or an unpacked release tarball) shows the release number
  (`v1.24.0`); any other checkout shows `branch-commit` (e.g. `main-a1b2c3d`), so
  a dev build never masquerades as a release. `configure.sh` computes this from
  the git state and the dashboard prefers it over the image-baked `VERSION` —
  which matters now that release images are pulled prebuilt: a pulled image run
  off an unreleased checkout still reports the dev id. The update-checker only
  compares against releases when actually running one.

## [1.23.0] - 2026-07-14

### Added

- **Prebuilt multi-arch images on GHCR.** Each release now publishes
  `ghcr.io/vijitsingh97/bitcoin-starter-stack-tor` and `-dashboard` for
  `linux/amd64` and `linux/arm64` (Raspberry Pi). `./stack up` pulls them instead
  of compiling — the compose services keep their `build:` blocks, so an
  unreleased commit (no matching published tag) still builds locally, with no
  change to the workflow. A new `images` job in the release workflow builds and
  pushes them, independent of the GitHub Release so a registry hiccup can't block
  the source tarball.

## [1.22.0] - 2026-07-13

### Added

- **Interactive setup wizard: `./stack init`.** An optional, friendlier path than
  editing `config.json`: it asks about a dashboard password, a Tor onion, inbound
  Tor connections, a fast clearnet initial sync, and pruning — each with a default
  you accept by pressing Enter. Writes a minimal `config.json` (only what you
  change) and offers to start. `./stack up` still needs none of this. Inbound Tor
  connections (`inbound_onion`) are documented as **encouraged** — they help the
  network with no IP exposure.

## [1.21.0] - 2026-07-13

### Added

- **Optional clearnet initial sync (`bitcoin.sync_over_clearnet`).** Tor-only IBD
  takes days; set this `true` to run the initial block download over clearnet
  (hours), then set it back to `false` and `./stack apply` to return to Tor-only.
  It **exposes your home IP** to peers during the sync (onion peers still route
  through Tor) — `configure.sh` warns loudly, and it's opt-in with a Tor-only
  default. The Tor-only routing moved from `bitcoin.conf` to the entrypoint so it
  can be toggled; the default and its egress audit are unchanged.

## [1.20.1] - 2026-07-13

### Changed

- The auto-generated RPC username now defaults to the conventional `bitcoin`
  (was a random `rpc<hex>`); the password stays strong-random. Internal only —
  used between the dashboard and bitcoind.

## [1.20.0] - 2026-07-13

### Changed

- **Zero-config setup — `./stack up` and you're running.** The only thing setup
  used to require was inventing an RPC username/password; those are internal
  (dashboard ↔ bitcoind, private network) and are now **auto-generated** (strong
  random, reused on re-runs). `config.json` is fully optional — with none, the
  stack starts a full node over Tor on sensible defaults. `./stack up` renders the
  config and starts on first launch, so first-run setup is a single command with
  nothing to edit. `config.json` (copy `config.example.json`, then `./stack apply`)
  is now only for the optional extras: dashboard password, Tor onion, notifications,
  pruning, `blockfilterindex`, preloaded wallets.

## [1.19.0] - 2026-07-13

### Added

- **Watch-only history is cached across removal.** Removing a wallet no longer
  discards its balance history — re-adding the same key restores the sparkline
  (and the unloaded Core wallet reloads instantly, no rescan). History is now
  keyed by a hash of the wallet's key rather than its label, so a differently-
  keyed wallet that reuses an old name gets a fresh series (and existing history
  is migrated over automatically). Balances themselves already update live as new
  transactions arrive — the node keeps the loaded wallet current and the
  dashboard re-reads it every 15s.
- **Last-known balance while the node is unreachable.** If the node can't be
  reached, a wallet shows its last cached balance (dimmed, "last known" on hover)
  instead of a dash.

## [1.18.5] - 2026-07-13

### Fixed

- **Watch-only rows now line up in three columns** — label, sparkline, balance —
  that stay aligned across every row (and the total) regardless of how wide any
  balance is. The sparkline used to drift left/right with the balance width; it's
  now a shared subgrid so the lines and numbers all align.

## [1.18.4] - 2026-07-13

### Fixed

- **The mid-width layout (~900–1024px) no longer overlaps.** In that range the
  tower was squeezed against the card, so the balances floating on its axis ran
  under the card and off the right edge. The side-by-side breakpoint is now
  1024px (was 900); below it everything stacks (balances centred at the top,
  card, tower behind) — the same clean layout as on phones. At ≥1024px the
  balances float above the tower as before, now guaranteed to clear the card and
  the viewport edge.

## [1.18.3] - 2026-07-13

### Changed

- **Roomier watch-only balances.** The balances block is wider (up to 460px,
  ~¾ of the tower's span) and the sparklines are longer/bolder, so the section
  reads as part of the tower. It's capped to the viewport and the sparkline goes
  compact on phones, so a long label + large balance still fit on one line
  across all screen sizes.

## [1.18.2] - 2026-07-13

### Fixed

- **A very large balance no longer truncates the "Total" label** (it showed as
  "Tot…" with e.g. an 80,000 BTC wallet). The balances block now sizes to its
  widest row (capped to the viewport) instead of a fixed width, so the columns
  still align and nothing is clipped.

## [1.18.1] - 2026-07-13

### Changed

- **Watch-only balances now align in two columns** — labels left-justified,
  balances right-justified on the same line (the total too), in a fixed-width
  block so the columns line up cleanly on desktop and mobile. Long labels
  truncate with an ellipsis instead of breaking the layout.

## [1.18.0] - 2026-07-13

### Added

- **`blockfilterindex` option to speed up watch-only rescans.** Set
  `"blockfilterindex": true` (full node only) and Bitcoin Core builds a compact
  block-filter index once; then every wallet rescan — current and future — uses
  it to skip non-matching blocks, turning a multi-hour scan into minutes. A
  shared cache, ~a few GB of disk plus a one-time background build. `configure.sh`
  rejects it on a pruned node (bitcoind can't run both). See
  [docs/watch-only.md](docs/watch-only.md#speeding-up-the-first-scan).

## [1.17.0] - 2026-07-13

### Added

- **Watch-only balance history.** Each wallet's balance is now sampled hourly and
  persisted to the `dashboard_state` volume, so the trend survives restarts (the
  fee sparkline, by contrast, is in-memory and resets). A small gold sparkline of
  the history shows next to each balance above the tower. It holds only balance
  numbers + timestamps — never keys — and never leaves the box.
- **Birthday hint in the add form** — a note that setting a wallet's birthday
  skips years of rescanning.

## [1.16.0] - 2026-07-13

### Changed

- **Watch-only balances now show in gold above the tower.** Each wallet's
  balance — and a total once you have more than one — renders in the gold accent
  font above the block tower (at the top of the column on mobile). The card below
  becomes the manager.

### Added

- **Click a wallet's key to expand it.** Each row shows the key/address as
  first-4…last-4 next to the label; click to reveal the full string, click again
  to collapse.
- **Mobile: scroll past the cards to see the tower on its own.** Extra scroll
  room below the cards lets the full block tower fill the screen.

## [1.15.1] - 2026-07-12

### Fixed

- **The watch-only add-form hints no longer get clipped.** The label and key
  inputs are full width now (the date field shares a row with Add), so long
  placeholders like "xpub / zpub / address / descriptor" are fully visible.
- **The dashboard picks up new CSS/JS immediately after an upgrade.** Static
  assets are revalidated each load (cheap 304s) rather than heuristically
  cached, so a deploy's changes show without a hard refresh.

### Added

- **End-to-end coverage for watch-only wallets.** The e2e provisions a wallet of
  each supported key type against the real node — an xpub, a zpub, Satoshi's
  genesis address, and his genesis public key (both resolving to
  1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa) — and CI covers the same types with mock
  data.

## [1.15.0] - 2026-07-12

### Added

- **Watch a single address.** The watch-only key field now accepts a plain
  address (`bc1…`, `1…`, `3…`), not just an `xpub`/descriptor — handy for a
  specific receive address, a donation address, or a paper wallet. It's wrapped
  into an `addr()` descriptor for you.

### Fixed

- **Un-ranged descriptors (a single address or fixed key) failed to import.** The
  import always sent a `range`, which Bitcoin Core rejects on a non-ranged
  descriptor (`"Range should not be specified for an un-ranged descriptor"`), so
  the wallet was created but never populated. The range is now sent only for
  ranged (`…/*`) descriptors. Bad input also gets a clearer message ("not an
  xpub/ypub/zpub, an output descriptor, or an address").

## [1.14.2] - 2026-07-12

### Fixed

- **The dashboard now keeps matching space below the last card.** The bottom
  buffer had been set on the flex container, and browsers drop a flex
  container's padding on the overflow (bottom) edge when the content is taller
  than the viewport — so the last card sat flush against the bottom. The buffer
  now lives on the content column, where it's always part of the scroll area
  (top and bottom now match).

## [1.14.1] - 2026-07-12

### Fixed

- **The dashboard header no longer clips at the top in a short window.** The
  cards are centered in the viewport, so a stack taller than the window — a short
  window, or a browser banner (e.g. Chrome's "make default" prompt) eating
  height — pushed the header off the top edge with no way to scroll up to it. The
  layout now falls back to top-aligned when it overflows (`align-items: safe
  center`) and carries a top/bottom buffer.

## [1.14.0] - 2026-07-12

### Added

- **Manage watch-only wallets from the dashboard.** A "Watch-only balances" card
  now lets you add a wallet (label + `xpub`/descriptor + optional birthday) and
  remove it with a ✕ — no config editing. The list is saved to the stack (held in
  Bitcoin Core, with the roster on a new `dashboard_state` volume) and survives
  restarts; `config.json`'s `wallets` becomes an optional first-run seed. A total
  is shown only when you have more than one wallet. See
  [docs/watch-only.md](docs/watch-only.md).

### Security

- The add/remove endpoints turn the dashboard into a control surface, so they're
  guarded: the existing dashboard password (when set) gates them, a required
  `X-Requested-With` header blocks cross-site (CSRF) writes, keys are validated
  server-side, the wallet count is capped, and the card warns when no password is
  set. Wallet names render via `textContent` (no HTML injection).

## [1.13.0] - 2026-07-12

### Added

- **Watch-only balances.** Add public keys to `config.json` (`wallets: [{ name,
  key, birthday? }]`) and the dashboard shows each balance and a total, read
  straight off your own node — your addresses never touch a block explorer.
  `key` accepts an `xpub`/`ypub`/`zpub` (script type inferred from the prefix,
  SLIP-132 keys converted to the `xpub` Core needs) or a full output descriptor
  for exact control. The node holds one spend-disabled descriptor wallet per
  key, so there are no private keys and no spend risk; the first import rescans
  the chain (bounded by an optional `birthday`) and shows `scanning…` until
  done. Full node only — a pruned node can't rescan history, so it's skipped
  there. See [docs/watch-only.md](docs/watch-only.md).

## [1.12.2] - 2026-07-12

### Fixed

- **The update checker only alerts on a strictly newer version now.** It
  compared the running version against the latest release with `!=`, so if
  the box was briefly ahead of the GitHub "latest release" (e.g. right after
  a deploy, before the API propagated), it would report the *older* release
  as "available" — "🆕 stack v1.12.0 available (running v1.12.1)". Now it
  compares numeric version parts and stays quiet unless a genuinely newer
  release exists. Same fix for the Bitcoin Core check.

## [1.12.1] - 2026-07-12

### Fixed

- **Sub-1 sat/vB fees showed "—".** A quiet-mempool estimate like 0.44 sat/vB
  was rounded to 0, and 0 rendered as a dash (so "1h" showed "—"). Fees now
  keep one decimal (e.g. `0.4`); a dash means genuinely no estimate.
- **The displayed stack version could go stale.** It came from `.env`, so a
  deploy that ran `docker compose build && up` without re-running
  `configure.sh` kept an old version (and tripped the update badge). The
  version is now baked into the dashboard image (`COPY VERSION`; the build
  context is the repo root), so it always matches the running code.

## [1.12.0] - 2026-07-11

### Added

- **Fee sparkline (back).** A 24-hour sparkline of the next-block fee under
  the fee row, from a small in-memory series sampled once a minute (`fee_history`,
  served at `/api/fees`). Fee-only — no height series or tower dependency.
- **Tab favicon** — a bitcoin ₿ mark, so the dashboard is recognisable in a
  browser tab.

## [1.11.1] - 2026-07-11

### Fixed

- Unfilled tower cubes now recede as a faint grid instead of a solid fill —
  in light mode the "not yet" slots were brighter than the filled gold and
  read as a highlighted patch next to the pulsing loading cube. Now only the
  single loading slot stands out.

## [1.11.0] - 2026-07-11

### Changed

- **The tower fills to the time of day, driven purely by the clock.** A day's
  layer now fills to where the day should be at ~10-minute spacing (one cube
  per 10 UTC minutes) rather than tracking exact block arrivals — so it's
  solid up to "now" (e.g. nearly full late in the UTC day). The single
  estimate marker is gone; the fill *is* the estimate, with the loading slot
  pulsing.

### Removed

- The in-memory history sampler, the fee/height sparklines, and the
  `/api/history` endpoint — no local history is tracked anymore. The tower
  needs only the clock and the block height, so the node-timestamp
  "blocks today" lookup is gone too.

## [1.10.1] - 2026-07-11

### Fixed

- **The tower's "blocks today" is now correct immediately and survives a
  restart.** It was counted from an in-memory series pinned at the first
  sample after start, so a mid-day container restart reset the day and the
  tower looked near-empty. It's now computed from the node's own block
  timestamps — a once-a-day binary search for the first block at/after UTC
  midnight — so late in the UTC day it shows a nearly full layer (e.g. ~137
  of 144 at 23:38 UTC), regardless of when the container started.

## [1.10.0] - 2026-07-11

### Added

- **The loading block pulses** in the tower — the cube for the block being
  mined now flashes while it waits, instead of sitting static.
- **Time-of-day estimate marker.** A faint outlined cube marks where the
  day's fill "should" be by now at Bitcoin's ~10-minute spacing, so you can
  see at a glance whether blocks are running ahead of or behind schedule.
  It uses UTC time-of-day, matching the UTC day boundary (Bitcoin block
  timestamps are Unix/UTC time).

## [1.9.0] - 2026-07-11

### Added

- **Fee and block-height sparklines.** The dashboard samples height + fee
  once a minute into an in-memory 24-hour series and draws a sparkline under
  each. Served as JSON at `/api/history` (behind the dashboard's optional
  auth).
- **The tower is now a day-clock.** A 12×12 layer is one UTC day: the first
  cube is the first block after 00:00, cubes fill as blocks arrive through
  the day, and at midnight the day is pushed down and a fresh one starts
  (driven by the real "blocks so far today" count, not a fixed block index).
- **"loading block N" header** above the tower — the next block the network
  is working on.

## [1.8.0] - 2026-07-11

### Changed

- **Healthchecks pings retry until one succeeds.** The dashboard pings every
  5 minutes (matching the recommended check period), but a failed ping now
  retries each minute instead of waiting a full cycle — so a brief Tor blip
  can't trip a tight Healthchecks grace window.
- **Notifications guide rewritten** ([docs/notifications.md](docs/notifications.md)):
  step-by-step Telegram and Healthchecks.io setup, the recommended check
  settings (Period 5 min, Grace 1–2 min) that match the ping cadence, and how
  to route Healthchecks' own alert into the same Telegram group.

## [1.7.0] - 2026-07-11

### Added

- **Mempool & fees on the dashboard** (and `/metrics`): transaction backlog
  (count + size) and sat/vB fee estimates for the next / ~30-min / ~1-hour
  blocks. Shown only once synced (`estimatesmartfee` has no data during
  initial block download). New Prometheus series `bitcoin_mempool_txs`,
  `bitcoin_mempool_bytes`, and `bitcoin_fee_sat_vb{blocks="1|3|6"}`.
- **Opt-in new-block Telegram alert** (`notifications.alert_new_block`,
  default off): a 🟧 alert as the node accepts each new block once synced.
  Off by default because a synced node finds ~144 blocks a day; skips the
  catch-up burst during sync.
- **Supply-chain lint in CI**: gitleaks (secret scan) and hadolint
  (Dockerfile lint), plus GitHub issue/PR templates and a code of conduct.

## [1.6.0] - 2026-07-11

### Changed

- **The dashboard is now published on host port 80** (was 8000), so it's
  reachable at `http://<host>` with no port in the URL. The container still
  serves on 8000 internally.
- **Responsive layout:** on a wide screen the status card sits on the left
  and the block tower gets the right half of the page, so the tower is no
  longer hidden behind the card. On a phone it stays centred with the tower
  behind, and the card no longer overflows a narrow viewport.

## [1.5.0] - 2026-07-11

### Changed

- **The block tower is now live data, not decoration.** A 12×12 layer is
  144 blocks — about a day. The dashboard renders the node's block height
  into the page, and the tower fills a cube per block; when a layer (a day)
  completes it pushes the tower down a level and the oldest layers slide off
  the bottom. It rips upward during initial sync and drifts a single block
  in over ~0.8s once synced. Still theme-aware, throttled, hidden-tab-paused,
  and reduced-motion-safe.

## [1.4.0] - 2026-07-11

### Added

- **Auto light/dark theme** with a top-right toggle (Auto → Light → Dark):
  follows the system preference in auto mode and remembers an explicit
  choice. Styles moved to a served stylesheet built on CSS variables, with
  an inline init script that sets the theme before first paint (no flash).
- **Animated block tower** — a 5×5 isometric tower in the bitcoin accent
  rises endlessly behind the status card, fading in and out at the screen
  edges. Theme-aware, throttled to 30fps, paused on a hidden tab, and
  honours `prefers-reduced-motion`. Purely decorative.
- **In-place refresh** — the status panel updates by polling and swapping
  itself, replacing the full-page `<meta refresh>` so the theme and the
  tower animation never reset.
- Frontend unit tests (`node --test`) for the theme-cycle and tower
  projection/timing logic, wired into CI.

## [1.3.0] - 2026-07-11

### Added

- Dashboard shows the stack version (from the `VERSION` file, via
  `STACK_VERSION`) in the footer and on the sync/loading page.
- Dashboard header badge now always states the node mode — **Full** or
  **Pruned · N GB** — instead of showing a badge only when pruned.

### Changed

- The dashboard's "Version:" row is relabelled **Bitcoin Core:** to
  distinguish the node version from the new stack version.

## [1.2.1] - 2026-07-11

### Fixed

- Dashboard rendered `🆕 <built-in method update of dict object ...>` on
  every page: the stats key `update` shadowed Python's `dict.update` in
  Jinja attribute lookup, which is always truthy. Renamed to
  `update_note`; a regression test pins the default page to badge-free.

## [1.2.0] - 2026-07-11

### Added

- **`./stack` CLI** — `up`/`down`/`restart`/`logs`/`apply`/`status` plus a
  read-only `doctor` report (deps, config freshness, disk, sync, tor,
  onion addresses).
- **`./stack backup` / `restore`** — config, credentials, and onion keys
  in one dated tarball; chain data deliberately excluded.
- **Prometheus `/metrics`** on the dashboard (hand-rolled text format,
  behind the same optional basic auth).
- **Update checker** — daily, over Tor: new Bitcoin Core or stack releases
  trigger a 🆕 Telegram alert and a dashboard badge. Informational only.
- **Egress audit in the e2e** — asserts every established connection from
  the bitcoin and dashboard containers terminates inside the stack subnet;
  the Tor-only claim is now tested, not just configured.
- `VERSION` file (rendered into the stack for the update checker).
- **Release automation** — `scripts/release.sh` preflight (clean tree,
  semver, tag collision, changelog section) + a tag-triggered workflow
  that re-runs the full test gate and publishes the GitHub Release with
  notes extracted from this file and a source tarball. Documented in
  [Releasing](docs/releasing.md).

### Changed

- Self-healing decided as a deliberate non-feature (restart policy covers
  crashes; no Docker-socket access for anything else) — documented in
  [Operations](docs/operations.md#self-healing-a-deliberate-non-feature).

## [1.1.0] - 2026-07-11

### Added

- **Telegram alerts** (`notifications.telegram_bot_token` / `telegram_chat_id`):
  debounced one-way alerts — node down/recovered, initial sync complete,
  disk low, startup heartbeat. Sent over Tor (`socks5h`), so alerts are
  never a clearnet beacon. No commands, nothing controls the node.
- **Healthchecks.io dead-man's switch** (`notifications.healthchecks_url`):
  a ping every 5 minutes (`/fail` variant while the node's RPC is down),
  also over Tor — an outside service alerts you when the whole box goes
  dark, the one failure an on-box monitor can't report.
- **Dashboard over Tor** (`dashboard.onion`): publishes the dashboard as an
  onion service for remote access with no port-forwarding; `configure.sh`
  warns if enabled without `dashboard.password`.
- Monitor unit tests, e2e assertion for the onion provisioning, and a
  [notifications guide](docs/notifications.md).

## [1.0.0] - 2026-07-11

First tagged release.

### Added

- **Pruned node support** — `bitcoin.prune_mb` in `config.json` (`0` = full
  node, `≥550` = pruned target in MB). Full validation in ~30 GB.
- **Optional inbound onion service** — `bitcoin.inbound_onion: true` has
  bitcoind register a Tor onion service (cookie-authed control port) and
  serve blocks to the network without exposing the host IP or opening a
  host port.
- **Optional dashboard authentication** — `dashboard.password` enables HTTP
  basic auth.
- **Dashboard: pruned badge and low-disk warning** — shows pruned status
  with the prune target, and flags free space under 50 GB.
- **rpcauth** — bitcoind receives a salted HMAC instead of the plaintext
  RPC password; `docker inspect bitcoin` reveals no secret. Health checks
  authenticate with Core's cookie file.
- **Test suite** — configure.sh end-to-end test, docker-compose contract
  test, dashboard unit tests, and a full-stack e2e boot test that runs in
  CI (pruned, onion-enabled, auth-enabled).
- **CI + supply-chain hygiene** — everything pinned (image digests, pip
  versions, GitHub Actions by SHA) and maintained by weekly Dependabot.
- **Docs** — getting started, hardware, configuration, architecture,
  operations; SECURITY.md threat model; CONTRIBUTING.md; MIT license.
- Container log rotation (10 MB × 3 per service).

### Changed

- **Bitcoin Core image: `lncm/bitcoind:v28.0` → official `bitcoin/bitcoin`
  (v31.1)** — lncm stopped publishing in January 2025; the official images
  track every Core release, so Dependabot catches node upgrades.
- Stack subnet moved to `172.29.0.0/24` (the old `172.28.0.0/24` collides
  with Pithead's network on a shared Docker host).
- Credentials flow `config.json` → gitignored `.env` → container
  environment; no tracked file is ever modified.

### Fixed

- Dashboard Docker build (referenced a misnamed file).
- bitcoind flags silently dropped by YAML line folding in the compose
  entrypoint — the bug that motivated the e2e boot test.
- Tor data directory group ownership (`tor:root` → `tor:tor`) so the
  bitcoin container can read the control-auth cookie via gid 101.

[Unreleased]: https://github.com/VijitSingh97/node-manager/compare/v1.33.16...HEAD
[1.33.16]: https://github.com/VijitSingh97/node-manager/compare/v1.33.15...v1.33.16
[1.33.15]: https://github.com/VijitSingh97/node-manager/compare/v1.33.14...v1.33.15
[1.33.14]: https://github.com/VijitSingh97/node-manager/compare/v1.33.13...v1.33.14
[1.33.13]: https://github.com/VijitSingh97/node-manager/compare/v1.33.12...v1.33.13
[1.33.12]: https://github.com/VijitSingh97/node-manager/compare/v1.33.11...v1.33.12
[1.33.11]: https://github.com/VijitSingh97/node-manager/compare/v1.33.10...v1.33.11
[1.33.10]: https://github.com/VijitSingh97/node-manager/compare/v1.33.9...v1.33.10
[1.33.9]: https://github.com/VijitSingh97/node-manager/compare/v1.33.8...v1.33.9
[1.33.8]: https://github.com/VijitSingh97/node-manager/compare/v1.33.7...v1.33.8
[1.33.7]: https://github.com/VijitSingh97/node-manager/compare/v1.33.6...v1.33.7
[1.33.6]: https://github.com/VijitSingh97/node-manager/compare/v1.33.5...v1.33.6
[1.33.5]: https://github.com/VijitSingh97/node-manager/compare/v1.33.4...v1.33.5
[1.33.4]: https://github.com/VijitSingh97/node-manager/compare/v1.33.3...v1.33.4
[1.33.3]: https://github.com/VijitSingh97/node-manager/compare/v1.33.2...v1.33.3
[1.33.2]: https://github.com/VijitSingh97/node-manager/compare/v1.33.1...v1.33.2
[1.33.1]: https://github.com/VijitSingh97/node-manager/compare/v1.33.0...v1.33.1
[1.33.0]: https://github.com/VijitSingh97/node-manager/compare/v1.32.0...v1.33.0
[1.32.0]: https://github.com/VijitSingh97/node-manager/compare/v1.31.1...v1.32.0
[1.31.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.31.1
[1.31.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.31.0
[1.30.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.30.0
[1.29.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.29.1
[1.29.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.29.0
[1.28.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.28.0
[1.27.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.27.0
[1.26.2]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.26.2
[1.26.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.26.1
[1.26.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.26.0
[1.25.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.25.1
[1.25.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.25.0
[1.24.2]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.24.2
[1.24.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.24.1
[1.24.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.24.0
[1.23.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.23.0
[1.22.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.22.0
[1.21.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.21.0
[1.20.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.20.1
[1.20.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.20.0
[1.19.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.19.0
[1.18.5]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.18.5
[1.18.4]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.18.4
[1.18.3]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.18.3
[1.18.2]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.18.2
[1.18.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.18.1
[1.18.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.18.0
[1.17.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.17.0
[1.16.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.16.0
[1.15.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.15.1
[1.15.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.15.0
[1.14.2]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.14.2
[1.14.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.14.1
[1.14.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.14.0
[1.13.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.13.0
[1.12.2]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.12.2
[1.12.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.12.1
[1.12.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.12.0
[1.11.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.11.1
[1.11.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.11.0
[1.10.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.10.1
[1.10.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.10.0
[1.9.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.9.0
[1.8.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.8.0
[1.7.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.7.0
[1.6.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.6.0
[1.5.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.5.0
[1.4.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.4.0
[1.3.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.3.0
[1.2.1]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.2.1
[1.2.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.2.0
[1.1.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.1.0
[1.0.0]: https://github.com/VijitSingh97/node-manager/releases/tag/v1.0.0
