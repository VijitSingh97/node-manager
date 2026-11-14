#!/usr/bin/env bash
# docker-compose.yml contract: renders with valid env, fails without
# credentials, and exposes nothing to the host except the dashboard port.
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  exit 1
}

command -v docker >/dev/null || {
  echo "SKIP: test_compose.sh (docker not available)"
  exit 0
}

export BITCOIN_RPC_USER=testuser
export BITCOIN_RPC_PASSWORD=testpass
export BITCOIN_RPCAUTH_SALT=cafe0123cafe0123cafe0123cafe0123
export BITCOIN_RPCAUTH_HASH=deadbeef
export BITCOIN_DATA_DIR=./data/bitcoin
export BITCOIN_DBCACHE=1234
export BITCOIN_PRUNE=555
export COMPOSE_PROFILES=bitcoin
export MONERO_RPC_USER=monerouser
export MONERO_RPC_PASSWORD=moneropassword
export MONERO_WALLET_RPC_USER=walletuser
export MONERO_WALLET_RPC_PASSWORD=walletpassword
export MONERO_WALLET_PASSWORD=walletfilepassword
export TARI_WALLET_RPC_USER=tariwalletuser
export TARI_WALLET_RPC_PASSWORD=tariwalletpassword
export TARI_WALLET_PASSWORD=tariwalletfilepassword
export TARI_WALLET_ENABLED=0
export LITECOIN_RPC_USER=litecoinuser
export LITECOIN_RPC_PASSWORD=litecoinpassword
export DOGECOIN_RPC_USER=dogecoinuser
export DOGECOIN_RPC_PASSWORD=dogecoinpassword
export BITCOINCASH_RPC_USER=bitcoincashuser
export BITCOINCASH_RPC_PASSWORD=bitcoincashpassword
export ZCASH_RPC_USER=zcashuser
export ZCASH_RPC_PASSWORD=zcashpassword
export WATCH_WALLETS_B64=watchblob
export DASHBOARD_BIND_IP=127.0.0.1
export STACK_VERSION=test

rendered=$(docker compose --env-file /dev/null config)
[ "$(grep -c 'container_name:' docker-compose.yml || true)" = 0 ] || fail "Compose services still use global container names"
grep -qF 'COPY --chmod=0644 build/dashboard/login.html ./login.html' build/dashboard/Dockerfile ||
  fail "dashboard runtime image does not copy login.html"
restart_rendered=$(STACK_RESTART_POLICY=unless-stopped docker compose --env-file /dev/null config)
legacy_cache_rendered=$(env -u BITCOIN_DBCACHE docker compose --env-file /dev/null config)

# Settings reach bitcoind's environment
echo "$rendered" | grep -q 'RPC_USER: testuser' || fail "RPC_USER not interpolated"
echo "$rendered" | grep -q 'RPCAUTH_SALT: cafe0123cafe0123cafe0123cafe0123' || fail "RPCAUTH_SALT not interpolated"
echo "$rendered" | grep -q 'RPCAUTH_HASH: deadbeef' || fail "RPCAUTH_HASH not interpolated"
echo "$rendered" | grep -q 'DBCACHE: "1234"' || fail "DBCACHE not interpolated"
echo "$legacy_cache_rendered" | grep -q 'DBCACHE: "3000"' || fail "legacy DBCACHE fallback not applied"
echo "$rendered" | grep -q 'PRUNE: "555"' || fail "PRUNE not interpolated"
echo "$rendered" | grep -q 'INBOUND_ONION: "0"' || fail "INBOUND_ONION default not applied"
echo "$rendered" | grep -q 'DASHBOARD_ONION: "0"' || fail "DASHBOARD_ONION default not applied"
echo "$rendered" | grep -q 'BITCOIN_RPC_EXPOSE: "0"' || fail "LAN RPC exposure default not applied"
echo "$rendered" | sed -n '/^  dashboard:$/,/^  tor:$/p' | grep -q 'BITCOIN_ENABLED: "1"' || fail "dashboard did not receive enabled Bitcoin state"
echo "$rendered" | sed -n '/^  dashboard:$/,/^  tor:$/p' | grep -q 'BITCOIN_RPC_EXPOSE: "0"' || fail "dashboard did not receive Bitcoin RPC exposure state"
echo "$rendered" | grep -q 'NODE_NAME: bitcoin-node' || fail "NODE_NAME default not applied"
echo "$rendered" | grep -q 'ALERT_NEW_BLOCK: "0"' || fail "ALERT_NEW_BLOCK default not applied"
echo "$rendered" | grep -q 'ALERT_NEW_BLOCK_MONERO: "0"' || fail "ALERT_NEW_BLOCK_MONERO default not applied"
for chain in TARI LITECOIN DOGECOIN BITCOINCASH ZCASH; do
  echo "$rendered" | grep -q "ALERT_NEW_BLOCK_${chain}: \"0\"" || fail "ALERT_NEW_BLOCK_${chain} default not applied"
done
echo "$rendered" | grep -q 'WATCH_WALLETS_B64: watchblob' || fail "WATCH_WALLETS_B64 not interpolated"
echo "$rendered" | grep -q 'SETTINGS_PORT: "8001"' || fail "internal settings listener is not configured"
[ "$({ grep -Fh "restart: \${STACK_RESTART_POLICY:-on-failure:5}" docker-compose.yml docker-compose.tari-wallet.yml || true; } | wc -l | tr -d ' ')" = 11 ] ||
  fail "not every application container uses the launch policy"
[ "$(echo "$rendered" | grep -c 'restart: on-failure:5')" = 3 ] || fail "active containers do not use bounded crash recovery"
[ "$(echo "$restart_rendered" | grep -c 'restart: unless-stopped')" = 3 ] || fail "launch-at-boot policy did not reach active containers"
grep -qF 'HiddenServicePort 80 172.29.0.27:8001' build/tor/entrypoint.sh || fail "onion dashboard does not use the management listener"

# The plaintext RPC password reaches ONLY the dashboard (bitcoind gets the
# rpcauth hash) — exactly one occurrence in the whole rendered config
count=$(echo "$rendered" | grep -c 'testpass' || true)
[ "$count" = "1" ] || fail "plaintext password appears $count times in rendered config, expected 1 (dashboard only)"

# Entrypoint defers expansion to container runtime ($$ escaped), so secrets
# are never baked into the rendered command line.
# All flags must sit on ONE line with the exec: a YAML-induced line break
# after -conf silently drops them (found the hard way; see tests/test_e2e.sh).
# shellcheck disable=SC2016 # matching the literal $$-escaped text is the point
echo "$rendered" | grep -qF -- 'exec bitcoind -datadir=/data -conf=/data/bitcoin.conf -rpcauth="$$RPCAUTH" -dbcache="$$DBCACHE" -prune="$$PRUNE" -blockfilterindex="$$BLOCKFILTERINDEX" -maxconnections=125 -inboundrelaypercent=100 $$NET_ARGS $$ONION_ARGS' ||
  fail "bitcoind exec line is missing runtime-env flags (or they were split onto another line)"
echo "$rendered" | grep -q -- "-rpcauth=testuser" && fail "credentials baked into rendered command line"
# Outbound routing is always Tor-only.
echo "$rendered" | grep -qF 'NET_ARGS="-proxy=172.29.0.25:9050 -onion=172.29.0.25:9050 -onlynet=onion"' ||
  fail "Tor-only routing missing from the entrypoint"
echo "$rendered" | grep -q 'SYNC_OVER_CLEARNET' && fail "clearnet sync bypass remains in compose"

# inbound-onion branch, pinned verbatim: regressing this once silently killed
# outbound onion sync (commit 0654ac0). Only appears when INBOUND_ONION=1.
# shellcheck disable=SC2016 # matching the literal $$-escaped text is the point
echo "$rendered" | grep -qF -- 'if [ "$$INBOUND_ONION" = "1" ]; then ONION_ARGS="-listen=1 -listenonion=1 -torcontrol=172.29.0.25:9051"; fi' ||
  fail "INBOUND_ONION branch line changed — check the inbound onion-service flags in the entrypoint"

# Bitcoin and dashboard cannot use Docker's external DNS forwarding, and only
# Tor has a deterministic external default gateway.
[ "$(echo "$rendered" | grep -c '^    dns:$')" = "2" ] || fail "bitcoin/dashboard DNS lock missing"
[ "$(echo "$rendered" | awk '$1 == "dns:" { getline; if ($1 == "-" && $2 == "127.0.0.1") count++ } END { print count + 0 }')" = "2" ] ||
  fail "bitcoin/dashboard DNS is not loopback-only"
echo "$rendered" | grep -A6 '^  btc_net:' | grep -q 'internal: true' || fail "btc_net is not internal"
echo "$rendered" | grep -A5 '^      tor_egress:' | grep -q 'gw_priority: 1' || fail "Tor egress gateway priority missing"

# The only published port is the dashboard on host port 80, loopback by default.
ports=$(echo "$rendered" | grep -c 'published: "80"' || true)
published=$(echo "$rendered" | grep -c 'published:' || true)
[ "$ports" = "1" ] || fail "expected dashboard published on 80, got $ports"
[ "$published" = "1" ] || fail "expected exactly 1 published port, got $published"
echo "$rendered" | grep -q 'published: "8332"' && fail "default config publishes Bitcoin RPC"
echo "$rendered" | grep -q 'host_ip: 127.0.0.1' || fail "dashboard does not bind loopback by default"
protected_rendered=$(DASHBOARD_BIND_IP=127.0.0.1 DASHBOARD_PUBLIC_URL=https://node.example.com DASHBOARD_PASSWORD=longenough12 docker compose --env-file /dev/null config)
echo "$protected_rendered" | grep -q 'DASHBOARD_PUBLIC_URL: https://node.example.com' || fail "dashboard public URL was not passed to the dashboard"
echo "$protected_rendered" | grep -q 'host_ip: 127.0.0.1' || fail "protected dashboard does not bind loopback"
echo "$rendered" | grep -q 'name: btc_net' && fail "internal network uses a global name instead of the Compose project"
echo "$rendered" | grep -q 'name: tor_egress' && fail "egress network uses a global name instead of the Compose project"
echo "$rendered" | sed -n '/^  dashboard:$/,/^  tor:$/p' | grep -q 'target: /tari-wallet' &&
  fail "dashboard creates Tari wallet storage when Tari is not selected"
tor_port=$(echo "$rendered" | sed -n '/^  tor:$/,/^networks:$/p' | awk '$1 == "host_ip:" { print $2 }')
[ "$tor_port" = "127.0.0.1" ] || fail "dashboard forwarder port is not owned by Tor"
echo "$rendered" | sed -n '/^  dashboard:$/,/^  tor:$/p' | grep -q 'host_ip:' &&
  fail "internal dashboard container still publishes a host port"
echo "$rendered" | sed -n '/^  tor:$/,/^networks:$/p' | grep -q 'nc -z 127.0.0.1 8000' ||
  fail "Tor healthcheck does not cover the dashboard forwarder"
echo "$rendered" | sed -n '/^  tor:$/,/^  bitcoin:$/p' | grep -q 'pids_limit: 100' ||
  fail "Tor container process limit missing"
grep -qF 'socat TCP4-LISTEN:8000,reuseaddr,fork,max-children=32 TCP4:172.29.0.27:8000' build/tor/entrypoint.sh ||
  fail "Tor entrypoint does not forward the published port to the fixed dashboard address"
grep -qF 'socat TCP4-LISTEN:8332,reuseaddr,fork,max-children=32 TCP4:172.29.0.26:8332' build/tor/entrypoint.sh ||
  fail "Tor entrypoint does not relay opt-in LAN RPC to Core"
grep -qF 'socat TCP4-LISTEN:18081,reuseaddr,fork,max-children=32 TCP4:172.29.0.30:18081' build/tor/entrypoint.sh ||
  fail "Tor entrypoint does not relay opt-in Monero RPC"
grep -qF 'socat TCP4-LISTEN:18083,reuseaddr,fork,max-children=32 TCP4:172.29.0.30:18083' build/tor/entrypoint.sh ||
  fail "Tor entrypoint does not relay opt-in Monero ZMQ"
grep -qF 'socat TCP4-LISTEN:18142,reuseaddr,fork,max-children=32 TCP4:172.29.0.32:18142' build/tor/entrypoint.sh || fail "Tor entrypoint does not relay opt-in Tari gRPC"
grep -qF 'socat TCP4-LISTEN:9332,reuseaddr,fork,max-children=32 TCP4:172.29.0.33:9332' build/tor/entrypoint.sh || fail "Tor entrypoint does not relay opt-in Litecoin RPC"
grep -qF 'socat TCP4-LISTEN:22555,reuseaddr,fork,max-children=32 TCP4:172.29.0.34:22555' build/tor/entrypoint.sh || fail "Tor entrypoint does not relay opt-in Dogecoin RPC"
grep -qF 'socat TCP4-LISTEN:18332,reuseaddr,fork,max-children=32 TCP4:172.29.0.35:8332' build/tor/entrypoint.sh || fail "Tor entrypoint does not relay opt-in Bitcoin Cash RPC"
grep -qF 'socat TCP4-LISTEN:18232,reuseaddr,fork,max-children=32 TCP4:172.29.0.27:18232' build/tor/entrypoint.sh || fail "Tor entrypoint does not relay opt-in Zcash RPC through the authenticated dashboard proxy"
grep -qF 'SOCKS4A:127.0.0.1:dnsseed.str4d.xyz:8233,socksport=9050' build/tor/entrypoint.sh || fail "Zcash bootstrap relay does not resolve its peer through Tor"
# shellcheck disable=SC2016
grep -qF 'if [ "${ZCASH_ENABLED:-0}" = "1" ] && [ "${ZCASH_ACTIVE:-1}" = "1" ]; then' build/tor/entrypoint.sh || fail "Zcash bootstrap relays run when the node is disabled or inactive"
echo "$rendered" | sed -n '/^  tor:$/,/^  bitcoin:$/p' | grep -q 'ZCASH_ENABLED: "0"' || fail "Tor did not receive disabled Zcash state"
echo "$rendered" | sed -n '/^  tor:$/,/^  bitcoin:$/p' | grep -q 'ZCASH_ACTIVE: "1"' || fail "Tor did not receive Zcash active state"

lan_rendered=$(DASHBOARD_BIND_IP=192.168.1.117 docker compose --env-file /dev/null config)
echo "$lan_rendered" | grep -q 'host_ip: 192.168.1.117' || fail "private-LAN dashboard bind was not rendered"

rpc_rendered=$(BITCOIN_RPC_EXPOSE=1 BITCOIN_RPC_BIND_IP=192.168.1.118 BITCOIN_RPC_PORT=18332 \
  COMPOSE_FILE=docker-compose.yml:docker-compose.rpc.yml docker compose --env-file /dev/null config)
echo "$rpc_rendered" | grep -q 'BITCOIN_RPC_EXPOSE: "1"' || fail "RPC relay was not enabled"
echo "$rpc_rendered" | grep -A5 'target: 8332' | grep -q 'published: "18332"' || fail "custom RPC host port was not rendered"
echo "$rpc_rendered" | grep -B2 -A5 'target: 8332' | grep -q 'host_ip: 192.168.1.118' || fail "private RPC host bind was not rendered"
[ "$(echo "$rpc_rendered" | grep -c 'published:' || true)" = "2" ] || fail "RPC override published an unexpected port"
echo "$rpc_rendered" | sed -n '/^  bitcoin:$/,/^  dashboard:$/p' | grep -q 'published:' &&
  fail "Bitcoin container publishes RPC directly instead of using the Tor relay"

monero_rendered=$(COMPOSE_PROFILES=bitcoin,monero MONERO_ENABLED=1 docker compose --env-file /dev/null config)
echo "$monero_rendered" | sed -n '/^  dashboard:$/,/^  monero:$/p' | grep -q 'MONERO_ENABLED: "1"' || fail "dashboard did not receive enabled Monero state"
echo "$monero_rendered" | grep -q '^  monero:' || fail "Monero profile did not start the Monero service"
echo "$monero_rendered" | grep -q '^  monero-wallet:' || fail "Monero profile did not start the wallet service"
wallet_rendered=$(echo "$monero_rendered" | sed -n '/^  monero-wallet:$/,/^  tor:$/p')
echo "$wallet_rendered" | grep -q 'ipv4_address: 172.29.0.31' || fail "Monero wallet fixed internal address changed"
echo "$wallet_rendered" | grep -q 'read_only: true' || fail "Monero wallet root filesystem is writable"
echo "$wallet_rendered" | grep -q 'source: monero_wallets' || fail "Monero wallet volume is not persistent"
echo "$wallet_rendered" | grep -q 'published:' && fail "Monero wallet RPC is published to the host"
echo "$wallet_rendered" | grep -q '127.0.0.1' || fail "Monero wallet DNS is not fail-closed"
echo "$wallet_rendered" | grep -q 'mem_limit: "1073741824"' || fail "Monero wallet memory guard changed"
echo "$wallet_rendered" | grep -q 'cpus: 0.5' || fail "Monero wallet CPU guard changed"
echo "$monero_rendered" | sed -n '/^  monero:$/,/^  tor:$/p' | grep -q 'ipv4_address: 172.29.0.30' ||
  fail "Monero fixed internal address changed"
echo "$monero_rendered" | sed -n '/^  monero:$/,/^  tor:$/p' | grep -q 'published:' &&
  fail "Monero publishes RPC or ZMQ directly"
echo "$monero_rendered" | sed -n '/^  monero:$/,/^  tor:$/p' | grep -q '127.0.0.1' ||
  fail "Monero DNS is not fail-closed"
custom_limits=$(COMPOSE_PROFILES=bitcoin,monero BITCOIN_STEADY_CPUS=0.5 BITCOIN_STEADY_MEM_LIMIT_MB=1536 MONERO_STEADY_CPUS=1.5 MONERO_STEADY_MEM_LIMIT_MB=3072 docker compose --env-file /dev/null config)
bitcoin_limits=$(echo "$custom_limits" | sed -n '/^  bitcoin:$/,/^  dashboard:$/p')
monero_limits=$(echo "$custom_limits" | sed -n '/^  monero:$/,/^  tor:$/p')
echo "$bitcoin_limits" | grep -q 'io.node-starter-stack.steady-cpus: "0.5"' || fail "Bitcoin cap edits do not change its Compose definition"
echo "$bitcoin_limits" | grep -q 'io.node-starter-stack.steady-memory-mb: "1536"' || fail "Bitcoin memory cap edits do not change its Compose definition"
echo "$monero_limits" | grep -q 'io.node-starter-stack.steady-cpus: "1.5"' || fail "Monero cap edits do not change its Compose definition"
echo "$monero_limits" | grep -q 'io.node-starter-stack.steady-memory-mb: "3072"' || fail "Monero memory cap edits do not change its Compose definition"
echo "$bitcoin_limits" | grep -Eq "io.node-starter-stack.resource-profile: ['\"]off['\"]" || fail "Bitcoin profile changes do not change its Compose definition"
echo "$monero_limits" | grep -Eq "io.node-starter-stack.resource-profile: ['\"]off['\"]" || fail "Monero profile changes do not change its Compose definition"
compact_limits=$(COMPOSE_PROFILES=bitcoin BITCOIN_RESOURCE_PROFILE=compact docker compose --env-file /dev/null config)
echo "$compact_limits" | sed -n '/^  bitcoin:$/,/^  dashboard:$/p' | grep -q 'io.node-starter-stack.resource-profile: compact' ||
  fail "disabling the Bitcoin compact profile would not change its Compose definition"
grep -q '^prune-blockchain=1$' build/monero/entrypoint.sh || fail "Monero pruning is not mandatory"
# shellcheck disable=SC2016
grep -q '^proxy=socks5://\$TOR_PROXY$' build/monero/entrypoint.sh || fail "Monero P2P proxy is not Tor"
# shellcheck disable=SC2016
grep -q '^tx-proxy=tor,\$TOR_PROXY,disable_noise$' build/monero/entrypoint.sh || fail "Monero transaction proxy is not Tor"
grep -q '^disable-dns-checkpoints=1$' build/monero/entrypoint.sh || fail "Monero DNS checkpoints remain enabled"
grep -q '^check-updates=disabled$' build/monero/entrypoint.sh || fail "Monero update egress remains enabled"
grep -q '^rpc-bind-port=18085$' build/monero/entrypoint.sh || fail "Monero internal RPC port changed"
grep -q '^rpc-restricted-bind-port=18081$' build/monero/entrypoint.sh || fail "Monero LAN RPC is not restricted"
grep -q '^rpc-restricted-bind-ip=0.0.0.0$' build/monero/entrypoint.sh || fail "Monero restricted RPC is not reachable by the Tor relay"
grep -q '^restricted-rpc=1$' build/monero/entrypoint.sh && fail "Monero globally restricts the dashboard RPC"
echo "$monero_rendered" | sed -n '/^  dashboard:$/,/^  monero:$/p' | grep -q 'MONERO_RPC_URL: http://172.29.0.30:18085' ||
  fail "dashboard is not wired to Monero internal RPC"
echo "$monero_rendered" | sed -n '/^  dashboard:$/,/^  monero-wallet:$/p' | grep -q 'MONERO_WALLET_RPC_URL: http://172.29.0.31:18082' ||
  fail "dashboard is not wired to Monero wallet RPC"
grep -q '^wallet-dir=/wallets$' build/monero/wallet-entrypoint.sh || fail "Monero wallet files are not in the private volume"
grep -q '^shared-ringdb-dir=/wallets/.shared-ringdb$' build/monero/wallet-entrypoint.sh || fail "Monero shared ring database is not in the private volume"
grep -q '^daemon-address=http://172.29.0.30:18085$' build/monero/wallet-entrypoint.sh || fail "Monero wallet RPC does not use the internal daemon"
grep -q '^rpc-bind-port=18082$' build/monero/wallet-entrypoint.sh || fail "Monero wallet RPC port changed"
grep -q 'chmod 0700 /wallets' build/monero/Dockerfile || fail "Monero wallet volume is not owner-only"
echo "$monero_rendered" | sed -n '/^  dashboard:$/,/^  monero:$/p' | grep -q 'target: /data-monero' ||
  fail "dashboard is missing the read-only Monero data mount"
echo "$monero_rendered" | grep -A4 'target: /data-monero' | grep -q 'read_only: true' ||
  fail "dashboard Monero data mount is writable"

monero_lan=$(COMPOSE_PROFILES=bitcoin,monero MONERO_ENABLED=1 MONERO_RPC_EXPOSE=1 MONERO_ZMQ_EXPOSE=1 \
  MONERO_RPC_BIND_IP=192.168.1.118 MONERO_RPC_PORT=28081 MONERO_ZMQ_BIND_IP=192.168.1.118 MONERO_ZMQ_PORT=28083 \
  COMPOSE_FILE=docker-compose.yml:docker-compose.monero-rpc.yml:docker-compose.monero-zmq.yml \
  docker compose --env-file /dev/null config)
echo "$monero_lan" | grep -A5 'target: 18081' | grep -q 'published: "28081"' || fail "Monero RPC LAN relay missing"
echo "$monero_lan" | grep -A5 'target: 18083' | grep -q 'published: "28083"' || fail "Monero ZMQ LAN relay missing"
echo "$monero_lan" | sed -n '/^  dashboard:$/,/^  monero:$/p' | grep -q 'MONERO_RPC_PORT: "28081"' || fail "dashboard did not receive Monero RPC port"
echo "$monero_lan" | sed -n '/^  dashboard:$/,/^  monero:$/p' | grep -q 'MONERO_ZMQ_PORT: "28083"' || fail "dashboard did not receive Monero ZMQ port"
[ "$(echo "$monero_lan" | grep -c 'published:' || true)" = "3" ] || fail "Monero overrides published an unexpected port"

all_nodes=$(COMPOSE_PROFILES=bitcoin,monero,tari,litecoin,dogecoin,bitcoincash,zcash TARI_ENABLED=1 TARI_WALLET_ENABLED=1 LITECOIN_ENABLED=1 DOGECOIN_ENABLED=1 BITCOINCASH_ENABLED=1 ZCASH_ENABLED=1 docker compose --env-file /dev/null -f docker-compose.yml -f docker-compose.tari-wallet.yml config)
for service in tari litecoin dogecoin bitcoincash zcash; do
  echo "$all_nodes" | grep -q "^  $service:" || fail "$service profile did not render"
  echo "$all_nodes" | sed -n "/^  $service:$/,/^  [a-z]/p" | grep -q '127.0.0.1' || fail "$service DNS is not fail-closed"
  echo "$all_nodes" | sed -n "/^  $service:$/,/^  [a-z]/p" | grep -q 'published:' && fail "$service publishes a port by default"
done
grep -qx 'proxy_bypass_for_outbound_tcp = false' build/tari/config.toml || fail "Tari can dial clearnet peers without Tor"
grep -qx 'proxy_bypass_addresses = \[\]' build/tari/config.toml || fail "Tari bypasses Tor for listed addresses"
tari_wallet_rendered=$(echo "$all_nodes" | sed -n '/^  tari-wallet:$/,/^  [a-z][a-z-]*:$/p')
echo "$tari_wallet_rendered" | grep -q 'ipv4_address: 172.29.0.37' || fail "Tari wallet fixed internal address changed"
echo "$tari_wallet_rendered" | grep -q 'read_only: true' || fail "Tari wallet root filesystem is writable"
echo "$tari_wallet_rendered" | grep -q 'source: tari_wallet' || fail "Tari wallet control volume is not persistent"
echo "$tari_wallet_rendered" | grep -q 'source: tari_wallet_data' || fail "Tari wallet private data volume is not persistent"
echo "$tari_wallet_rendered" | grep -q '127.0.0.1' || fail "Tari wallet DNS is not fail-closed"
echo "$tari_wallet_rendered" | grep -q 'published:' && fail "Tari wallet gRPC is published to the host"
echo "$all_nodes" | sed -n '/^  dashboard:$/,/^  dogecoin:$/p' | grep -q 'TARI_WALLET_GRPC_URL: http://172.29.0.37:18143' || fail "dashboard is not wired to Tari wallet gRPC"
dashboard_nodes=$(echo "$all_nodes" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p')
echo "$dashboard_nodes" | grep -B2 -A2 'target: /tari-wallet' | grep -q 'source: tari_wallet' || fail "dashboard cannot write Tari wallet control requests"
echo "$dashboard_nodes" | grep -q 'source: tari_wallet_data' && fail "dashboard can read Tari wallet private data"
echo "$dashboard_nodes" | grep -q 'TARI_WALLET_ENABLED: "1"' || fail "dashboard did not enable Tari wallet integration"
grep -q '^onlynet=onion$' build/bitcoin-family/entrypoint.sh || fail "Litecoin, Dogecoin and Bitcoin Cash are not Tor-only"
echo "$all_nodes" | sed -n '/^  bitcoincash:$/,/^  dashboard:$/p' | grep -q '^    command:' && fail "Bitcoin Cash command bypasses its private runtime config"

echo "$all_nodes" | grep -q 'build/zcash/zakurad.toml' || fail "Zcash default config file changed"
echo "$all_nodes" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'ZCASH_ADDRESS_INDEX: "0"' || fail "dashboard did not default Zcash address index off"
index_nodes=$(COMPOSE_PROFILES=zcash ZCASH_ENABLED=1 ZCASH_ADDRESS_INDEX=1 ZCASH_TOML=zakurad-address-index.toml docker compose --env-file /dev/null -f docker-compose.yml config)
echo "$index_nodes" | grep -q 'build/zcash/zakurad-address-index.toml' || fail "Zcash address index config file was not mounted"
echo "$index_nodes" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'ZCASH_ADDRESS_INDEX: "1"' || fail "dashboard did not receive Zcash address index flag"

new_lan=$(TARI_GRPC_EXPOSE=1 TARI_GRPC_BIND_IP=192.168.1.10 TARI_GRPC_PORT=28142 \
  LITECOIN_RPC_EXPOSE=1 LITECOIN_RPC_BIND_IP=192.168.1.10 LITECOIN_RPC_PORT=29332 \
  DOGECOIN_RPC_EXPOSE=1 DOGECOIN_RPC_BIND_IP=192.168.1.10 DOGECOIN_RPC_PORT=32555 \
  BITCOINCASH_RPC_EXPOSE=1 BITCOINCASH_RPC_BIND_IP=192.168.1.10 BITCOINCASH_RPC_PORT=28332 \
  ZCASH_RPC_EXPOSE=1 ZCASH_RPC_BIND_IP=192.168.1.10 ZCASH_RPC_PORT=28232 \
  COMPOSE_FILE=docker-compose.yml:docker-compose.tari-grpc.yml:docker-compose.litecoin-rpc.yml:docker-compose.dogecoin-rpc.yml:docker-compose.bitcoincash-rpc.yml:docker-compose.zcash-rpc.yml \
  docker compose --env-file /dev/null config)
for port in 28142 29332 32555 28332 28232; do
  echo "$new_lan" | grep -q "published: \"$port\"" || fail "new node LAN listener $port was not rendered"
done
[ "$(echo "$new_lan" | grep -c 'published:' || true)" = 6 ] || fail "new node overrides published an unexpected port"
echo "$new_lan" | sed -n '/^  zcash:$/,/^  [a-z]/p' | grep -q 'published:' && fail "Zcash native RPC is published instead of the dashboard proxy"

# Without credentials the stack refuses to start
if env -u BITCOIN_RPC_USER -u BITCOIN_RPC_PASSWORD docker compose --env-file /dev/null config >/dev/null 2>&1; then
  fail "compose config succeeded without credentials"
fi
if env -u STACK_VERSION docker compose --env-file /dev/null config >/dev/null 2>&1; then
  fail "compose config silently selected an unversioned project image"
fi

# The dashboard mounts the chain read-only and gets a separate writable volume
# for watch-only wallet state (so the chain datadir is never writable from it)
echo "$rendered" | grep -q 'target: /data' || fail "dashboard missing /data mount"
echo "$rendered" | grep -q 'target: /state' || fail "dashboard missing writable /state volume"

echo "PASS: test_compose.sh"
