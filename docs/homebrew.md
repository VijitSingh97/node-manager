# Homebrew installation

Homebrew support installs the same Docker Compose stack used on Linux. On macOS,
Docker Desktop supplies the Linux virtual machine that runs the containers. The
formula installs only the command and immutable stack files; it does not install
or start Docker, download images, run setup, or start a node.
Homebrew installs Bash, flock, jq, and OpenSSL for the host commands; Linuxbrew
also installs iproute2. Docker remains an external prerequisite.

Version `v1.32.0` is the first Homebrew release. The release workflow publishes
the runtime archive and all required container images before it opens the
generated formula PR. The version becomes available from the tap only after
that PR is reviewed and merged. The tap keeps its legacy name for compatibility
but uses the canonical `node-manager` repository URL below.

## macOS

Install and start [Docker Desktop](https://docs.docker.com/desktop/setup/install/mac-install/),
then confirm that `docker compose version` reports 2.33.1 or newer. Docker
Desktop must have enough disk space and memory for the nodes you configure; see [node requirements](nodes.md#storage-and-resource-planning). Enable
Docker Desktop's start-at-login option if nodes should return after a reboot.

```sh
brew tap vijitsingh97/bitcoin-starter-stack https://github.com/VijitSingh97/node-manager.git
brew install vijitsingh97/bitcoin-starter-stack/bitcoin-starter-stack
bitcoin-stack init
```

If the formula update PR has not merged yet, `brew install` cannot install
`v1.32.0`; use the release tarball instead or wait for the tap update. Do not
substitute an unreleased branch or a locally generated formula for a public
installation.

Installation is intentionally inert. `bitcoin-stack init` starts a default
pruned Bitcoin node without questions. It preserves a `config.json` created
before first start, downloads missing versioned images, and prints the dashboard
URL. The dashboard chooses a private LAN address automatically, falling back
to localhost, and uses port **8080** so the command needs no root privileges.
Change `dashboard.bind_ip` or `dashboard.port` in configuration to override this.

The dashboard gets one generated 32-character password on a new install; `init`
prints it once and retains it across applies. A non-empty `dashboard.password`
is required before configuration is applied. Each
selected authenticated node gets a separate generated `admin` login, retained
in private state. No node RPC, gRPC, or ZMQ port is exposed by default.

Global Settings installs additional nodes, controls launch at login, and
contains Appearance, Dashboard password, Telegram, and Healthchecks. Open a
tower for its block alerts, optional LAN interfaces, and RPC credentials.
Personal view-only wallet material is separate from these service logins;
never enter a seed phrase or private spend key.

Choosing **Install** opens a confirmation dialog before anything is downloaded.
It shows the default Homebrew data folder, the node's estimated post-sync disk
use, and its optional LAN interfaces and ports. Every interface is closed by
default. The host rejects an invalid folder or an enabled port that is already
in use before it changes the running stack. The **Config** section directly
below Healthchecks shows the exact `config.json` path used by this installation.

The default user-owned paths survive Homebrew upgrades:

- configuration: `~/Library/Application Support/bitcoin-starter-stack/config/config.json`
- generated state and backups: `~/Library/Application Support/bitcoin-starter-stack/state/`
- chain data: `~/Library/Application Support/bitcoin-starter-stack/data/<node>/`

On macOS, set `BITCOIN_STACK_HOME` before invoking the command to move all three defaults
under another directory. The individual `BITCOIN_STACK_CONFIG_DIR`,
`BITCOIN_STACK_STATE_DIR`, and `BITCOIN_STACK_DATA_DIR` variables remain
available for separate locations.

Homebrew services do not inherit variables from a shell profile. When using
custom paths, put the same values in
`~/.homebrew/services/bitcoin-starter-stack.env`, one `KEY=value` per line, then
restart the service. Homebrew retains this service environment across upgrades.
For example:

```text
BITCOIN_STACK_HOME=/Volumes/Node Data/bitcoin-starter-stack
```

Setup starts the Homebrew host supervisor so dashboard controls work. It can
also be managed explicitly with:

```sh
brew services start bitcoin-starter-stack
```

The per-user service launches at **login**, rather than before login, following
[Homebrew service conventions](https://docs.brew.sh/Manpage#services-subcommand). The
application's **Launch at login** setting defaults off: the supervisor remains
available but does not start the stack on the next login until you enable it.
Enable Docker Desktop's own start-at-login option separately if desired.
Turning the application setting off leaves the current containers running and
disables automatic startup after Docker restarts. Fresh installs still retry
a crashed container up to five times during the current Docker session. Run `bitcoin-stack up` to start them
manually after a later login. Source installs without a managed
service show this setting as unavailable.

The supervisor processes node installation, port changes, and post-sync caps.
It never runs initial setup or downloads images just because you log in; an
explicit dashboard **Install** request authorizes downloading that node's
images. If configuration was edited while stopped, it keeps the last approved
version and offers the pending file in Settings. Stopping the Homebrew service
stops the supervisor; use `bitcoin-stack down` to stop the containers too.

## Linuxbrew

Homebrew on Linux is supported, although Ubuntu users can use the native APT
package instead. Install Docker Engine and Compose 2.33.1 or newer separately,
configure Docker so the current user can run it without `sudo`, and enable
Docker at boot. The formula uses the same install and setup commands as macOS.

Linuxbrew follows the XDG directory convention:

- configuration: `${XDG_CONFIG_HOME:-~/.config}/bitcoin-starter-stack/config.json`
- generated state: `${XDG_DATA_HOME:-~/.local/share}/bitcoin-starter-stack/state/`
- chain data: `${XDG_DATA_HOME:-~/.local/share}/bitcoin-starter-stack/data/<node>/`

`brew services start bitcoin-starter-stack` uses a user systemd service on
Linux. A logged-out user service may require lingering to remain active; the APT
package is the simpler choice for a system-wide Ubuntu server. The service waits
for both saved configuration and Docker; it does not run setup or pull
images.

## Edit and check the configuration

Edit the existing `config.json` at the platform path above, then run:

```sh
bitcoin-stack apply
bitcoin-stack status
```

`apply` explicitly approves the edited file, renders the private state and recreates only changed containers from
images already on the machine. It does not download images. Run
`bitcoin-stack pull` first when an upgrade needs new images. Use
`bitcoin-stack doctor` for a read-only dependency, configuration, disk, node,
and Tor report. See the [configuration reference](configuration.md) for every
field. A dashboard password changed in the gear menu is stored as a hash in
dashboard state and takes precedence over the baseline configuration; see
[credential operations](operations.md#dashboard-and-node-credentials).

## Upgrade and removal

Back up before an upgrade, then apply the new immutable payload to the existing
configuration:

```sh
bitcoin-stack backup
brew update
brew upgrade bitcoin-starter-stack
bitcoin-stack pull
bitcoin-stack apply
```

Homebrew replaces the versioned Cellar payload and retains the user-owned
configuration, dashboard state, and chain data. Uninstalling the formula also
leaves those user directories in place. Stop the service and containers first
if the installation is being retired:

```sh
brew services stop bitcoin-starter-stack
bitcoin-stack down
brew uninstall bitcoin-starter-stack
```

## Test a local release artifact

An unreleased branch uses image tags that do not exist in GHCR. Before testing
its Homebrew formula, archive the committed revision and build every
first-party image with the exact `VERSION` stored in that archive. Building
images does not start a node:

```sh
source_dir=$(pwd)
preview_dir=$(mktemp -d)
git archive --format=tar.gz --prefix=bitcoin-starter-stack/ \
  --output="$preview_dir/bitcoin-starter-stack.tar.gz" HEAD
tar -xzf "$preview_dir/bitcoin-starter-stack.tar.gz" -C "$preview_dir"
cd "$preview_dir/bitcoin-starter-stack"
version=$(cat VERSION)

for image in tor monero litecoin dogecoin bitcoincash; do
  docker build \
    --tag "ghcr.io/vijitsingh97/bitcoin-starter-stack-$image:$version" \
    "build/$image"
done

for image in dashboard tari-wallet; do
  docker build \
    --tag "ghcr.io/vijitsingh97/bitcoin-starter-stack-$image:$version" \
    --file "build/$image/Dockerfile" .
done
```

Generate the formula from that same archive, rather than from the working tree.
Create a private test tap once, then generate its formula:

```sh
tap=local/node-manager
brew tap-new --no-git "$tap"
formula="$(brew --repository "$tap")/Formula/bitcoin-starter-stack.rb"
version=$(cat VERSION)
package_version=${version%%-*}
PACKAGE_VERSION="$package_version" "$source_dir/scripts/build-homebrew-formula.sh" \
  --url "file://$preview_dir/bitcoin-starter-stack.tar.gz" \
  --output "$formula" "$preview_dir/bitcoin-starter-stack.tar.gz"
brew install "$tap/bitcoin-starter-stack"
brew test "$tap/bitcoin-starter-stack"
```

For an existing test tap, use its name, skip `brew tap-new`, and use `brew reinstall`
in place of `brew install`. This installs committed `HEAD`; uncommitted source
changes are excluded.

Fresh Homebrew installations use the `node-manager-brew` Compose project, which
gives their containers and named volumes project-scoped names. Existing
installations keep their saved `bitcoin-starter-stack-brew` project and volumes,
so reinstalling does not silently select an empty replacement chain. Use
`bitcoin-stack` commands instead of addressing containers by generated name.
Run one installation per Docker host, with all selected chains in that stack.
On an existing test installation, do not run a fresh `init`; use
`bitcoin-stack apply` after reinstalling so its configuration and chain data
remain selected. For a genuinely fresh test, run `bitcoin-stack init` only
after the matching images above are present.
