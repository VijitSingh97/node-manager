#!/usr/bin/env bash
# Boots a preseeded stack and verifies node auth, Tor-only wiring, onion ingress, and dashboard auth end to end.
# E2E_SYNC=1 also verifies preseeded and zero-config sync starts over real Tor; it is slow and opt-in.
# Post-sync behavior on an already-synced node is covered by tests/test_postsync.sh.
# This is the test that catches config-plumbing bugs the static contract
# test can't.
#
# Runs real containers (project-scoped names and a fixed dashboard port) and writes config.json/.env in
# the checkout, so it self-skips unless in CI or explicitly requested, and
# refuses to run where a config already exists.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -z "${CI:-}" ] && [ -z "${RUN_E2E:-}" ]; then
  echo "SKIP: test_e2e.sh (set RUN_E2E=1; boots real containers on port 80)"
  exit 0
fi

# Never clobber a real deployment: a production checkout has these files.
if [ -f config.json ] || [ -f .env ] || [ -f .approved-config.json ] || [ -f .approved.env ]; then
  echo "FAIL: runtime configuration already exists — run the e2e from a clean checkout"
  exit 1
fi

export BITCOIN_STACK_PROJECT_NAME="node-manager-e2e-$$-$RANDOM"
export COMPOSE_PROJECT_NAME="$BITCOIN_STACK_PROJECT_NAME"

fail() {
  echo "FAIL: $1"
  docker compose ps || true
  docker compose logs --tail 50 || true
  exit 1
}

service_id() {
  docker compose ps -q "$1"
}

service_inspect() {
  local service id
  service=$1
  shift
  id=$(service_id "$service")
  [ -n "$id" ] || return 1
  docker inspect "$id" "$@"
}

BITCOIN_DATA_DIR=$(mktemp -d)
MONERO_DATA_DIR=$(mktemp -d)
WALLET_DATA_DIR=$(mktemp -d)
chmod 777 "$BITCOIN_DATA_DIR" "$MONERO_DATA_DIR" "$WALLET_DATA_DIR" # nodes run as uid 1000; CI runners often aren't

# bitcoind writes as uid 1000, which the invoking user may not be able to
# delete — clean the datadir through a root container instead of rm -rf
cleanup() {
  docker compose down -v --remove-orphans >/dev/null 2>&1 || true
  docker run --rm -v "$BITCOIN_DATA_DIR":/cleanup alpine:3.24 sh -c 'rm -rf /cleanup/* /cleanup/.[!.]*' >/dev/null 2>&1 || true
  docker run --rm -v "$MONERO_DATA_DIR":/cleanup alpine:3.24 sh -c 'rm -rf /cleanup/* /cleanup/.[!.]*' >/dev/null 2>&1 || true
  docker run --rm -v "$WALLET_DATA_DIR":/cleanup alpine:3.24 sh -c 'rm -rf /cleanup/* /cleanup/.[!.]*' >/dev/null 2>&1 || true
  rmdir "$BITCOIN_DATA_DIR" 2>/dev/null || true
  rmdir "$MONERO_DATA_DIR" 2>/dev/null || true
  rmdir "$WALLET_DATA_DIR" 2>/dev/null || true
  [ -z "${dashboard_backup_test_dir:-}" ] || rm -rf "$dashboard_backup_test_dir"
  [ -z "${dashboard_backup_archive:-}" ] || rm -f "$dashboard_backup_archive"
  [ -z "${dashboard_headers:-}" ] || rm -f "$dashboard_headers"
  rm -f config.json .env .approved-config.json .approved.env # created by this test (guarded above)
}
trap cleanup EXIT

host_ip=$(python3 - <<'PY'
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.connect(("192.0.2.1", 9))
print(s.getsockname()[0])
s.close()
PY
)
test_lan_rpc=true
if [ "$(uname -s)" = Darwin ] && [ ! -S /var/run/com.docker.vmnetd.sock ]; then
  host_ip=127.0.0.1
  test_lan_rpc=false
fi
jq -n --arg bitcoin_data "$BITCOIN_DATA_DIR" --arg monero_data "$MONERO_DATA_DIR" \
  --arg ip "$host_ip" --argjson lan_rpc "$test_lan_rpc" \
  '{
    bitcoin: {
      enabled: true,
      node_username: "admin",
      node_password: "e2eBitcoinRpcPassword123456789",
      data_dir: $bitcoin_data,
      dbcache_mb: 300,
      prune_mb: 1000,
      inbound_onion: true
    },
    monero: {
      enabled: true,
      node_username: "admin",
      node_password: "e2eMoneroRpcPassword123456789",
      data_dir: $monero_data
    },
    dashboard: {
      password: "e2eDashboardSharedPassword123",
      onion: true,
      bind_ip: $ip,
      launch_at_boot: false
    }
  } |
   if $lan_rpc then
     .bitcoin.rpc_expose = true | .bitcoin.rpc_bind_ip = $ip | .bitcoin.rpc_port = 18332 |
     .monero.rpc_expose = true | .monero.rpc_bind_ip = $ip | .monero.rpc_port = 28081
   else . end' >config.json
chmod 600 config.json
./configure.sh >/dev/null
test_stack_version=$(sed -n 's/^STACK_VERSION=//p' .env)
[ -n "$test_stack_version" ] || fail "configure.sh did not render STACK_VERSION"
STACK_VERSION="$test_stack_version" docker compose build tor monero monero-wallet dashboard >/dev/null ||
  fail "local E2E images did not build"
STACK_VERSION="$test_stack_version" docker compose pull bitcoin >/dev/null ||
  fail "pinned Bitcoin image did not download"
./stack init >/dev/null || fail "preseeded first-run initialization failed"
dashboard_url="http://$host_ip:80"

# The credentials the setup generated — every later assertion uses these.
rpc_user=$(sed -n 's/^BITCOIN_RPC_USER=//p' .env)
rpc_pass=$(sed -n 's/^BITCOIN_RPC_PASSWORD=//p' .env)
monero_rpc_user=$(sed -n 's/^MONERO_RPC_USER=//p' .env)
monero_rpc_pass=$(sed -n 's/^MONERO_RPC_PASSWORD=//p' .env)
monero_wallet_rpc_user=$(sed -n 's/^MONERO_WALLET_RPC_USER=//p' .env)
monero_wallet_rpc_pass=$(sed -n 's/^MONERO_WALLET_RPC_PASSWORD=//p' .env)
if [ -z "$rpc_user" ] || [ -z "$rpc_pass" ] || [ -z "$monero_rpc_user" ] || [ -z "$monero_rpc_pass" ] ||
  [ -z "$monero_wallet_rpc_user" ] || [ -z "$monero_wallet_rpc_pass" ]; then
  fail "configure.sh did not render separate node RPC credentials into .env"
fi

wallet_rpc() {
  printf '%s' "$1" | docker compose exec -T monero-wallet curl --silent --show-error --fail --digest \
    --user "$monero_wallet_rpc_user:$monero_wallet_rpc_pass" -H 'content-type: application/json' \
    --data-binary @- http://127.0.0.1:18082/json_rpc
}

docker compose up -d --pull never --no-build --wait --wait-timeout 300 ||
  fail "stack did not reach healthy within 5 minutes"
# --wait passing also proves the cookie-based bitcoin healthcheck works
docker compose exec -T dashboard sh -c 'test -z "$(find /app -name "*.test.mjs" -o -name "*_test.go" -o -name tests -o -name parity)"' ||
  fail "dashboard image includes development-only files"

# rpcauth accepts the generated plaintext password end to end
docker compose exec -T bitcoin bitcoin-cli -rpcuser="$rpc_user" -rpcpassword="$rpc_pass" getblockchaininfo |
  grep -q '"chain"' || fail "RPC rejected the generated password against the rpcauth hash"
if [ "$test_lan_rpc" = true ]; then
  curl --noproxy '*' -fsS --user "$rpc_user:$rpc_pass" -H 'content-type: application/json' \
    --data-binary '{"jsonrpc":"1.0","id":"e2e","method":"getblockchaininfo","params":[]}' \
    "http://$host_ip:18332" | jq -e '.result.chain != null and .error == null' >/dev/null ||
    fail "LAN RPC relay did not authenticate to Core"
fi

docker compose exec -T monero /usr/local/bin/monero-healthcheck || fail "Monero authenticated health check failed"
docker compose exec -T monero-wallet /usr/local/bin/monero-wallet-healthcheck || fail "Monero wallet RPC health check failed"
wallet_ports=$(service_inspect monero-wallet --format '{{json .HostConfig.PortBindings}}')
[ "$wallet_ports" = "{}" ] || [ "$wallet_ports" = "null" ] ||
  fail "Monero wallet RPC published a host port"
monero_info=$(docker compose exec -T monero curl --silent --show-error --fail --digest \
  --user "$monero_rpc_user:$monero_rpc_pass" -H 'content-type: application/json' \
  --data-binary '{"jsonrpc":"2.0","id":"e2e","method":"get_info"}' http://127.0.0.1:18085/json_rpc) ||
  fail "Monero internal RPC rejected its generated Digest credentials"
printf '%s' "$monero_info" | jq -e '.result.restricted == false and (.result.height | type == "number") and (.result.version | length > 0) and .result.start_time > 0' >/dev/null ||
  fail "Monero internal RPC redacted node health fields"
if docker compose exec -T monero curl --silent --fail --digest --user "$monero_rpc_user:wrong" \
  -H 'content-type: application/json' --data-binary '{"jsonrpc":"2.0","id":"e2e","method":"get_info"}' \
  http://127.0.0.1:18085/json_rpc >/dev/null 2>&1; then
  fail "Monero RPC accepted an invalid password"
fi
restricted_info=$(docker compose exec -T monero curl --silent --show-error --fail --digest \
  --user "$monero_rpc_user:$monero_rpc_pass" -H 'content-type: application/json' \
  --data-binary '{"jsonrpc":"2.0","id":"e2e","method":"get_info"}' http://127.0.0.1:18081/json_rpc) ||
  fail "Monero restricted RPC rejected its generated Digest credentials"
printf '%s' "$restricted_info" | jq -e '.result.restricted == true and (.result.height | type == "number")' >/dev/null ||
  fail "Monero public RPC is not restricted"
printf '%s' "$restricted_info" | jq -e '(.result.version // "") == "" and (.result.start_time // 0) == 0 and (.result.pruning_seed // 0) == 0' >/dev/null ||
  fail "Monero restricted RPC exposed private node metadata"
restricted_admin=$(docker compose exec -T monero curl --silent --show-error --digest \
  --user "$monero_rpc_user:$monero_rpc_pass" -H 'content-type: application/json' \
  --data-binary '{"jsonrpc":"2.0","id":"e2e","method":"get_connections"}' http://127.0.0.1:18081/json_rpc) ||
  fail "Monero restricted RPC request failed before returning its denial"
printf '%s' "$restricted_admin" | jq -e '.error != null' >/dev/null ||
  fail "Monero restricted RPC allowed an administrative method"
if [ "$test_lan_rpc" = true ]; then
  lan_admin=$(curl --noproxy '*' -sS --digest --user "$monero_rpc_user:$monero_rpc_pass" \
    -H 'content-type: application/json' --data-binary '{"jsonrpc":"2.0","id":"e2e","method":"get_connections"}' \
    "http://$host_ip:28081/json_rpc") || fail "Monero LAN restricted RPC relay did not authenticate"
  printf '%s' "$lan_admin" | jq -e '.error != null' >/dev/null ||
    fail "Monero LAN restricted RPC allowed an administrative method"
fi

# ...but the plaintext password is nowhere in the bitcoin container's config
service_inspect bitcoin | grep "$rpc_pass" >/dev/null && fail "plaintext RPC password leaked into the bitcoin container"

dashboard_headers=$(mktemp)
code=$(curl --noproxy '*' -s -D "$dashboard_headers" -o /dev/null -w '%{http_code}' "$dashboard_url")
[ "$code" = "302" ] || fail "dashboard did not redirect to login (HTTP $code, expected 302)"
grep -qi '^Location: /login' "$dashboard_headers" || fail "dashboard redirect did not target /login"
! grep -qi '^WWW-Authenticate:' "$dashboard_headers" || fail "dashboard emitted a Basic auth challenge"
rm -f "$dashboard_headers"
dashboard_headers=""

# With the password, it authenticates to the node and renders live stats
body=""
for _ in 1 2 3 4 5 6; do
  body=$(curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url" || true)
  grep -q "Sync Progress" <<<"$body" && break
  sleep 5
done
grep -q "Sync Progress" <<<"$body" || fail "dashboard never rendered live stats"
grep -q "Pruned" <<<"$body" || fail "dashboard missing pruned badge on a pruned node"
dashboard_monero=$(curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url/api/monero") ||
  fail "dashboard Monero health API was unavailable"
printf '%s' "$dashboard_monero" | jq -e '.enabled == true and .available == true and (.version | length > 0) and .uptime_seconds > 0 and (.pruned | type == "boolean")' >/dev/null ||
  fail "dashboard Monero health API omitted unrestricted node fields"

wallet_fail() {
  echo "FAIL: $1"
  exit 1
}

fixture_name="_e2e_$(openssl rand -hex 6)"
fixture_password=$(openssl rand -hex 24)
create_request=$(jq -nc --arg name "$fixture_name" --arg password "$fixture_password" \
  '{jsonrpc:"2.0",id:"e2e",method:"create_wallet",params:{filename:$name,password:$password,language:"English"}}')
create_response=$(wallet_rpc "$create_request") || wallet_fail "throwaway Monero fixture wallet could not be created"
printf '%s' "$create_response" | jq -e '.error == null and .result != null' >/dev/null ||
  wallet_fail "throwaway Monero fixture wallet returned an error"
source_address=$(wallet_rpc '{"jsonrpc":"2.0","id":"e2e","method":"get_address"}' | jq -er '.result.address') ||
  wallet_fail "throwaway Monero address was unavailable"
source_view_key=$(wallet_rpc '{"jsonrpc":"2.0","id":"e2e","method":"query_key","params":{"key_type":"view_key"}}' | jq -er '.result.key') ||
  wallet_fail "throwaway Monero view key was unavailable"
wallet_rpc '{"jsonrpc":"2.0","id":"e2e","method":"close_wallet"}' >/dev/null ||
  wallet_fail "throwaway Monero fixture wallet could not be closed"
open_response=$(wallet_rpc "$(jq -nc --arg filename "$fixture_name" --arg password "$fixture_password" \
  '{jsonrpc:"2.0",id:"e2e",method:"open_wallet",params:{filename:$filename,password:$password}}')") ||
  wallet_fail "throwaway Monero fixture wallet could not be reopened"
printf '%s' "$open_response" | jq -e '.error == null and .result != null' >/dev/null ||
  wallet_fail "throwaway Monero fixture wallet reopen returned an error"
height_response=$(wallet_rpc '{"jsonrpc":"2.0","id":"e2e","method":"get_height"}') ||
  wallet_fail "throwaway Monero fixture wallet height was unavailable"
printf '%s' "$height_response" | jq -e '.error == null and (.result.height | type == "number")' >/dev/null ||
  wallet_fail "throwaway Monero fixture wallet height returned an error"
docker compose exec -T monero-wallet sh -c 'test -d /wallets/.shared-ringdb && test -w /wallets/.shared-ringdb' ||
  wallet_fail "Monero shared ring database is not writable in the private wallet volume"
wallet_rpc '{"jsonrpc":"2.0","id":"e2e","method":"close_wallet"}' >/dev/null ||
  wallet_fail "reopened Monero fixture wallet could not be closed"
docker compose exec -T -e FIXTURE_NAME="$fixture_name" monero-wallet sh -c 'rm -f "/wallets/$FIXTURE_NAME" "/wallets/$FIXTURE_NAME.keys"' ||
  wallet_fail "throwaway Monero fixture wallet could not be removed"

watch_add=$(jq -nc --arg address "$source_address" --arg view_key "$source_view_key" \
  '{name:"E2E view only",address:$address,view_key:$view_key,restore_height:0}')
watch_response=$(printf '%s' "$watch_add" | curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 \
  -H 'X-Requested-With: fetch' -H 'Content-Type: application/json' --data-binary @- \
  "$dashboard_url/api/monero/watch") || wallet_fail "real Monero watch-only API create failed"
watch_id=$(printf '%s' "$watch_response" | jq -er '.id') || wallet_fail "watch-only API omitted its wallet ID"
printf '%s' "$watch_response" | grep -Fq "$source_view_key" && wallet_fail "watch-only create response disclosed the private view key"

for forbidden in seed spend_key; do
  unsafe=$(printf '%s' "$watch_add" | jq -c --arg field "$forbidden" '. + {($field):"forbidden"}')
  code=$(printf '%s' "$unsafe" | curl --noproxy '*' -s -o /dev/null -w '%{http_code}' \
    -u admin:e2eDashboardSharedPassword123 -H 'X-Requested-With: fetch' -H 'Content-Type: application/json' \
    --data-binary @- "$dashboard_url/api/monero/watch")
  [ "$code" = 400 ] || wallet_fail "$forbidden field was not rejected before wallet RPC"
done

watch_list=$(curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url/api/monero/watch") ||
  wallet_fail "watch-only API list failed"
printf '%s' "$watch_list" | jq -e --arg id "$watch_id" '.wallets | length == 1 and .[0].id == $id and .[0].state == "pending" and .[0].balance_atomic == ""' >/dev/null ||
  wallet_fail "initial-sync wallet did not remain safely pending"
printf '%s' "$watch_list" | grep -Fq "$source_view_key" && wallet_fail "watch-only list disclosed the private view key"
printf '%s' "$watch_list" | grep -Fq 'wallet_file' && wallet_fail "watch-only list disclosed its internal wallet filename"
docker compose exec -T -e NEEDLE="$source_view_key" dashboard sh -c 'test "$(stat -c %a /state/monero_watch.json)" = 600 && ! grep -Fq "$NEEDLE" /state/monero_watch.json' ||
  wallet_fail "watch-only state permissions or view-key retention are unsafe"
if docker compose logs dashboard monero-wallet 2>&1 | grep -Fq "$source_view_key"; then
  wallet_fail "watch-only services logged the private view key"
fi

docker compose restart dashboard >/dev/null || wallet_fail "dashboard restart failed with a managed watch wallet"
restarted_watch=""
for _ in $(seq 1 30); do
  restarted_watch=$(curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url/api/monero/watch" || true)
  printf '%s' "$restarted_watch" | jq -e --arg id "$watch_id" '.wallets | length == 1 and .[0].id == $id' >/dev/null 2>&1 && break
  sleep 1
done
printf '%s' "$restarted_watch" | jq -e --arg id "$watch_id" '.wallets | length == 1 and .[0].id == $id' >/dev/null ||
  wallet_fail "watch-only roster did not survive dashboard restart"
printf '%s' "$restarted_watch" | grep -Fq "$source_view_key" && wallet_fail "restarted API disclosed the private view key"

code=$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' -u admin:e2eDashboardSharedPassword123 \
  -H 'X-Requested-With: fetch' -X DELETE "$dashboard_url/api/monero/watch/$watch_id")
[ "$code" = 200 ] || wallet_fail "throwaway watch-only row could not be removed"
unset watch_add unsafe create_request create_response open_response height_response watch_response watch_list restarted_watch source_view_key source_address fixture_password

# The inbound onion service registers with tor's control port: once
# ADD_ONION succeeds, the .onion address appears in localaddresses.
# (Core's "tor: Got service ID" log line only exists with -debug=tor.)
onion_ok=""
for _ in $(seq 1 24); do
  if docker compose exec -T bitcoin bitcoin-cli -datadir=/data getnetworkinfo | grep -q '\.onion'; then
    onion_ok=1
    break
  fi
  sleep 10
done
[ -n "$onion_ok" ] || fail "bitcoind never registered an onion service with tor"

# The dashboard hidden service is provisioned (keys + hostname generated)
docker compose exec -T tor cat /var/lib/tor/dashboard_onion/hostname | grep -q '\.onion$' ||
  fail "dashboard onion hostname was not provisioned"

# Prometheus metrics behind the same auth
curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url/metrics" | grep -q "bitcoin_node_up 1" ||
  fail "metrics endpoint missing bitcoin_node_up"

curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url/api/settings" | grep -q '"can_configure":true' ||
  fail "authenticated LAN settings are not configurable"
settings_body='{"telegram_enabled":false,"telegram_chat_id":"","healthchecks_enabled":false,"alert_new_block":true}'
code=$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' -H 'X-Requested-With: fetch' \
  -H 'Content-Type: application/json' -d "$settings_body" "$dashboard_url/api/settings")
[ "$code" = 401 ] || fail "unauthenticated LAN notification settings write was not denied"
code=$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' -u x:wrong -H 'X-Requested-With: fetch' \
  -H 'Content-Type: application/json' -d "$settings_body" "$dashboard_url/api/settings")
[ "$code" = 401 ] || fail "wrong-password LAN notification settings write was not denied"
code=$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' -u admin:e2eDashboardSharedPassword123 \
  -H 'Content-Type: application/json' -d "$settings_body" "$dashboard_url/api/settings")
[ "$code" = 403 ] || fail "LAN notification settings write without CSRF header was not denied"
curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 -H 'X-Requested-With: fetch' -H 'Content-Type: application/json' \
  -d "$settings_body" "$dashboard_url/api/settings" | grep -q '"overridden":true' ||
  fail "authenticated LAN notification settings write failed"
docker compose exec -T dashboard sh -c 'test "$(stat -c %a /state/notification_settings.json)" = 600' ||
  fail "saved notification settings are not private"
docker compose up -d --no-deps --force-recreate --wait --wait-timeout 300 dashboard ||
  fail "dashboard recreation failed"
curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url/api/settings" | grep -q '"alert_new_block":true' ||
  fail "notification settings did not survive container recreation"
dashboard_backup_test_dir=$(mktemp -d "${TMPDIR:-/tmp}/node-manager-dashboard-backup.XXXXXX")
dashboard_backup_archive=$(./stack backup | sed -n 's/^Wrote \(.*\) (config, .*$/\1/p')
[ -f "$dashboard_backup_archive" ] || fail "backup did not create its archive"
tar xzf "$dashboard_backup_archive" -C "$dashboard_backup_test_dir"
grep -q '"alert_new_block":true' "$dashboard_backup_test_dir/dashboard_state/notification_settings.json" ||
  fail "backup did not preserve dashboard notification settings"
# Restore only dashboard state to avoid restarting Tor while checking saved settings.
tar czf "$dashboard_backup_test_dir/restore.tar.gz" -C "$dashboard_backup_test_dir" dashboard_state
settings_body='{"telegram_enabled":false,"telegram_chat_id":"","healthchecks_enabled":false,"alert_new_block":false}'
curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 -H 'X-Requested-With: fetch' -H 'Content-Type: application/json' \
  -d "$settings_body" "$dashboard_url/api/settings" >/dev/null
./stack restore -y "$dashboard_backup_test_dir/restore.tar.gz" >/dev/null || fail "dashboard state restore failed"
docker compose exec -T dashboard sh -c 'test "$(stat -c %u:%a /state/notification_settings.json)" = 1000:600' ||
  fail "restored dashboard notification settings lost private ownership or permissions"
docker compose up -d --no-deps --force-recreate --wait --wait-timeout 300 dashboard ||
  fail "dashboard recreation after state restore failed"
curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url/api/settings" | grep -q '"alert_new_block":true' ||
  fail "dashboard did not load restored notification settings"
settings_body='{"reset":true}'
curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 -H 'X-Requested-With: fetch' -H 'Content-Type: application/json' \
  -d "$settings_body" "$dashboard_url/api/settings" | grep -q '"overridden":false' ||
  fail "notification reset did not restore the server configuration"

restarts=$(service_inspect tor --format '{{.RestartCount}}')
docker compose exec -T tor sh -c 'kill "$(pidof socat)"'
forwarder_recovered=""
for _ in $(seq 1 30); do
  current=$(service_inspect tor --format '{{.RestartCount}}')
  if [ "$current" -gt "$restarts" ] && curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url/metrics" | grep -q "bitcoin_node_up 1"; then
    forwarder_recovered=1
    break
  fi
  sleep 2
done
[ -n "$forwarder_recovered" ] || fail "Tor/dashboard forwarder did not restart cleanly"

restarts=$(service_inspect tor --format '{{.RestartCount}}')
docker compose exec -T tor sh -c 'kill "$(pidof tor)"'
tor_recovered=""
for _ in $(seq 1 30); do
  current=$(service_inspect tor --format '{{.RestartCount}}')
  if [ "$current" -gt "$restarts" ] && curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url/metrics" | grep -q "bitcoin_node_up 1"; then
    tor_recovered=1
    break
  fi
  sleep 2
done
[ -n "$tor_recovered" ] || fail "Tor child failure did not restart the supervised container"

# Tor must be the only service with an external route. Prove Tor reaches the
# network, then stop it and make real direct TCP and DNS attempts from both
# application network namespaces; every attempt must fail closed.
tor_bootstrapped=""
for _ in $(seq 1 30); do
  if docker compose logs tor 2>&1 | grep "Bootstrapped 100" >/dev/null; then
    tor_bootstrapped=1
    break
  fi
  sleep 10
done
[ -n "$tor_bootstrapped" ] || fail "Tor never reached the public network"
helper_image=$(service_inspect dashboard --format '{{.Config.Image}}')
docker compose exec -T dashboard sh -c 'command -v nc >/dev/null && command -v nslookup >/dev/null && nc -z -w 3 172.29.0.26 8332' ||
  fail "dashboard network probes are unavailable or cannot reach local RPC"
docker run --rm --pull never --network "container:$(service_id bitcoin)" --entrypoint /bin/sh "$helper_image" \
  -c 'command -v nc >/dev/null && command -v nslookup >/dev/null && nc -z -w 3 172.29.0.25 9050' ||
  fail "bitcoin network probes are unavailable or cannot reach local Tor"
docker run --rm --pull never --network "container:$(service_id monero)" --entrypoint /bin/sh "$helper_image" \
  -c 'command -v nc >/dev/null && command -v nslookup >/dev/null && nc -z -w 3 172.29.0.25 9050' ||
  fail "Monero network probes are unavailable or cannot reach local Tor"
docker compose stop tor >/dev/null
[ "$(service_inspect bitcoin --format '{{json .HostConfig.Dns}}')" = '["127.0.0.1"]' ] ||
  fail "bitcoin does not have loopback-only DNS configured"
[ "$(service_inspect dashboard --format '{{json .HostConfig.Dns}}')" = '["127.0.0.1"]' ] ||
  fail "dashboard does not have loopback-only DNS configured"
[ "$(service_inspect monero --format '{{json .HostConfig.Dns}}')" = '["127.0.0.1"]' ] ||
  fail "Monero does not have loopback-only DNS configured"
if docker compose exec -T dashboard nc -z -w 3 1.1.1.1 443; then
  fail "dashboard reached a public IP directly with Tor offline"
fi
if docker compose exec -T dashboard nc -z -w 3 2606:4700:4700::1111 443; then
  fail "dashboard reached a public IPv6 address directly with Tor offline"
fi
if docker compose exec -T dashboard nslookup example.com >/dev/null 2>&1; then
  fail "dashboard resolved public DNS with Tor offline"
fi
if docker compose exec -T dashboard nslookup example.com 8.8.8.8 >/dev/null 2>&1; then
  fail "dashboard reached a public DNS server directly with Tor offline"
fi
if docker run --rm --pull never --network "container:$(service_id bitcoin)" --entrypoint /bin/sh "$helper_image" -c 'nc -z -w 3 1.1.1.1 443'; then
  fail "bitcoin reached a public IP directly with Tor offline"
fi
if docker run --rm --pull never --network "container:$(service_id bitcoin)" --entrypoint /bin/sh "$helper_image" -c 'nc -z -w 3 2606:4700:4700::1111 443'; then
  fail "bitcoin reached a public IPv6 address directly with Tor offline"
fi
if docker run --rm --pull never --network "container:$(service_id bitcoin)" --entrypoint /bin/sh "$helper_image" -c 'nslookup example.com' >/dev/null 2>&1; then
  fail "bitcoin resolved public DNS with Tor offline"
fi
if docker run --rm --pull never --network "container:$(service_id bitcoin)" --entrypoint /bin/sh "$helper_image" -c 'nslookup example.com 8.8.8.8' >/dev/null 2>&1; then
  fail "bitcoin reached a public DNS server directly with Tor offline"
fi
if docker run --rm --pull never --network "container:$(service_id monero)" --entrypoint /bin/sh "$helper_image" -c 'nc -z -w 3 1.1.1.1 443'; then
  fail "Monero reached a public IP directly with Tor offline"
fi
if docker run --rm --pull never --network "container:$(service_id monero)" --entrypoint /bin/sh "$helper_image" -c 'nc -z -w 3 2606:4700:4700::1111 443'; then
  fail "Monero reached a public IPv6 address directly with Tor offline"
fi
if docker run --rm --pull never --network "container:$(service_id monero)" --entrypoint /bin/sh "$helper_image" -c 'nslookup example.com' >/dev/null 2>&1; then
  fail "Monero resolved public DNS with Tor offline"
fi
if docker run --rm --pull never --network "container:$(service_id monero)" --entrypoint /bin/sh "$helper_image" -c 'nslookup example.com 8.8.8.8' >/dev/null 2>&1; then
  fail "Monero reached a public DNS server directly with Tor offline"
fi
docker compose start tor >/dev/null
for _ in $(seq 1 30); do
  [ "$(service_inspect tor --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}')" = "healthy" ] && break
  sleep 2
done
[ "$(service_inspect tor --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}')" = "healthy" ] ||
  fail "Tor did not recover after fail-closed egress test"

# Egress audit: every ESTABLISHED connection from the bitcoin and dashboard
# containers must terminate inside the stack subnet (172.29.0.x renders as
# xx001DAC in /proc/net/tcp little-endian hex) — i.e. everything goes to tor
# or intra-stack RPC, and nothing dials clearnet directly.
for c in bitcoin monero monero-wallet dashboard; do
  bad=$(docker compose exec -T "$c" awk 'NR>1 && $4=="01" {split($3,r,":"); if (r[1] !~ /001DAC$/) print r[1]":"r[2]}' /proc/net/tcp 2>/dev/null || true)
  [ -z "$bad" ] || fail "clearnet egress from $c: $bad"
  bad6=$(docker compose exec -T "$c" sh -c 'awk "NR>1 && \$4==\"01\" {print \$3}" /proc/net/tcp6 2>/dev/null' || true)
  [ -z "$bad6" ] || fail "unexpected IPv6 egress from $c: $bad6"
done

# Sync-start check (opt-in: real Tor traffic, takes many minutes). Proves the
# node finds peers, that every peer is an onion peer, and that header sync
# begins — not the whole sync, just that it starts. Cold-cache onion peer
# discovery on an empty datadir is slow, so the budgets are generous — run
# this on real hardware, not in the CI gate.
sync_starts() { # $1: label for messages
  # separate the failure modes: Tor itself not bootstrapping (up to 5 min)
  # vs. bitcoind not finding onion peers through a working Tor
  boot_ok=""
  for _ in $(seq 1 30); do
    if docker compose logs tor 2>&1 | grep "Bootstrapped 100" >/dev/null; then
      boot_ok=1
      break
    fi
    sleep 10
  done
  [ -n "$boot_ok" ] || fail "$1: tor never reached Bootstrapped 100%"

  peer_ok=""
  for _ in $( # up to 20 min for the first peer: cold onion discovery is slow
    seq 1 120
  ); do
    conns=$(docker compose exec -T bitcoin bitcoin-cli -datadir=/data getnetworkinfo | grep -o '"connections": *[0-9]*' | grep -o '[0-9]*' || echo 0)
    if [ "${conns:-0}" -ge 1 ]; then
      peer_ok=1
      break
    fi
    sleep 10
  done
  [ -n "$peer_ok" ] || fail "$1: no peers found over Tor within 20 minutes (tor was bootstrapped)"

  clearnet_peers=$(docker compose exec -T bitcoin bitcoin-cli -datadir=/data getpeerinfo | grep '"addr":' | grep -v '\.onion' || true)
  [ -z "$clearnet_peers" ] || fail "$1: non-onion peer(s) connected: $clearnet_peers"

  # "sync started" = headers flowing. Core's headers PRESYNC (redownload
  # protection) downloads the whole ~950k-header chain from one peer before
  # getblockchaininfo.headers ever moves off 0 — 15+ min over Tor — so watch
  # getpeerinfo's presynced_headers too; it moves within ~a minute of a good
  # peer.
  hdr_ok=""
  for _ in $( # 10 min for the first headers to flow
    seq 1 60
  ); do
    hdrs=$(docker compose exec -T bitcoin bitcoin-cli -datadir=/data getblockchaininfo | grep -o '"headers": *[0-9]*' | grep -o '[0-9]*' || echo 0)
    presync=$(docker compose exec -T bitcoin bitcoin-cli -datadir=/data getpeerinfo | grep -oE '"presynced_headers": [0-9]+' | grep -oE '[0-9]+' | sort -n | tail -1 || echo 0)
    if [ "${hdrs:-0}" -gt 0 ] || [ "${presync:-0}" -gt 0 ]; then
      hdr_ok=1
      break
    fi
    sleep 10
  done
  [ -n "$hdr_ok" ] || fail "$1: headers never started flowing — sync did not start"
  echo "  $1: sync starts — $conns peer(s), all onion, headers=$hdrs presync=$presync"
}

if [ -n "${E2E_SYNC:-}" ]; then
  sync_starts "preseeded setup"

  # Fresh defaults must reject missing authentication before any service starts.
  docker compose down -v --remove-orphans >/dev/null 2>&1
  rm -f config.json .env .approved-config.json .approved.env
  docker run --rm -v "$BITCOIN_DATA_DIR":/cleanup alpine:3.24 sh -c 'rm -rf /cleanup/* /cleanup/.[!.]*' >/dev/null 2>&1 || true
  ./configure.sh >/dev/null 2>&1 && fail "fresh defaults accepted a missing dashboard password"
  [ ! -f .env ] || fail "failed authentication setup rendered a runtime"
  jq -nc --arg data_dir "$BITCOIN_DATA_DIR" --arg bind_ip "$host_ip" \
    '{bitcoin:{data_dir:$data_dir},dashboard:{password:"e2eDashboardSharedPassword123",bind_ip:$bind_ip}}' >config.json
  ./configure.sh >/dev/null
  ./stack up >/dev/null || fail "fresh defaults: startup failed"
  docker compose up -d --pull never --no-build --wait --wait-timeout 300 ||
    fail "fresh defaults: stack did not reach healthy"
  dashboard_url="http://$(sed -n 's/^DASHBOARD_BIND_IP=//p' .env):80"
  code=$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' "$dashboard_url")
  [ "$code" = "302" ] || fail "fresh defaults: anonymous dashboard did not redirect to sign-in (HTTP $code)"
  code=$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' -u admin:e2eDashboardSharedPassword123 "$dashboard_url")
  [ "$code" = "200" ] || fail "fresh defaults: authenticated dashboard was not reachable (HTTP $code)"
  sync_starts "fresh authenticated defaults"
else
  echo "  (sync-start checks skipped — set E2E_SYNC=1 to include them)"
fi

# Exercise archival Core watch-wallet compatibility on a separate datadir.
docker compose stop dashboard bitcoin >/dev/null
sed -i.bak \
  -e "s|^BITCOIN_DATA_DIR=.*|BITCOIN_DATA_DIR=$WALLET_DATA_DIR|" \
  -e 's|^BITCOIN_PRUNE=.*|BITCOIN_PRUNE=0|' \
  -e 's|^DASHBOARD_PASSWORD=.*|DASHBOARD_PASSWORD=e2eDashboardSharedPassword123|' \
  .env && rm -f .env.bak
docker compose up -d --no-deps --force-recreate --wait --wait-timeout 300 bitcoin dashboard ||
  fail "full-node wallet stage did not reach healthy"

dashboard_url="http://$(sed -n 's/^DASHBOARD_BIND_IP=//p' .env):80"
body=$(curl --noproxy '*' -sf -u admin:e2eDashboardSharedPassword123 "$dashboard_url" || true)
grep -q "Full" <<<"$body" || fail "dashboard missing full-node badge in wallet stage"
rpc_user=$(sed -n 's/^BITCOIN_RPC_USER=//p' .env)
rpc_pass=$(sed -n 's/^BITCOIN_RPC_PASSWORD=//p' .env)
BITCOIN_RPC_USER="$rpc_user" BITCOIN_RPC_PASSWORD="$rpc_pass" \
  E2E_DASHBOARD_URL="$dashboard_url" E2E_DASHBOARD_PASSWORD=e2eDashboardSharedPassword123 python3 build/dashboard/tests/e2e_watch.py ||
  fail "Go watch-only provisioning failed"

echo "PASS: test_e2e.sh"
