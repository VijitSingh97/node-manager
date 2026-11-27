<div align="center">

# Node Manager

### A lightweight dashboard for running and monitoring cryptocurrency nodes.

[![CI](https://github.com/VijitSingh97/node-manager/actions/workflows/ci.yml/badge.svg)](https://github.com/VijitSingh97/node-manager/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/VijitSingh97/node-manager)](https://github.com/VijitSingh97/node-manager/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
![Platform: Ubuntu and macOS](https://img.shields.io/badge/Platform-Ubuntu%20%7C%20macOS-4B5563)
![Runtime: Tor-only](https://img.shields.io/badge/Runtime-Tor--only-7D4698?logo=torproject&logoColor=white)

Run Bitcoin, Monero, Tari, Litecoin, Dogecoin, Bitcoin Cash, and Zcash from one
small Go dashboard. Public runtime traffic stays behind Tor. The dashboard and
optional node interfaces stay on your trusted LAN. Wallets are view-only: Node
Manager never accepts a seed phrase or private spend key.

<img src="./images/nodes.png" width="100%" alt="Node Manager in light mode with seven cryptocurrency node towers" />

<sub>Preview with sample data. Your dashboard reads your own nodes.</sub>

</div>

## Highlights

- **Balances in milliseconds.** An optional balance index keeps a local copy of
  your node's coin set, so a watched wallet's balance is a database read
  instead of a full `scantxoutset` walk of the chain. Measured on the reference
  node, a 4-core, 7.6 GiB Mac mini:

  | Chain | One address by full scan | Same address from the index (median) |
  | --- | --- | --- |
  | Bitcoin | 163.6 s | about 0.1 s (the genesis address, 83,490 coins) |
  | Litecoin | 39 to 58 s | 5 to 8 ms (6,526 coins) |
  | Bitcoin Cash | 44 to 82 s | 7.4 ms or less (6,667 coins) |
  | Dogecoin | no scan: Dogecoin Core 1.14.9 has no `scantxoutset` | under 1 ms (2,555 coins); 0.84 s for a 917,097-coin address |

  The index and the scan agreed on every wallet tested (13 on Bitcoin, 16 on
  Litecoin, 16 on Bitcoin Cash), and the public addresses among them matched
  block explorers. Dogecoin has no scan to compare with; its index matched the
  node's whole coin set, and 8 of 8 rich-list addresses matched two explorers.
  A new Bitcoin block reached watched balances in about 5 s (median of 12
  blocks). Indexes exist for Bitcoin (pruned nodes), Litecoin,
  Bitcoin Cash and Dogecoin. APT and Homebrew installs seed each one from the
  node once it has synced (a source checkout seeds by hand); a Bitcoin seed
  takes about 20 minutes, with its RPC down for about 5. Disk is about 4.5 GB
  for Litecoin, 5.2 GB for Bitcoin Cash, 18.4 GB for Dogecoin and 7.5 GiB for
  Bitcoin, which needs up to about 17 GiB while it seeds. See
  [Watch-only wallets](docs/watch-only.md) and the
  [performance history](docs/measurements/performance-history.md).
- **Tor-only at runtime.** The nodes, wallet services and dashboard share an
  internal Docker network with no route out; only the Tor container reaches the
  internet. Bitcoin, Litecoin, Dogecoin, Bitcoin Cash and Tari reach clearnet
  peers through Tor exits, or onion peers only with one setting; Monero and
  Zcash always go through Tor exits. Bitcoin, Litecoin and Bitcoin Cash accept
  inbound peers through their own onion services without port forwarding. Prices, alerts and
  release checks go over Tor too; package and image downloads use the host's
  connection. [Tor boundary](docs/tor-boundary.md) lists every egress path and
  the test that guards it.
- **View-only wallets on every chain.** Watch Bitcoin xpubs, descriptors and
  addresses; Litecoin, Bitcoin Cash, Dogecoin and Zcash addresses; and Monero
  and Tari wallets by their view keys. Balances come from your own node, wallet
  keys and addresses never go to an outside service, and seed phrases and
  spend keys are refused. Dogecoin and Zcash watching uses indexes that
  dashboard installs turn on. Zcash watches transparent addresses only, Monero
  shows received amounts until you import key images, and Litecoin balances
  leave out MWEB funds.
- **Seven chains, pruned by default.** Install, activate and deactivate
  Bitcoin, Monero, Tari, Litecoin, Dogecoin, Bitcoin Cash and Zcash from the
  dashboard. Every chain runs pruned by default: plan 28 to 56 GiB for a
  default Bitcoin node instead of about 840 GiB archival.
  [Node storage and resources](docs/nodes.md) gives each chain's disk plan and
  how far it has been tested.
- **Made for small, always-on hardware.** After initial sync, fresh APT
  installs (and Homebrew with its optional service) cap each node at about
  1 CPU and 2 to 4 GiB, an experimental compact profile. An optional Linux
  thermal throttle halves node CPU caps when the host runs hot instead of
  stopping anything.
- **It watches itself.** Optional Telegram alerts (node down and recovered,
  sync complete, low disk, new release), a Healthchecks dead-man's switch, an
  event log in the dashboard, and opt-in health endpoints for your own
  monitors. Alerts and pings go over Tor.

## Requirements

Ubuntu 24.04 (APT or source) or macOS (Homebrew), with Docker Engine or Docker
Desktop and Docker Compose 2.33.1 or newer. See
[Getting started](docs/getting-started.md#prerequisites) for Docker setup and
[Hardware](docs/hardware.md) for sizing.

## Install on Ubuntu (APT)

Install Docker Engine and Compose first (see
[Prerequisites](docs/getting-started.md#prerequisites)). The package does not
install Docker, and `init` fails without it.

```bash
sudo apt update
sudo apt install ca-certificates curl
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://vijitsingh97.github.io/node-manager/node-manager.asc \
  | sudo tee /etc/apt/keyrings/node-manager.asc >/dev/null
sudo tee /etc/apt/sources.list.d/node-manager.sources >/dev/null <<'EOF'
Types: deb
URIs: https://vijitsingh97.github.io/node-manager/
Suites: ./
Signed-By: /etc/apt/keyrings/node-manager.asc
EOF
sudo apt update
sudo apt install node-manager
sudo node-manager init
```

The signing key fingerprint is
`72FD656754BD7534E274360A37070C2730C0B4EA`. `Signed-By` limits that key to
this repository.

`init` creates a private configuration, downloads the required container images,
starts a pruned Bitcoin node, and prints the dashboard URL, for example
`http://<lan-ip>`. The package itself
does not start containers during installation. Configuration is in
`/etc/node-manager/config.json`; data is in `/var/lib/node-manager/`.

To install from source instead, see
[Install from source on Ubuntu](docs/getting-started.md#install-from-source-on-ubuntu).

## Install on macOS (Homebrew)

Install and start [Docker Desktop](https://docs.docker.com/desktop/setup/install/mac-install/),
then:

```bash
brew tap vijitsingh97/node-manager https://github.com/VijitSingh97/node-manager.git
brew install vijitsingh97/node-manager/node-manager
node-manager init
```

See the [Homebrew guide](docs/homebrew.md) for paths, services, upgrades, and
removal.

## First login

`init` prints the dashboard URL and a generated 32-character password, once. The
username is `admin`. The dashboard listens on port `80` (APT) or `8080`
(Homebrew), on a private LAN address chosen at setup, or on localhost if none
exists. If you missed the password and never changed it, read `dashboard.password` from
`/etc/node-manager/config.json` (APT) or the path in the
[Homebrew guide](docs/homebrew.md). See
[Operations](docs/operations.md#dashboard-and-node-credentials).

Use Node Manager only on a trusted LAN. The dashboard serves plain HTTP with one
password, so never forward it or a node interface to the public internet. For
remote access, use the [Caddy recipe](docs/notifications.md#public-vps-access-with-caddy)
and read the [privacy model](docs/privacy.md) first.

## Updating

The dashboard shows a notice when a new release is out. Back up, then update the
way you installed.

Ubuntu / APT:

```bash
sudo node-manager backup
sudo apt update
sudo apt upgrade
```

The package downloads new images and restarts the running services itself, so
no `pull` or `apply` is needed. On amd64 and arm64 an upgrade from 1.37.0 or
earlier also installs the recommended `node-manager-hostd` helper for 10-second
host readings. If `dpkg -s node-manager-hostd` says it is missing, run
`sudo apt install node-manager-hostd` ([details](docs/operations.md#host-readings)).

macOS / Homebrew:

```bash
node-manager backup
brew update
brew upgrade node-manager
node-manager pull
node-manager apply
```

Details and recovery: [Upgrading](docs/operations.md#upgrading).

## Documentation

See the [documentation index](docs/README.md). Start with:

- [Getting started](docs/getting-started.md)
- [Configuration reference](docs/configuration.md)
- [Node storage and resources](docs/nodes.md)
- [Watch-only wallets](docs/watch-only.md)
- [Notifications and remote access](docs/notifications.md)
- [Operations and upgrades](docs/operations.md)
- [Tor and security model](docs/privacy.md)
- [Contributing](CONTRIBUTING.md)

Node Manager is released under the [MIT License](LICENSE).
