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
echo "$rendered" | grep -q 'TOR_INBOUND: "1"' || fail "inbound Tor connections do not default on"
echo "$rendered" | grep -q 'TOR_CLEARNET: "1"' || fail "clearnet peers over Tor do not default on"
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
# Every outbound connection goes through Tor; onion-only is the opt-out.
echo "$rendered" | grep -qF 'NET_ARGS="-proxy=172.29.0.25:9050 -onion=172.29.0.25:9050"' ||
  fail "Tor routing missing from the entrypoint"
# shellcheck disable=SC2016 # matching the literal $$-escaped text is the point
echo "$rendered" | grep -qF 'if [ "$$TOR_CLEARNET" != "1" ]; then NET_ARGS="$$NET_ARGS -onlynet=onion"; fi' ||
  fail "onion-only opt-out missing from the entrypoint"
echo "$rendered" | grep -q 'SYNC_OVER_CLEARNET' && fail "clearnet sync bypass remains in compose"

# inbound-onion branch, pinned verbatim: without the container-address onion
# bind tor forwards inbound peers to its own loopback, and bitcoind must never
# reach tor's control port.
# shellcheck disable=SC2016 # matching the literal $$-escaped text is the point
echo "$rendered" | grep -qF -- 'if [ "$$TOR_INBOUND" = "1" ]; then ONION_ARGS="-listen=1 -listenonion=0 -bind=172.29.0.26:8334=onion"; fi' ||
  fail "inbound onion branch line changed — check the inbound onion-service flags in the entrypoint"
# shellcheck disable=SC2016 # matching the literal $$-escaped text is the point
echo "$rendered" | grep -qF -- "grep -Eqx '[a-z2-7]{56}\\.onion' /var/lib/tor-onions/bitcoin; then ONION_ARGS=\"\$\$ONION_ARGS -externalip=\$\$(cat /var/lib/tor-onions/bitcoin)\"" ||
  fail "bitcoind does not advertise a validated onion address from Tor"
echo "$rendered" | grep -q 'torcontrol' && fail "bitcoind still uses tor's control port"
grep -qi '^ControlPort' build/tor/torrc && fail "Tor still opens a control port"
grep -q 'control_address' build/tari/config.toml && fail "Tari points at a Tor control port"
grep -q 'control_address' build/tari-wallet/config.toml && fail "Tari wallet points at a Tor control port"
for service in "8333 172.29.0.26:8334" "9333 172.29.0.33:9334" "8333 172.29.0.35:8334"; do
  grep -qF "service=\"$service\"" build/tor/entrypoint.sh || fail "Tor does not publish the onion service $service"
done

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
all_nodes_json=$(COMPOSE_PROFILES=bitcoin,monero,tari,litecoin,dogecoin,bitcoincash,zcash TARI_ENABLED=1 TARI_WALLET_ENABLED=1 LITECOIN_ENABLED=1 DOGECOIN_ENABLED=1 BITCOINCASH_ENABLED=1 ZCASH_ENABLED=1 docker compose --env-file /dev/null -f docker-compose.yml -f docker-compose.tari-wallet.yml config --format json)
# Only Tor may reach the outside: every other service is internal-only, with loopback DNS and no published port.
echo "$all_nodes_json" | jq -e '[.services | to_entries[] | select(.key != "tor") | .value.networks | keys] | length == 10 and all(. == ["btc_net"])' >/dev/null ||
  fail "a service other than Tor joins a network with an external route"
echo "$all_nodes_json" | jq -e '[.services | to_entries[] | select(.key != "tor") | .value.dns] | all(. == ["127.0.0.1"])' >/dev/null ||
  fail "a service other than Tor can resolve DNS directly"
echo "$all_nodes_json" | jq -e '[.services | to_entries[] | select(.key != "tor") | .value.ports // []] | all(length == 0)' >/dev/null ||
  fail "a service other than Tor publishes a host port"
echo "$all_nodes_json" | jq -e '.networks.btc_net.internal == true' >/dev/null || fail "btc_net is not internal"
echo "$all_nodes_json" | jq -e '[.services | to_entries[] | select(.key != "tor") | .value.volumes // [] | .[] | select(.source == "tor_data")] | length == 0' >/dev/null ||
  fail "a service other than Tor can read Tor's keys"
echo "$all_nodes_json" | jq -e '[.services[] | .group_add // []] | all(length == 0)' >/dev/null || fail "a service joins another container's group"
echo "$all_nodes_json" | jq -e '[.services | to_entries[] | select(.key != "tor") | .value.volumes // [] | .[] | select(.source == "tor_onions") | .read_only] | length == 3 and all(. == true)' >/dev/null ||
  fail "onion hostnames are not shared read-only with exactly the three inbound nodes"
echo "$all_nodes" | sed -n '/^  tor:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'TOR_ONION_NODES' || fail "Tor does not receive the inbound onion node list"
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
# shellcheck disable=SC2016 # matching the literal variable reference is the point
grep -q '^proxy=$TOR_PROXY$' build/bitcoin-family/entrypoint.sh || fail "Litecoin, Dogecoin and Bitcoin Cash do not route through Tor"
grep -qF "printf 'onlynet=onion" build/bitcoin-family/entrypoint.sh || fail "family nodes lost the onion-only opt-out"
for service in litecoin:172.29.0.33:9334 bitcoincash:172.29.0.35:8334; do
  rendered_service=$(echo "$all_nodes" | sed -n "/^  ${service%%:*}:$/,/^  [a-z][a-z-]*:$/p")
  echo "$rendered_service" | grep -q "ONION_BIND: ${service#*:}" || fail "${service%%:*} onion target is not its container address"
  echo "$rendered_service" | grep -q 'TOR_INBOUND: "1"' || fail "${service%%:*} inbound Tor connections do not default on"
  echo "$rendered_service" | grep -q "ONION_HOSTNAME: /var/lib/tor-onions/${service%%:*}" || fail "${service%%:*} does not read its own onion hostname"
done
echo "$all_nodes" | sed -n '/^  dogecoin:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'TOR_INBOUND' && fail "Dogecoin cannot publish a v3 onion service"
echo "$all_nodes" | sed -n '/^  dogecoin:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'SEED_DOH_HOSTS: seed.multidoge.org seed2.multidoge.org' ||
  fail "Dogecoin does not look up its seed peers"
for service in litecoin bitcoincash; do
  echo "$all_nodes" | sed -n "/^  $service:\$/,/^  [a-z][a-z-]*:\$/p" | grep -q 'SEED_DOH_HOSTS' && fail "$service looks up seed peers it does not need"
done
grep -qx 'type = "__TRANSPORT__"' build/tari/config.toml || fail "Tari transport is not chosen at runtime"
echo "$all_nodes" | sed -n '/^  bitcoincash:$/,/^  dashboard:$/p' | grep -q '^    command:' && fail "Bitcoin Cash command bypasses its private runtime config"

echo "$all_nodes" | grep -q 'build/zcash/zakurad.toml' || fail "Zcash default config file changed"
echo "$all_nodes" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'ZCASH_ADDRESS_INDEX: "0"' || fail "dashboard did not default Zcash address index off"
index_nodes=$(COMPOSE_PROFILES=zcash ZCASH_ENABLED=1 ZCASH_ADDRESS_INDEX=1 ZCASH_TOML=zakurad-address-index.toml docker compose --env-file /dev/null -f docker-compose.yml config)
echo "$index_nodes" | grep -q 'build/zcash/zakurad-address-index.toml' || fail "Zcash address index config file was not mounted"
echo "$index_nodes" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'ZCASH_ADDRESS_INDEX: "1"' || fail "dashboard did not receive Zcash address index flag"

echo "$all_nodes" | sed -n '/^  dogecoin:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'DOGECOIN_WATCH_WALLET: "0"' || fail "Dogecoin watch wallet did not default off"
echo "$all_nodes" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'DOGECOIN_WATCH_WALLET: "0"' || fail "dashboard did not default the Dogecoin watch wallet off"
doge_nodes=$(COMPOSE_PROFILES=dogecoin DOGECOIN_ENABLED=1 DOGECOIN_PRUNE=0 DOGECOIN_WATCH_WALLET=1 DOGECOIN_RPC_USER=u DOGECOIN_RPC_PASSWORD=p docker compose --env-file /dev/null -f docker-compose.yml config)
echo "$doge_nodes" | sed -n '/^  dogecoin:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'DOGECOIN_WATCH_WALLET: "1"' || fail "Dogecoin node did not receive the watch wallet flag"
echo "$doge_nodes" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'DOGECOIN_WATCH_WALLET: "1"' || fail "dashboard did not receive the Dogecoin watch wallet flag"

entry_dir=$(mktemp -d)
cat >"$entry_dir/dogecoind" <<'STUB'
#!/bin/sh
cat "${2#-conf=}"
STUB
chmod +x "$entry_dir/dogecoind"
entry_conf() { env -i PATH="$entry_dir:$PATH" NODE_BIN=dogecoin NODE_ENV_PREFIX=DOGECOIN TOR_PROXY=t:1 DOGECOIN_RPC_USER=u DOGECOIN_RPC_PASSWORD=p DOGECOIN_PRUNE="$1" "${@:2}" sh build/bitcoin-family/entrypoint.sh; }
entry_conf 10000 | grep -q '^disablewallet=1$' || fail "default Dogecoin config enabled the wallet"
entry_conf 0 DOGECOIN_WATCH_WALLET=0 | grep -q '^disablewallet=1$' || fail "watch wallet 0 enabled the wallet"
watch_conf=$(entry_conf 0 DOGECOIN_WATCH_WALLET=1)
echo "$watch_conf" | grep -q 'disablewallet' && fail "watch wallet left disablewallet in the config"
echo "$watch_conf" | grep -q '^prune=0$' || fail "watch wallet config lost its prune setting"
echo "$watch_conf" | grep -q '^proxy=t:1$' || fail "watch wallet config lost Tor routing"
default_conf=$(entry_conf 0)
echo "$default_conf" | grep -q '^dnsseed=1$' || fail "family nodes do not reach clearnet peers through Tor by default"
echo "$default_conf" | grep -q 'onlynet' && fail "family nodes are still onion-only by default"
echo "$default_conf" | grep -q '^listen=0$' || fail "family nodes listen without inbound Tor connections"
onion_conf=$(entry_conf 0 TOR_CLEARNET=0)
echo "$onion_conf" | grep -q '^onlynet=onion$' || fail "onion-only opt-out does not restrict peers"
echo "$onion_conf" | grep -q '^dnsseed=0$' || fail "onion-only opt-out still queries DNS seeds"
entry_conf 0 TOR_INBOUND=1 | grep -q '^listen=0$' || fail "a node without an onion target listened"
echo abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz2345.onion >"$entry_dir/onion"
inbound_conf=$(entry_conf 0 TOR_INBOUND=1 ONION_BIND=10.0.0.5:9334 ONION_HOSTNAME="$entry_dir/onion")
for line in listen=1 listenonion=0 bind=127.0.0.1 bind=10.0.0.5:9334=onion externalip=abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz2345.onion; do
  echo "$inbound_conf" | grep -qx "$line" || fail "inbound config lacks $line"
done
echo "$inbound_conf" | grep -q 'torcontrol' && fail "family nodes still use tor's control port"
printf 'evil.onion\nrpcallowip=0.0.0.0/0\n' >"$entry_dir/onion"
entry_conf 0 TOR_INBOUND=1 ONION_BIND=10.0.0.5:9334 ONION_HOSTNAME="$entry_dir/onion" 2>/dev/null | grep -Eq '^(externalip|rpcallowip=0)' &&
  fail "family nodes trusted a malformed onion hostname"
[ "$(entry_conf 10000 | grep -vc '^disablewallet=1$')" = "$(echo "$watch_conf" | grep -c '')" ] || fail "watch wallet changed more than disablewallet"

doh_dir=$(mktemp -d)
cat >"$doh_dir/curl" <<'STUB'
#!/bin/sh
for arg in "$@"; do printf '[%s]' "$arg"; done >>"$DOH_LOG"
echo >>"$DOH_LOG"
proxied=0 noproxy=0 prev=
for arg in "$@"; do
  [ "$prev" = --socks5-hostname ] && [ "$arg" = t:1 ] && proxied=1
  [ "$prev" = --noproxy ] && [ -z "$arg" ] && noproxy=1
  prev=$arg
done
[ "$proxied$noproxy" = 11 ] || exit 7
[ "${DOH_FAIL:-0}" = 1 ] && exit 28
printf '{"Status":0,"Answer":[%s]}' "$DOH_ANSWER"
STUB
printf '#!/bin/sh\n' >"$doh_dir/sleep"
chmod +x "$doh_dir/curl" "$doh_dir/sleep"
answer() { for ip in "$@"; do printf '{"name":"seed.example","type":1,"data":"%s"},' "$ip"; done | sed 's/,$//'; }
good_ips="1.2.3.4 5.6.7.8 9.9.9.9 11.22.33.44 45.67.89.10 50.60.70.80 81.82.83.84 91.92.93.94 101.102.103.104 200.201.202.203"
bad_ips="999.1.1.1 256.0.0.1 1.2.3 10.0.0.5 127.0.0.1 192.168.1.2 172.16.0.1 172.31.255.255 100.64.0.1 100.127.0.1 169.254.1.1 224.0.0.1 255.255.255.255 0.0.0.0 192.0.2.1 192.0.0.8 198.18.0.1 198.19.255.1 198.51.100.7 203.0.113.9 2001:db8::1 8.8.4.4\\nrpcallowip=0.0.0.0/0"
doh_conf() {
  data=$1
  shift
  [ -n "$data" ] || data=$(mktemp -d "$doh_dir/data.XXXXXX")
  entry_conf 0 SEED_DOH_HOSTS="seed.example seed2.example" SEED_PORT=22556 DOH_LOG="$doh_dir/log" NODE_DATA_DIR="$data" \
    PATH="$doh_dir:$entry_dir:$PATH" "$@" 2>/dev/null
}
mkdir "$doh_dir/kept" "$doh_dir/failed"
: >"$doh_dir/log"
# shellcheck disable=SC2086
seeded_conf=$(doh_conf "$doh_dir/kept" DOH_ANSWER="$(answer $good_ips $bad_ips)")
echo "$seeded_conf" | grep -q '^dnsseed=0$' || fail "Dogecoin still dials seeders Tor cannot resolve"
[ "$(echo "$seeded_conf" | grep -c '^addnode=')" = 8 ] || fail "Dogecoin did not add 8 seed peers"
echo "$seeded_conf" | grep '^addnode=' | grep -vEx "addnode=($(echo "$good_ips" | sed 's/\./\\./g; s/ /|/g')):22556" &&
  fail "Dogecoin added a seed peer the resolver did not return as a valid public address"
echo "$seeded_conf" | grep -q 'rpcallowip=0' && fail "a seed answer injected a config line"
[ "$(grep -c '' "$doh_dir/log")" = 2 ] || fail "seed lookup did not query each seeder once"
grep -vF '[-q][-s][--noproxy][][--proto][=https][--max-time][20][--socks5-hostname][t:1]' "$doh_dir/log" | grep -q . &&
  fail "a seed lookup could leave the Tor proxy, HTTPS or the time limit"
grep -qF '[https://cloudflare-dns.com/dns-query?name=seed2.example&type=A]' "$doh_dir/log" || fail "seed lookup used an unexpected resolver"
[ -e "$doh_dir/kept/.seed-lookup-done" ] || fail "a successful seed lookup was not recorded"
: >"$doh_dir/log"
again_conf=$(doh_conf "$doh_dir/kept" DOH_ANSWER="$(answer 1.2.3.4)")
[ -s "$doh_dir/log" ] && fail "Dogecoin looked up seed peers again after a successful lookup"
echo "$again_conf" | grep -q '^dnsseed=0$' || fail "a later start re-enabled the dead seeders"
echo "$again_conf" | grep -q '^addnode=' && fail "a later start added seed peers without a lookup"
: >"$doh_dir/log"
failed_conf=$(doh_conf "$doh_dir/failed" DOH_FAIL=1 DOH_ANSWER=)
echo "$failed_conf" | grep -q '^dnsseed=0$' || fail "a failed seed lookup re-enabled the dead seeders"
echo "$failed_conf" | grep -q '^addnode=' && fail "a failed seed lookup still added peers"
[ "$(grep -c '' "$doh_dir/log")" = 6 ] || fail "a failed seed lookup did not retry both seeders three times"
[ -e "$doh_dir/failed/.seed-lookup-done" ] && fail "a failed seed lookup was recorded, so it would never be retried"
for ip in $bad_ips; do
  doh_conf "" DOH_ANSWER="$(answer "$ip")" | grep -q '^addnode=' && fail "Dogecoin accepted the seed answer $ip"
done
for ip in 1.0.0.1 172.15.0.1 172.32.0.1 100.63.255.255 100.128.0.1 192.0.1.1 198.17.0.1 198.20.0.1 223.255.255.255; do
  [ "$(doh_conf "" DOH_ANSWER="$(answer "$ip")" | grep '^addnode=')" = "addnode=$ip:22556" ] || fail "Dogecoin rejected the public seed answer $ip"
done
: >"$doh_dir/log"
doh_conf "" TOR_CLEARNET=0 DOH_ANSWER="$(answer 1.2.3.4)" | grep -q '^onlynet=onion$' || fail "onion-only Dogecoin lost its restriction"
[ -s "$doh_dir/log" ] && fail "onion-only Dogecoin looked up clearnet seed peers"
entry_conf 0 DOH_LOG="$doh_dir/log" PATH="$doh_dir:$entry_dir:$PATH" | grep -q '^dnsseed=1$' || fail "nodes without seed hosts lost DNS seeding"
[ -s "$doh_dir/log" ] && fail "a node without seed hosts looked up seed peers"
echo "$all_nodes" | sed -n '/^  dogecoin:$/,/^  [a-z][a-z-]*:$/p' | grep -q '^    init: true$' || fail "Dogecoin cannot be stopped promptly during its seed lookup"
rm -rf "$doh_dir" "$entry_dir"

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

base_render=$(docker compose --env-file /dev/null -f docker-compose.yml config)
echo "$base_render" | grep -q 'utxo' && fail "default compose mentions the UTXO index"
echo "$base_render" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p' | grep -Eq 'mem_limit|cpus:' && fail "default dashboard gained a resource limit"
utxo_render=$(docker compose --env-file /dev/null -f docker-compose.yml -f docker-compose.utxo-index.yml config)
utxo_dashboard=$(echo "$utxo_render" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p')
echo "$utxo_dashboard" | grep -q 'UTXO_INDEX_ENABLED: "1"' || fail "UTXO index flag did not reach the dashboard"
echo "$utxo_dashboard" | grep -q 'UTXO_INDEX_DIR: /utxo-index' || fail "UTXO index directory did not reach the dashboard"
echo "$utxo_dashboard" | grep -q 'mem_limit: "1073741824"' || fail "UTXO index overlay did not set the dashboard memory limit"
echo "$utxo_dashboard" | grep -q 'cpus: 0.5' || fail "UTXO index overlay did not set the dashboard CPU limit"
echo "$utxo_dashboard" | grep -B2 -A2 'target: /utxo-index' | grep -q 'source: utxo_index' || fail "dashboard is missing the utxo_index volume"
echo "$utxo_render" | sed -n '/^  bitcoin:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'utxo' && fail "UTXO index overlay changed the bitcoin service"
diff <(echo "$base_render" | grep -v '^  dashboard:$') <(echo "$utxo_render" | grep -v '^  dashboard:$') | grep '^<' | grep -q . && fail "UTXO index overlay removed existing configuration"
for key in networks ports extra_hosts dns; do
  base_count=$(echo "$base_render" | grep -Ec "^ +$key:" || true)
  utxo_count=$(echo "$utxo_render" | grep -Ec "^ +$key:" || true)
  [ "$base_count" = "$utxo_count" ] || fail "UTXO index overlay changed $key settings"
done

ltc_render=$(docker compose --env-file /dev/null -f docker-compose.yml -f docker-compose.litecoin-utxo-index.yml config)
ltc_dashboard=$(echo "$ltc_render" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p')
echo "$ltc_dashboard" | grep -q 'LITECOIN_UTXO_INDEX_ENABLED: "1"' || fail "Litecoin index flag did not reach the dashboard"
echo "$ltc_dashboard" | grep -q 'LITECOIN_UTXO_INDEX_DIR: /utxo-index-litecoin' || fail "Litecoin index directory did not reach the dashboard"
echo "$ltc_dashboard" | grep -q 'mem_limit: "1073741824"' || fail "Litecoin index overlay did not set the dashboard memory limit"
echo "$ltc_dashboard" | grep -q 'cpus: 0.5' || fail "Litecoin index overlay did not set the dashboard CPU limit"
echo "$ltc_dashboard" | grep -B2 -A2 'target: /utxo-index-litecoin' | grep -q 'source: utxo_index_litecoin' || fail "dashboard is missing the utxo_index_litecoin volume"
echo "$ltc_render" | sed -n '/^  litecoin:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'utxo' && fail "Litecoin index overlay changed the litecoin service"

bch_render=$(docker compose --env-file /dev/null -f docker-compose.yml -f docker-compose.bitcoincash-utxo-index.yml config)
bch_dashboard=$(echo "$bch_render" | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p')
echo "$bch_dashboard" | grep -q 'BITCOINCASH_UTXO_INDEX_ENABLED: "1"' || fail "Bitcoin Cash index flag did not reach the dashboard"
echo "$bch_dashboard" | grep -q 'BITCOINCASH_UTXO_INDEX_DIR: /utxo-index-bitcoincash' || fail "Bitcoin Cash index directory did not reach the dashboard"
echo "$bch_dashboard" | grep -q 'mem_limit: "2147483648"' || fail "Bitcoin Cash index overlay did not set the dashboard memory limit to 2g"
echo "$bch_dashboard" | grep -q 'GOMEMLIMIT: 1600MiB' || fail "Bitcoin Cash index overlay did not set GOMEMLIMIT"
echo "$bch_dashboard" | grep -q 'cpus: 0.5' || fail "Bitcoin Cash index overlay did not set the dashboard CPU limit"
echo "$bch_dashboard" | grep -B2 -A2 'target: /utxo-index-bitcoincash' | grep -q 'source: utxo_index_bitcoincash' || fail "dashboard is missing the utxo_index_bitcoincash volume"
echo "$bch_render" | sed -n '/^  bitcoincash:$/,/^  [a-z][a-z-]*:$/p' | grep -q 'utxo' && fail "Bitcoin Cash index overlay changed the bitcoincash service"

# The dashboard's effective limits for every index combination, rendered in the order configure.sh writes COMPOSE_FILE.
for combo in none btc ltc bch btc+bch ltc+bch btc+ltc+bch; do
  dir=$(mktemp -d)
  cp configure.sh "$dir/"
  config='{"dashboard":{"password":"composetest12"}'
  case "+$combo+" in *+btc+*) config+=',"bitcoin":{"utxo_index":true}' ;; esac
  case "+$combo+" in *+ltc+*) config+=',"litecoin":{"enabled":true,"utxo_index":true}' ;; esac
  case "+$combo+" in *+bch+*) config+=',"bitcoincash":{"enabled":true,"prune_mb":0,"utxo_index":true}' ;; esac
  printf '%s}\n' "$config" >"$dir/config.json"
  (cd "$dir" && ./configure.sh >/dev/null) || fail "configure.sh failed for the $combo index combination"
  files=()
  IFS=: read -r -a order <<<"$(sed -n 's/^COMPOSE_FILE=//p' "$dir/.env")"
  for file in "${order[@]}"; do files+=(-f "$file"); done
  dashboard=$(docker compose --env-file "$dir/.env" "${files[@]}" config | sed -n '/^  dashboard:$/,/^  [a-z][a-z-]*:$/p')
  rm -rf "$dir"
  if [[ "+$combo+" == *+bch+* ]]; then
    echo "$dashboard" | grep -q 'mem_limit: "2147483648"' || fail "with the $combo indexes the dashboard is not capped at 2g"
    echo "$dashboard" | grep -q 'GOMEMLIMIT: 1600MiB' || fail "with the $combo indexes the dashboard has no GOMEMLIMIT"
  elif [ "$combo" = none ]; then
    echo "$dashboard" | grep -q 'mem_limit' && fail "without an index the dashboard got a memory limit"
    echo "$dashboard" | grep -q 'GOMEMLIMIT' && fail "without an index the dashboard got GOMEMLIMIT"
  else
    echo "$dashboard" | grep -q 'mem_limit: "1073741824"' || fail "with the $combo indexes the dashboard is not capped at 1g"
    echo "$dashboard" | grep -q 'GOMEMLIMIT' && fail "with the $combo indexes the dashboard got the Bitcoin Cash GOMEMLIMIT"
  fi
done

echo "PASS: test_compose.sh"
