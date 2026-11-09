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
optional node interfaces stay on your trusted LAN.

<img src="./images/nodes.png" width="100%" alt="Node Manager in light mode with seven cryptocurrency node towers" />

<sub>Preview with sample data. Your dashboard reads your own nodes.</sub>

</div>

## Why Node Manager

- One lightweight dashboard for new or existing nodes.
- Pruned storage by default, with uncapped initial sync and configurable
  post-sync resource limits.
- Tor-only public egress for nodes, prices, alerts, and update checks.
- Private LAN dashboard with a generated password on new installs; node interfaces
  are closed until you enable them.
- View-only wallet monitoring where the node can support it. Node Manager never
  accepts a seed phrase or private spend key.
- Telegram alerts, Healthchecks.io, live sync progress, disk monitoring, and
  per-node controls.

The package, command, images, and formula are named `node-manager`. Existing
`bitcoin-starter-stack` installations upgrade in place: APT migrates
automatically through a transitional package, and `bitcoin-stack` remains as a
compatibility command.

## Ubuntu install with APT

Requires Ubuntu 24.04, Docker Engine, and Docker Compose 2.33.1 or newer. Install
and start Docker using the [official Ubuntu instructions](https://docs.docker.com/engine/install/ubuntu/),
then verify it:

```bash
sudo docker info
sudo docker compose version
```

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
starts a pruned Bitcoin node, and prints the dashboard URL. The package itself
does not start containers during installation.

Later `apt upgrade` runs download the release images and recreate changed
services for an initialized, running stack. This may briefly restart affected
nodes. A stopped stack stays stopped, and pending configuration edits are not
applied automatically.

Configuration: `/etc/node-manager/config.json`<br>
State and chain data: `/var/lib/node-manager/`

See the [getting-started guide](docs/getting-started.md) for custom storage,
first-boot configuration, upgrades, and package-feed trust details.

## Install from source on Ubuntu

Install Docker Engine and Docker Compose 2.33.1 or newer using the
[official Ubuntu instructions](https://docs.docker.com/engine/install/ubuntu/).
Complete Docker's
[Linux post-install steps](https://docs.docker.com/engine/install/linux-postinstall/)
so your user can run Docker without `sudo`, then install the host tools and
check out the published release:

```bash
sudo apt update
sudo apt install ca-certificates curl git iproute2 jq openssl util-linux
git clone --branch v1.33.5 --depth 1 https://github.com/VijitSingh97/node-manager.git
cd node-manager
./stack init
```

This uses the published container images; it does not build the application.
Configuration and node data stay in the checkout. Use `./stack status`,
`./stack up`, and `./stack down` to manage it.

## macOS install with Homebrew

Install and start [Docker Desktop](https://docs.docker.com/desktop/setup/install/mac-install/),
then verify Docker Compose 2.33.1 or newer:

```bash
docker info
docker compose version
```

Install Node Manager and start the default node:

```bash
brew tap vijitsingh97/node-manager https://github.com/VijitSingh97/node-manager.git
brew install vijitsingh97/node-manager/node-manager
node-manager init
```

The dashboard defaults to port `8080`. Setup chooses a private LAN address when
one is available and prints the exact URL.

Configuration: `~/Library/Application Support/node-manager/config/config.json`<br>
State and chain data: `~/Library/Application Support/node-manager/`

Enable **Launch at login** in Settings if the stack should return when you sign
in. Docker Desktop must also be running. See the [Homebrew guide](docs/homebrew.md)
for custom paths, services, upgrades, and removal.

## Safe defaults

A fresh `init` starts Bitcoin only with 10 GB of recent block files retained.
It generates one 32-character dashboard password, prints it once, and keeps it
in the mode-600 configuration. The dashboard is available only on the selected
localhost or private-LAN address. Every RPC, gRPC, and ZMQ interface is closed.
Launch at boot or login is off. Configure a baseline dashboard password before
any apply; existing saved dashboard hashes remain usable during recovery.

Use Node Manager only on a trusted LAN unless you configure the protected VPS
profile below. Never forward the dashboard or a node interface directly to the
public internet. LAN HTTP and node RPC are unencrypted. Read the [privacy model](docs/privacy.md)
before enabling remote access.

### Public VPS access

Prefer a private VPN such as Tailscale or WireGuard. For HTTPS access through a
host-managed Caddy reverse proxy, add `dashboard.public_url` as the exact origin,
set a dashboard password of 12–128 letters or numbers, and bind the dashboard to
loopback. Choose this HTTPS ingress or the Tor onion ingress; enabling both
at once is rejected:

```json
"dashboard": {
  "public_url": "https://node.example.com",
  "password": "ReplaceWithYourOwnRandomPassword",
  "bind_ip": "127.0.0.1",
  "port": 8080
}
```

Replace the example password with your own random value; `openssl rand -hex 16`
generates a compatible password.

For APT, edit `/etc/node-manager/config.json` with `sudo` and run
`sudo node-manager apply`; for a source install, edit `config.json` and run
`./stack apply`. See [Caddyfile.example](docs/Caddyfile.example) for a minimal
host config. Install Caddy from its [official Ubuntu packages](https://caddyserver.com/docs/install#debian-ubuntu-raspbian),
copy the example to `/etc/caddy/Caddyfile`, set `dashboard.port` to `8080` in
the config, point the domain's DNS at the VPS, replace the domain, and allow
Caddy's ports 80/443 for redirects and automatic public certificates.
If both ports are restricted to a VPN or IP allowlist, use DNS-based certificate
validation or the VPN's HTTPS service instead.
Validate with
`sudo caddy validate --config /etc/caddy/Caddyfile`, then `sudo systemctl reload caddy`.
The example uses backend `127.0.0.1:8080` so Caddy owns the public 80/443 ports;
if the dashboard binds to `::1`, use `[::1]:8080` instead.
Preserve `Host`, overwrite
`X-Node-Manager-Client-IP`, and allow only Caddy's 80/443 and any VPN ports in the host
firewall. Docker published ports bypass UFW. Keep all RPC, ZMQ, and gRPC
exposure settings false. ACME and Tailscale remain host-managed outside Tor.

Install more chains from **Settings → Node set**. Before downloading anything,
Node Manager shows the data path, estimated disk requirement, and optional LAN
interfaces. All interfaces remain closed unless you explicitly enable them.

## Hardware and storage

Use an SSD and start with one chain on smaller machines. Pruning reduces retained
storage; initial sync still downloads and validates the chain history.

Sync is uncapped by default. The experimental post-sync caps are **1 CPU / 2 GiB**
for Bitcoin and Monero and **1 CPU / 4 GiB** for the other nodes. Wallet services,
Tor, Docker, and your operating system need additional memory. These are editable
ceilings, not qualified minimum hardware.

See [per-node disk and resource estimates](docs/nodes.md) before adding chains.

## After installation

```bash
node-manager status
node-manager up
node-manager down
```

Use `sudo` with an APT installation. Configure nodes from the dashboard and see
[Operations](docs/operations.md) for backups, upgrades, logs, and troubleshooting.
The dashboard reports new versions. Install them through `sudo apt upgrade`,
Homebrew, or the [GitHub release](https://github.com/VijitSingh97/node-manager/releases/latest).

## Documentation

- [Getting started](docs/getting-started.md)
- [Configuration reference](docs/configuration.md)
- [Node storage and resources](docs/nodes.md)
- [Watch-only wallets](docs/watch-only.md)
- [Notifications](docs/notifications.md)
- [Operations and upgrades](docs/operations.md)
- [Tor and security model](docs/privacy.md)
- [Contributing](CONTRIBUTING.md)

Node Manager is released under the [MIT License](LICENSE).
