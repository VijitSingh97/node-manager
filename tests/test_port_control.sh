#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  exit 1
}

tmp=$(mktemp -d)
listener_pid=
cleanup() {
  if [ -n "$listener_pid" ]; then
    kill "$listener_pid" 2>/dev/null || true
    wait "$listener_pid" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

fake=$tmp/docker
cat >"$fake" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
if [ "$1" = inspect ]; then
  case "$3:$4" in
    '{{.Id}}:dashboard') printf '%s\n' dashboard-id ;;
    '{{.Id}}:tor') printf '%s\n' tor-id ;;
    *project.config_files*':tor-id') printf '%s\n' "${FAKE_TOR_CONFIG_FILES:-$BITCOIN_STACK_ROOT/docker-compose.yml}" ;;
    *project.config_files*) printf '%s\n' "${FAKE_CONFIG_FILES:-$BITCOIN_STACK_ROOT/docker-compose.yml}" ;;
    *HostConfig.NanoCpus*) printf '%s\n' "${FAKE_NODE_LIMITS:-0 0 0}" ;;
    *State.Health*) printf 'running healthy\n' ;;
    '{{json .NetworkSettings.Ports}}:tor-id') printf '%s\n' "$FAKE_TOR_PORTS" ;;
    '{{json .NetworkSettings.Ports}}:'*)
      if [ -n "${FAKE_OTHER_PORTS:-}" ]; then printf '%s\n' "$FAKE_OTHER_PORTS"; else printf '%s\n' '{}'; fi
      ;;
  esac
  exit 0
fi
if [ "$1" = ps ] && [ "$2" = -aq ]; then
  case "$*" in
    *com.docker.compose.service=dashboard*) [ "${FAKE_DASHBOARD_MISSING:-0}" = 1 ] || printf '%s\n' dashboard-id ;;
    *com.docker.compose.service=tor*) printf '%s\n' tor-id ;;
    *com.docker.compose.service=bitcoincash*) printf '%s\n' bitcoincash-id ;;
    *com.docker.compose.service=bitcoin*) printf '%s\n' bitcoin-id ;;
    *com.docker.compose.service=monero-wallet*) printf '%s\n' monero-wallet-id ;;
    *com.docker.compose.service=monero*) printf '%s\n' monero-id ;;
    *com.docker.compose.service=litecoin*) printf '%s\n' litecoin-id ;;
    *com.docker.compose.service=dogecoin*) printf '%s\n' dogecoin-id ;;
    *com.docker.compose.service=zcash*) printf '%s\n' zcash-id ;;
  esac
  exit 0
fi
if [ "$1" = ps ] && [ "$2" = -q ]; then
  printf '%s\n' tor-id ${FAKE_OTHER_DOCKER_IDS:-}
  exit 0
fi
if [ "$1" = compose ]; then
  exit 0
fi
if [ "$1" = update ]; then
  exit 0
fi
[ "$1" = exec ] || exit 1
shift
if [ "${1:-}" = -i ]; then shift; fi
if [ "${1:-}" = -u ]; then shift 2; fi
[ "$1" = dashboard-id ]
shift
if [ "$1" = sh ] && [ "$2" = -c ]; then
  command=$3
  command=$(printf '%s' "$command" | sed "s#/state#$FAKE_DASH_STATE#g")
  sh -c "$command"
  exit
fi
args=
for arg in "$@"; do
  case "$arg" in /state/*) arg=$FAKE_DASH_STATE/${arg#/state/} ;; esac
  args="$args '$arg'"
done
eval "exec $args"
EOF
chmod +x "$fake" scripts/node-port-control
fake_ss=$tmp/ss
cat >"$fake_ss" <<'EOF'
#!/bin/sh
set -eu
[ "${FAKE_SS_FAIL:-0}" != 1 ] || exit 1
printf '%s' "${FAKE_SS_LISTENERS:-}"
EOF
chmod +x "$fake_ss"
fake_ip=$tmp/ip
cat >"$fake_ip" <<'EOF'
#!/bin/sh
set -eu
[ "${FAKE_IP_FAIL:-0}" != 1 ] || exit 1
printf '%s' "${FAKE_IP_ADDRESSES:-}"
EOF
chmod +x "$fake_ip"
fake_netstat=$tmp/netstat
cat >"$fake_netstat" <<'EOF'
#!/bin/sh
set -eu
[ "${FAKE_NETSTAT_FAIL:-0}" != 1 ] || exit 1
printf '%s' "${FAKE_NETSTAT_LISTENERS:-}"
EOF
chmod +x "$fake_netstat"
fake_ifconfig=$tmp/ifconfig
cat >"$fake_ifconfig" <<'EOF'
#!/bin/sh
set -eu
[ "${FAKE_IFCONFIG_FAIL:-0}" != 1 ] || exit 1
printf '%s' "${FAKE_IFCONFIG_ADDRESSES:-}"
EOF
chmod +x "$fake_ifconfig"

new_case() {
  case_root=$tmp/$1
  mkdir -p "$case_root/root/scripts" "$case_root/state/dashboard" "$case_root/config"
  cp scripts/node-port-control "$case_root/root/scripts/"
  cp configure.sh "$case_root/root/"
  : >"$case_root/root/docker-compose.yml"
  cat >"$case_root/root/stack" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_STACK_LOG"
if [ "${FAKE_APPLY_FAIL_ONCE:-0}" = 1 ] && [ ! -e "$FAKE_APPLY_FAIL_MARKER" ]; then
  : >"$FAKE_APPLY_FAIL_MARKER"
  if [ "${1:-}" = apply-credential-settings ] && [ "${2:-}" = bitcoin ]; then
    changed_password=$(jq -r '.bitcoin.node_password // empty' "$BITCOIN_STACK_CONFIG_DIR/config.json")
    if [ -n "$changed_password" ]; then
      sed "s/^BITCOIN_RPC_PASSWORD=.*/BITCOIN_RPC_PASSWORD=$changed_password/" "$BITCOIN_STACK_STATE_DIR/.env" >"$BITCOIN_STACK_STATE_DIR/.env.tmp"
      mv "$BITCOIN_STACK_STATE_DIR/.env.tmp" "$BITCOIN_STACK_STATE_DIR/.env"
    fi
  fi
  exit 1
fi
[ "${FAKE_APPLY_FAIL:-0}" != 1 ] || exit 1
if [ "${1:-}" = approve-rendered-config ]; then
  jq -S . "$BITCOIN_STACK_CONFIG_DIR/config.json" >"$BITCOIN_STACK_STATE_DIR/.approved-config.tmp"
  chmod 600 "$BITCOIN_STACK_STATE_DIR/.approved-config.tmp"
  mv "$BITCOIN_STACK_STATE_DIR/.approved-config.tmp" "$BITCOIN_STACK_STATE_DIR/.approved-config.json"
  cp "$BITCOIN_STACK_STATE_DIR/.env" "$BITCOIN_STACK_STATE_DIR/.approved.env"
  chmod 600 "$BITCOIN_STACK_STATE_DIR/.approved.env"
fi
EOF
  chmod +x "$case_root/root/stack"
  cat >"$case_root/state/.env" <<'EOF'
DASHBOARD_PASSWORD=testpassword
BITCOIN_RPC_EXPOSE=1
MONERO_RPC_EXPOSE=1
MONERO_ZMQ_EXPOSE=1
EOF
  cat >"$case_root/config/config.json" <<'EOF'
{"bitcoin":{"enabled":true,"node_password":"bitcoinpassword1234567890123456","rpc_expose":true,"rpc_bind_ip":"192.168.1.10","rpc_port":8332,"steady_cpus":0.5},"monero":{"enabled":true,"node_password":"moneropassword12345678901234567","rpc_expose":true,"rpc_bind_ip":"192.168.1.10","rpc_port":18081,"zmq_expose":true,"zmq_bind_ip":"192.168.1.10","zmq_port":18083},"tari":{"enabled":true,"grpc_expose":false},"litecoin":{"enabled":true,"node_password":"litecoinpassword123456789012345","rpc_expose":false},"dogecoin":{"enabled":true,"node_password":"dogecoinpassword123456789012345","rpc_expose":false},"bitcoincash":{"enabled":true,"node_password":"bitcoincashpassword1234567890123","rpc_expose":false},"zcash":{"enabled":true,"node_password":"zcashpassword123456789012345678","rpc_expose":false},"dashboard":{"password":"testpassword"},"other":{"keep":"yes"}}
EOF
  jq -S . "$case_root/config/config.json" >"$case_root/state/.approved-config.json"
  chmod 600 "$case_root/state/.approved-config.json"
  cp "$case_root/state/.env" "$case_root/state/.approved.env"
  chmod 600 "$case_root/state/.approved.env"
  export BITCOIN_STACK_ROOT=$case_root/root
  export BITCOIN_STACK_STATE_DIR=$case_root/state
  export BITCOIN_STACK_CONFIG_DIR=$case_root/config
  export DOCKER_BIN=$fake FAKE_DASH_STATE=$case_root/state/dashboard
  export SS_BIN=$fake_ss
  export IP_BIN=$fake_ip NETSTAT_BIN=$fake_netstat IFCONFIG_BIN=$fake_ifconfig PORT_CONTROL_OS=Linux
  export FAKE_IP_ADDRESSES='1: lo inet 127.0.0.1/8 scope host lo
2: eth0 inet 192.168.1.10/24 scope global eth0
2: eth0 inet 192.168.1.20/24 scope global secondary eth0
2: eth0 inet 192.168.1.25/24 scope global secondary eth0
2: eth0 inet 192.168.1.30/24 scope global secondary eth0
2: eth0 inet 10.0.0.20/24 scope global secondary eth0
2: eth0 inet 172.16.1.20/24 scope global secondary eth0'
  export FAKE_DOCKER_LOG=$case_root/docker.log FAKE_STACK_LOG=$case_root/stack.log
  export PORT_CONTROL_NOW_EPOCH=1000 FAKE_APPLY_FAIL=0 FAKE_APPLY_FAIL_ONCE=0 FAKE_APPLY_FAIL_MARKER=$case_root/apply-failed
  export FAKE_TOR_PORTS='{"8332/tcp":[{"HostIp":"192.168.1.10","HostPort":"8332"}],"18081/tcp":[{"HostIp":"192.168.1.10","HostPort":"18081"}],"18083/tcp":[{"HostIp":"192.168.1.10","HostPort":"18083"}]}'
  unset FAKE_CONFIG_FILES FAKE_TOR_CONFIG_FILES FAKE_OTHER_DOCKER_IDS FAKE_OTHER_PORTS FAKE_SS_LISTENERS FAKE_SS_FAIL FAKE_DASHBOARD_MISSING
  unset FAKE_IP_FAIL FAKE_NETSTAT_FAIL FAKE_IFCONFIG_FAIL
  unset FAKE_NETSTAT_LISTENERS FAKE_IFCONFIG_ADDRESSES
  unset FAKE_NODE_LIMITS
  : >"$FAKE_DOCKER_LOG"
  : >"$FAKE_STACK_LOG"
}

request() {
  if [ "${APPROVE_BEFORE_REQUEST:-1}" = 1 ]; then
    jq -S . "$case_root/config/config.json" >"$case_root/state/.approved-config.json"
    chmod 600 "$case_root/state/.approved-config.json"
  fi
  printf '%s\n' "$1" >"$FAKE_DASH_STATE/port-control.request"
}

status() {
  jq -r "$1" "$FAKE_DASH_STATE/port-control.status"
}

credential_request() {
  if [ "${APPROVE_BEFORE_REQUEST:-1}" = 1 ]; then
    jq -S . "$case_root/config/config.json" >"$case_root/state/.approved-config.json"
    chmod 600 "$case_root/state/.approved-config.json"
  fi
  printf '%s\n' "$1" >"$FAKE_DASH_STATE/credential-control.request"
}

credential_status() {
  jq -r "$1" "$FAKE_DASH_STATE/credential-control.status"
}

new_case close
mkdir -p "$case_root/state/port-control"
exec 7>"$case_root/state/port-control/.lock"
flock 7
if check_output=$(scripts/node-port-control --check-ports '[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"8.8.8.8","host_port":28332}]'); then
  fail "port preflight skipped validation while the normal control lock was held"
fi
[ "$check_output" = bind_address_unavailable:bitcoin_rpc ] || fail "port preflight returned the wrong typed error"
flock -u 7
exec 7>&-
FAKE_CONFIG_FILES=/tmp/not-this-stack.yml
export FAKE_CONFIG_FILES
if check_output=$(scripts/node-port-control --check-ports '[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":28332}]'); then
  fail "port preflight accepted a dashboard owned by another stack"
fi
[ "$check_output" = port_check_failed ] || fail "foreign dashboard preflight returned the wrong error"
unset FAKE_CONFIG_FILES
FAKE_DASHBOARD_MISSING=1
export FAKE_DASHBOARD_MISSING
if check_output=$(scripts/node-port-control --check-ports '[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":28332}]'); then
  fail "port preflight accepted a missing dashboard"
fi
[ "$check_output" = port_check_failed ] || fail "missing dashboard preflight returned the wrong error"
unset FAKE_DASHBOARD_MISSING
request '{"id":"11111111111111111111111111111111","changes":[{"port":"bitcoin_rpc","enabled":false},{"port":"monero_zmq","enabled":false}],"requested_at":1000}'
scripts/node-port-control
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = false ] || fail "Bitcoin RPC remained enabled"
[ "$(jq -r '.monero.zmq_expose' "$case_root/config/config.json")" = false ] || fail "Monero ZMQ remained enabled"
[ "$(jq -r '.monero.rpc_expose' "$case_root/config/config.json")" = true ] || fail "unrequested Monero RPC changed"
[ "$(jq -r '.other.keep' "$case_root/config/config.json")" = yes ] || fail "unrelated config changed"
[ "$(stat -c %a "$case_root/config/config.json" 2>/dev/null || stat -f %Lp "$case_root/config/config.json")" = 600 ] || fail "config permissions are not private"
[ "$(status .state)" = applied ] || fail "successful close status was not applied"
[ "$(jq -S -c . "$case_root/config/config.json")" = "$(jq -S -c . "$case_root/state/.approved-config.json")" ] ||
  fail "successful close did not advance the approved config"
[ -s "$case_root/state/.approved.env" ] || fail "successful close did not approve its rendered environment"
[ "$(status '.affected_services | join(",")')" = tor,dashboard ] || fail "status did not name the affected services"
grep -qx apply-port-settings "$FAKE_STACK_LOG" || fail "close did not use the bounded port-settings apply"
grep -q pull "$FAKE_STACK_LOG" && fail "close attempted an image download"
grep -q testpassword "$FAKE_DOCKER_LOG" "$FAKE_STACK_LOG" && fail "close leaked a credential into command logs"
if [ -e "$FAKE_DASH_STATE/port-control.request" ] || [ -e "$FAKE_DASH_STATE/port-control.processing" ]; then
  fail "request was not consumed"
fi
[ "$(cat "$FAKE_DASH_STATE/port-control.capability")" = ready ] || fail "capability heartbeat missing"

request '{"id":"11111111111111111111111111111111","changes":[{"port":"monero_rpc","enabled":false}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = rejected ] || fail "replayed request was accepted"
[ "$(grep -c '^apply-port-settings$' "$FAKE_STACK_LOG")" = 1 ] || fail "replay invoked apply"

for payload in \
  '{"id":"22222222222222222222222222222222","changes":[{"port":"unknown","enabled":false}],"requested_at":1000}' \
  '{"id":"33333333333333333333333333333333","changes":[{"port":"monero_rpc","enabled":false},{"port":"monero_rpc","enabled":false}],"requested_at":1000}' \
  '{"id":"44444444444444444444444444444444","changes":[{"port":"monero_rpc","enabled":false}],"requested_at":699}' \
  '{"id":"55555555555555555555555555555555","changes":[{"port":"monero_rpc","enabled":false}],"requested_at":1000,"extra":true}' \
  'not-json'; do
  request "$payload"
  scripts/node-port-control
  [ "$(status .state)" = rejected ] || fail "invalid request was accepted: $payload"
done
printf '%4097s' x >"$FAKE_DASH_STATE/port-control.request"
scripts/node-port-control
[ "$(status .state)" = rejected ] || fail "oversized request was accepted"
[ "$(grep -c '^apply-port-settings$' "$FAKE_STACK_LOG")" = 1 ] || fail "invalid request invoked apply"

new_case pending-config
jq '.other.external="pending"' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
APPROVE_BEFORE_REQUEST=0 request '{"id":"11111111111111111111111111111113","changes":[{"port":"bitcoin_rpc","enabled":false}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = rejected ] || fail "pending config did not block a port change"
[ "$(status .error_code)" = config_pending ] || fail "pending config rejection lacked typed status"
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = true ] || fail "pending config port request changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "pending config port request invoked apply"

new_case open
jq '.bitcoin.rpc_expose=false | .monero.rpc_expose=false | .monero.zmq_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
request '{"id":"10101010101010101010101010101010","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":28332},{"port":"monero_zmq","enabled":true,"bind_ip":"10.0.0.20","host_port":28083}],"requested_at":1000}'
scripts/node-port-control
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = true ] || fail "Bitcoin RPC was not opened"
[ "$(jq -r '.bitcoin.rpc_bind_ip' "$case_root/config/config.json")" = 192.168.1.20 ] || fail "Bitcoin RPC bind was not saved"
[ "$(jq -r '.bitcoin.rpc_port' "$case_root/config/config.json")" = 28332 ] || fail "Bitcoin RPC port was not saved"
[ "$(jq -r '.monero.zmq_expose' "$case_root/config/config.json")" = true ] || fail "Monero ZMQ was not opened"
[ "$(jq -r '.monero.zmq_bind_ip' "$case_root/config/config.json")" = 10.0.0.20 ] || fail "Monero ZMQ bind was not saved"
[ "$(jq -r '.monero.zmq_port' "$case_root/config/config.json")" = 28083 ] || fail "Monero ZMQ port was not saved"
[ "$(status .state)" = applied ] || fail "successful open status was not applied"
grep -qx apply-port-settings "$FAKE_STACK_LOG" || fail "open did not use the bounded port-settings apply"

new_case change-open-port
request '{"id":"11111111111111111111111111111112","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"172.16.1.20","host_port":38332}],"requested_at":1000}'
scripts/node-port-control
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = true ] || fail "editing an open listener closed it"
[ "$(jq -r '.bitcoin.rpc_bind_ip' "$case_root/config/config.json")" = 172.16.1.20 ] || fail "open listener bind edit was not saved"
[ "$(jq -r '.bitcoin.rpc_port' "$case_root/config/config.json")" = 38332 ] || fail "open listener port edit was not saved"
[ "$(status .state)" = applied ] || fail "open listener edit was not applied"

new_case additional-node-ports
port_specs='tari_grpc:tari:grpc:18142
litecoin_rpc:litecoin:rpc:19332
dogecoin_rpc:dogecoin:rpc:22555
bitcoincash_rpc:bitcoincash:rpc:18332
zcash_rpc:zcash:rpc:18232'
request '{"id":"40404040404040404040404040404040","changes":[{"port":"tari_grpc","enabled":true,"bind_ip":"192.168.1.20","host_port":18142},{"port":"litecoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":19332},{"port":"dogecoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":22555},{"port":"bitcoincash_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":18332},{"port":"zcash_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":18232}],"requested_at":1000}'
scripts/node-port-control
printf '%s\n' "$port_specs" | while IFS=: read -r setting node kind host_port; do
  [ "$(jq -r --arg node "$node" --arg kind "$kind" '.[$node][$kind + "_expose"]' "$case_root/config/config.json")" = true ] ||
    fail "$setting was not opened"
  [ "$(jq -r --arg node "$node" --arg kind "$kind" '.[$node][$kind + "_bind_ip"]' "$case_root/config/config.json")" = 192.168.1.20 ] ||
    fail "$setting bind was not saved"
  [ "$(jq -r --arg node "$node" --arg kind "$kind" '.[$node][$kind + "_port"]' "$case_root/config/config.json")" = "$host_port" ] ||
    fail "$setting port was not saved"
  [ "$(status '.affected_services | join(",")')" = tor,dashboard ] || fail "$setting named the wrong affected services"
done
request '{"id":"50505050505050505050505050505050","changes":[{"port":"tari_grpc","enabled":true,"bind_ip":"192.168.1.25","host_port":19142},{"port":"litecoin_rpc","enabled":true,"bind_ip":"192.168.1.25","host_port":20332},{"port":"dogecoin_rpc","enabled":true,"bind_ip":"192.168.1.25","host_port":23555},{"port":"bitcoincash_rpc","enabled":true,"bind_ip":"192.168.1.25","host_port":19332},{"port":"zcash_rpc","enabled":true,"bind_ip":"192.168.1.25","host_port":19232}],"requested_at":1000}'
scripts/node-port-control
printf '%s\n' "$port_specs" | while IFS=: read -r setting node kind host_port; do
  changed_port=$((host_port + 1000))
  [ "$(jq -r --arg node "$node" --arg kind "$kind" '.[$node][$kind + "_expose"]' "$case_root/config/config.json")" = true ] ||
    fail "$setting edit closed the listener"
  [ "$(jq -r --arg node "$node" --arg kind "$kind" '.[$node][$kind + "_bind_ip"]' "$case_root/config/config.json")" = 192.168.1.25 ] ||
    fail "$setting edited bind was not saved"
  [ "$(jq -r --arg node "$node" --arg kind "$kind" '.[$node][$kind + "_port"]' "$case_root/config/config.json")" = "$changed_port" ] ||
    fail "$setting edited port was not saved"
done
request '{"id":"60606060606060606060606060606060","changes":[{"port":"tari_grpc","enabled":false},{"port":"litecoin_rpc","enabled":false},{"port":"dogecoin_rpc","enabled":false},{"port":"bitcoincash_rpc","enabled":false},{"port":"zcash_rpc","enabled":false}],"requested_at":1000}'
scripts/node-port-control
printf '%s\n' "$port_specs" | while IFS=: read -r setting node kind _host_port; do
  [ "$(jq -r --arg node "$node" --arg kind "$kind" '.[$node][$kind + "_expose"]' "$case_root/config/config.json")" = false ] ||
    fail "$setting was not closed"
done
[ "$(grep -c '^apply-port-settings$' "$FAKE_STACK_LOG")" = 3 ] || fail "additional node port changes did not each apply once"

new_case additional-node-conflict
original_config=$(cat "$case_root/config/config.json")
request '{"id":"70707070707070707070707070707070","changes":[{"port":"litecoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":29332},{"port":"dogecoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":29332}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = rejected ] || fail "additional node port conflict was accepted"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "additional node conflict changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "additional node conflict invoked apply"

new_case invalid-open
original_config=$(cat "$case_root/config/config.json")
for payload in \
  '{"id":"20202020202020202020202020202020","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"0.0.0.0","host_port":28332}],"requested_at":1000}' \
  '{"id":"21212121212121212121212121212121","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"127.0.0.1","host_port":28332}],"requested_at":1000}' \
  '{"id":"22222222222222222222222222222223","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"8.8.8.8","host_port":28332}],"requested_at":1000}' \
  '{"id":"23232323232323232323232323232323","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.001.20","host_port":28332}],"requested_at":1000}' \
  '{"id":"24242424242424242424242424242424","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"fd00::1","host_port":28332}],"requested_at":1000}' \
  '{"id":"25252525252525252525252525252525","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":0}],"requested_at":1000}' \
  '{"id":"26262626262626262626262626262626","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":"28332"}],"requested_at":1000}' \
  '{"id":"27272727272727272727272727272727","changes":[{"port":"bitcoin_rpc","enabled":false,"bind_ip":"192.168.1.20","host_port":28332}],"requested_at":1000}'; do
  request "$payload"
  scripts/node-port-control
  [ "$(status .state)" = rejected ] || fail "unsafe open request was accepted: $payload"
done
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "invalid open request changed server config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "invalid open request invoked apply"

new_case conflict
jq '.bitcoin.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
request '{"id":"30303030303030303030303030303030","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":18081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = rejected ] || fail "colliding open request was accepted"
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = false ] || fail "colliding open request poisoned config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "colliding open request invoked apply"

new_case host-port-conflict
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export FAKE_SS_LISTENERS='LISTEN 0 4096 0.0.0.0:28081 0.0.0.0:*'
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303031","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = rejected ] || fail "occupied host port was accepted"
[ "$(status .error_code)" = port_in_use ] || fail "occupied host port lacked typed error"
[ "$(status .port)" = monero_rpc ] || fail "occupied host port status named the wrong setting"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "occupied host port changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "occupied host port invoked apply"

if command -v ss >/dev/null 2>&1 && command -v openssl >/dev/null 2>&1; then
  new_case real-host-port-conflict
  jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
  mv "$case_root/config/config.next" "$case_root/config/config.json"
  SS_BIN=$(command -v ss)
  export SS_BIN
  openssl s_server -accept 0.0.0.0:49555 -nocert -quiet -ign_eof </dev/null >"$case_root/listener.log" 2>&1 &
  listener_pid=$!
  listener_ready=0
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if "$SS_BIN" -H -ltn | grep -Eq '(^|[[:space:]])(0\.0\.0\.0|\*):49555([[:space:]]|$)'; then
      listener_ready=1
      break
    fi
    sleep 0.1
  done
  [ "$listener_ready" = 1 ] || fail "real TCP listener did not start"
  original_config=$(cat "$case_root/config/config.json")
  request '{"id":"30303030303030303030303030303036","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":49555}],"requested_at":1000}'
  scripts/node-port-control
  [ "$(status .error_code)" = port_in_use ] || fail "real host listener lacked typed conflict status"
  [ "$(status .port)" = monero_rpc ] || fail "real host listener named the wrong setting"
  [ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "real host listener changed config"
  [ ! -s "$FAKE_STACK_LOG" ] || fail "real host listener invoked apply"
  kill "$listener_pid" 2>/dev/null || true
  wait "$listener_pid" 2>/dev/null || true
  listener_pid=
fi

if [ "$(uname -s)" = Darwin ] && [ -x /usr/sbin/netstat ] && command -v ifconfig >/dev/null 2>&1; then
  lan_ip=$(ifconfig -a | awk '
    $1 == "inet" && ($2 ~ /^10\./ || $2 ~ /^192\.168\./ || $2 ~ /^172\.(1[6-9]|2[0-9]|3[01])\./) { print $2; exit }
  ')
  macos_port=$(/usr/sbin/netstat -an -p tcp 2>/dev/null | awk '
    $1 ~ /^tcp(4|6|46)?$/ && $NF == "LISTEN" {
      endpoint=$4
      if (!match(endpoint, /\.[0-9]+$/)) next
      address=substr(endpoint, 1, RSTART - 1)
      port=substr(endpoint, RSTART + 1)
      if ((address == "*" || address == "0.0.0.0" || address == "::") && port > 1024 &&
          port != 8082 && port != 8083 && port != 8332 && port != 18081 && port != 18083) {
        print port
        exit
      }
    }
  ')
  if [ -n "$lan_ip" ] && [ -n "$macos_port" ]; then
    new_case real-macos-host-port-conflict
    jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
    mv "$case_root/config/config.next" "$case_root/config/config.json"
    PORT_CONTROL_OS=Darwin
    NETSTAT_BIN=/usr/sbin/netstat
    IFCONFIG_BIN=$(command -v ifconfig)
    export PORT_CONTROL_OS NETSTAT_BIN IFCONFIG_BIN
    jq -S . "$case_root/config/config.json" >"$case_root/state/.approved-config.json"
    original_config=$(cat "$case_root/config/config.json")
    request "{\"id\":\"30303030303030303030303030303041\",\"changes\":[{\"port\":\"monero_rpc\",\"enabled\":true,\"bind_ip\":\"$lan_ip\",\"host_port\":$macos_port}],\"requested_at\":1000}"
    scripts/node-port-control
    [ "$(status .error_code)" = port_in_use ] ||
      fail "real macOS host listener status was $(status .state): $(status .message)"
    [ "$(status .port)" = monero_rpc ] || fail "real macOS host listener named the wrong setting"
    [ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "real macOS host listener changed config"
    [ ! -s "$FAKE_STACK_LOG" ] || fail "real macOS host listener invoked apply"
  fi
fi

new_case docker-port-conflict
jq '.monero.zmq_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export FAKE_OTHER_DOCKER_IDS=other-id
export FAKE_OTHER_PORTS='{"28083/tcp":[{"HostIp":"0.0.0.0","HostPort":"28083"}]}'
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303032","changes":[{"port":"monero_zmq","enabled":true,"bind_ip":"192.168.1.10","host_port":28083}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .error_code)" = port_in_use ] || fail "other Docker port lacked typed conflict status"
[ "$(status .port)" = monero_zmq ] || fail "other Docker conflict named the wrong setting"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "other Docker conflict changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "other Docker conflict invoked apply"

new_case mapped-ipv6-conflict
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export FAKE_SS_LISTENERS='LISTEN 0 4096 [::ffff:192.168.1.10]:28081 [::]:*'
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303037","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .error_code)" = port_in_use ] || fail "IPv4-mapped IPv6 listener was missed"
[ "$(status .port)" = monero_rpc ] || fail "IPv4-mapped listener named the wrong setting"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "IPv4-mapped listener changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "IPv4-mapped listener invoked apply"

new_case own-port-swap
export FAKE_SS_LISTENERS='LISTEN 0 4096 192.168.1.10:8332 0.0.0.0:*'
request '{"id":"30303030303030303030303030303033","changes":[{"port":"bitcoin_rpc","enabled":false},{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":8332}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = applied ] || fail "own Tor listener prevented a port swap"
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = false ] || fail "port swap did not close Bitcoin RPC"
[ "$(jq -r '.monero.rpc_port' "$case_root/config/config.json")" = 8332 ] || fail "port swap did not move Monero RPC"

new_case inspection-failure
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export FAKE_SS_FAIL=1
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303034","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = failed ] || fail "listener inspection failure did not fail safely"
[ "$(status .error_code)" = port_check_failed ] || fail "listener inspection failure lacked typed status"
[ "$(status 'has("port")')" = false ] || fail "listener inspection failure included a misleading port"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "inspection failure changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "inspection failure invoked apply"

new_case address-inspection-failure
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export FAKE_IP_FAIL=1
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303038","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = failed ] || fail "address inspection failure did not fail safely"
[ "$(status .error_code)" = port_check_failed ] || fail "address inspection failure lacked typed status"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "address inspection failure changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "address inspection failure invoked apply"

new_case unassigned-address
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303039","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.99","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = rejected ] || fail "unassigned LAN address was accepted"
[ "$(status .error_code)" = bind_address_unavailable ] || fail "unassigned LAN address lacked typed status"
[ "$(status .port)" = monero_rpc ] || fail "unassigned LAN address named the wrong setting"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "unassigned LAN address changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "unassigned LAN address invoked apply"

new_case macos-host-port-conflict
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export PORT_CONTROL_OS=Darwin
export FAKE_NETSTAT_LISTENERS='Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)
tcp46      0      0  *.28081                *.*                    LISTEN
tcp6       0      0  ::1.12345              *.*                    LISTEN
tcp6       0      0  ::.28082                *.*                    LISTEN'
export FAKE_IFCONFIG_ADDRESSES='lo0: flags=8049<UP,LOOPBACK,RUNNING,MULTICAST> mtu 16384
	inet 127.0.0.1 netmask 0xff000000
en0: flags=8863<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	inet 192.168.1.10 netmask 0xffffff00 broadcast 192.168.1.255'
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303040","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .error_code)" = port_in_use ] || fail "macOS wildcard listener was missed"
[ "$(status .port)" = monero_rpc ] || fail "macOS listener named the wrong setting"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "macOS listener changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "macOS listener invoked apply"

new_case macos-ipv6-wildcard-conflict
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export PORT_CONTROL_OS=Darwin
export FAKE_NETSTAT_LISTENERS='tcp6       0      0  ::.28082                *.*                    LISTEN'
export FAKE_IFCONFIG_ADDRESSES='	inet 192.168.1.10 netmask 0xffffff00 broadcast 192.168.1.255'
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303043","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28082}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .error_code)" = port_in_use ] || fail "macOS IPv6 wildcard listener was missed"
[ "$(status .port)" = monero_rpc ] || fail "macOS IPv6 listener named the wrong setting"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "macOS IPv6 listener changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "macOS IPv6 listener invoked apply"

new_case macos-listener-inspection-failure
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export PORT_CONTROL_OS=Darwin FAKE_NETSTAT_FAIL=1
export FAKE_IFCONFIG_ADDRESSES='	inet 192.168.1.10 netmask 0xffffff00 broadcast 192.168.1.255'
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303042","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = failed ] || fail "macOS listener inspection failure did not fail safely"
[ "$(status .error_code)" = port_check_failed ] || fail "macOS listener inspection failure lacked typed status"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "macOS inspection failure changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "macOS inspection failure invoked apply"

new_case macos-listener-inspection-malformed
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export PORT_CONTROL_OS=Darwin
export FAKE_NETSTAT_LISTENERS='tcp4       0      0  malformed              *.*                    LISTEN'
export FAKE_IFCONFIG_ADDRESSES='	inet 192.168.1.10 netmask 0xffffff00 broadcast 192.168.1.255'
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303044","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = failed ] || fail "malformed macOS listener inspection did not fail safely"
[ "$(status .error_code)" = port_check_failed ] || fail "malformed macOS listener inspection lacked typed status"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "malformed macOS inspection changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "malformed macOS inspection invoked apply"

new_case macos-listener-unexpected-protocol
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export PORT_CONTROL_OS=Darwin
export FAKE_NETSTAT_LISTENERS='tcp99      0      0  *.28081                *.*                    LISTEN'
export FAKE_IFCONFIG_ADDRESSES='\tinet 192.168.1.10 netmask 0xffffff00 broadcast 192.168.1.255'
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303045","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = failed ] || fail "unexpected macOS TCP protocol did not fail safely"
[ "$(status .error_code)" = port_check_failed ] || fail "unexpected macOS TCP protocol lacked typed status"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "unexpected macOS TCP protocol changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "unexpected macOS TCP protocol invoked apply"

new_case wrong-tor
jq '.monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
export FAKE_TOR_CONFIG_FILES=/another/install/docker-compose.yml
original_config=$(cat "$case_root/config/config.json")
request '{"id":"30303030303030303030303030303035","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.10","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .error_code)" = port_check_failed ] || fail "unrelated Tor container was trusted"
[ "$(cat "$case_root/config/config.json")" = "$original_config" ] || fail "unrelated Tor container changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "unrelated Tor container invoked apply"

new_case disabled-node
jq '.monero.enabled=false | .monero.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
request '{"id":"31313131313131313131313131313131","changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":28081}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = rejected ] || fail "disabled node listener was opened"
[ "$(jq -r '.monero.rpc_expose' "$case_root/config/config.json")" = false ] || fail "disabled node request poisoned config"

new_case weak-credentials
jq '.bitcoin.rpc_expose=false | .bitcoin.node_password="short"' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
request '{"id":"32323232323232323232323232323232","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.20","host_port":28332}],"requested_at":1000}'
scripts/node-port-control
[ "$(status .state)" = rejected ] || fail "listener with weak saved credentials was opened"
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = false ] || fail "weak-credential request poisoned config"

new_case weak-credentials-close
jq '.bitcoin.node_password="short"' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
request '{"id":"33333333333333333333333333333334","changes":[{"port":"bitcoin_rpc","enabled":false}],"requested_at":1000}'
scripts/node-port-control
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = false ] || fail "weak credentials prevented reducing exposure"
[ "$(status .state)" = applied ] || fail "safe close with weak credentials was rejected"

new_case generated-credentials
jq 'del(.bitcoin.node_password) | .bitcoin.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
printf '%s\n' 'BITCOIN_RPC_PASSWORD=generatedpassword123456789012345678' >>"$case_root/state/.env"
request '{"id":"34343434343434343434343434343434","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.30","host_port":58332}],"requested_at":1000}'
scripts/node-port-control
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = true ] || fail "generated saved credential could not open Bitcoin RPC"
[ "$(status .state)" = applied ] || fail "generated saved credential request was rejected"
grep -q generatedpassword "$FAKE_DOCKER_LOG" "$FAKE_STACK_LOG" && fail "generated RPC credential leaked into command logs"

new_case concurrent-edit
real_cmp=$(command -v cmp)
mkdir -p "$case_root/bin"
cat >"$case_root/bin/cmp" <<'EOF'
#!/bin/sh
set -eu
if [ ! -e "$CAS_EDIT_MARKER" ]; then
  jq '.dashboard.password="changedconcurrently1234567890" | .monero.rpc_expose=false | .other.concurrent="preserved"' "$CAS_CONFIG" >"$CAS_CONFIG.next"
  mv "$CAS_CONFIG.next" "$CAS_CONFIG"
  : >"$CAS_EDIT_MARKER"
fi
exec "$REAL_CMP" "$@"
EOF
chmod +x "$case_root/bin/cmp"
export REAL_CMP=$real_cmp CAS_CONFIG=$case_root/config/config.json CAS_EDIT_MARKER=$case_root/cas-edited
saved_path=$PATH
PATH=$case_root/bin:$PATH
request '{"id":"12121212121212121212121212121212","changes":[{"port":"bitcoin_rpc","enabled":false}],"requested_at":1000}'
scripts/node-port-control
PATH=$saved_path
unset REAL_CMP CAS_CONFIG CAS_EDIT_MARKER
[ "$(status .state)" = rejected ] || fail "concurrent external edit was accepted"
[ "$(status .error_code)" = config_pending ] || fail "concurrent external edit lacked pending-config status"
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = true ] || fail "concurrent edit request changed the requested port"
[ "$(jq -r '.monero.rpc_expose' "$case_root/config/config.json")" = false ] || fail "concurrent port closure was reverted"
[ "$(jq -r '.dashboard.password' "$case_root/config/config.json")" = changedconcurrently1234567890 ] || fail "concurrent password edit was reverted"
[ "$(jq -r '.other.concurrent' "$case_root/config/config.json")" = preserved ] || fail "concurrent unrelated edit was reverted"
[ ! -s "$FAKE_STACK_LOG" ] || fail "concurrent external edit invoked apply"

new_case interrupted
mkdir -p "$case_root/state/port-control"
printf '%s\n' 88888888888888888888888888888888 >"$case_root/state/port-control/inflight"
jq '.bitcoin.rpc_expose=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
printf '%s\n' '{"id":"88888888888888888888888888888888","changes":[{"port":"bitcoin_rpc","enabled":false}],"requested_at":1000}' >"$FAKE_DASH_STATE/port-control.processing"
scripts/node-port-control
[ "$(status .state)" = applied ] || fail "interrupted close was not resumed"
grep -qx apply-port-settings "$FAKE_STACK_LOG" || fail "interrupted close did not retry apply"

new_case interrupted-before-commit
mkdir -p "$case_root/state/port-control"
printf '%s\n' 89898989898989898989898989898989 >"$case_root/state/port-control/inflight"
printf '%s\n' '{"id":"89898989898989898989898989898989","changes":[{"port":"bitcoin_rpc","enabled":false},{"port":"monero_zmq","enabled":false}],"requested_at":1000}' >"$FAKE_DASH_STATE/port-control.processing"
scripts/node-port-control
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = false ] || fail "pre-commit recovery left Bitcoin RPC open"
[ "$(jq -r '.monero.zmq_expose' "$case_root/config/config.json")" = false ] || fail "pre-commit recovery left Monero ZMQ open"
[ "$(jq -r '.monero.rpc_expose' "$case_root/config/config.json")" = true ] || fail "pre-commit recovery changed an unrequested port"
[ "$(status .state)" = applied ] || fail "pre-commit recovery was not applied"
grep -qx apply-port-settings "$FAKE_STACK_LOG" || fail "pre-commit recovery did not invoke apply"

new_case guard
export FAKE_CONFIG_FILES=/another/install/docker-compose.yml
request '{"id":"66666666666666666666666666666666","changes":[{"port":"bitcoin_rpc","enabled":false}],"requested_at":1000}'
scripts/node-port-control
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = true ] || fail "wrong-stack guard changed config"
[ ! -e "$FAKE_DASH_STATE/port-control.capability" ] || fail "wrong-stack dashboard received a capability"
[ ! -s "$FAKE_STACK_LOG" ] || fail "wrong-stack dashboard invoked apply"

new_case no-password
sed -i.bak 's/DASHBOARD_PASSWORD=.*/DASHBOARD_PASSWORD=/' "$case_root/state/.env"
: >"$FAKE_DASH_STATE/port-control.capability"
scripts/node-port-control
[ "$(cat "$FAKE_DASH_STATE/port-control.capability")" = ready ] || fail "passwordless dashboard did not receive port control"
[ ! -e "$FAKE_DASH_STATE/credential-control.capability" ] || fail "passwordless dashboard received credential control"
printf '%s\n' '{"version":1,"iterations":210000,"salt":"0123456789abcdef0123456789abcdef","hash":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}' >"$FAKE_DASH_STATE/dashboard_auth.json"
scripts/node-port-control
[ "$(jq -r .timestamp "$FAKE_DASH_STATE/credential-control.capability")" = 1000 ] || fail "saved dashboard authentication did not enable credential control"
printf '%s\n' '{"version":1,"iterations":210000,"salt":"bad","hash":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}' >"$FAKE_DASH_STATE/dashboard_auth.json"
scripts/node-port-control
[ ! -e "$FAKE_DASH_STATE/credential-control.capability" ] || fail "malformed saved dashboard authentication enabled credential control"

new_case credential-reset
jq 'del(.bitcoin.enabled)' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
old_password=$(jq -r .bitcoin.node_password "$case_root/config/config.json")
credential_request '{"id":"abababababababababababababababab","node":"bitcoin","requested_at":1000}'
scripts/node-port-control
new_password=$(jq -r .bitcoin.node_password "$case_root/config/config.json")
[ "$new_password" != "$old_password" ] || fail "credential reset kept the old password"
printf '%s' "$new_password" | grep -Eq '^[0-9a-f]{48}$' || fail "credential reset did not generate a 48-hex password"
[ "$(credential_status .state)" = applied ] || fail "credential reset was not applied"
[ "$(credential_status .node)" = bitcoin ] || fail "credential status omitted the node"
[ "$(credential_status '.affected_services | join(",")')" = bitcoin,dashboard ] || fail "Bitcoin credential reset named the wrong services"
grep -qx 'apply-credential-settings bitcoin' "$FAKE_STACK_LOG" || fail "credential reset did not use the bounded apply command"
grep -q "$new_password" "$FAKE_DOCKER_LOG" "$FAKE_STACK_LOG" && fail "generated credential leaked into command logs"
if [ -e "$FAKE_DASH_STATE/credential-control.request" ] || [ -e "$FAKE_DASH_STATE/credential-control.processing" ]; then
  fail "credential request was not consumed"
fi

credential_request '{"id":"abababababababababababababababab","node":"bitcoin","requested_at":1000}'
scripts/node-port-control
[ "$(credential_status .state)" = rejected ] || fail "replayed credential reset was accepted"
[ "$(grep -c '^apply-credential-settings bitcoin$' "$FAKE_STACK_LOG")" = 1 ] || fail "credential replay invoked apply"

new_case credential-conflict
request '{"id":"cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd","changes":[{"port":"bitcoin_rpc","enabled":false}],"requested_at":1000}'
credential_request '{"id":"dededededededededededededededede","node":"bitcoin","requested_at":1000}'
scripts/node-port-control
[ "$(credential_status .state)" = rejected ] || fail "credential reset raced another control request"
[ -e "$FAKE_DASH_STATE/port-control.request" ] || fail "credential conflict consumed the other request"
[ ! -s "$FAKE_STACK_LOG" ] || fail "credential conflict invoked apply"

new_case credential-disabled
credential_request '{"id":"efefefefefefefefefefefefefefefef","node":"litecoin","requested_at":1000}'
scripts/node-port-control
[ "$(credential_status .state)" = rejected ] || fail "credential reset accepted a closed LAN RPC"
[ ! -s "$FAKE_STACK_LOG" ] || fail "closed LAN credential reset invoked apply"

new_case credential-inactive
jq '.bitcoin.active=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
old_password=$(jq -r .bitcoin.node_password "$case_root/config/config.json")
credential_request '{"id":"ffffffffffffffffffffffffffffffff","node":"bitcoin","requested_at":1000}'
scripts/node-port-control
[ "$(credential_status .state)" = rejected ] || fail "credential reset accepted an inactive node"
[ "$(jq -r .bitcoin.node_password "$case_root/config/config.json")" = "$old_password" ] || fail "inactive credential reset changed config"
[ ! -s "$FAKE_STACK_LOG" ] || fail "inactive credential reset invoked apply"

new_case credential-apply-failure
old_password=$(jq -r .monero.node_password "$case_root/config/config.json")
export FAKE_APPLY_FAIL_ONCE=1
credential_request '{"id":"12121212121212121212121212121213","node":"monero","requested_at":1000}'
scripts/node-port-control
[ "$(jq -r .monero.node_password "$case_root/config/config.json")" = "$old_password" ] || fail "failed credential apply did not restore the prior password"
[ "$(credential_status .state)" = failed ] || fail "failed credential apply status was not recorded"
[ "$(credential_status '.affected_services | join(",")')" = monero,monero-wallet,dashboard ] || fail "Monero credential reset named the wrong services"
[ ! -e "$FAKE_DASH_STATE/credential-control.capability" ] || fail "failed credential apply retained a capability"
[ "$(grep -c '^apply-credential-settings monero$' "$FAKE_STACK_LOG")" = 2 ] || fail "failed credential apply did not restore affected services"

new_case credential-env-rollback
jq 'del(.bitcoin.node_password)' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
printf '%s\n' 'BITCOIN_RPC_PASSWORD=oldgeneratedpassword123456789012345' >>"$case_root/state/.env"
chmod 600 "$case_root/state/.env"
old_env=$(cat "$case_root/state/.env")
APPROVE_BEFORE_REQUEST=1 credential_request '{"id":"16161616161616161616161616161616","node":"bitcoin","requested_at":1000}'
cp "$case_root/state/.env" "$case_root/state/.approved.env"
approved_config_hash=$(shasum -a 256 "$case_root/state/.approved-config.json")
approved_env_hash=$(shasum -a 256 "$case_root/state/.approved.env")
export FAKE_APPLY_FAIL_ONCE=1
scripts/node-port-control
[ "$(cat "$case_root/state/.env")" = "$old_env" ] || fail "failed credential reset retained the new generated environment password"
[ "$(shasum -a 256 "$case_root/state/.approved-config.json")" = "$approved_config_hash" ] || fail "failed credential reset advanced approved config"
[ "$(shasum -a 256 "$case_root/state/.approved.env")" = "$approved_env_hash" ] || fail "failed credential reset advanced approved environment"
[ "$(credential_status .state)" = failed ] || fail "credential environment rollback did not report failure"

new_case credential-interrupted
mkdir -p "$case_root/state/credential-control"
cp "$case_root/config/config.json" "$case_root/state/credential-control/transaction.before"
jq '.bitcoin.node_password="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"' "$case_root/config/config.json" >"$case_root/state/credential-control/transaction.after"
cp "$case_root/state/.env" "$case_root/state/credential-control/transaction.env.before"
printf '0 0 0\n' >"$case_root/state/credential-control/transaction.limits"
printf 'apply\n' >"$case_root/state/credential-control/transaction.intent"
printf '13131313131313131313131313131313\n' >"$case_root/state/credential-control/inflight"
chmod 600 "$case_root/state/credential-control"/*
printf '%s\n' '{"id":"13131313131313131313131313131313","node":"bitcoin","requested_at":1000}' >"$FAKE_DASH_STATE/credential-control.processing"
scripts/node-port-control
[ "$(jq -r .bitcoin.node_password "$case_root/config/config.json")" = aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ] ||
  fail "interrupted credential reset generated a second password"
[ "$(credential_status .state)" = applied ] || fail "interrupted credential reset did not resume"
[ ! -e "$case_root/state/credential-control/inflight" ] || fail "completed credential transaction was not cleared"

new_case credential-terminal-replay
mkdir -p "$case_root/state/credential-control"
printf '%s\n' '{"id":"15151515151515151515151515151515","node":"bitcoin","state":"applied","message":"RPC password reset.","affected_services":["bitcoin","dashboard"]}' >"$case_root/state/credential-control/transaction.result"
cp "$case_root/config/config.json" "$case_root/state/credential-control/transaction.before"
cp "$case_root/config/config.json" "$case_root/state/credential-control/transaction.after"
cp "$case_root/state/.env" "$case_root/state/credential-control/transaction.env.before"
printf '0 0 0\n' >"$case_root/state/credential-control/transaction.limits"
printf 'apply\n' >"$case_root/state/credential-control/transaction.intent"
printf '15151515151515151515151515151515\n' >"$case_root/state/credential-control/inflight"
chmod 600 "$case_root/state/credential-control"/*
printf '%s\n' '{"id":"15151515151515151515151515151515","node":"bitcoin","requested_at":1000}' >"$FAKE_DASH_STATE/credential-control.processing"
scripts/node-port-control
[ "$(credential_status .state)" = applied ] || fail "terminal credential result was not replayed"
[ ! -s "$FAKE_STACK_LOG" ] || fail "terminal credential replay applied another password"
[ ! -e "$case_root/state/credential-control/transaction.result" ] || fail "terminal credential result was not cleared"
[ ! -e "$case_root/state/credential-control/inflight" ] || fail "terminal credential replay did not clear recovery state"
[ ! -e "$FAKE_DASH_STATE/credential-control.processing" ] || fail "terminal credential replay did not consume the request"

new_case credential-double-failure
old_password=$(jq -r .bitcoin.node_password "$case_root/config/config.json")
export FAKE_APPLY_FAIL=1
credential_request '{"id":"14141414141414141414141414141414","node":"bitcoin","requested_at":1000}'
scripts/node-port-control
[ "$(jq -r .bitcoin.node_password "$case_root/config/config.json")" = "$old_password" ] || fail "double failure did not select the prior password"
[ "$(credential_status .state)" = failed ] || fail "double failure status was not recorded"
[ "$(cat "$case_root/state/credential-control/transaction.intent")" = rollback ] || fail "double failure did not preserve rollback intent"
for artifact in transaction.before transaction.after transaction.env.before transaction.limits transaction.intent inflight; do
  [ -s "$case_root/state/credential-control/$artifact" ] || fail "double failure discarded $artifact"
done
[ -s "$FAKE_DASH_STATE/credential-control.processing" ] || fail "double failure discarded the pending recovery request"
[ ! -e "$FAKE_DASH_STATE/credential-control.capability" ] || fail "double failure retained a credential capability"
export FAKE_APPLY_FAIL=0
scripts/node-port-control
[ "$(jq -r .bitcoin.node_password "$case_root/config/config.json")" = "$old_password" ] || fail "recovery retry changed the prior password"
[ "$(credential_status .state)" = failed ] || fail "recovery retry did not retain the failed result"
[ "$(credential_status .message)" = "RPC password could not be applied; prior credentials were restored." ] || fail "recovery retry did not report restored credentials"
[ ! -e "$case_root/state/credential-control/inflight" ] || fail "recovery retry did not clear the transaction"
[ ! -e "$FAKE_DASH_STATE/credential-control.processing" ] || fail "recovery retry did not consume the request"
[ "$(grep -c '^apply-credential-settings bitcoin$' "$FAKE_STACK_LOG")" = 3 ] || fail "double failure recovery did not retry the selected baseline"

new_case apply-failure
export FAKE_APPLY_FAIL_ONCE=1
request '{"id":"77777777777777777777777777777777","changes":[{"port":"monero_rpc","enabled":false}],"requested_at":1000}'
scripts/node-port-control
[ "$(jq -r '.monero.rpc_expose' "$case_root/config/config.json")" = true ] || fail "failed apply did not restore the prior config"
[ "$(status .state)" = failed ] || fail "failed apply status was not recorded"
request '{"id":"78787878787878787878787878787878","changes":[{"port":"monero_rpc","enabled":false}],"requested_at":1000}'
scripts/node-port-control
[ "$(jq -r '.monero.rpc_expose' "$case_root/config/config.json")" = false ] || fail "port change could not be retried after rollback"
[ "$(status .state)" = applied ] || fail "retried port change was not applied"

new_case actual-stack
cp stack configure.sh docker-compose.yml docker-compose.rpc.yml docker-compose.monero-rpc.yml docker-compose.monero-zmq.yml "$case_root/root/"
printf '1.2.3\n' >"$case_root/root/VERSION"
: >"$FAKE_DOCKER_LOG"
request '{"id":"91919191919191919191919191919191","changes":[{"port":"bitcoin_rpc","enabled":false}],"requested_at":1000}'
saved_path=$PATH
PATH=$tmp:$PATH
scripts/node-port-control
PATH=$saved_path
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = false ] || fail "actual stack close did not persist"
grep -q 'compose .*up -d --pull never --no-build --no-deps tor dashboard' "$FAKE_DOCKER_LOG" ||
  fail "actual stack close did not target only Tor and dashboard"
: >"$FAKE_DOCKER_LOG"
request '{"id":"92929292929292929292929292929292","changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.25","host_port":48332}],"requested_at":1000}'
PATH=$tmp:$PATH
scripts/node-port-control
PATH=$saved_path
[ "$(jq -r '.bitcoin.rpc_expose' "$case_root/config/config.json")" = true ] || fail "actual stack open did not persist"
[ "$(jq -r '.bitcoin.rpc_bind_ip' "$case_root/config/config.json")" = 192.168.1.25 ] || fail "actual stack open lost its bind"
[ "$(jq -r '.bitcoin.rpc_port' "$case_root/config/config.json")" = 48332 ] || fail "actual stack open lost its port"
grep -q 'compose .*up -d --pull never --no-build --no-deps tor dashboard' "$FAKE_DOCKER_LOG" ||
  fail "actual stack open did not target only Tor and dashboard"

credential_targets='bitcoin:bitcoin dashboard
monero:monero monero-wallet dashboard
litecoin:litecoin dashboard
dogecoin:dogecoin dashboard
bitcoincash:bitcoincash dashboard
zcash:dashboard'
export FAKE_NODE_LIMITS='1000000000 2147483648 2147483648'
printf '%s\n' "$credential_targets" | while IFS=: read -r node services; do
  : >"$FAKE_DOCKER_LOG"
  PATH=$tmp:$PATH "$case_root/root/stack" apply-credential-settings "$node"
  grep -q "compose .*up -d --pull never --no-build --no-deps $services" "$FAKE_DOCKER_LOG" ||
    fail "$node credential apply targeted the wrong services"
  if [ "$node" != zcash ]; then
    grep -q "update --cpus 1.000000000 --memory 2147483648 --memory-swap 2147483648 $node-id" "$FAKE_DOCKER_LOG" ||
      fail "$node credential apply did not restore its resource limits"
  fi
done
if PATH=$tmp:$PATH "$case_root/root/stack" apply-credential-settings tari >/dev/null 2>&1; then
  fail "passwordless Tari interface accepted a credential apply"
fi
jq '.bitcoin.active=false' "$case_root/config/config.json" >"$case_root/config/config.next"
mv "$case_root/config/config.next" "$case_root/config/config.json"
if PATH=$tmp:$PATH "$case_root/root/stack" apply-credential-settings bitcoin >/dev/null 2>&1; then
  fail "direct credential apply started an inactive node"
fi

grep -q '^ProtectSystem=strict$' packaging/systemd/node-manager-port-control.service || fail "systemd service is not filesystem-hardened"
grep -q '^RestrictAddressFamilies=AF_UNIX AF_NETLINK$' packaging/systemd/node-manager-port-control.service ||
  fail "systemd service lacks its restricted Unix and socket-diagnostic families"
grep -q '^Persistent=true$' packaging/systemd/node-manager-port-control.timer || fail "systemd timer does not catch up after reboot"

echo "PASS: test_port_control.sh"
