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
no `pull` or `apply` is needed. Upgrading from 1.37.0 or earlier on amd64 or
arm64, also run `sudo apt install node-manager-hostd` once for 10-second host
readings ([details](docs/operations.md#host-readings)).

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
