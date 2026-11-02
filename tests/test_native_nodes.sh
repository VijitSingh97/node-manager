#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."

if [ "${RUN_NATIVE_NODE_SMOKE:-0}" != 1 ]; then
  echo "SKIP: test_native_nodes.sh (set RUN_NATIVE_NODE_SMOKE=1 and NATIVE_NODE_CHAIN=<chain>)"
  exit 0
fi

chain=${NATIVE_NODE_CHAIN:-}
case "$chain" in
  tari | litecoin | dogecoin | bitcoincash | zcash | monero) ;;
  *)
    echo "FAIL: NATIVE_NODE_CHAIN must be tari, litecoin, dogecoin, bitcoincash, zcash, or monero" >&2
    exit 1
    ;;
esac

for tool in docker jq curl openssl python3; do
  if ! command -v "$tool" >/dev/null; then
    echo "FAIL: $tool is required" >&2
    exit 1
  fi
done

subnet=${NATIVE_NODE_SUBNET:-172.31.241.0/24}
if ! subnet=$(python3 - "$subnet" <<'PY'
import ipaddress
import sys

try:
    network = ipaddress.IPv4Network(sys.argv[1], strict=True)
except ValueError:
    raise SystemExit(1)
private_ranges = tuple(ipaddress.IPv4Network(value) for value in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"))
if network.prefixlen != 24 or not any(network.subnet_of(parent) for parent in private_ranges) or network == ipaddress.IPv4Network("172.29.0.0/24"):
    raise SystemExit(1)
print(network.with_prefixlen)
PY
); then
  echo "FAIL: NATIVE_NODE_SUBNET must be a canonical RFC1918 /24 other than 172.29.0.0/24" >&2
  exit 1
fi
subnet_prefix=${subnet%.0/24}

smoke_timeout=${NATIVE_NODE_SMOKE_TIMEOUT:-300}
case "$smoke_timeout" in
  '' | *[!0-9]*) echo "FAIL: NATIVE_NODE_SMOKE_TIMEOUT must be numeric" >&2; exit 1 ;;
esac
if [ "$smoke_timeout" -lt 10 ] || [ "$smoke_timeout" -gt 900 ]; then
  echo "FAIL: NATIVE_NODE_SMOKE_TIMEOUT must be between 10 and 900 seconds" >&2
  exit 1
fi

dashboard_port=${NATIVE_NODE_DASHBOARD_PORT:-38083}
case "$chain" in
  tari) rpc_port=${NATIVE_NODE_RPC_PORT:-38142}; override=docker-compose.tari-grpc.yml ;;
  litecoin) rpc_port=${NATIVE_NODE_RPC_PORT:-39332}; override=docker-compose.litecoin-rpc.yml ;;
  dogecoin) rpc_port=${NATIVE_NODE_RPC_PORT:-39555}; override=docker-compose.dogecoin-rpc.yml ;;
  bitcoincash) rpc_port=${NATIVE_NODE_RPC_PORT:-38332}; override=docker-compose.bitcoincash-rpc.yml ;;
  zcash) rpc_port=${NATIVE_NODE_RPC_PORT:-38232}; override=docker-compose.zcash-rpc.yml ;;
  monero) rpc_port=${NATIVE_NODE_RPC_PORT:-38081}; override=docker-compose.monero-rpc.yml ;;
esac
for port in "$dashboard_port" "$rpc_port"; do
  case "$port" in
    '' | *[!0-9]*) echo "FAIL: smoke ports must be numeric" >&2; exit 1 ;;
  esac
  if [ "$port" -lt 1024 ] || [ "$port" -gt 65535 ]; then
    echo "FAIL: smoke ports must be between 1024 and 65535" >&2
    exit 1
  fi
done
if [ "$dashboard_port" = "$rpc_port" ]; then
  echo "FAIL: dashboard and RPC smoke ports must differ" >&2
  exit 1
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/node-stack-native-${chain}.XXXXXX")
project="native-${chain}-$$"
compose_file="$work/compose.json"
source_model="$work/source.json"
secret=$(openssl rand -hex 24)
dashboard_secret=$(openssl rand -hex 24)
started=0

cleanup() {
  status=$?
  teardown_failed=0
  trap - EXIT INT TERM
  if [ "$started" = 1 ]; then
    if ! docker compose -p "$project" -f "$compose_file" down -v --remove-orphans >"$work/compose-down.log" 2>&1; then
      teardown_failed=1
    fi
    if [ -f "$compose_file" ]; then
      while IFS= read -r image; do
        if ! image_id=$(docker image ls -q "$image" 2>/dev/null); then
          teardown_failed=1
        elif [ -n "$image_id" ] && ! docker image rm "$image" >/dev/null 2>&1; then
          teardown_failed=1
        fi
      done < <(jq -r --arg project "$project" '.services[].image | select(startswith($project + "-"))' "$compose_file")
    fi
    for container in "$project-tor" "$project-dashboard" "$project-$chain"; do
      if ! residual=$(docker container ls -aq --filter "name=^/${container}$" 2>/dev/null); then
        teardown_failed=1
      elif [ -n "$residual" ]; then
        teardown_failed=1
      fi
    done
    for network in "$project-internal" "$project-egress"; do
      if ! residual=$(docker network ls -q --filter "name=^${network}$" 2>/dev/null); then
        teardown_failed=1
      elif [ -n "$residual" ]; then
        teardown_failed=1
      fi
    done
    if [ -f "$compose_file" ]; then
      while IFS= read -r volume; do
        if ! residual=$(docker volume ls -q --filter "name=^${volume}$" 2>/dev/null); then
          teardown_failed=1
        elif [ -n "$residual" ]; then
          teardown_failed=1
        fi
      done < <(jq -r '.volumes[].name' "$compose_file")
      while IFS= read -r image; do
        if ! residual=$(docker image ls -q "$image" 2>/dev/null); then
          teardown_failed=1
        elif [ -n "$residual" ]; then
          teardown_failed=1
        fi
      done < <(jq -r --arg project "$project" '.services[].image | select(startswith($project + "-"))' "$compose_file")
    fi
  fi
  if [ "$teardown_failed" = 1 ]; then status=1; fi
  if [ "$status" = 0 ]; then
    case "$work" in
      "${TMPDIR:-/tmp}"/node-stack-native-*) rm -rf "$work" 2>/dev/null || true ;;
    esac
  else
    chmod 0700 "$work" 2>/dev/null || true
    echo "Failed project: $project" >&2
    echo "Failure artifacts (private; may contain credentials): $work" >&2
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$work/data"/{bitcoin,monero,tari,litecoin,dogecoin,bitcoincash,zcash} "$work/assets/tari" "$work/assets/zcash"
chmod 0777 "$work/data" "$work/data"/*
sed "s/172\\.29\\.0/$subnet_prefix/g" build/tor/entrypoint.sh >"$work/tor-entrypoint.sh"
chmod 0755 "$work/tor-entrypoint.sh"
cp build/tari/entrypoint.sh "$work/assets/tari/entrypoint.sh"
sed "s/172\\.29\\.0/$subnet_prefix/g" build/tari/config.toml >"$work/assets/tari/config.toml"
chmod 0755 "$work/assets/tari" "$work/assets/tari/entrypoint.sh"
chmod 0644 "$work/assets/tari/config.toml"
sed "s/172\\.29\\.0/$subnet_prefix/g" build/zcash/zakurad.toml >"$work/assets/zcash/zakurad.toml"
cp build/zcash/healthcheck.sh "$work/assets/zcash/healthcheck.sh"
chmod 0755 "$work/assets/zcash/healthcheck.sh"
chmod 0644 "$work/assets/zcash/zakurad.toml"

entrypoint_override=
case "$chain" in
  litecoin | dogecoin | bitcoincash)
    entrypoint_override="$work/$chain-entrypoint.sh"
    sed "s/172\\.29\\.0/$subnet_prefix/g" "build/$chain/entrypoint.sh" >"$entrypoint_override"
    chmod 0755 "$entrypoint_override"
    ;;
esac

prefix=$(printf '%s' "$chain" | tr '[:lower:]' '[:upper:]')
if [ "$chain" = bitcoincash ]; then prefix=BITCOINCASH; fi
exposure=RPC
if [ "$chain" = tari ]; then exposure=GRPC; fi

env \
  COMPOSE_FILE="docker-compose.yml:$override" COMPOSE_PROFILES="$chain" \
  STACK_VERSION=native-smoke \
  STACK_ROOT="$PWD" STACK_ASSET_ROOT="$PWD" \
  BITCOIN_ENABLED=0 BITCOIN_RPC_USER=unused BITCOIN_RPC_PASSWORD=unused \
  BITCOIN_RPCAUTH_SALT=00000000000000000000000000000000 BITCOIN_RPCAUTH_HASH=00 \
  BITCOIN_DATA_DIR="$work/data/bitcoin" MONERO_DATA_DIR="$work/data/monero" \
  TARI_DATA_DIR="$work/data/tari" LITECOIN_DATA_DIR="$work/data/litecoin" \
  DOGECOIN_DATA_DIR="$work/data/dogecoin" BITCOINCASH_DATA_DIR="$work/data/bitcoincash" \
  ZCASH_DATA_DIR="$work/data/zcash" DASHBOARD_BIND_IP=127.0.0.1 DASHBOARD_PORT="$dashboard_port" \
  DASHBOARD_PASSWORD="$dashboard_secret" MONERO_RPC_USER=unused MONERO_RPC_PASSWORD=unused \
  TARI_GRPC_URL="http://$subnet_prefix.25:18142" \
  MONERO_WALLET_RPC_USER=unused MONERO_WALLET_RPC_PASSWORD=unused MONERO_WALLET_PASSWORD=unused \
  TARI_WALLET_RPC_USER=unused TARI_WALLET_RPC_PASSWORD=unused TARI_WALLET_PASSWORD=unused \
  LITECOIN_RPC_USER=unused LITECOIN_RPC_PASSWORD=unused DOGECOIN_RPC_USER=unused DOGECOIN_RPC_PASSWORD=unused \
  BITCOINCASH_RPC_USER=unused BITCOINCASH_RPC_PASSWORD=unused ZCASH_RPC_USER=unused ZCASH_RPC_PASSWORD=unused \
  "${prefix}_ENABLED=1" "${prefix}_RPC_USER=smoke" "${prefix}_RPC_PASSWORD=$secret" \
  "${prefix}_${exposure}_EXPOSE=1" "${prefix}_${exposure}_BIND_IP=127.0.0.1" \
  "${prefix}_${exposure}_PORT=$rpc_port" \
  docker compose --env-file /dev/null --profile "$chain" config --format json >"$source_model"

jq --arg project "$project" --arg subnet "$subnet" --arg net "$subnet_prefix" \
  --arg work "$work" --arg chain "$chain" --arg entrypoint "$entrypoint_override" '
    walk(if type == "string" then gsub("172\\.29\\.0"; $net) else . end)
    | .name = $project
    | del(.services["monero-wallet"], .services["tari-wallet"])
    | .services |= with_entries(
        .value.container_name = ($project + "-" + .key)
        | if .value.build then .value.image = ($project + "-" + .key + ":smoke") else . end
        | del(.value.profiles))
    | .networks.btc_net.name = ($project + "-internal")
    | .networks.btc_net.ipam.config = [{"subnet": $subnet}]
    | .networks.tor_egress.name = ($project + "-egress")
    | .volumes |= with_entries(.value.name = ($project + "-" + .key))
    | .services.tor.volumes += [{"type":"bind","source":($work + "/tor-entrypoint.sh"),"target":"/entrypoint.sh","read_only":true,"bind":{}}]
    | if $chain == "tari" then
        .services.tari.volumes |= map(if .target == "/var/tari/config" then .source = ($work + "/assets/tari") else . end)
      elif $chain == "zcash" then
        .services.zcash.volumes |= map(
          if .target == "/etc/zakura/zakurad.toml" then .source = ($work + "/assets/zcash/zakurad.toml")
          elif .target == "/usr/local/bin/node-stack-healthcheck" then .source = ($work + "/assets/zcash/healthcheck.sh")
          else . end)
      elif ($chain == "litecoin" or $chain == "dogecoin" or $chain == "bitcoincash") then
        .services[$chain].volumes += [{"type":"bind","source":$entrypoint,"target":"/usr/local/bin/entrypoint.sh","read_only":true,"bind":{}}]
      else . end
  ' "$source_model" >"$compose_file"

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

jq -e --arg chain "$chain" '.services | (length == 3 and has("dashboard") and has("tor") and has($chain))' \
  "$compose_file" >/dev/null || fail "isolated model contains unexpected services"
! grep -q '172\.29\.0\.' "$compose_file" || fail "isolated model still references the live subnet"
jq -e --arg project "$project" --arg chain "$chain" --arg work "$work" '
  .name == $project and
  ([.services[].container_name | startswith($project + "-")] | all) and
  (.networks.btc_net.internal == true) and
  (.networks.btc_net.name == ($project + "-internal")) and
  (.networks.tor_egress.name == ($project + "-egress")) and
  ([.services[].ports[]? | .host_ip == "127.0.0.1"] | all) and
  ((.services[$chain].ports // []) | length == 0) and
  ((.services[$chain].networks | keys) == ["btc_net"]) and
  ([.services[].volumes[]? | select(.target | startswith("/data")) | .source | startswith($work + "/data/")] | all)
  ' "$compose_file" >/dev/null || fail "isolated model failed its resource boundary checks"
if [ "$chain" = tari ]; then
  jq -e --arg url "http://$subnet_prefix.25:18142" '.services.dashboard.environment.TARI_GRPC_URL == $url' \
    "$compose_file" >/dev/null || fail "Tari collector does not use the isolated Tor gRPC relay"
fi

if [ "${NATIVE_NODE_SMOKE_RENDER_ONLY:-0}" = 1 ]; then
  echo "PASS: isolated $chain native-node smoke model rendered"
  exit 0
fi

started=1
docker compose -p "$project" -f "$compose_file" up -d --build --pull missing >"$work/compose-up.log" 2>&1 ||
  fail "$chain Compose build or startup failed"

node_container="$project-$chain"
internal_network="$project-internal"
[ "$(docker inspect "$node_container" --format '{{len .NetworkSettings.Networks}}')" = 1 ] ||
  fail "$chain joined more than the isolated internal network"
[ "$(docker inspect "$node_container" --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}')" = "$internal_network" ] ||
  fail "$chain did not join the isolated internal network"
[ "$(docker network inspect "$internal_network" --format '{{.Internal}}')" = true ] ||
  fail "$chain network is not internal"

case "$chain" in
  litecoin) config_probe='test -r /tmp/litecoin.conf' ;;
  dogecoin) config_probe='test -r /tmp/dogecoin.conf' ;;
  bitcoincash) config_probe='test -r /tmp/bitcoin.conf' ;;
  monero) config_probe='test -r /tmp/monerod.conf' ;;
  tari) config_probe='test -r /var/tari/node/runtime-config.toml' ;;
  zcash) config_probe='test -r /etc/zakura/zakurad.toml' ;;
esac
config_wait=60
if [ "$smoke_timeout" -lt "$config_wait" ]; then config_wait=$smoke_timeout; fi
config_deadline=$((SECONDS + config_wait))
config_ready=0
while [ "$SECONDS" -lt "$config_deadline" ]; do
  if [ "$(docker inspect "$node_container" --format '{{.State.Status}}' 2>/dev/null || true)" = running ] &&
    docker exec "$node_container" sh -c "$config_probe" >/dev/null 2>&1; then
    config_ready=1
    break
  fi
  sleep 2
done
runtime_state() {
  docker inspect "$node_container" --format 'status={{.State.Status}} restarting={{.State.Restarting}} exit={{.State.ExitCode}}' 2>/dev/null || printf unavailable
}
[ "$config_ready" = 1 ] || fail "$chain runtime config was not readable within ${config_wait}s ($(runtime_state))"

assert_runtime_config() {
  unsafe_message=$1
  shift
  if "$@"; then return; fi
  state=$(runtime_state)
  case "$state" in
    status=running*) fail "$unsafe_message" ;;
    *) fail "$chain runtime config probe became unavailable ($state)" ;;
  esac
}

case "$chain" in
  litecoin | dogecoin | bitcoincash)
    assert_runtime_config "$chain runtime config allows non-onion discovery" \
      docker exec "$node_container" sh -c 'grep -qx "onlynet=onion" /tmp/*.conf && grep -qx "dnsseed=0" /tmp/*.conf && grep -qx "discover=0" /tmp/*.conf'
    ;;
  monero)
    # shellcheck disable=SC2016
    assert_runtime_config "Monero runtime config allows an external bypass" \
      docker exec "$node_container" sh -c 'grep -qx "proxy=socks5://$TOR_PROXY" /tmp/monerod.conf && grep -qx "tx-proxy=tor,$TOR_PROXY,disable_noise" /tmp/monerod.conf && grep -qx "disable-dns-checkpoints=1" /tmp/monerod.conf'
    ;;
  tari)
    assert_runtime_config "Tari runtime config allows a proxy bypass" \
      docker exec "$node_container" sh -c 'grep -qx "proxy_bypass_for_outbound_tcp = false" /var/tari/node/runtime-config.toml && grep -qx "proxy_bypass_addresses = \[\]" /var/tari/node/runtime-config.toml && grep -qx "pruning_horizon = 10000" /var/tari/node/runtime-config.toml'
    ;;
  zcash)
    # shellcheck disable=SC2016
    assert_runtime_config "Zcash runtime config contains a direct external peer" \
      docker exec -e SMOKE_TOR_IP="$subnet_prefix.25" "$node_container" sh -c '
        peers=$(sed -n "/^initial_mainnet_peers = \[/,/^\]/p" /etc/zakura/zakurad.toml)
        [ "$(printf "%s\n" "$peers" | grep -c "^[[:space:]]*\"")" = 4 ] || exit 1
        for port in 18233 18234 18235 18236; do
          printf "%s\n" "$peers" | grep -Fq "\"$SMOKE_TOR_IP:$port\"" || exit 1
        done
      '
    ;;
esac

dashboard_url="http://127.0.0.1:$dashboard_port/api/$chain"
status_file="$work/status.json"
deadline=$((SECONDS + smoke_timeout))
while [ "$SECONDS" -lt "$deadline" ]; do
  printf 'user = "admin:%s"\nsilent\nshow-error\nmax-time = 10\n' "$dashboard_secret" |
    curl --config - "$dashboard_url" >"$status_file" 2>/dev/null || true
  jq -e '.enabled == true and .available == true' "$status_file" >/dev/null 2>&1 && break
  sleep 5
done
jq -e '.enabled == true and .available == true' "$status_file" >/dev/null 2>&1 ||
  fail "$chain did not become available through the dashboard collector"

if [ "$chain" = monero ]; then
  jq -e '
    (.height | type == "number") and (.target_height | type == "number") and
    (.version | type == "string" and length > 0) and (.pruned | type == "boolean") and
    (.uptime_available | type == "boolean") and
    (if .uptime_available then .uptime_seconds >= 0 else .uptime_seconds == 0 end) and
    (.database_size_available | type == "boolean") and
    (if .database_size_available then .database_size_bytes > 0 else .database_size_bytes == 0 end) and
    (.mempool_txs_available | type == "boolean") and
    (if .mempool_txs_available then .mempool_txs >= 0 else .mempool_txs == 0 end)
  ' "$status_file" >/dev/null || fail "Monero collector omitted availability/zero distinctions"
else
  jq -e '
    (.supported == true) and (.height | type == "number") and (.target_height | type == "number") and
    (.version | type == "string") and (.pruned_available | type == "boolean") and
    (.uptime_available | type == "boolean") and
    (if .uptime_available then .uptime_seconds >= 0 else .uptime_seconds == 0 end) and
    (.connections_available | type == "boolean") and
    (if .connections_available then .connections >= 0 else .connections == 0 end) and
    (.database_size_available | type == "boolean") and
    (if .database_size_available then .database_size_bytes > 0 else .database_size_bytes == 0 end) and
    (.mempool_txs_available | type == "boolean") and
    (if .mempool_txs_available then .mempool_txs >= 0 else .mempool_txs == 0 end)
  ' "$status_file" >/dev/null || fail "$chain collector omitted availability/zero distinctions"
fi

case "$chain" in
  tari)
    jq -e '
      (.pruned_available | type == "boolean") and
      (if .pruned_available then .pruned == true else .pruned == false end) and
      .fees_available == false and (.fee_estimates | length) == 0 and (.fee_unit // "") == ""
    ' "$status_file" >/dev/null || fail "Tari collector returned an invalid pruning or fee contract"
    ;;
  litecoin)
    jq -e 'if .fees_available then .fee_unit == "litoshi/vB" and (.fee_estimates | length) == 3 else (.fee_estimates | length) == 0 end' "$status_file" >/dev/null ||
      fail "Litecoin collector returned an invalid fee contract"
    ;;
  dogecoin)
    jq -e 'if .fees_available then .fee_unit == "DOGE/kB" and (.fee_estimates | length) == 3 else (.fee_estimates | length) == 0 end' "$status_file" >/dev/null ||
      fail "Dogecoin collector returned an invalid fee contract"
    ;;
  bitcoincash)
    jq -e 'if .fees_available then .fee_unit == "sat/B" and (.fee_estimates | length) == 1 else (.fee_estimates | length) == 0 end' "$status_file" >/dev/null ||
      fail "Bitcoin Cash collector returned an invalid fee contract"
    ;;
  zcash)
    jq -e 'if .fees_available then .fee_unit == "zats/action" and (.fee_estimates | length) == 1 else (.fee_estimates | length) == 0 end' "$status_file" >/dev/null ||
      fail "Zcash collector returned an invalid fee contract"
    ;;
  monero)
    jq -e 'if .fees_available then (.fee_estimates_atomic_per_byte | length) > 0 else (.fee_estimates_atomic_per_byte | length) == 0 end' "$status_file" >/dev/null ||
      fail "Monero collector returned an invalid fee contract"
    ;;
esac

rpc_body='{"jsonrpc":"2.0","id":"smoke","method":"getblockchaininfo","params":[]}'
case "$chain" in
  litecoin | dogecoin | bitcoincash)
    printf 'user = "smoke:%s"\nsilent\nshow-error\nfail\nmax-time = 10\nheader = "Content-Type: application/json"\n' "$secret" |
      curl --config - --data-binary "$rpc_body" "http://127.0.0.1:$rpc_port/" | jq -e '.result.blocks >= 0 and .error == null' >/dev/null ||
      fail "$chain published JSON-RPC did not return live daemon state"
    bad_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 --user smoke:wrong \
      -H 'Content-Type: application/json' --data-binary "$rpc_body" "http://127.0.0.1:$rpc_port/" || true)
    [ "$bad_code" = 401 ] || fail "$chain published JSON-RPC accepted invalid credentials"
    ;;
  monero)
    monero_body='{"jsonrpc":"2.0","id":"smoke","method":"get_info"}'
    printf 'user = "smoke:%s"\ndigest\nsilent\nshow-error\nfail\nmax-time = 10\nheader = "Content-Type: application/json"\n' "$secret" |
      curl --config - --data-binary "$monero_body" "http://127.0.0.1:$rpc_port/json_rpc" | jq -e '.result.height >= 0 and .error == null' >/dev/null ||
      fail "Monero published restricted JSON-RPC did not return live daemon state"
    admin_body='{"jsonrpc":"2.0","id":"smoke","method":"get_connections"}'
    printf 'user = "smoke:%s"\ndigest\nsilent\nshow-error\nfail\nmax-time = 10\nheader = "Content-Type: application/json"\n' "$secret" |
      curl --config - --data-binary "$admin_body" "http://127.0.0.1:$rpc_port/json_rpc" | jq -e '.error != null' >/dev/null ||
      fail "Monero published restricted JSON-RPC allowed an administrative method"
    if curl -s --fail --max-time 10 --digest --user smoke:wrong -H 'Content-Type: application/json' --data-binary "$monero_body" \
      "http://127.0.0.1:$rpc_port/json_rpc" >/dev/null 2>&1; then
      fail "Monero published JSON-RPC accepted invalid credentials"
    fi
    ;;
  zcash)
    printf 'user = "smoke:%s"\nsilent\nshow-error\nfail\nmax-time = 10\nheader = "Content-Type: application/json"\n' "$secret" |
      curl --config - --data-binary "$rpc_body" "http://127.0.0.1:$rpc_port/" | jq -e '.result.blocks >= 0 and .error == null' >/dev/null ||
      fail "Zcash published read-only JSON-RPC did not return live daemon state"
    bad_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 --user smoke:wrong \
      -H 'Content-Type: application/json' --data-binary "$rpc_body" "http://127.0.0.1:$rpc_port/" || true)
    [ "$bad_code" = 401 ] || fail "Zcash published JSON-RPC accepted invalid credentials"
    ;;
  tari)
    python3 - "$rpc_port" <<'PY' || fail "Tari published gRPC relay is unreachable"
import socket
import sys
with socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=10):
    pass
PY
    ;;
esac

protocol=JSON-RPC
if [ "$chain" = tari ]; then protocol=gRPC; fi
echo "PASS: $chain native daemon, isolated Tor path, published $protocol, and dashboard collector"
