# Changelog

All notable changes to this project are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/); versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `utxo-index bench --chain bitcoincash` times Bitcoin Cash balance lookups with
  and without the index, as it does for Bitcoin and Litecoin. Bitcoin Cash Node
  cannot expand descriptors, so each Bitcoin Cash wallet in the file is one
  address written as `addr(<address>)`, and a descriptor is refused.

## [1.41.0] - 2026-11-24

### Added

- An **Events** button (three lines) between the User and Settings buttons
  opens the dashboard's event log. From this version on the log also records
  each dashboard start with its version, sign-ins with the client address,
  sign-outs, password changes, RPC credential reveals and resets, settings
  changes (field names only), balance index seeds and verifies, watch-only
  wallet changes, and node installs, deletions and activations, so
  `./stack events` shows them too. The panel also lists what the log already
  held, such as node down and recovered, initial sync complete, low disk, and
  host thermal and Tor actions. Filter it by category or node and page back
  through older events; the oldest drop off once the log passes about 1 MB. It
  reads `/state/events.log` through a new signed-in `GET /api/events`, and
  hides the text of host and monitor lines that look like they hold a secret.

### Changed

- Error messages on the watch-only wallet cards (Bitcoin, Monero, Tari,
  Litecoin, Dogecoin, Bitcoin Cash and Zcash) and in the node Activate and
  Deactivate dialog follow one rule: a reply of 1 to 200 characters is shown,
  and an empty or longer one is replaced by the card's own message. Before,
  most of these cards showed a long reply cut off at 200 characters, and the
  Bitcoin card showed it whole. The Monero and Tari cards still show any
  64-character hex string in a reply as "[redacted]".

### Fixed

- The Activate, Deactivate and Start buttons on node panels work again when
  some of Bitcoin, Litecoin, Dogecoin and Bitcoin Cash are turned off, as in a
  Bitcoin-only setup. The host helper's capability file has one entry for each
  of those chains that is off, and from 1.39.0 to 1.40.3 the dashboard stopped
  offering node power once that file passed 1 KB, usually with three or four of
  them off, depending on install paths. The buttons then stayed greyed out with
  "Node controls require the installed host helper." even though the helper was
  installed and other host actions worked.
- Telegram no longer gets a flood of old host alerts, such as thermal
  throttles from days ago, as it could after the upgrade to 1.40.3. Host events
  older than 6 hours are not sent, even ones that could not be delivered during
  a longer outage, though they stay in the event log. Each minute's host events
  arrive as one message of up to 20 events, followed by "… and N more host
  events" when there are more, so a heat spike that caps and releases several
  nodes sends one message a minute instead of one per node.

## [1.40.3] - 2026-11-24

### Added

- The APT feed address, https://vijitsingh97.github.io/node-manager/, opens a
  project page instead of a 404. The page carries a search and link-preview
  description, structured data, a sitemap and an `llms.txt` summary for AI
  agents, and the repository has the same `llms.txt` at its root. The page is
  published with the APT feed, so it goes live with this release (#443).

### Changed

- Release tarballs and Debian packages now contain only what installs and runs
  Node Manager. The guides, README, changelog, security policy, example config,
  Dockerfiles and Go and web sources are no longer shipped; read the guides on
  GitHub. The license is kept (`LICENSE`, and `/usr/share/doc/node-manager/copyright`
  in the package).

### Fixed

- Installing a node from the dashboard on an APT install no longer fails with
  "Port availability could not be checked" when you turn on a LAN port.
- Litecoin, Dogecoin and Bitcoin Cash installed from the dashboard now get the
  2048 MiB memory limit the documentation promises, not double that. Nodes you
  already installed keep the value saved in their settings. The memory figures in
  the hardware and configuration guides now agree.
- Telegram alerts for the host, such as a thermal throttle or a node the host
  stopped, are now sent. They were never sent in the running dashboard.
- A failed `stack init` no longer leaves copies of your `.env`, passwords
  included, in the state folder.
- If removing the Tari wallet fails and is rolled back, the wallet now starts
  again instead of sitting stopped while its health check stays green.
- `stack status` now checks the Tari wallet, so an unhealthy one is reported and
  the command exits non-zero.
- Every APT package upgrade now also downloads the new image of each node that
  already has an older image on the machine, whether the node is running,
  inactive or deleted, so Activate no longer fails after an upgrade. Nodes that
  were never downloaded are not pulled, and a failed extra pull only warns.
- `node-manager pull`, the upgrade step for Homebrew and source installs, now
  also downloads the new image of every other node that already has an image on
  the machine, including inactive and deleted nodes, so Activate works after
  those upgrades too. These extra pulls never start a container, and a failed
  one only warns.
- Activating a node that was inactive during a package upgrade now downloads its
  missing image first instead of failing with no explanation. A failed
  activation keeps its reason, including the last error line, in the node panel
  and the event log until the node reaches the requested state.
- A node that was just activated now says "Starting…" until it answers, instead
  of "Restarting after a settings change…". Restarts caused by settings changes
  keep their wording.

## [1.40.2] - 2026-11-22

### Fixed

- The balance index badge and bar now show on Litecoin, Dogecoin and Bitcoin
  Cash when Bitcoin is not installed. The badge and card use one name per index
  state, refusals are explained in plain words instead of internal guard names,
  the bar keeps its warning and ready colours on every chain, and a blocked bar
  shows as an empty hatched track instead of moving like progress (#426).
- The wallet list refresh no longer collapses an expanded address or moves
  keyboard focus, and waits while a dialog is open. The 30 s live refresh keeps
  focus on the price currency menu (#433).
- Closing Settings asks before discarding staged installs, moves, deletes,
  config runs and Reset to default. If the browser closes Settings without
  letting it ask, staged changes that were not saved yet are discarded and
  Settings says so when it opens again; a save already running carries on.
  Saving block alerts with Telegram off says no alert will be sent. Escape
  closes only the open dialog, not the node panel behind it. Turning off an
  index and removing a wallet use the dashboard's own confirm dialog (#434).
- On phones, the node panel close button, chain values and the tower overview
  are no longer covered by the theme toggle, the fixed Settings and account
  buttons or the host stats panel, and the add-address form shows its full
  hints on desktop (#430).
- Dashboard text meets WCAG AA contrast in both themes on the page, cards,
  tags and host-stats tiles, including each node's own text colour, and a test
  computes those pairs from the stylesheet. Focus rings reach 3:1 and stay at
  full strength on inactive towers, stale balances and prices show in the
  muted colour in italics instead of fading, unreachable towers say so in text
  and to screen readers under the same rule, and the CPU and memory pies have
  a legend (#431).
- The shortened wallet address, the host usage pies and the disk bars work
  from the keyboard. The theme toggle announces its current mode, and the
  chain cards no longer make screen readers repeat them on every refresh
  (#432).
- Dashboard instructions name the real place for each setting (the User
  dialog, Node controls, the launch option), show the command for how Node
  Manager was installed once the host helper has reported it (`sudo
  node-manager` for APT, `node-manager` for Homebrew, `./stack` otherwise),
  give every chain the same
  recovery hint when its node is not answering, and use one casing and
  ellipsis style. The Bitcoin and Monero LAN access text follows the RPC
  setting (#427).
- The Bitcoin card shows its update time in the browser's time zone, a Latest
  block row and a Synced or Synchronizing line like the other chains, and all
  watch-only cards share one heading style, an Add wallet button, the same
  scan-failure text and the same add and remove confirmations (#428).
- Settings shows why a control is disabled (Delete, a balance index switch,
  Install) and the "Click to undo" hint as text instead of only a tooltip, and
  the Thermal throttle switch moved from Appearance to the Node set group
  (#435).
- The docs match the dashboard again: the password is in the User dialog, the
  Node set and Thermal throttle locations, alert names, GiB units and the
  watch-only wallet wording (#429).

## [1.40.1] - 2026-11-21

### Fixed

- `node-manager backup` no longer warns "dashboard onion keys not captured
  (is the tor container up?)" when the dashboard onion is off. The warning
  appeared on every backup with `dashboard.onion` off, the default, and pointed
  at Tor although nothing was missing. A dashboard onion key left over from an
  earlier enable is still backed up.

## [1.40.0] - 2026-11-21

### Added

- Automatic recovery when Tor stops completing circuits. The tor container's
  healthcheck opens a real stream through Tor's SocksPort to
  `www.torproject.org:443`, `check.torproject.org:443` and `api.github.com:443`
  (the last is already reached through Tor by the dashboard) every 30 seconds
  and goes unhealthy after 4 minutes of failed probes, or when fewer than half of
  the probes in the last 10 minutes succeeded, so a brown-out with a few
  successes is caught too. Three good probes in a row clear it. Time while Tor
  reports the network down with no relay connection is not counted, so a host
  outage costs at most one restart, about 6 minutes in, before Tor notices it is
  offline. Tor's control socket (a unix socket inside the
  container, no `ControlPort`) is read only for context. `node-control` saves a
  diagnostic snapshot, restarts Tor once, clears Tor's saved state (guards and learned
  circuit timing; the whole state file is deleted) once if it still fails while
  relay connections are up (at most once in 7 days), and otherwise pauses with an event naming the likely cause. A
  container unhealthy for 10 minutes with no probe result is handled the same
  way. It resumes by itself when the probe succeeds, logs the recovery, timed from the first failed probe, to the
  events log (forwarded to Telegram once Tor works again), never restarts more
  than 3 times in 6 hours, and can be turned off with
  `node-control/tor-watchdog.disabled` in the state directory.

- `./stack utxo-index verify [--chain bitcoin|litecoin|dogecoin|bitcoincash]`
  (`node-manager utxo-index verify` on package installs) checks a balance index
  against the node's own UTXO set. It is meant for after a seed. The dashboard
  waits up to 10 minutes for the index to reach the node tip. It then counts the
  index and calls the node's `gettxoutsetinfo` at that same block, and prints
  both coin counts and totals. The verdict is PASS, or FAIL with a non-zero exit.
  Only PASS exits 0. FAIL, every refusal (printed as `refused:`) and a command
  timeout exit 1. The refusals cover an index behind the tip, a tip that moved, and
  a node call that failed or timed out. If the dashboard stops mid-wait, the command
  says so and exits 1 at once.
  The call uses `hash_type none` on Bitcoin Core 32.1, Litecoin 0.21.5.8 and
  Bitcoin Cash Node 29.2.0. Dogecoin Core 1.14.9 takes no argument and always
  hashes. The walk takes no `cs_main` in any of these versions, but node-down
  alerts are still held while it runs, for at most 30 minutes. The hold is saved
  with the seed state, so a dashboard restarted during the node's walk keeps
  holding alerts until that deadline. The command
  confirms first, or takes `-y`. Regtest runs on all four nodes matched.
  Mainnet timing is not measured.

- `tari.grpc_extra_methods` allows more Tari gRPC methods on top of the built-in
  list, for example `["list_headers"]`. `configure.sh` accepts only lowercase
  snake_case names and passes them to the node, which adds them to its
  `grpc_server_allow_methods`.

### Changed

- `configure.sh` reads `config.json` with two `jq` runs instead of about 130. On
  an Apple Silicon Mac a run took about 0.3 s instead of 1.2 s; the Intel mini PC
  is not measured. A node power change runs it twice, so the container gets its
  stop or start sooner. The `.env` it writes and its validation messages are
  unchanged.

- On Debian and Ubuntu a dashboard request now starts its host helper at once.
  New `node-manager-node-control.path` and `node-manager-port-control.path`
  units watch the request files in the dashboard state volume, and upgrades
  enable them. The helper timers now run once a minute (were every 10 seconds for
  node control and 30 seconds for LAN port control). That is the slowest rate
  that keeps each host capability inside the dashboard's 90-second limit. An
  idle run also costs less. Node control makes 2 Docker calls instead of 6 and
  4 jq calls instead of 18. Port control makes 2 or 3 Docker calls instead of 9.
  Measured in docker-in-docker on an Apple M5 (median of 6 interleaved rounds of
  20 idle runs, Docker daemon included): node control fell from 398 to 123 ms of
  CPU per run and port control from 299 to 102 ms. At the new rates that is
  about 14 CPU-seconds an hour instead of 179. A Docker data root outside
  `/var/lib/docker`, or another project name, is added to the watch when Node
  Manager starts. In the first 5 minutes after boot a helper that cannot reach
  the dashboard retries every 10 seconds. Homebrew's host
  loop still runs both helpers every 10 seconds, since launchd cannot watch files
  inside Docker Desktop's VM, and gets the cheaper runs.

- APT upgrades survive a transient image pull failure, and a failed activation
  no longer looks like success. On 2026-11-20 one ghcr.io blob failed through
  Tor during a 1.39.0 upgrade. Activation gave up after 985 s, apt still
  reported success, and a manual retry worked in about 5 minutes. Activation now
  pulls each running service's image on its own, with a 10-minute limit, up to
  3 tries 15 s and 30 s apart, and logs each try's duration in the APT output.
  Pulls use the same Docker daemon path as before. If activation still fails,
  APT keeps the package installed and exits 0, so later APT runs are not
  blocked. The last line Node Manager prints then starts with
  `NODE MANAGER ACTIVATION FAILED` and gives the retry command, and
  `node-manager doctor` repeats it until an activation succeeds.

- The Bitcoin Cash balance index fits the same 1 GiB dashboard limit as the other
  indexes (#363). Its overlay now sets `mem_limit: 1g` and no `GOMEMLIMIT`,
  instead of 2g with `GOMEMLIMIT=1600MiB`. A block of more than 50,000 coin
  operations is applied in pieces of 50,000, each committed on its own, so no
  database batch or undo record holds a whole 32 MB block. Only the last piece
  moves the index's tip. Pieces left by a stop or crash are rolled back when the
  index reopens, and a reorg undoes such a block piece by piece. On the regtest
  replay of four 31 MB blocks at 0.5 CPU, the dashboard under 1 GiB finished in 78
  to 109 s and peaked at 0.69 to 0.77 GiB RSS and 296 MiB of live heap. The same
  replay with whole blocks under the 2 GiB cap took 966 s and peaked at 1.57 GiB
  RSS and 945 MiB, in the same session. Mainnet is not measured. A block over 1.2
  million coin operations still stops the index, because the parsed block stays
  whole while its pieces apply.
- The host helper does less work per power request. It reads a request in one
  dashboard call instead of four and its fields with one `jq` call. Its own
  `stack up` no longer renders the config a third time or re-registers the host
  timers and agent. It refreshes its heartbeat when a request finishes, so after
  a stop that took longer than 90 seconds the dashboard shows node controls
  again at once instead of at the next timer run. A request read that fails
  partway is retried instead of rejected. The node and wallet service lists and
  the runtime state expression are each written once.
- The tor healthcheck runs every 30 seconds with a 75-second timeout, 1 retry
  and a 2-minute start period (was 60 seconds, 10, 3 and 5 minutes).
- The health probe opens Tor's control socket only after a failed probe, so a
  healthy Tor logs no control connections.
- The package ownership check before every start and stop asks Docker once for
  all of the project's containers. It used to ask once per service and twice
  more for each existing container, up to 33 calls.

### Fixed

- Activating a node from the dashboard no longer hangs the host helper. The
  helper held the config lock and then ran `stack up` without saying so, and
  `stack up` waited for that same lock, so the request could not finish and the
  dashboard soon reported the host helper missing. The helper also approved the
  change only after starting, so `stack up` would have started the node from the
  old approved config. It now approves an activation before starting the node,
  passes the lock it holds, and after a failed change re-approves the restored
  config before bringing services back. Deactivating was not affected.
- The host helpers (`node-manager-node-control` and `node-manager-port-control`)
  no longer fail their systemd unit when the dashboard container is stopped,
  restarting or being recreated, as it is during a container update. Such a run
  is skipped and the next timer or path trigger tries again. Other Docker errors
  still fail the unit.
- The Bitcoin node card shows its balance index bar. Since 1.39.0 the page
  fetched the Bitcoin index status only while the loading screen was up, so a
  running Bitcoin node never showed the bar. Litecoin, Dogecoin and Bitcoin
  Cash were not affected.
- The stack survives the host getting a different LAN address after a
  reboot, power cut or network change. With `dashboard.bind_ip` set to `auto`,
  the host helper waits until the old address has been gone for a minute and the
  new one has held for that minute. It then re-renders the last approved
  configuration and recreates only Tor and the dashboard on the new address. A
  pending `config.json` change stays pending. LAN RPC, ZMQ and gRPC listeners
  follow only onto the same network, because Tari gRPC and Monero ZMQ have no
  password. When the old address is inside the new address's subnet, listeners
  on it move in both `config.json` and the
  approved snapshot, even when the 1.39.0 failed-start recovery re-rendered the
  dashboard first. On any other network, or next to a `/32`, point-to-point or
  unreadable subnet, listeners whose address is gone are paused: rendered
  unpublished with the configuration unchanged, so Tor still starts. They are
  published again once their address has been back for a minute. It also moves
  the dashboard off the `127.0.0.1` fallback left by a start before the network
  came up, and recreates a dashboard that kept the old address, both of which
  the 1.39.0 failed-start recovery could cause. A fixed `dashboard.bind_ip` that
  disappears is reported once in `./stack events` and left unchanged.
  `./stack doctor` lists every published address the host no longer has and
  every paused listener. `configure.sh --detect-bind-ip` prints the address
  `auto` would pick.

- The Tari node no longer makes Docker log a DNS resolver error every 2
  minutes. Its Tari Pulse service looked up `checkpoints.tari.com` through the
  system resolver, which the Tor-only network can never answer. The node config
  now sets `tari_pulse_enabled = false`. Sync is unchanged, and the dashboard
  never read the checkpoint result.
- Node activate and deactivate rejections now say why, for example "Bitcoin was
  not changed: review the pending config change first", instead of a generic
  "state change was rejected". Unknown reasons keep the generic wording.

## [1.39.0] - 2026-11-20

### Added

- Bitcoin Cash's balance index (`bitcoincash.utxo_index`) works on a pruned node.
  Bitcoin Cash Node 29.2.0 has no coin-set export, so a seed now works like
  Dogecoin's: the node stops briefly while its chainstate is copied, then the
  dashboard builds the index from the copy and follows raw blocks from there. The
  reader was checked against a real Bitcoin Cash Node regtest chainstate (every
  script type, CashTokens outputs unwrapped to their script, coinbase, spent and
  unspendable outputs, multi-byte output indexes); the fixture is rebuilt by
  `tools/regenerate-bitcoincash-chainstate-fixture.py`. The copy marks Bitcoin
  Cash as restarting, so it raises no node-down alert, and it is refused if the
  node was flushing when it was taken. Seed time and index size on mainnet are not
  measured yet.
- `./stack utxo-seed bitcoincash --replay` replays every block of an archival
  node, the previous behaviour, which is still what an install with no host
  helper uses automatically.
- The install dialog offers "Instant balances (balance index)" next to the data
  folder for Bitcoin, Litecoin, Dogecoin and Bitcoin Cash, ticked by default. The
  install request carries an optional `utxo_index` boolean; the host helper accepts
  both request shapes, and the dashboard sends the option only when the helper
  reports `install_index_supported`.
- Every node card shows a progress bar for its balance index (waiting for the node
  to sync, building with a percentage, catching up, ready, seeding failed), hidden
  when the index is off. `/api/utxo-index` gains `node_synced`, `waiting_for`,
  `needs_host` and `seed_stopped`.
- Settings shows the host's real rejection message (for example which node an index
  option needs) instead of only "The requested host change did not pass
  validation", and the host helper reports it as the status `detail`.
- The host helper refuses a seed with `busy` while another index is seeding or a
  chainstate copy is in progress, and with `recently_started` within 10 minutes of
  the node starting; neither stops the node, and neither counts as a failed seed.

### Changed

- The Seed now buttons and the explanatory paragraphs under Settings → Balance index
  are gone. An index seeds itself as soon as it is on and the node has synced, on
  every chain, without asking: never while the node syncs, within 10 minutes of its
  start, or beside another seed; those waits re-check every 30 seconds. A seed whose
  host preparation fails (a chainstate copy exit 2 or 3) now counts towards the
  three-failure stop, so a Dogecoin or Bitcoin Cash node is not stopped every hour
  indefinitely, and an index that turns invalid is reseeded (not within a day of a
  good seed). After the third failure the bar reads "seeding failed" with the
  reason, and turning the index off and on clears the count.
- A failed or rejected host change in Settings no longer drops the changes queued
  after it. Changes are matched to the host's result by action, node and request id,
  independent changes still go out (only later changes to the same node are skipped),
  and the final line names each failure with its real message and what was skipped.
- Settings greys out a balance index switch the host would refuse and shows the
  host's reason as its tooltip (`install_index_supported` and `utxo_index_blocked`
  in the capability file; the dashboard also refuses such a request itself).
- `node-control` accepts the install request with or without `utxo_index`, so a new
  dashboard and an old helper (or the reverse) still install: the dashboard omits the
  option when the helper does not report support for it.
- Upgrade: existing installs keep their current index setting. Nothing is turned on,
  seeded or restarted by the upgrade, an index that is off stays off (turn it on in
  Settings and it seeds itself), and a seed that already failed three times keeps
  its stop until you turn the index off and on. A host that never set an index key
  still defaults to off; the default-on applies to nodes installed through the
  dialog, which writes the key you choose.
- Dashboard memory is unchanged: 1g for any mix of Bitcoin, Litecoin and Dogecoin
  indexes and 2g with `GOMEMLIMIT` 1600MiB once Bitcoin Cash is on. Memory with all
  four indexes and one seeding has not been measured; see `docs/hardware.md`.

- A Bitcoin Cash install from the dashboard is now pruned by default (10,000 MiB
  of block files) with its balance index on, instead of archival. The install
  disk estimate reads "Not measured" until a pruned sync is measured.
  `configure.sh` and the host helper no longer refuse the index on a pruned
  Bitcoin Cash node. Existing installs keep their settings; nothing seeds or
  restarts on upgrade.
- A seed request may name its `mode` (`chainstate` or `replay`); a request with
  no mode behaves as before.
- Zcash runs Zakura 1.8.9 (from 1.8.8).

### Fixed

- Node cards no longer show a dash for Node Data Size and Uptime when the node
  cannot report them (Tari, Zcash, and Dogecoin's uptime). The dashboard falls
  back to the host collector's measured data directory size and the node
  container's start time, both added to `host-stats.json` (`started_at` per
  node), and still shows a dash when that file is missing or stale. A node's own
  size and uptime win when it reports them. The fallback size is the whole data
  directory and can lag by one measurement cycle, and the host collector now
  inspects each running node container once per sample to read its start time.
- The Bitcoin card shows a dash for Connections when `getpeerinfo` fails instead
  of `0 in · 0 out`.
- The Bitcoin and Monero cards no longer show an empty Open ports row.
- A node that is down, starting or not answering keeps its tower in the skyline
  view. It is drawn greyed out at the last height and sync progress the dashboard
  saw, with no "loading block" label, and the coin's name on its card turns red
  (the card shows even with tower labels off). The tower returns to its colours
  and label as soon as a status poll gets a height again. A node counts as
  unreachable when it is switched on but stopped, restarting, or without a
  successful health probe in the last 2 minutes, and its latest status poll gave
  no height. The height and progress come from `monitor_state.json`
  (`last_block` and the new `sync_percent`), so they survive dashboard restarts,
  and `/api/nodes` now returns `last_height` and `last_sync_percent`. A node that
  has never reported a height draws no tower, only its card with the red name.
  A deactivated node also keeps its greyed tower, but its name stays grey.
- Tari no longer reports height 0 while it starts up. Its sync progress call
  returns zero heights then, so the dashboard keeps the stored tip height. This
  caused the "loading block 1" label with no tower after a power cut.
- The stack comes back after a power cut or reboot even when the host gets its
  LAN address late. Docker gives up on a container it cannot start: when the
  host booted before DHCP assigned the address Tor publishes the dashboard on,
  Tor stayed down, taking the dashboard, node peers and alerts with it until
  someone recreated it by hand. With Launch at boot on, the host helper now
  recreates any stack container that Docker could not start because a published
  port could not be bound, retrying after 30 s and then less often (at most every
  10 minutes) until it starts, and records it in `./stack events`. Other start
  errors, containers you stopped and deactivated nodes are left alone.
  `up --force-recreate <service>` recreates a service by hand.

## [1.38.0] - 2026-11-17

### Added

- Instant Dogecoin balances: `dogecoin.utxo_index` (Settings → Balance index →
  "Instant Dogecoin balances") keeps a Dogecoin balance index, on a pruned node
  too, and the Dogecoin watch-only card reads its balances from it once it is at
  the node's tip. Dogecoin Core 1.14.9 has no coin-set export, so a seed stops the
  Dogecoin node while its chainstate is copied, starts it again, and
  builds the index from the copy; the index then follows new blocks, merge-mined
  ones included. A Dogecoin install from the dashboard turns the index on, and the
  archival watch wallet is no longer needed for balances. If a pruned node drops
  blocks the index still needs, Settings offers Seed now. The copy marks
  Dogecoin as restarting while it is stopped, from Settings or the command line,
  so it raises no node-down alert. Dogecoin is not in `utxo-index bench` or its CI
  test, since Dogecoin Core 1.14.9 has none of the RPCs it uses; its index is
  checked by a regtest test against the node's own `gettxout`. The seed time and
  index size on mainnet are not measured yet.
- Host vitals redesign: the Node Manager usage panel now shows Temperature,
  Fan, Uptime and Network as four tiles above the disk bars. Temperature has a
  dot that runs from blue to red up to the 88 °C throttle limit, the fan icon
  spins in proportion to rpm over the fan's maximum (still at 0 rpm), uptime has
  a ring that fills over each day, and network down and up are green and blue.
  The collector now reports each fan's maximum (`fans_max_rpm`, from hwmon
  `fanN_max`). Animations stop when the system asks for reduced motion.
- Instant Bitcoin Cash balances: `bitcoincash.utxo_index` (Settings → Balance
  index → "Instant Bitcoin Cash balances") keeps a Bitcoin Cash balance index, and
  the Bitcoin Cash watch-only card reads its balances from it once it is at the
  node's tip. It needs an archival node (`bitcoincash.prune_mb: 0`). Bitcoin Cash
  Node has no coin-set export, so the index replays every block from genesis once
  the node has synced: a long one-time job that keeps its progress across stops
  and restarts. A Bitcoin Cash install from the dashboard is now archival with the
  index on, and the install dialog shows the archival size. Existing installs keep
  their setting. The Bitcoin Cash card now holds 50 addresses. With the index on,
  the dashboard's memory limit is 2 GiB with `GOMEMLIMIT=1600MiB`, not 1 GiB:
  plan for the dashboard using up to 2 GiB on top of the Bitcoin Cash node's own
  plan in docs/nodes.md. That is a cap, not memory set aside: on ordinary blocks
  the dashboard should stay near today's use, and it only climbs while it applies
  a block of tens of MB. It applies a block of up to 1.2 million coin operations;
  a larger block, or one over 32 MB, stops the index at that height without
  discarding it, until such blocks can be applied in pieces (#363). On regtest,
  31 MB blocks of about 0.9 million operations peaked at 1.50 GiB; these are
  lower bounds, and mainnet is not measured yet.
- A weekly, on-demand test that starts every node (Tari and its wallet, Litecoin,
  Dogecoin, Bitcoin Cash and Zcash as well as Bitcoin and Monero) and checks that
  none can reach the internet or resolve a public name while Tor is stopped.
- `utxo-index bench --chain litecoin` measures the Litecoin balance index the
  same way as Bitcoin's; `--chain bitcoin` is the default. With `--scan` the
  bench now runs `scantxoutset` for each wallet alone as well as for all of them,
  prints each wallet's scan time next to its index p50 with the speedup, and
  exits non-zero when a wallet's index balance differs from its scan at the same
  block. A CI test per indexed chain checks the index against a fake node's scan
  on pseudo-random wallets and that it is faster by at least 50 times for a single
  address and 2 times for a 2,000-script wallet (synthetic, #367).

### Changed

- The Debian and Ubuntu host helpers poll less often to cut idle CPU: node
  control every 10 seconds (was 2) and LAN port control every 30 seconds (was
  15). A node or credential change from the dashboard can now take up to about
  10 seconds to start, and a LAN port change up to about 30 seconds. Port
  control looks up the dashboard container once per run instead of for every
  command, and the resource-profile helper reads the container list and `.env`
  once per run instead of calling `docker compose ps` and `sed` repeatedly.
  Homebrew's host loop already ran every 10 seconds and is unchanged.
- The actuator endpoints now use the same login as the dashboard (user `admin`
  and the dashboard password, over HTTP Basic auth) instead of a generated
  `actuator` login. Settings no longer shows an actuator username and
  password, and a leftover `actuator-credentials.json` is deleted on start.
  A wrong login still gets a 401 with a `WWW-Authenticate` challenge, a
  switched-off endpoint a 404, and repeated wrong passwords share the dashboard
  sign-in limit. A monitor given this login can also sign in to the dashboard.
  Upgrade note: monitors that sent the generated login must be changed to the
  dashboard login.
- Settings → Actuator has **Copy curl for health** and **Copy curl for info**
  buttons, shown while the matching switch is saved on. Each asks for the
  dashboard password, has the server check it, then copies a complete curl
  command for the endpoint with the admin login and that password filled in. On a plain
  `http://` address, where the browser blocks clipboard access, it falls back
  to a selected field with a **Copy again** button (pressed with the click, so
  Safari allows it) or Ctrl+C / Cmd+C. The command holds the password and will
  stay in shell history.
- The `WWW-Authenticate` challenge is now sent only with actuator 401 and 429
  answers, and a leftover actuator credentials file that cannot be deleted is
  logged with a fixed message.
- Host vitals now refresh every 10 seconds on Debian and Ubuntu installs. A
  small Go helper, the new recommended `node-manager-hostd` package, replaces the
  shell collector on a systemd timer and is much cheaper than the minute-by-minute
  shell run. Existing installs add it once with
  `sudo apt-get install node-manager-hostd`; without it the shell collector runs
  as before. Homebrew installs are unchanged.

### Fixed

- Turning Launch at boot off on an APT install no longer fails with "Launch
  setting is already in that state", and a Settings change that is already in
  effect no longer drops the changes queued after it, such as a node delete.

## [1.37.0] - 2026-11-17

### Added

- Tests that fail when anything in the scripts or the Docker stack could leave
  the Tor boundary, and `docs/tor-boundary.md`, which lists every egress path
  with file and line evidence, the gaps found, and how to extend the tests. A
  stdlib-only Python test checks the Compose files (only `tor` has an external
  network, loopback-only DNS, no published node ports, no proxy overrides), the
  Tor relays, and the Tor settings of every node, and scans scripts,
  entrypoints and Dockerfiles for network commands against
  `tests/tor_boundary_allowlist.txt`. A Go test walks the dashboard sources and
  fails on any new direct network use (including the Tari wallet launcher), and the container end-to-end test now
  probes every running service for direct TCP, IPv6 and DNS egress with Tor
  stopped, with a positive control from the Tor container.
- Settings → Node set has a **Delete…** button next to **Move data…** that
  removes a deactivated node so it can be installed again from scratch. It is
  disabled for an active node, a node that is not installed, and the last
  installed node. A dialog warns that the step cannot be undone, names the data
  folder, balance-index or wallet volumes, configuration and credentials, and
  watch-only wallets with their saved balances and history that will go, and
  needs the node's name typed before it enables **Delete on Save**. The host
  re-checks the request, applies the smaller configuration first (a failed apply
  restores the old one and deletes nothing), refuses unsafe data folders
  (symbolic links, `/`, your home, the app, state, config or Docker
  directories, another node's folder), then removes the node's containers,
  volumes and data, clears its resource-profile and seed state, and logs a
  `Host: <Node> deleted` event. The dashboard drops that coin's watch-only
  wallets and balance history and keeps retrying across restarts; a failed
  handoff of that removal to the dashboard is retried on the next helper run.
- Instant Litecoin balances: `litecoin.utxo_index` (Settings → Balance index →
  "Instant Litecoin balances") keeps a Litecoin balance index, and the Litecoin
  watch-only card reads its balances from it once it is seeded, updating within a
  minute of each block. It is on by default when Litecoin is installed from the
  dashboard, seeds itself like the Bitcoin index, and has `utxo-seed litecoin`
  and `utxo-index remove litecoin`. Only one balance index seeds at a time.
  Balances cover transparent outputs only; funds inside MWEB are not counted.
- Nodes that are restarting because of a settings change now turn grey with a
  **Restarting** badge and "Restarting after a settings change…" on the panel,
  instead of looking down or connecting. A node counts as restarting from the
  moment an apply of the pending configuration, a network setting change (clearnet
  over Tor, incoming connections), a node install or data move, a node
  activation, or an RPC password reset is requested until its RPC answers again,
  for at most 10 minutes. `/api/nodes` reports it as `restarting`, so it survives
  a page reload, and node-down alerts stay quiet meanwhile; the dashboard keeps
  probing restarting nodes so the flag clears as soon as they answer. After an
  apply the host helper lists the services it recreated in the status file, so
  only those nodes show as restarting. A node that is known to
  have failed to start still shows Stopped with the reason. LAN port changes
  restart only Tor and the dashboard, so nodes stay as they are.
- The Bitcoin balance index is on by default for new pruned installs. APT and
  Homebrew setup, and installing Bitcoin from the dashboard, write
  `bitcoin.utxo_index: true` when the node is pruned; existing installs and
  archival nodes keep their setting. An enabled index that has never been seeded,
  or whose last seed failed, now seeds itself: once an hour the dashboard checks
  the seed guards and, when they pass, asks the host helper to prepare the seed
  pipe and request, so no `utxo-seed` command is needed. A refused check stays
  in the index status with its reason. A seed that starts and fails, or is cut
  short by a dashboard restart, is retried after 6 hours, then 24 hours, and not
  again after a third failure until you start one. Cancelling a seed with
  `utxo-seed cancel` stops automatic seeding until you start one. The automatic
  seed holds node-down alerts as a manual seed does.
- A **Seed now** button on the balance index card and in Settings → Balance
  index starts a seed through the host helper when the index needs one. The
  `utxo-seed` command is shown only where host controls are unavailable.
- UI flow tests drive the real dashboard in headless Chrome: they flip every
  Settings control on and off through the page, save, and check both what the
  server or host helper received and what the reopened dialog shows, covering
  appearance, Telegram, Healthchecks, health endpoints, new-block alerts, the
  host switches and balance indexes (including a cancelled turn-off), Run new
  config, Install and Move data staging, and node Activate, Deactivate and Start.
  They run in CI as a "UI flows" step; see CONTRIBUTING.md. This is the first
  phase: the password form, LAN ports, credential reveal, sign out, theme toggle,
  per-card currency selects and wallet add and remove are not covered yet.
- A node whose container is stopped outside the dashboard (`node-manager stop`,
  `docker stop`, a crash that exhausted its restarts) now shows its tower greyed
  with a Stopped status and a Start node button instead of Connecting forever.
  The resource-profile host helper reports each node container's running state,
  and the dashboard treats missing or stale reports as running. Starting an
  already-active node re-runs its services.
- The Node manager usage panel now shows the host's CPU and board temperature,
  fan speed, uptime and network rate (received and sent, summed over the
  physical interfaces). The resource-profile host helper collects them with the
  other host figures, reading the same thermal zones as the thermal throttle;
  each reading the host cannot provide, such as every sensor on Homebrew for
  macOS, shows "not available". No interface names or addresses are shown.

### Changed

- The two actuator endpoints now have separate switches in Settings → Actuator
  (**Serve /actuator/health** and **Serve /actuator/info**, both off by
  default), and both always need HTTP Basic auth with a generated actuator
  username and random password. Settings shows both with a Copy button. The
  anonymous overall-status answer is gone, the dashboard `admin` login no
  longer opens them, wrong logins get a 401 with a `WWW-Authenticate`
  challenge, and a switched-off endpoint answers 404 whatever is sent. Saved
  settings with the old single switch on turn both on after the upgrade.
  Upgrade note: a monitor that polled `/actuator/health` without a login now
  gets a 401 until it sends the actuator credentials shown in Settings.
- `GET /actuator/info` lists, per node, every private-LAN service it offers:
  whether it is exposed, protocol, bind address, port, a ready-to-use URI and
  the login a LAN client needs (none for Monero ZMQ and Tari gRPC). The
  response now contains the RPC passwords of exposed services, so it stays
  behind the actuator login.
- Settings no longer carry the explanatory notes under Network, Balance index
  and the actuator endpoints.
- The Litecoin and Bitcoin Cash watch-only cards scan every due address in one
  `scantxoutset` walk instead of one walk per address, and the Litecoin card holds
  up to 50 addresses (was 25). The Litecoin card notes that funds inside MWEB are
  not counted.
- The new-version notice and Settings now link to the README's Updating section
  instead of repeating the update commands.
- Settings ends with **Reset to default**, **Cancel** and **Save**, and that
  bar stays at the bottom of the dialog while it scrolls. Reset to default
  replaces Use server config: it fills in the default of every Settings
  control, from Appearance and alerts to launch at boot, both Network switches
  and the Balance index, and drops staged installs and moves. Nothing applies
  until Save. Cancel closes Settings, like ✕.
- New Zcash installs from the dashboard turn on Zakura's address index so watch
  wallets work from the start; existing installs keep their setting.
- Up to 50 Bitcoin watch-only wallets instead of 20, and `utxo-index bench`
  takes up to 50 wallets.
- Zcash watch-only wallets (`zcash.address_index`) refresh when the node's best
  block changes, checked every minute, instead of once an hour; the lookup is a
  local index read. Litecoin, Bitcoin Cash and Dogecoin keep the hourly scan.

### Removed

- The Zcash panel no longer shows the Zakura end-of-support warning; the
  new-version notice is the only update prompt.

### Fixed

- Every dashboard connection to a node or wallet, including both Tari gRPC
  channels, now ignores proxy settings, never follows a redirect, and refuses to
  connect to a public address. The Bitcoin, Monero, Monero wallet and Tari wallet
  URLs are now checked as internal addresses even when that node is off.
- UPnP, NAT-PMP and update checks are now switched off in the node configs instead
  of relying on defaults: `natpmp=0` for Bitcoin Core, `upnp=0` for Litecoin,
  Dogecoin and Bitcoin Cash (plus `natpmp=0` for Bitcoin Cash), and
  `check_interval = 0` for the Tari node. Monero also gets `no-igd=1`; the pinned
  0.18.5.4 has no UPnP code, so this only keeps port mapping off if a later
  release restores it.
- The Tari wallet config no longer names a Tor control port the stack never
  opens. The wallet does not use that setting in Tari v6.2.0. This was checked
  against the Tari source only; the wallet image was not started with the new file.
- Saving LAN port changes, or resetting a node's RPC password, reported
  "Request already processed." even though the change applied. Both recreate
  the dashboard, and the host helper then wrote its result into the old
  dashboard container, failed, and left the request to be seen again as a replay
  on its next run. It now finds the current dashboard container for every
  write.
- Exposing a node's RPC, ZMQ or gRPC port on the LAN failed with "Port
  availability could not be checked." on any host with a listener that `ss`
  prints with an interface scope, such as Tailscale's `[fd7a:…]%tailscale0:56603`
  or a link-local service. The port check could not read such lines, so it
  refused every change. It now reads them, and an
  IPv4 listener written as `address%interface:port` is still caught as a port in
  use.
- A node that fails to start no longer restarts forever. With launch at boot
  on, Docker retried a crashing container indefinitely and the dashboard showed
  it as connecting. The host helper now stops a node whose container keeps
  exiting with an error within a minute of starting for 10 minutes (at least 5
  restarts), sends one "<Node> is not coming up" alert with the last error (a
  Host event, forwarded to Telegram), and the panel shows Stopped with the same
  reason; **Start node** tries again. Nodes that crash after running a while, or
  recover within 10 minutes, keep Docker's normal restarts.
- "Price unavailable" no longer appears for a minute or two after a settings
  change restarts Tor and the dashboard. The dashboard now saves its last good
  prices in its state directory (`prices.json`), shows them as stale with their
  original time after a restart, and replaces them when the next fetch over Tor
  succeeds. While prices are unavailable or stale the page checks every 10
  seconds instead of every minute, so fresh prices appear as soon as Tor is up.
- A failed node install now says why. Settings shows the last error line from
  the step that failed (for example an image that could not be pulled), and a
  "<Node> install failed" Host event reaches Telegram. Host events other than
  thermal ones are now marked 🔴 instead of 🌡.
- Zcash can no longer be installed on an x86-64 processor without the BMI2 and
  ADX instructions (before Intel Core 5th generation or AMD Ryzen). Zakura 1.8
  images require them and exit at startup otherwise; Settings now disables the
  Install button with the reason, and the host helper rejects the install.
- Adding a watch-only wallet right after the dashboard starts could leave it in
  an error state until removed and re-added. The startup pass and the add request
  could both create and import into the same Bitcoin Core wallet at once, leaving
  extra descriptors that later checks reject. Provisioning is now serialized per
  wallet key.
- The usage pies attribute CPU and memory to the right owner. The Monero and Tari
  wallet containers now count toward their node instead of the Node Manager
  slice, which is now labelled "Node Manager (dashboard and Tor)". Each
  container's CPU is read from its cgroup over the same second as the host
  total, so "Other processes" no longer drops to 0 or swells because the figures
  came from different moments. Container memory now includes the container's own
  kernel memory (page tables, unreclaimable slab, socket buffers), which was
  counted under "Other processes" (about 270 MiB on an eleven-container host).
- `utxo-seed litecoin` checked litecoind's running commands without the node's
  RPC configuration, so it stopped with "Litecoin RPC did not answer". It now
  passes the same configuration file the node's health check uses.
- `utxo-index bench` reads a relative wallet file path from the directory it was
  run in. On APT installs it looked in `/var/lib/node-manager` instead and
  reported a valid file as malformed; a missing file is now named as unreadable.

## [1.36.0] - 2026-11-16

### Added

- `utxo-index bench <wallets.json> [--scan [-y]] [--repeat N]` measures balance
  lookups on a host with the Bitcoin balance index ready. The dashboard times
  script derivation, cold and repeated index lookups per wallet, batches of 1,
  5 and 20 wallets and the cached wallet read, and with `--scan` also runs
  `scantxoutset` over the same wallets and compares the coins. It needs no
  dashboard password and leaves the wallet list untouched; the result is saved
  with mode 600. `docs/measurements/lookup-bench.md` is the runbook, with public
  test wallets in `bench-wallets.json`, and `scripts/measure-block-latency.sh`
  measures new-block-to-balance time from logs (#236).
- Docs: how long nodes and onion services take to reconnect after the Tor
  container restarts, which happens when incoming connections are turned on or
  off, when Bitcoin, Litecoin or Bitcoin Cash is activated or deactivated with
  incoming connections on, and on upgrades.
- Health and info endpoints in the style of Spring Boot Actuator, off by
  default and switched under Settings → Health endpoints.
  `GET /actuator/health` returns the overall status (`UP`, `DEGRADED` or
  `DOWN`; 503 only for `DOWN`) without sign-in, so plain uptime checkers can
  poll it. Signed-in callers also get per-component status for the dashboard,
  each active node (synced, syncing, busy or down), Tor and the balance index.
  `GET /actuator/info` needs sign-in and reports the version, install kind and
  each chain's network, height, sync progress, peers and node version. Both
  read state the dashboard already keeps, and neither returns wallet data,
  credentials, onion addresses or host paths.
- A "Balance index" switch in Settings (shown when Bitcoin is installed) turns
  `bitcoin.utxo_index` on and off from the dashboard, the same way the network
  switches work: the host helper saves the setting, applies it, and restores
  the previous setting if the apply fails. Turning it off asks first. The
  Bitcoin watch-only wallets card now carries a badge with the index state
  (ready, catching up, seeding, needs a seed, degraded or off) that links to the
  index card. Seeding still runs from the host.

### Changed

- While a chain installs, its Settings → Node set row reads "Installing…"
  instead of a disabled "Install" button, and a data move reads "Moving
  data…", including after a page reload. The row appears as soon as the
  host accepts the change instead of on the next poll.
- With the Bitcoin balance index ready, a new block reaches the watched
  wallets' saved balances right away. The dashboard waits on bitcoind's
  `waitfornewblock` instead of checking every 10 seconds, applies the block to
  the index, and then refreshes the wallets instead of waiting for their next
  30-second pass. The page shows it on its next 15-second refresh. Measured
  before this change on the reference node (#236): a median 19 seconds from a
  block to the first wallet refresh, and about half of blocks reached wallets
  first through the slower per-block scan update.
- Nothing in Settings applies on click any more. Launch at boot, both Network
  switches, the Balance index, Install, Move data, Run new config and Use
  server config wait for **Save**; the Install and Move data dialogs only check
  their values, and clicking a row marked "Installs on Save" or "Moves on Save"
  undoes it. Save sends host changes one after another (a pending config
  first, then switches, installs and data moves; keep the page open until the
  last one applies) and stops at the first failure. Reopening Settings
  discards unsaved changes. The User dialog's Change password and Sign out
  still act at once.
- A person icon above the Settings gear opens Account (sign out) and then
  Dashboard password; both left the Settings dialog.

### Fixed

- Dogecoin finds peers through Tor. Its two DNS seeds cannot be resolved by Tor
  exit relays, so a fresh node relied on built-in seeds from February 2024 and
  took 30 to 75 minutes to reach a few peers. The container now looks the seeds
  up with DNS-over-HTTPS (Cloudflare) through Tor and adds up to 8 current
  peers; fresh nodes had 6 and 7 peers within 2 minutes in testing. Dogecoin's
  own DNS seeding is off. After one successful lookup, restarts use the saved
  peer list; a failed lookup falls back to the built-in seeds and is retried on
  the next start. Every request still goes through Tor (#296).
- Coins worth less than one unit of the chosen currency no longer show a
  rounded-down price such as "Tari price $0.00". The panel and tower price the
  smallest power of ten worth at least one unit, such as "10,000 Tari price
  $8.03", and recompute it from each new quote and currency.

## [1.35.1] - 2026-11-16

### Fixed

- APT upgrades now set up incoming onion connections. An APT upgrade to 1.35.0
  kept the earlier version's approved settings, which have no onion node list,
  so Tor published no node onion while Bitcoin, Litecoin and Bitcoin Cash still
  waited about 2 minutes at each start for one and then ran without it. The
  package upgrade now adds the missing network settings the same way
  `configure.sh` does, from the approved configuration and node selection. On an
  APT install already on 1.35.0, this upgrade adds them and recreates Litecoin
  and Bitcoin Cash. Bitcoin's upstream image does not change, so run
  `sudo node-manager restart bitcoin` once afterwards to make it advertise its
  onion.

## [1.35.0] - 2026-11-15

### Added

- Settings → Network has two switches that apply at any time, like the LAN
  port toggles: **Reach clearnet peers through Tor** and **Accept incoming
  connections over Tor**. Each saves `config.json` (`network.clearnet_over_tor`,
  `network.inbound_connections`) and restarts the affected nodes, rolling back
  if the restart fails.
- `./stack utxo-index remove [-y]` deletes the balance index volume after
  `bitcoin.utxo_index` is off and applied. It asks first, and refuses while the
  option is on or a container still mounts the volume.
- With the balance index off on a pruned Bitcoin node, the dashboard shows a
  one-line hint naming `bitcoin.utxo_index` and `docs/watch-only.md`. Unpruned
  nodes see nothing.
- With the Bitcoin balance index on, a wallet row whose balance came from the
  index says "From the balance index", the index card says balances are
  confirmed only, and each seed phase change is logged once.
- The balance index logs its state once at startup, not only on later changes,
  and logs seed progress once per 10 percent step.
- The seed warning (`./stack utxo-seed`, the dashboard card and `configure.sh`)
  gives the measured duration of about 20 minutes, that Bitcoin RPC stops
  answering for about 5 minutes and then answers slowly, and the disk use of
  about 7.5 GiB, up to about 17 GiB while seeding.
- Operator docs for the measured seed budget, the "Needs a new seed" causes,
  upgrades, and removing the index and its volume.

### Changed

- All seven Node Status panels use the same pruning badge ("Pruned" or "Full"),
  the same row labels ("Version:", "RPC:", "gRPC:" for Tari), and the same
  uptime, connection, size, sync progress and mempool formats. The Bitcoin badge
  no longer shows the prune target, and Monero's footer shows the stack version.
- Zcash runs Zakura 1.8.8 (from 1.8.6). It keeps the same end-of-support
  height, block 3,643,309 (around 2026-12-03), so a later Zakura is still needed
  before then.
- Nodes now reach clearnet peers through Tor by default. Bitcoin, Litecoin,
  Dogecoin and Bitcoin Cash drop `onlynet=onion` and keep their Tor proxy, so
  clearnet dials leave through a Tor exit relay; the family nodes also query
  their DNS seeds through Tor. Tari keeps `tcp_tor`. Every node still sits on
  the internal network with no route around Tor. Set
  `network.clearnet_over_tor` to `false` for onion peers only (Tari then uses
  its onion-only `tor` transport). Monero and Zcash already reached clearnet
  peers through Tor and have no onion-only mode.
- **Incoming connections over Tor are on by default** and now cover Litecoin and
  Bitcoin Cash as well as Bitcoin. `network.inbound_connections` replaces
  `bitcoin.inbound_onion`, which is no longer read: an install that had
  `"inbound_onion": false` (the old example default) starts accepting incoming
  onion connections after upgrading. Turn it off in Settings → Network or with
  `"network": {"inbound_connections": false}`. Dogecoin 1.14 only understands
  the retired v2 onion addresses, so it stays outbound-only.
- The Tor container now publishes each node's incoming onion service from a
  static `HiddenServiceDir` (`/var/lib/tor/<node>_onion` in the `tor_data`
  volume) and passes the node only its public hostname, through a new read-only
  `tor_onions` volume. Bitcoin gets a new onion address once after upgrading;
  the old `onion_v3_private_key` in its data directory is no longer used.
  `./stack backup` and `restore` now carry each node's onion keys from the Tor
  volume, and restore restarts those nodes so they advertise the restored
  addresses.

### Security

- Tor no longer opens a control port. Before, a node accepting incoming
  connections joined Tor's group and mounted its data volume to read the control
  cookie, so a compromised node daemon could reconfigure Tor for every node. No
  node container can now read Tor's keys or change its configuration.

### Fixed

- Monero no longer shows "Full" on a pruned node, and Tari no longer shows
  "Pruning unknown" before its first prune; both always run pruned and now say so.
- The balance index directory is restricted to 0700 even when it is an existing
  Docker volume mount point; on a package install it was left at 0755. Its
  contents are public chain data, but the docs promised 0700.
- Bitcoin's inbound onion service now reaches bitcoind. bitcoind told Tor to
  forward incoming onion connections to `127.0.0.1:8334`, which is Tor's own
  container, so the onion address registered but every incoming connection
  failed. The onion service now targets bitcoind's internal address.

## [1.34.0] - 2026-11-14

### Added

- The Zcash panel warns when Zakura is within about 14 days of its end-of-support
  height (block 3,643,309 for Zakura 1.8.6 and 1.8.7, around 2026-12-03), and the
  monitor records one event per crossing. See `docs/nodes.md`.
- Opt-in Bitcoin balance index for pruned nodes (`bitcoin.utxo_index`, off by
  default), so watched-wallet balances come from a local index instead of a
  `scantxoutset` walk of the whole coin set.
  - Turning it on adds the `docker-compose.utxo-index.yml` overlay: a
    `utxo_index` volume and a 1g memory limit with 0.5 CPU on the dashboard.
    `./stack apply` recreates only the dashboard, never bitcoind. Existing
    installs render the same configuration after upgrade.
  - `./stack utxo-seed [-y]` fills the index once from `dumptxoutset`, read
    through a pipe in the bitcoin container, so no 9 GB dump file is written.
    Bitcoin RPC fails for about 5 minutes during the dump; allow at least 15
    minutes. It refuses to start unless the node is synced with a fresh tip,
    no other dump or scan is running, 27 GB is free, the dashboard is capped at
    0.5 CPU and the host is at or below 75 C. While it runs it holds the
    pruned-wallet scanner, pauses reading at 85 C and resumes at 72 C, aborts
    below 2 GB free, accepts the result only if it matches the RPC result, and
    holds node-down alerts for up to 15 minutes. `./stack utxo-seed cancel`
    stops it, and `stop`, `down` and `restart` cancel a running seed and
    unblock the dump before stopping bitcoind. See "Seeding the Bitcoin
    balance index" in `docs/operations.md`.
  - After the seed the dashboard follows the node tip, polling every 10
    seconds and applying each block with `getblock` verbosity 3 in one fsynced
    batch, and rolls back reorgs up to 144 blocks deep from its own undo
    journal. It pauses below 2 GB free and goes `invalid` (persisted, never
    migrated or reseeded automatically) on a format or chain mismatch, a
    deeper reorg, falling behind the node's retained blocks, or a storage
    error.
  - Watched-wallet balances come from the index while it is `ready` and level
    with the node tip, and the wallet's scripts can be derived: addr, pkh,
    wpkh, sh(wpkh), wsh and tr shapes, indexes 0 to 999 of receive and change,
    derived through bitcoind. Each row reports `balance_source` as `index` or
    `scan`. Wallets with other shapes (for example `pk()` or `combo()`), and
    every wallet while the index is in any other state, keep the existing
    `scantxoutset` flow unchanged. Derived script sets are cached in `utxo_scripts.json`
    (mode 0600) under the state directory, and logs carry counts, heights and
    durations only. Non-pruned nodes keep the Core-wallet path.
  - `GET /api/utxo-index` and a dashboard card report the index state (`off`,
    `not_seeded`, `seeding`, `catching_up`, `ready`, `degraded` or
    `invalid`), the index and node heights, seed phase and progress, refusals
    and the host command to run. The JSON fields are listed in
    `docs/operations.md`.
  - The dashboard now links Pebble, a pure Go key-value store. The binary
    grows by about 5.5 MB and `THIRD_PARTY_NOTICES` gains the new modules.
- Opt-in `zcash.address_index` runs Zakura with checkpoint sync off so the
  dashboard can show watch-only balances for transparent Zcash addresses from
  your own node, with the Zcash Community Grants address as an excluded example.
  It needs a fresh data directory and full resync, and `configure.sh` refuses it
  on an existing checkpoint-pruned database. The default is unchanged.
- Opt-in `dogecoin.watch_wallet` runs Dogecoin as an archival node with a
  watch-only wallet so the dashboard can show balances for public Dogecoin
  addresses from your own node, with the Dogecoin Foundation address as an
  excluded example. It needs `dogecoin.prune_mb` set to 0, a fresh data
  directory and a full unpruned sync, and `configure.sh` refuses it on a
  database that ran pruned. Adding an address starts a rescan that can take
  hours; a balance appears only after a recorded complete import, failed
  imports retry after six hours, and the example is never scanned. The
  default is unchanged.

### Changed

- The Litecoin, Dogecoin and Bitcoin Cash images now build from one shared
  base in `build/bitcoin-family/`, with one entrypoint and healthcheck, so a
  base, package or startup fix lands once instead of three times. Binaries,
  users, paths and settings are unchanged; Litecoin and Dogecoin images now
  also carry `libatomic1`, which Bitcoin Cash already needed.

## [1.33.19] - 2026-11-13

### Changed

- Zcash runs Zakura 1.8.6 (from 1.8.5), and the Debian and Alpine base images
  for Monero, Litecoin, Dogecoin, Bitcoin Cash and the dashboard are refreshed
  to their current digests.
- Tari now also connects to the peers that advertise clearnet addresses, still
  only through Tor: its transport moves from onion-only (`tor`) to `tcp_tor`,
  and with the proxy bypass off each clearnet dial leaves through a Tor exit
  relay. Exit connections are faster and steadier than onion ones, which helps
  the node find sync peers. The Tari container stays on the internal network,
  so nothing can connect directly even if the setting changed, and a compose
  test now guards the bypass settings.
- Every chain's watch-only wallets now render through one shared component, so
  Bitcoin, Monero, Tari, Litecoin and Bitcoin Cash rows look and behave the
  same: the name (with an ⓘ tooltip for notes such as public-example caveats),
  a comma-grouped amount and unit, an approximate value in the chosen currency
  when the amount is a real balance, one status line, and a sparkline in the
  chain's own colour. Tari and the family chains now show their balances on
  their tower like Bitcoin and Monero, and every wallet card lists wallets the
  same way, with a confirmed ✕ to remove one.
- The Monero example wallet shows its total received as
  "137,131.438327228521 XMR" with "Total received" on the status line and the
  view-key caveat in its tooltip, and every Monero wallet row shows its
  approximate value in the chosen currency like Bitcoin's public example; the
  status line still says when the figure is a total received rather than a
  verified balance.
- `scripts/lint.sh` is the one lint entrypoint for CI and `tests/run.sh`. It
  runs shellcheck over a single file list (including `packaging/postinst` and
  `scripts/node-manager`), ruff, actionlint and gofmt. The lint images are
  digest-pinned in `.github/tool-images/Dockerfile` beside the other CI tools,
  so Dependabot bumps them too.

### Fixed

- The Tor image builds again: Alpine 3.24 replaced Tor 0.4.9.15 with 0.4.9.17,
  so the pinned package no longer existed and every image build and CI run
  failed. The pin now names 0.4.9.17-r0.
- `configure.sh` no longer claims `blockfilterindex` cannot run on a pruned node
  (Core allows it). It now refuses only to add the index to an already pruned
  node, which bitcoind cannot start with, and docs state the real rule (#218).
- The "initial sync complete" alert no longer repeats every few minutes after
  a node has synced. Monero (and Tari) briefly report syncing whenever a new
  block arrives, and each blip re-announced the sync; once a node has synced,
  the alert now re-arms only after it stays behind for 10 minutes.

## [1.33.18] - 2026-11-13

### Added

- Watch-only balance scans log their outcome, the `scantxoutset` duration and
  how long the wallet waited for its result, with no address or key, giving a
  real baseline for scan speed (#214).

### Fixed

- The Monero watch-only example wallet ("Monero General Fund") can now finish
  loading. Its incoming-transfers reply (about 8,500 outputs, 3.8 MB) exceeded
  the 1 MiB Monero RPC reply limit, so every refresh after the scan reached the
  tip failed; the wallet RPC client now accepts replies up to 64 MiB while the
  daemon client keeps 1 MiB. The wallet container's memory limit rises from
  512 MiB to 1 GiB, because scanning that wallet plateaus at about 500 MiB.

### Changed

- On pruned nodes, Bitcoin watch-only balances now follow each new block from
  the cached coins instead of rescanning the whole UTXO set: spends and new
  payments are applied within about 30 seconds of a block, and a full
  `scantxoutset` runs only after a reorganization, a gap of more than 144
  blocks, or a missing block (#215).

## [1.33.17] - 2026-11-13

### Fixed

- "Remember me" dashboard logins now last their full 30 days across
  dashboard restarts and upgrades. Remembered sessions are saved as token
  hashes in `/state/sessions.json`, bound to the current password, so
  changing the password in the dashboard or `config.json` still signs every
  browser out, and a logout is never undone by a restart.
- The Monero watch-only wallet card no longer gets stuck on "needs a dashboard
  restart". The minute refresh opened wallets with an empty file name, so a
  wallet's scan never started; it now keeps the scanning wallet open instead of
  reloading its whole cache every minute, stores it once a scan completes, and
  a lost reply to a read-only wallet call is retried on the next refresh rather
  than stopping the wallet service. The dashboard log now names the call
  whenever the service does stop.
- The Tari wallet no longer crash-loops on the default `admin` RPC user name:
  only the RPC password needs at least 8 characters.
- The thermal throttle now releases nodes that run without a CPU cap. Docker
  ignores `--cpus 0`, so the release left them at 0.25 CPU; they now get every
  host CPU back, and a release that does not take effect is retried.
- The dashboard no longer reports a Tari node at height 0 as synced, which
  sent a false "initial sync complete" alert after every failed sync attempt.

### Changed

- Bitcoin watch-only balance notes show only the scan time.

## [1.33.16] - 2026-11-12

### Added

- Watch-only Bitcoin balances group digits with commas and show the
  equivalent amount in the configured currency next to each balance and
  the total, from the dashboard's existing price feed.
- On pruned nodes the watch-only sparkline now charts the wallet's
  confirmed unspent funds by arrival block, from the first coin still held
  to the scan tip, instead of hourly balance snapshots. Existing wallets
  pick this up on their next scan.

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
  with another project's network on a shared Docker host).
- Credentials flow `config.json` → gitignored `.env` → container
  environment; no tracked file is ever modified.

### Fixed

- Dashboard Docker build (referenced a misnamed file).
- bitcoind flags silently dropped by YAML line folding in the compose
  entrypoint — the bug that motivated the e2e boot test.
- Tor data directory group ownership (`tor:root` → `tor:tor`) so the
  bitcoin container can read the control-auth cookie via gid 101.

[Unreleased]: https://github.com/VijitSingh97/node-manager/compare/v1.41.0...HEAD
[1.41.0]: https://github.com/VijitSingh97/node-manager/compare/v1.40.3...v1.41.0
[1.40.3]: https://github.com/VijitSingh97/node-manager/compare/v1.40.2...v1.40.3
[1.40.2]: https://github.com/VijitSingh97/node-manager/compare/v1.40.1...v1.40.2
[1.40.1]: https://github.com/VijitSingh97/node-manager/compare/v1.40.0...v1.40.1
[1.40.0]: https://github.com/VijitSingh97/node-manager/compare/v1.39.0...v1.40.0
[1.39.0]: https://github.com/VijitSingh97/node-manager/compare/v1.38.0...v1.39.0
[1.38.0]: https://github.com/VijitSingh97/node-manager/compare/v1.37.0...v1.38.0
[1.37.0]: https://github.com/VijitSingh97/node-manager/compare/v1.36.0...v1.37.0
[1.36.0]: https://github.com/VijitSingh97/node-manager/compare/v1.35.1...v1.36.0
[1.35.1]: https://github.com/VijitSingh97/node-manager/compare/v1.35.0...v1.35.1
[1.35.0]: https://github.com/VijitSingh97/node-manager/compare/v1.34.0...v1.35.0
[1.34.0]: https://github.com/VijitSingh97/node-manager/compare/v1.33.19...v1.34.0
[1.33.19]: https://github.com/VijitSingh97/node-manager/compare/v1.33.18...v1.33.19
[1.33.18]: https://github.com/VijitSingh97/node-manager/compare/v1.33.17...v1.33.18
[1.33.17]: https://github.com/VijitSingh97/node-manager/compare/v1.33.16...v1.33.17
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
