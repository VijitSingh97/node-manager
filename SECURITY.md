# Security Policy

## Reporting

Report vulnerabilities privately via
[GitHub Security Advisories](https://github.com/VijitSingh97/node-manager/security/advisories/new).
Please don't open public issues for security problems.

## Threat model (what this stack does and doesn't protect)

**Protects:**

- **Your IP from the Bitcoin network.** All P2P traffic goes through Tor
  (`proxy=` to the tor container). Clearnet peers are reached through Tor exit
  relays, and `network.clearnet_over_tor: false` restricts nodes to onion peers.
  Inbound connections (`network.inbound_connections`, on by default) arrive
  through an onion service without publishing a host P2P port. The Tor
  container publishes those onion services itself and opens no control port;
  node containers get only their public onion hostname, never Tor's keys.
- **RPC from the outside.** Node RPC ports remain internal by default. Optional LAN
  relays bind only to private host addresses; authenticated interfaces retain
  separate generated node credentials. Public and wildcard binds are rejected.
- **Credentials from git.** RPC credentials live only in gitignored files —
  `config.json` and the rendered `.env` (mode 600) — and are never written
  into tracked files.
- **Protected VPS mode.** Set `dashboard.public_url` to the exact HTTPS origin used
  by your reverse proxy. Configuration then requires a baseline dashboard password
  and loopback binding; the proxy must preserve the `Host` header and terminate TLS.
  Use the optional [Caddy example](docs/Caddyfile.example), overwrite the client-IP
  header, and keep the backend reachable only on loopback. Caddy's HTTPS listener
  and ACME are host-managed; they are separate from the application's Tor traffic.
- **Your IP from notification services.** Opt-in Telegram alerts and
  Healthchecks.io pings route through the stack's Tor proxy (`socks5h`),
  so those endpoints see a Tor exit, never your address. (The Telegram bot
  token in `.env`/dashboard env can send messages as your bot if leaked —
  it cannot read other chats or control the node.)
- **The plaintext RPC password from the bitcoin container.** bitcoind gets
  only a salted HMAC (`rpcauth`); `docker inspect bitcoin` reveals no
  password. The plaintext exists in the dashboard's environment (it must
  authenticate) and in `.env`. Its own health check uses Bitcoin Core's
  cookie file, not the password.

**Accepted risks (by design — know them before deploying):**

- **The dashboard requires a baseline password.** It selects a private host IPv4
  address, falling back to loopback. Homebrew uses port 8080; APT/source use port
  80. Signed-in users can view wallet information and saved notification values,
  install or deactivate nodes, and change settings. Public/wildcard binds are
  rejected. Never port-forward the dashboard; HTTP sign-in and sessions are
  unencrypted. Credential reveal/reset and onion publication require a dashboard
  password.
- **A public VPS needs an operator-managed edge.** Prefer Tailscale or WireGuard.
  Otherwise terminate HTTPS in host Caddy or another reverse proxy and forward
  only to the loopback dashboard port. Docker-published ports bypass UFW, so bind
  the dashboard to loopback and allow only proxy/VPN ports in the host firewall.
  Keep RPC, ZMQ, and gRPC interfaces closed to the public internet; ACME and VPN
  services remain host-managed and outside Tor. Sustained anonymous requests can
  exhaust fresh-login capacity; existing sessions continue, so retain VPN,
  firewall allowlists, or SSH access as the reliable recovery path.
- **Anyone with Docker access on the host can read the dashboard's RPC
  password** via `docker inspect dashboard`. Docker access is
  root-equivalent on the host anyway, so this is not treated as a boundary.
- **Initial block download trusts the peers reached through Tor.** Clearnet
  peers are reached through Tor exit relays, and a malicious exit can see the
  unencrypted P2P traffic it carries or withhold blocks. Proof of work still
  stops a forged chain, but a node with few peers is open to the usual
  eclipse-attack tradeoffs. Set `network.clearnet_over_tor` to `false` to use
  onion peers only.
