# Tor-only runtime networking

Bitcoin, Monero and its wallet service, Tari and its wallet service, Litecoin, Dogecoin, Bitcoin Cash,
Zcash through Zakura, and the dashboard have no direct external network route.
Their Docker network is internal; only the Tor container also joins an external
network. These services use numeric addresses for local services, and local DNS
forwarding is disabled. Bitcoin-family nodes and Monero use their native Tor
proxy settings. Tari uses its native Tor transport. Prices, alerts, and release
checks send hostnames to Tor for remote resolution. An unavailable Tor proxy
causes those requests to fail, without a direct fallback.

Zakura has no native Tor transport. Its peer list contains only four internal
TCP listeners on the Tor container. Each listener resolves and reaches one
upstream seed through Tor SOCKS4A; Zakura has no direct DNS or external route.
If Tor or every bridge is unavailable, peer discovery fails closed instead of
using clearnet.
The existing Tor container forwards LAN HTTP to the dashboard's fixed internal
address; the dashboard itself has no published port or external network.

The dashboard selects a private LAN IPv4 address by default and new installs
generate a password. Anyone with access to that trusted LAN can view wallet
information, edit saved notification secrets, and operate node controls only
after signing in. Set
`dashboard.bind_ip` to `127.0.0.1` for localhost only, or to a specific private
host address. Wildcards and public IPs are rejected; `::1` is accepted only for
protected HTTPS origin mode. Choose the protected HTTPS origin or the optional
onion service; enabling both together is rejected.
LAN HTTP is unencrypted: use a trusted LAN, or access the onion service with Tor
Browser. For a VPS, set `dashboard.public_url` to the exact HTTPS origin, set a
dashboard password, and bind to `127.0.0.1`. A host-managed Caddy instance can
terminate HTTPS and reverse-proxy to the loopback dashboard port; preserve the
`Host` header and configure the host firewall for only HTTPS or VPN access.
The host port is owned by the Tor container and forwarded to the dashboard's
fixed internal address; the dashboard itself stays on the internal network.

Optional LAN wallet RPC uses the same boundary: a private host address on the
Tor container relays authenticated TCP to Core's fixed internal address. The
Bitcoin container keeps no host port or external route. The listener is absent
unless `bitcoin.rpc_expose` is enabled; public, wildcard, loopback, and IPv6
binds are rejected. RPC is unencrypted, so keep it on a trusted LAN and do not
forward it through a router. Exposed RPC requires a password of at least 24
characters, and each host relay accepts at most 32 concurrent connections. See
[LAN wallet RPC](configuration.md#lan-wallet-rpc-opt-in).

Zcash LAN RPC terminates at the Go dashboard's authenticated read-only proxy.
The proxy forwards only a fixed allowlist of read-only methods to Zakura's
cookie-authenticated native RPC at `172.29.0.36:8232`. Native Zakura RPC remains
unpublished on the internal Docker network and is never the target of the host
relay. The healthcheck makes the rotating cookie group-readable only to the
non-root dashboard group. Treat the internal network as part of the trusted
stack boundary because a compromised sibling container could still reach
another internal service.

Monero view-only monitoring does not publish another port. The
dashboard sends a primary address and private view key to the internal wallet
service at `172.29.0.31:18082`. That service stores only an encrypted watch-only
wallet file in its private volume. Dashboard metadata is mode `600` and does not
retain the raw view key. A private spend key or seed is never accepted, so this
path cannot sign or send. Treat the private view key and signed key images as
sensitive: use the trusted LAN dashboard or its authenticated onion service, and
do not put them in `config.json`, Git, URLs, or command lines.

Tari view-only monitoring also publishes no port. The dashboard authenticates to
the wallet sidecar's internal gRPC service at `172.29.0.37:18143`; the wallet
queries the base node's internal HTTP service at `tari:9000`. Both remain on the
internal network. Generated `TARI_WALLET_RPC_PASSWORD` and `TARI_WALLET_PASSWORD`
values are separate from the dashboard password and optional LAN mining gRPC.

Wallet creation accepts one public spend key and private view key, never a seed
or private spend key. The view key crosses the shared owner-only `tari_wallet`
volume in a mode-600 request file and that handoff is removed after creation.
The dashboard metadata does not retain it. Tari v6.1 does store the private view
key in plaintext inside its native SQLite wallet database, however; the wallet
password does not provide encryption at rest. That database and its archives are
isolated in the sidecar-only `tari_wallet_data` volume, which the dashboard cannot
read. Protect the Docker data root and
use encrypted host storage when encryption at rest is required. A view key can
reveal funds and history but cannot sign a transaction. Removing the wallet
archives that owner-only database rather than deleting it or the Tari base node.
Failed imports are retained in the same private archive for recovery; removing a
dashboard row does not erase archived view keys.

## Downloads and upgrades

Starting or applying the stack uses only images already present locally, with
automatic pulls and builds disabled. Missing images cause a failure. `./stack
pull` is the explicit download command and uses the Docker daemon's existing host
network settings. The stack neither requires a host proxy nor changes Docker's
daemon configuration. An operator-configured Docker proxy continues to apply.

The explicit `init` command downloads missing images and starts the default or
preconfigured stack without questions. A dashboard **Install node** request also
authorizes the host helper to download that node's versioned images. Routine starts
and applies retain the `--pull never --no-build` policy; logging in or rebooting
does not authorize a new download.

The dashboard checks release metadata through Tor and only reports availability.
It cannot download or apply application updates. Users update through their
package manager or obtain a release directly from GitHub.

APT, Git clones, browser downloads, and Docker registry pulls are host activity,
outside the running container boundary. They use the host connection and may
reveal its public IP unless the operator has independently configured a proxy.
This does not add an external route to the Bitcoin or dashboard containers.

## Package feed

Version `v1.32.0` publishes the first signed APT feed. Its dedicated signing-key
fingerprint is `72FD 6567 54BD 7534 E274 360A 3707 0C27 30C0 B4EA`. Wait for the
release APT job and GitHub Pages deployment to succeed. The standard
[APT setup](../README.md#ubuntu-install-with-apt) downloads the key over HTTPS
and scopes it to this repository with `Signed-By`. To verify the key against the
published fingerprint before installing it, use this optional procedure:

```bash
sudo apt update
sudo apt install -y ca-certificates curl gnupg

expected='72FD656754BD7534E274360A37070C2730C0B4EA'
printf '%s\n' "$expected" | grep -Eq '^[0-9A-Fa-f]{40}$' || {
  echo 'The configured APT signing fingerprint is invalid.' >&2
  exit 1
}
key=$(mktemp)
trap 'rm -f "$key"' EXIT
curl --fail --show-error --silent \
  https://vijitsingh97.github.io/node-manager/node-manager.asc \
  --output "$key"
listing=$(gpg --batch --show-keys --with-colons "$key") || {
  echo 'Unable to parse the APT signing key.' >&2
  exit 1
}
actual=$(awk -F: '
  $1 == "pub" { primary_keys++ }
  $1 == "fpr" && primary_fingerprint == "" { primary_fingerprint = $10 }
  END { print primary_keys ":" primary_fingerprint }
' <<<"$listing") || exit 1
[ "$actual" = "1:$expected" ] || {
  echo 'APT signing key does not contain exactly the expected primary key.' >&2
  exit 1
}
sudo install -d -m 0755 /etc/apt/keyrings
sudo install -m 0644 "$key" /etc/apt/keyrings/node-manager.asc
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

Fresh APT installation does not run setup or start containers; `init` does that
explicitly. Later APT upgrades download the release images and recreate the
services that were running. A stopped stack stays stopped, and pending config
edits remain unapproved. Downloads use the host connection. The signed package
pins exact multi-architecture image digests, so Docker verifies the image content
rather than trusting a mutable version tag. Release consumers use prebuilt images
and do not compile on the node.

This protects the stack's network paths. It cannot guarantee anonymity against
traffic correlation, a compromised host/Tor daemon, router port forwarding, or
operator-created alternate networks and proxies.

References: [Docker internal networks](https://docs.docker.com/reference/compose-file/networks/#internal),
[Go SOCKS proxy support](https://go.dev/src/net/http/transport.go).
