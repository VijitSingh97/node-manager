# Getting Started

## Prerequisites

- **Linux** with [Docker Engine](https://docs.docker.com/engine/install/ubuntu/),
  or **macOS** with Docker Desktop. Docker Compose **2.33.1 or newer** must be
  available to the account running Node Manager.
- The APT package installs its host dependencies. Source installs need
  `ca-certificates`, `curl`, `git`, `iproute2`, `jq`, `openssl`, and
  `util-linux`. Homebrew installs bring their command-line dependencies with
  the formula; Docker is still installed separately.
- Hardware per [Hardware Requirements](hardware.md) — Bitcoin plans for
  28–56 GiB SSD space and Monero for 120–150 GiB. The other nodes need expandable
  storage until their current full-sync totals are measured. Bitcoin and Monero
  use an experimental 1 CPU / 2 GiB steady budget; Tari, Litecoin, Dogecoin,
  Bitcoin Cash, and Zcash use 1 CPU / 4 GiB each. Shared services and host
  overhead are additional. Initial sync is uncapped.
  For sync headroom, plan around 2 CPU cores and 4 GiB RAM per node; these are
  estimates, not minimum requirements or measured average usage. More resources
  can shorten validation; Tor and SSD performance also affect sync time.

Setup chooses a private LAN address automatically and prints the URL. Optional
on Linux: `avahi-daemon` also lets other machines use `http://<hostname>.local`
(append `:8080` for Homebrew). Set `dashboard.bind_ip` explicitly to use another
private address or `127.0.0.1` for access only from this machine.

On Ubuntu, install `jq`, `openssl`, `curl`, `iproute2`, and optionally
`avahi-daemon` with APT.

## Install

### Homebrew on macOS or Linux

Homebrew installs the latest release available in this legacy-named tap. The
release workflow publishes the archive and images first, then opens a PR that
updates the formula. A new version becomes available after that PR is reviewed
and merged:

```bash
brew tap vijitsingh97/bitcoin-starter-stack https://github.com/VijitSingh97/node-manager.git
brew install vijitsingh97/bitcoin-starter-stack/bitcoin-starter-stack
bitcoin-stack init
```

Installation is inert. The explicit `init` command creates default private
configuration, downloads missing images, and starts Bitcoin without prompts. Homebrew stores configuration
and chain data outside its Cellar so upgrades preserve them:

| Host | Configuration | Generated state and chain data |
|---|---|---|
| macOS | `~/Library/Application Support/bitcoin-starter-stack/config/config.json` | `~/Library/Application Support/bitcoin-starter-stack/state/` and `data/<node>/` |
| Linuxbrew | `${XDG_CONFIG_HOME:-~/.config}/bitcoin-starter-stack/config.json` | `${XDG_DATA_HOME:-~/.local/share}/bitcoin-starter-stack/state/` and `data/<node>/` |

Before first Linuxbrew startup, Node Manager uses an already downloaded local
image to give each empty default data directory the UID expected by its
container (`1000:1000`, or `10001:1000` for Zcash). It never changes a non-empty
directory or a custom data path automatically. If an existing/custom path has
the wrong ownership, startup stops and prints the exact migration command so
you can inspect the path before running it.

Setup starts the host supervisor for dashboard controls. Use **Settings →
Launch at login** to choose whether the configured application should return
when you sign in. On a Mac, Docker Desktop must be running; its own start-at-login
setting is separate. The user service cannot start containers before login.
Turning this option off leaves the current nodes running. Use
`bitcoin-stack down` to stop them now.

Homebrew uses port **8080**. Setup prints the selected private-LAN URL, or
localhost if no private interface is available. See the complete
[Homebrew guide](homebrew.md). Host-path and control tests run on macOS and
Ubuntu CI; a full initial sync and post-sync cap trial on an Intel Mac remains
unqualified.

### APT on Ubuntu

Version `v1.32.0` is the first Ubuntu 24.04 APT release. Its release workflow
publishes the package only after the matching amd64 and arm64 images pass the
release gate, verifies the signed feed with APT, and then deploys it to GitHub
Pages. It installs the `bitcoin-stack` command globally and keeps mutable files
in standard system locations:

- configuration: `/etc/bitcoin-starter-stack/config.json`
- generated credentials and backups: `/var/lib/bitcoin-starter-stack/`
- chain data: `/var/lib/bitcoin-starter-stack/data/<node>/`

APT installation does not start containers, run setup, or change an existing
checkout. Run `sudo bitcoin-stack init` explicitly after installation. A new
package install defaults to `prune_mb: 10000`; existing configuration and
generated state always win, so an upgrade never converts a full node to pruning.
Existing package installations retain their saved Compose project and volumes.
A source-managed deployment keeps its own configuration and upgrade commands;
installing the package does not migrate it automatically. Package mode refuses
to operate on services owned by another installation.

For an initialized APT deployment that is running, later package upgrades
download the new release images and recreate only changed services. An affected
node may restart briefly during `apt upgrade`. If the stack was stopped, the
upgrade leaves it stopped. A configuration file that differs from its last
approved state remains pending and is not applied by the unattended upgrade.
Homebrew and source installations continue to use their manual update flows.

Add the dedicated signed repository:

```bash
sudo apt update
sudo apt install ca-certificates curl
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://vijitsingh97.github.io/node-manager/bitcoin-starter-stack.asc \
  | sudo tee /etc/apt/keyrings/bitcoin-starter-stack.asc >/dev/null
sudo tee /etc/apt/sources.list.d/bitcoin-starter-stack.sources >/dev/null <<'EOF'
Types: deb
URIs: https://vijitsingh97.github.io/node-manager/
Suites: ./
Signed-By: /etc/apt/keyrings/bitcoin-starter-stack.asc
EOF
sudo apt update
sudo apt install bitcoin-starter-stack
sudo bitcoin-stack init
```

The signing key fingerprint is
`72FD656754BD7534E274360A37070C2730C0B4EA`. `Signed-By` limits that key to
this repository. See the [privacy guide](privacy.md#package-feed) for manual
fingerprint verification.

`init` downloads missing images using the host's existing Docker settings and
starts a pruned Bitcoin node. It does not ask setup questions. Docker stores
image layers in its managed storage. Setup does not change Docker's daemon or
proxy configuration. Use **Settings → Node set** later to install another chain.

### Install from source on Ubuntu

Install Docker Engine and Docker Compose 2.33.1 or newer using the
[official Ubuntu instructions](https://docs.docker.com/engine/install/ubuntu/).
Complete Docker's
[Linux post-install steps](https://docs.docker.com/engine/install/linux-postinstall/)
so your user can run Docker without `sudo`, then install Node Manager's host
tools and check out the published release:

```bash
sudo apt update
sudo apt install ca-certificates curl git iproute2 jq openssl util-linux
git clone --branch v1.33.1 --depth 1 https://github.com/VijitSingh97/node-manager.git
cd node-manager
./stack init
```

The release checkout uses published container images; it does not build the
application. Configuration, generated state, and node data stay in the checkout.
`./stack init` uses the no-question defaults below; customize `config.json`
before running it when you need different first-boot settings. Use
`./stack status`, `./stack up`, and `./stack down` to manage it. Development
branches use unpublished image tags and require images built in a separate
development environment, so do not use one for installation.

For an offline installation, unpack the source and load all required images
locally. `init` sees that they are present and starts without downloading.
Routine `up` and `apply` never download or build implicitly; `pull` is the
explicit command for obtaining images ahead of time.

### Existing Git checkouts after the repository rename

GitHub redirects the old repository URL, but an existing checkout can record the
canonical URL once:

```bash
git remote set-url origin https://github.com/VijitSingh97/node-manager.git
```

Package installs do not need this step. Their package, command, and data-path
names intentionally remain `bitcoin-starter-stack` and `bitcoin-stack`.

## Default first start

Run `bitcoin-stack init` for Homebrew, `sudo bitcoin-stack init` for APT, or
`./stack init` for source. A fresh run:

1. Starts **Bitcoin only**, pruned to 10 GB of recent block files (plan roughly
   28–56 GiB total disk space).
2. Chooses an IPv4 address on your private LAN, with localhost as a fallback,
   and prints the actual dashboard URL. Homebrew uses port 8080; APT/source use 80.
3. Generates and persists one 32-character dashboard password and keeps every
   node LAN interface closed. The `init` output prints the dashboard login once;
   later applies do not rotate it. Configure rejects missing or empty dashboard
   passwords. Node RPC passwords are
   generated separately with username `admin` and retained on later applies.
4. Downloads missing versioned container images and starts the application.
5. Leaves launch at boot/login off. APT and Homebrew users can enable it in
   global Settings. After a later reboot with it off, run `bitcoin-stack up`
   (`sudo` for APT), or `./stack up` for source, to start the application
   manually.

Fresh containers retry a crash up to five times even when launch at boot/login
is off; they do not automatically return after Docker itself restarts. Existing
installations retain their saved reboot behavior until you change it.

HTTP is unencrypted even when a password is set. Use VPN access or the protected
HTTPS reverse-proxy profile for remote administration. See the [privacy model](privacy.md).

Open the printed URL, then use the gear menu to install additional nodes and
configure Appearance, Telegram, Healthchecks, and
launch at boot/login where the installation manages that feature. **Install**
first opens a confirmation dialog. It fills in that node's default host data
folder, shows its estimated post-sync disk use, and lists the optional LAN
interfaces and ports. All interface toggles default off. Change the folder or
opt into an interface before confirming; the host validates the folder and
checks enabled ports for conflicts before it changes the running stack. The
install preserves every existing node and its data. Watch progress or correct a
failed installation in the same panel. View-only wallets are added in the
relevant tower; setup does not ask for wallet keys or seed phrases.

The **Config** section directly below Healthchecks shows the actual host
`config.json` path reported by the installed helper. Use that path when following
the file-edit instructions below; the display itself is read-only.

On a macOS source checkout, `./stack init` starts a host helper for the current
login session so dashboard node installs and LAN-port changes work. Source
installs do not register a login service, so **Launch at login** is unavailable;
run `./stack up` after signing in again. The Homebrew package registers the
managed user service when it is installed.

Fresh APT and Homebrew installs select `resource_profile: compact`. The host
helper waits for three spaced, healthy, current sync checks before applying
1 CPU / 2048 MiB to Bitcoin or Monero, or 1 CPU / 4096 MiB to the other nodes,
with no extra swap. Initial sync is uncapped. Source first-start selections and
existing profiles stay off unless configured; a node installed later from the
dashboard selects `compact`. These ceilings remain experimental until sustained
operation is qualified; they do not mean a whole 2 GiB VPS can host multiple
nodes. See [resource caps](configuration.md#resource-caps).

## Customize before first start

Install the command first, but do not run `init` yet. Create `config.json` at
your installation's configuration path:

| Install | File to create |
|---|---|
| APT | `/etc/bitcoin-starter-stack/config.json` |
| macOS Homebrew | `~/Library/Application Support/bitcoin-starter-stack/config/config.json` |
| Linuxbrew | `${XDG_CONFIG_HOME:-~/.config}/bitcoin-starter-stack/config.json` |
| Source | `config.json` beside `stack` |

For example, this selects Bitcoin and Monero and keeps automatic LAN detection:

```json
{
  "bitcoin": { "enabled": true },
  "monero": { "enabled": true },
  "dashboard": { "bind_ip": "auto", "password": "ReplaceWithYourOwnRandomPassword" }
}
```

For Monero alone, set `bitcoin.enabled` to `false`. Add `data_dir` to a node's
section to choose a mounted SSD. Replace the example password with your own
random value of 12–128 letters or numbers; `openssl rand -hex 16` generates a
compatible password. `dashboard.port` changes the dashboard's host port.
Keys you omit retain their defaults. The full template and key descriptions
are in [Configuration](configuration.md). Never put a seed phrase or private
spend key in configuration.

Create missing parent folders, save the file with permissions `600`, and run
your installation's `init` command. APT configuration is owned by root, so use
`sudo` when editing it. `init` preserves supplied values and starts that
configuration without a wizard. Existing data and generated credentials are
retained.

## Later configuration changes

Dashboard changes save through the host helper. For a file edit, change the
existing `config.json` and run `bitcoin-stack apply` (`sudo` for APT), or
`./stack apply` for source. This explicitly approves the new configuration and
recreates changed services using local images. If you manually enable a new
node in the file, download its images first with `pull`.

If the file changed while the application was stopped, a manual `up` or
`restart` asks **Config change detected. Run new config?** Declining keeps the
last approved settings and retains your edit for later review. An unattended
boot does the same without waiting for terminal input; global Settings shows
the pending change and lets you approve it. A private approved snapshot makes
this possible; never delete it as a way to apply an edit.

## First start: what to expect

1. **Tor bootstraps** (a minute or two):

   ```bash
   bitcoin-stack logs tor    # package install; wait for "Bootstrapped 100% (done)"
   ./stack logs tor          # source install
   ```

2. **Bitcoin Core finds onion peers and syncs headers**, then blocks:

   ```bash
   bitcoin-stack logs bitcoin    # package install
   ./stack logs bitcoin          # source install
   ```

3. **Monero**, if selected, starts a pruned sync through Tor. Its node status
   uses authenticated internal RPC; LAN clients use a separate restricted
   listener. An internal view-only wallet service supports dashboard monitoring;
   it has no host port and cannot spend. No miner starts.

4. **The dashboard** at the URL printed by setup shows the configured towers. Click a tower to
   open its chain's card. It shows a connecting state until RPC comes up,
   then sync progress. After its wallet section, that node's settings contain
   block alerts, LAN ports, and any authenticated RPC credentials. The gear
   menu contains node installation, launch at boot/login, Appearance, Dashboard
   password, Telegram, and Healthchecks settings. A disabled node says **Not configured**.

Automatic binding selects a private LAN address each time configuration is
rendered. Set `dashboard.bind_ip` to `127.0.0.1` for localhost only, or choose a
specific RFC1918 host address and apply. Wildcard and public binds are rejected.

The node downloads and validates the whole historical chain, currently hundreds
of GB (roughly 900 GB), even though it later retains only 10,000 MiB of block files.
Over Tor, expect the first sync to take days. The dashboard remains informative
during sync. Container health is visible in `docker ps` — the selected
services define health checks, though `bitcoin` only reports healthy once
its RPC is warmed up.

## Verify it's working

```bash
./stack status        # source install
sudo bitcoin-stack status  # package install
bitcoin-stack status  # Homebrew install
```

If something looks wrong, see
[Operations → Troubleshooting](operations.md#troubleshooting).
