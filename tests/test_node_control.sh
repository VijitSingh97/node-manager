#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() { echo "FAIL: $1"; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
root=$tmp/root
state=$tmp/state
config=$tmp/config
dashboard_state=$tmp/dashboard
mkdir -p "$root/scripts" "$state" "$config" "$dashboard_state"
cp scripts/node-control "$root/scripts/"
: >"$root/scripts/node-port-control"
cat >"$root/scripts/node-port-control" <<'EOF'
#!/bin/sh
if [ "${FAKE_PORT_CHECK_RESULT:-}" != "" ]; then
  printf '%s\n' "$FAKE_PORT_CHECK_RESULT"
  exit 1
fi
exit 0
EOF
: >"$root/docker-compose.yml"
cat >"$root/configure.sh" <<'EOF'
#!/bin/sh
set -eu
jq -e 'type=="object"' "$BITCOIN_STACK_CONFIG_DIR/config.json" >/dev/null
zdir=$(jq -r 'select(.zcash.address_index == true) | .zcash.data_dir // empty' "$BITCOIN_STACK_CONFIG_DIR/config.json")
[ -z "$zdir" ] || [ ! -e "$zdir/old.db" ] || exit 1
if [ "${BITCOIN_STACK_VALIDATE_ONLY:-0}" != 1 ]; then
  if [ "${FAKE_CONFIGURE_FAIL_ONCE:-0}" = 1 ] && [ ! -e "$FAKE_CONFIGURE_FAIL_MARKER" ]; then
    : >"$FAKE_CONFIGURE_FAIL_MARKER"
    exit 1
  fi
  printf 'DASHBOARD_PASSWORD=testpassword\n' >"$BITCOIN_STACK_STATE_DIR/.env"
fi
EOF
cat >"$root/stack" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_STACK_LOG"
[ "${FAKE_STACK_INTERRUPT_ON_PULL:-0}" != 1 ] || [ "${1:-}" != pull ] || {
  kill -KILL "$PPID"
  exit 1
}
[ "${FAKE_STACK_FAIL_ONCE:-0}" != 1 ] || {
  if [ ! -e "$FAKE_STACK_FAIL_MARKER" ]; then
    : >"$FAKE_STACK_FAIL_MARKER"
    [ "${1:-}" != apply ] || printf 'PARTIAL=1\n' >"$BITCOIN_STACK_STATE_DIR/.env"
    exit 1
  fi
}
[ "${FAKE_STACK_FAIL_APPLY_ONCE:-0}" != 1 ] || {
  if [ "${1:-}" = apply ] && [ ! -e "$FAKE_STACK_FAIL_MARKER" ]; then
    : >"$FAKE_STACK_FAIL_MARKER"
    exit 1
  fi
}
[ "${FAKE_STACK_FAIL_APPLY:-0}" != 1 ] || [ "${1:-}" != apply ] || exit 1
[ "${FAKE_STACK_FAIL:-0}" != 1 ]
if [ "${1:-}" = apply ] || [ "${1:-}" = approve-rendered-config ]; then
  jq -S . "$BITCOIN_STACK_CONFIG_DIR/config.json" >"$BITCOIN_STACK_STATE_DIR/.approved-config.tmp"
  chmod 600 "$BITCOIN_STACK_STATE_DIR/.approved-config.tmp"
  mv "$BITCOIN_STACK_STATE_DIR/.approved-config.tmp" "$BITCOIN_STACK_STATE_DIR/.approved-config.json"
  cp "$BITCOIN_STACK_STATE_DIR/.env" "$BITCOIN_STACK_STATE_DIR/.approved.env"
  chmod 600 "$BITCOIN_STACK_STATE_DIR/.approved.env"
fi
EOF
chmod +x "$root/configure.sh" "$root/stack" "$root/scripts/node-control" "$root/scripts/node-port-control"
cat >"$tmp/docker" <<'EOF'
#!/usr/bin/env bash
set -eu
if [ "$1" = ps ]; then
  case "$*" in *com.docker.compose.service=dashboard*) [ "${FAKE_DASHBOARD_MISSING:-0}" = 1 ] || printf 'dashboard-id\n' ;; esac
  case "$*" in *com.docker.compose.service=bitcoin* | *com.docker.compose.service=litecoin*)
    all="$*"
    [ "${FAKE_NODE_RUNNING:-0}" != 1 ] || printf '%s-node\n' "${all##*com.docker.compose.service=}" ;;
  esac
  exit 0
fi
if [ "$1" = inspect ]; then
  case "$3" in
    '{{.Id}}') [ "${FAKE_DASHBOARD_MISSING:-0}" = 1 ] || printf 'dashboard-id\n' ;;
    *RestartPolicy*) printf '%s/docker-compose.yml|on-failure|3\n' "$BITCOIN_STACK_ROOT" ;;
    *project.config_files*) printf '%s/docker-compose.yml\n' "$BITCOIN_STACK_ROOT" ;;
  esac
  exit 0
fi
[ "$1" != update ] || {
  printf '%s\n' "$*" >>"$FAKE_STACK_LOG"
  [ "${FAKE_DOCKER_UPDATE_FAIL:-0}" != 1 ] || exit 1
  if [ "${FAKE_DOCKER_UPDATE_FAIL_ONCE:-0}" = 1 ] && [ ! -e "$FAKE_DOCKER_UPDATE_FAIL_MARKER" ]; then
    : >"$FAKE_DOCKER_UPDATE_FAIL_MARKER"
    exit 1
  fi
  exit 0
}
[ "$1" = exec ] || exit 1
shift
[ "${1:-}" != -i ] || shift
if [ "${1:-}" = -u ]; then shift 2; fi
if [ "${1%-node}" != "$1" ] && [ "$2" = sh ] && [ "$3" = -c ]; then
  exec sh -c "${4//\/data/$FAKE_NODE_DATA/${1%-node}}"
fi
[ "$1" = dashboard-id ]
shift
if [ "$1" = sh ] && [ "$2" = -c ]; then
  command=${3//\/state/$FAKE_DASH_STATE}
  exec sh -c "$command"
fi
args=()
for arg in "$@"; do args+=("${arg//\/state/$FAKE_DASH_STATE}"); done
exec "${args[@]}"
EOF
chmod +x "$tmp/docker"
cat >"$config/config.json" <<'EOF'
{"bitcoin":{"enabled":true,"active":true},"monero":{"enabled":true,"active":true},"tari":{"enabled":true,"active":true},"litecoin":{"enabled":false,"data_dir":"/preset/litecoin","resource_profile":"compact","steady_cpus":0.75,"steady_mem_limit_mb":3072},"dogecoin":{"enabled":false},"bitcoincash":{"enabled":false}}
EOF
printf 'DASHBOARD_PASSWORD=testpassword\n' >"$state/.env"
printf 'DASHBOARD_BIND_IP=192.168.1.20\n' >>"$state/.env"
jq -S . "$config/config.json" >"$state/.approved-config.json"
cp "$state/.env" "$state/.approved.env"
export BITCOIN_STACK_ROOT=$root BITCOIN_STACK_STATE_DIR=$state BITCOIN_STACK_CONFIG_DIR=$config
export DOCKER_BIN=$tmp/docker FAKE_DASH_STATE=$dashboard_state FAKE_STACK_LOG=$tmp/stack.log NODE_CONTROL_NOW_EPOCH=1000 FAKE_NODE_DATA=$tmp/node-data
export FAKE_STACK_FAIL_ONCE=0 FAKE_STACK_FAIL_MARKER=$tmp/stack-failed
export FAKE_CONFIGURE_FAIL_ONCE=0 FAKE_CONFIGURE_FAIL_MARKER=$tmp/configure-failed
export FAKE_DOCKER_UPDATE_FAIL_ONCE=0 FAKE_DOCKER_UPDATE_FAIL_MARKER=$tmp/docker-update-failed
export FAKE_DOCKER_UPDATE_FAIL=0
export FAKE_STACK_INTERRUPT_ON_PULL=0
export FAKE_DASHBOARD_MISSING=0
export NODE_CONTROL_ARCH=x86_64 NODE_CONTROL_CPU_FLAGS="fpu sse4_2 avx2 bmi1 bmi2 adx"

request() { printf '%s\n' "$1" >"$dashboard_state/node-control.request"; }
status() { jq -r "$1" "$dashboard_state/node-control.status"; }

request '{"id":"11111111111111111111111111111111","node":"tari","active":false,"confirmation":"deactivate","requested_at":1000}'
"$root/scripts/node-control"
config_real=$(cd "$config" && pwd -P)
jq -e --arg config_path "$config_real/config.json" --arg bitcoin_path "$root/data/bitcoin" '
  .config_path==$config_path and .data_paths.bitcoin==$bitcoin_path and .data_paths.monero!=null
' "$dashboard_state/node-control.capability" >/dev/null || fail "host catalog paths were not published"
[ "$(jq -r .tari.active "$config/config.json")" = false ] || fail "Tari was not deactivated"
grep -qx 'stop tari-wallet tari' "$tmp/stack.log" || fail "Tari wallet was not stopped before its node"
[ "$(status .state)" = applied ] || fail "deactivation status was not applied"
[ -s "$state/.approved.env" ] || fail "deactivation did not approve its rendered environment"
[ "$(jq -r '.bitcoin,.monero,.tari' "$dashboard_state/node-runtime.json" | tr '\n' ' ')" = 'true true false ' ] || fail "runtime state map is wrong"
if [ "$(uname -s)" = Darwin ]; then
  runtime_mode=$(stat -f %Lp "$dashboard_state/node-runtime.json")
else
  runtime_mode=$(stat -c %a "$dashboard_state/node-runtime.json")
fi
[ "$runtime_mode" = 600 ] || fail "runtime state is not private"

request '{"id":"22222222222222222222222222222222","node":"tari","active":true,"confirmation":"","requested_at":1000}'
"$root/scripts/node-control"
[ "$(jq -r .tari.active "$config/config.json")" = true ] || fail "Tari was not reactivated"
grep -qx 'up tari tari-wallet' "$tmp/stack.log" || fail "Tari node was not started before its wallet"
[ "$(jq -r .tari.active "$state/.approved-config.json")" = true ] || fail "node state did not advance approved config"

: >"$tmp/stack.log"
request '{"id":"2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a","node":"tari","active":true,"confirmation":"","requested_at":1000}'
"$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "starting an already-active node was rejected"
grep -qx 'up tari tari-wallet' "$tmp/stack.log" || fail "starting an already-active node did not run stack up"
[ "$(jq -r .tari.active "$config/config.json")" = true ] || fail "start changed the node config"

request '{"id":"22222222222222222222222222222222","node":"tari","active":false,"confirmation":"deactivate","requested_at":1000}'
"$root/scripts/node-control"
[ "$(status .error_code)" = replayed ] || fail "replayed request was accepted"

request '{"id":"33333333333333333333333333333333","node":"litecoin","active":false,"confirmation":"deactivate","requested_at":1000}'
"$root/scripts/node-control"
[ "$(status .error_code)" = node_not_configured ] || fail "disabled node was changed"

printf '%4097s' x >"$dashboard_state/node-control.request"
"$root/scripts/node-control"
[ "$(status .error_code)" = invalid_request ] || fail "oversized request was not rejected"
[ ! -e "$dashboard_state/node-control.processing" ] || fail "oversized request remained stuck"

request '{"id":"44444444444444444444444444444444","node":"monero","active":false,"confirmation":"deactivate","requested_at":1000}'
FAKE_STACK_FAIL_ONCE=1 "$root/scripts/node-control"
[ "$(jq -r .monero.active "$config/config.json")" = true ] || fail "failed apply did not restore config"
[ "$(status .error_code)" = apply_failed ] || fail "failed apply lacked typed status"
tail -n 2 "$tmp/stack.log" | grep -qx 'stop monero-wallet monero' || fail "failed Monero apply was not attempted"
tail -n 2 "$tmp/stack.log" | grep -qx 'up monero monero-wallet' || fail "partial Monero stop was not restored"

request '{"id":"45454545454545454545454545454545","node":"bitcoin","active":false,"confirmation":"deactivate","requested_at":1000}'
FAKE_CONFIGURE_FAIL_ONCE=1 "$root/scripts/node-control"
[ "$(jq -r .bitcoin.active "$config/config.json")" = true ] || fail "failed render did not restore config"
[ "$(status .error_code)" = apply_failed ] || fail "failed render lacked typed status"
! grep -q '^stop bitcoin$' "$tmp/stack.log" || fail "failed render changed a service"

printf 'DASHBOARD_PASSWORD=\n' >"$state/.env"
request '{"id":"55555555555555555555555555555555","node":"bitcoin","active":false,"confirmation":"deactivate","requested_at":1000}'
"$root/scripts/node-control"
[ "$(jq -r .bitcoin.active "$config/config.json")" = false ] || fail "passwordless trusted-LAN node control was blocked"
jq -e '.install_supported and (.boot_supported|not) and (.boot_enabled|not) and (.config_pending|not) and .platform=="source" and .boot_kind=="unsupported"' "$dashboard_state/node-control.capability" >/dev/null || fail "host capability is wrong"

request '{"action":"install","id":"66666666666666666666666666666666","node":"litecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(jq -r '.litecoin.enabled and .litecoin.active and (.litecoin.rpc_expose|not) and .litecoin.steady_cpus==0.75 and .litecoin.steady_mem_limit_mb==3072 and .litecoin.data_dir=="/preset/litecoin"' "$config/config.json")" = true ] || fail "safe Litecoin defaults did not preserve preset settings"
[ "$(jq -r .litecoin.utxo_index "$config/config.json")" = true ] || fail "a dashboard Litecoin install did not turn on its balance index"
if [ "$(status .action)" != install ] || [ "$(status .state)" != applied ]; then
  fail "install status was not applied"
fi
grep -qx 'pull --policy missing' "$tmp/stack.log" || fail "install did not preserve unpublished local images"
request '{"action":"install","id":"66666666666666666666666666666666","node":"litecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = replayed ] || fail "replayed install request was accepted"
request '{"action":"install","id":"67676767676767676767676767676767","node":"dogecoin","requested_at":699}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .id)" = 67676767676767676767676767676767 ] || fail "stale install response lost its request ID"
[ "$(status .error_code)" = invalid_request ] || fail "stale install request was accepted"

request '{"action":"install","id":"68686868686868686868686868686868","node":"bitcoincash","requested_at":1000}'
FAKE_STACK_INTERRUPT_ON_PULL=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control" >/dev/null 2>&1 || true
[ "$(jq -r .bitcoincash.enabled "$config/config.json")" = true ] || fail "interrupted install did not reach its durable transaction"
[ -d "$state/node-control/install-transaction" ] || fail "interrupted install did not retain recovery state"
FAKE_DASHBOARD_MISSING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(jq -r .bitcoincash.enabled "$config/config.json")" = false ] || fail "dashboard liveness gate blocked interrupted install recovery"
[ ! -e "$state/node-control/install-transaction" ] || fail "headless recovery left install recovery state"
[ -e "$dashboard_state/node-control.processing" ] || fail "headless recovery discarded the request before retry"
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "interrupted install was not safely retried"
[ "$(jq -r .bitcoincash.enabled "$config/config.json")" = true ] || fail "retried install was not applied"
[ ! -e "$state/node-control/install-transaction" ] || fail "completed retry left install recovery state"

mkdir "$state/node-control/install-transaction"
cat >"$state/node-control/install-transaction/meta.json" <<'EOF'
{"id":"69696969696969696969696969696969","node":"bitcoincash","phase":"applied","had_config":false,"had_env":false,"had_approved_config":false,"had_approved_env":false}
EOF
printf '%s\n' '{"action":"install","id":"69696969696969696969696969696969","node":"bitcoincash","requested_at":1000}' >"$dashboard_state/node-control.processing"
FAKE_DASHBOARD_MISSING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ -d "$state/node-control/install-transaction" ] || fail "headless applied recovery discarded its completion record"
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
if [ "$(status .id)" != 69696969696969696969696969696969 ] || [ "$(status .state)" != applied ]; then
  fail "applied install recovery did not finalize the original request"
fi
if [ -e "$state/node-control/install-transaction" ] || [ -e "$dashboard_state/node-control.processing" ]; then
  fail "applied install recovery was not finalized"
fi

request '{"action":"launch_at_boot","enabled":true,"id":"76767676767676767676767676767676","requested_at":1000}'
FAKE_DOCKER_UPDATE_FAIL_ONCE=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = apply_failed ] || fail "failed launch policy update lacked typed status"
[ "$(jq -r '.dashboard.launch_at_boot // false' "$config/config.json")" = false ] || fail "failed launch policy update changed config"
grep -q '^update --restart=on-failure:3 dashboard-id$' "$tmp/stack.log" || fail "failed launch policy update did not restore retry count"
request '{"action":"launch_at_boot","enabled":true,"id":"75757575757575757575757575757575","requested_at":1000}'
FAKE_DOCKER_UPDATE_FAIL=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = rollback_failed ] || fail "failed launch policy rollback lacked typed status"
[ "$(jq -r '.dashboard.launch_at_boot // false' "$config/config.json")" = false ] || fail "failed launch policy rollback changed config"
request '{"action":"launch_at_boot","enabled":true,"id":"77777777777777777777777777777777","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(jq -r .dashboard.launch_at_boot "$config/config.json")" = true ] || fail "launch-at-boot setting was not saved"
grep -q '^update --restart=unless-stopped dashboard-id$' "$tmp/stack.log" || fail "running containers did not receive launch policy"
[ "$(status .enabled)" = true ] || fail "launch status omitted its state"
[ "$(jq -r .boot_enabled "$dashboard_state/node-control.capability")" = true ] || fail "enabled launch state was stale after save"
request '{"action":"launch_at_boot","enabled":false,"id":"72727272727272727272727272727272","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=brew NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "launch-at-login disable did not complete"
[ "$(status .enabled)" = false ] || fail "disabled launch status omitted its state"
jq -e '.boot_kind=="login" and (.boot_enabled|not)' "$dashboard_state/node-control.capability" >/dev/null || fail "disabled launch state was stale after save"
BITCOIN_STACK_PACKAGE_MANAGED=brew NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
jq -e '.boot_kind=="login" and (.boot_enabled|not)' "$dashboard_state/node-control.capability" >/dev/null || fail "disabled launch state changed when reopened"

jq -e '.clearnet_over_tor and .inbound_connections' "$dashboard_state/node-control.capability" >/dev/null || fail "network settings did not default on"
request '{"action":"inbound_connections","enabled":true,"id":"71717171717171717171717171717171","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = unchanged ] || fail "unchanged network setting was applied"
rm -f "$FAKE_STACK_FAIL_MARKER"
request '{"action":"inbound_connections","enabled":false,"id":"70707070707070707070707070707070","requested_at":1000}'
FAKE_STACK_FAIL_APPLY_ONCE=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = apply_failed ] || fail "failed network apply lacked typed status"
jq -e '.network.inbound_connections == null' "$config/config.json" >/dev/null || fail "failed network apply changed config"
[ "$(grep -c '^apply$' "$tmp/stack.log")" -ge 2 ] || fail "failed network apply did not re-apply the prior config"
request '{"action":"inbound_connections","enabled":false,"id":"6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d","requested_at":1000}'
FAKE_STACK_FAIL_APPLY=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = rollback_failed ] || fail "failed network rollback lacked typed status"
jq -e '.network.inbound_connections == null' "$config/config.json" >/dev/null || fail "failed network rollback changed config"
: >"$tmp/stack.log"
request '{"action":"clearnet_over_tor","enabled":false,"id":"6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .enabled)" = applied:false ] || fail "clearnet setting was not applied"
[ "$(jq -r .network.clearnet_over_tor "$config/config.json")" = false ] || fail "clearnet setting was not saved"
grep -qx apply "$tmp/stack.log" || fail "network setting did not re-apply the stack"
jq -e '(.clearnet_over_tor|not) and .inbound_connections' "$dashboard_state/node-control.capability" >/dev/null || fail "network capability was stale after save"
request '{"action":"clearnet_over_tor","enabled":"no","id":"6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = invalid_request ] || fail "malformed network request was not rejected"

jq -e '.utxo_index == false' "$dashboard_state/node-control.capability" >/dev/null || fail "balance index did not default off"
request '{"action":"utxo_index","enabled":false,"id":"e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = unchanged ] || fail "unchanged balance index setting was applied"
cp "$config/config.json" "$tmp/config.keep"
jq '.bitcoin.enabled=false' "$tmp/config.keep" >"$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_index","enabled":true,"id":"e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = validation_failed ] || { cat "$dashboard_state/node-control.status"; fail "balance index turned on without Bitcoin"; }
jq -e '.bitcoin.utxo_index == null' "$config/config.json" >/dev/null || fail "rejected balance index request changed config"
cp "$tmp/config.keep" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
rm -f "$FAKE_STACK_FAIL_MARKER"
request '{"action":"utxo_index","enabled":true,"id":"e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2","requested_at":1000}'
FAKE_STACK_FAIL_APPLY_ONCE=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = apply_failed ] || fail "failed balance index apply lacked typed status"
jq -e '.bitcoin.utxo_index == null' "$config/config.json" >/dev/null || fail "failed balance index apply changed config"
request '{"action":"utxo_index","enabled":true,"id":"e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1","requested_at":1000}'
FAKE_STACK_FAIL_APPLY=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = rollback_failed ] || { cat "$dashboard_state/node-control.status"; fail "failed balance index rollback lacked typed status"; }
jq -e '.bitcoin.utxo_index == null' "$config/config.json" >/dev/null || fail "failed balance index rollback changed config"
: >"$tmp/stack.log"
request '{"action":"utxo_index","enabled":true,"id":"e7e7e7e7e7e7e7e7e7e7e7e7e7e7e7e7","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .enabled)" = applied:true ] || fail "balance index was not turned on"
[ "$(jq -r .bitcoin.utxo_index "$config/config.json")" = true ] || fail "balance index setting was not saved"
grep -qx apply "$tmp/stack.log" || fail "balance index did not re-apply the stack"
jq -e '.utxo_index' "$dashboard_state/node-control.capability" >/dev/null || fail "balance index capability was stale after save"
printf '{"seed_state":"seeding"}\n' >"$dashboard_state/utxo-seed.json"
request '{"action":"utxo_index","enabled":false,"id":"e8e8e8e8e8e8e8e8e8e8e8e8e8e8e8e8","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = seed_running ] || fail "balance index turned off during a seed"
[ "$(jq -r .bitcoin.utxo_index "$config/config.json")" = true ] || fail "rejected turn-off changed config"

jq -e '.utxo_seed_supported' "$dashboard_state/node-control.capability" >/dev/null || fail "seed support was not published"
mkdir -p "$FAKE_NODE_DATA/bitcoin" "$FAKE_NODE_DATA/litecoin"
: >"$FAKE_NODE_DATA/bitcoin/utxo-seed.dat"
request '{"action":"utxo_seed","id":"f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1","node":"bitcoin","requested_at":1000}'
FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = seed_running ] || fail "a seed was prepared while one was running"
[ -f "$FAKE_NODE_DATA/bitcoin/utxo-seed.dat" ] || fail "a running seed lost its dump file"
rm -f "$dashboard_state/utxo-seed.json"
request '{"action":"utxo_seed","id":"f2f2f2f2f2f2f2f2f2f2f2f2f2f2f2f2","node":"bitcoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = apply_failed ] || fail "a seed was prepared for a stopped node"
request '{"action":"utxo_seed","id":"f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3","node":"bitcoin","requested_at":1000}'
FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .node)" = applied:bitcoin ] || { cat "$dashboard_state/node-control.status"; fail "bitcoin seed was not prepared"; }
[ -p "$FAKE_NODE_DATA/bitcoin/utxo-seed.dat.incomplete" ] && [ ! -e "$FAKE_NODE_DATA/bitcoin/utxo-seed.dat" ] || fail "the seed pipe was not made fresh"
jq -e 'keys==["id","requested_at"] and .requested_at==1000 and .id=="f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3"' "$dashboard_state/utxo-seed.request" >/dev/null || fail "the bitcoin seed request did not carry the host request id"
jq '.litecoin.utxo_index=false' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_seed","id":"f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4","node":"litecoin","requested_at":1000}'
FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = validation_failed ] || fail "a seed was prepared for an index that is off"
[ ! -e "$dashboard_state/litecoin-utxo-seed.request" ] && [ ! -e "$FAKE_NODE_DATA/litecoin/utxo-seed.dat.incomplete" ] || fail "a refused seed left files"
cp "$config/config.json" "$tmp/config.keep"
jq '.litecoin.utxo_index=true' "$tmp/config.keep" >"$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_seed","id":"f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5","node":"litecoin","requested_at":1000}'
FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] && [ -p "$FAKE_NODE_DATA/litecoin/utxo-seed.dat.incomplete" ] || fail "litecoin seed was not prepared"
jq -e '.requested_at==1000' "$dashboard_state/litecoin-utxo-seed.request" >/dev/null || fail "the litecoin seed request was not written under its prefix"
cp "$tmp/config.keep" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_seed","id":"f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6","node":"dogecoin","requested_at":1000}'
FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = invalid_request ] || fail "a seed request for an unsupported node was accepted"

printf '{"seed_state":"seeding"}\n' >"$dashboard_state/litecoin-utxo-seed.json"
jq '.litecoin.utxo_index=true' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_index","enabled":false,"id":"f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7","node":"litecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = seed_running ] || fail "the Litecoin index turned off during its seed"
rm -f "$dashboard_state/litecoin-utxo-seed.json"
request '{"action":"utxo_index","enabled":false,"id":"f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8","node":"litecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .node)" = applied:litecoin ] || { cat "$dashboard_state/node-control.status"; fail "the Litecoin index was not turned off"; }
[ "$(jq -r .litecoin.utxo_index "$config/config.json"):$(jq -r .bitcoin.utxo_index "$config/config.json")" = false:true ] || fail "turning off the Litecoin index touched the wrong key"
jq -e '(.utxo_index_litecoin|not) and .utxo_index' "$dashboard_state/node-control.capability" >/dev/null || fail "Litecoin index capability was stale after turning it off"
request '{"action":"utxo_index","enabled":true,"id":"f9f9f9f9f9f9f9f9f9f9f9f9f9f9f9f9","node":"litecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] && [ "$(jq -r .litecoin.utxo_index "$config/config.json")" = true ] || fail "the Litecoin index was not turned on"
jq -e '.utxo_index_litecoin' "$dashboard_state/node-control.capability" >/dev/null || fail "Litecoin index capability was stale after turning it on"
jq '.litecoin.enabled=false | .litecoin.utxo_index=false' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_index","enabled":true,"id":"fafafafafafafafafafafafafafafafa","node":"litecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = validation_failed ] || fail "the Litecoin index turned on without Litecoin"
jq '.litecoin.enabled=true' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
rm -f "$dashboard_state/utxo-seed.json"
request '{"action":"utxo_index","enabled":false,"id":"e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .enabled)" = applied:false ] || fail "balance index was not turned off"
jq -e '.utxo_index|not' "$dashboard_state/node-control.capability" >/dev/null || fail "balance index capability stayed on after turning off"
request '{"action":"utxo_index","enabled":"yes","id":"e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = invalid_request ] || fail "malformed balance index request was not rejected"


approved_before=$(shasum -a 256 "$state/.approved-config.json" "$state/.approved.env")
rm -f "$FAKE_STACK_FAIL_MARKER"
request '{"action":"install","id":"78787878787878787878787878787878","node":"dogecoin","requested_at":1000}'
FAKE_STACK_FAIL_ONCE=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(jq -r .dogecoin.enabled "$config/config.json")" = false ] || fail "failed install left Dogecoin configured"
[ "$(status .error_code)" = apply_failed ] || fail "failed install lacked typed status"
[ "$(shasum -a 256 "$state/.approved-config.json" "$state/.approved.env")" = "$approved_before" ] || fail "failed install advanced approved snapshots"
grep -qx 'stop dogecoin' "$tmp/stack.log" || fail "failed install did not stop its partial service"

jq '.dashboard.external_edit=true' "$config/config.json" >"$config/config.tmp" && mv "$config/config.tmp" "$config/config.json"
request '{"action":"install","id":"88888888888888888888888888888888","node":"dogecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = config_pending ] || fail "install silently approved an external config edit"
[ "$(jq -r .dogecoin.enabled "$config/config.json")" = false ] || fail "pending config was mutated by install"
revision=$(jq -S -c . "$config/config.json" | shasum -a 256 | awk '{print $1}')
rm -f "$FAKE_STACK_FAIL_MARKER"
request "{\"action\":\"apply_config\",\"config_revision\":\"$revision\",\"id\":\"89898989898989898989898989898989\",\"requested_at\":1000}"
FAKE_STACK_FAIL_ONCE=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = apply_failed ] || fail "partial config apply did not report a restored failure"
[ "$(jq -r .dashboard.external_edit "$config/config.json")" = true ] || fail "failed config apply discarded the pending edit"
cmp -s "$state/.env" "$state/.approved.env" || fail "failed config apply did not restore the approved environment"
grep -qx up "$tmp/stack.log" || fail "failed config apply did not reconcile the approved runtime"
request "{\"action\":\"apply_config\",\"config_revision\":\"$revision\",\"id\":\"99999999999999999999999999999999\",\"requested_at\":1000}"
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "reviewed pending config was not applied"
cmp -s <(jq -S . "$config/config.json") "$state/.approved-config.json" || fail "applied config was not approved"

rm -f "$config/config.json" "$FAKE_STACK_FAIL_MARKER"
deleted_revision=$(printf '{}\n' | shasum -a 256 | awk '{print $1}')
request "{\"action\":\"apply_config\",\"config_revision\":\"$deleted_revision\",\"id\":\"90909090909090909090909090909090\",\"requested_at\":1000}"
FAKE_STACK_FAIL_ONCE=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = apply_failed ] || fail "deleted pending config failure lacked restored status"
[ ! -e "$config/config.json" ] || fail "failed deleted-config apply recreated the pending file"
cp "$state/.approved-config.json" "$config/config.json"

install_case=0
for interface_case in \
  'bitcoin|.bitcoin.rpc_expose == false' \
  'monero|(.monero.rpc_expose == false and .monero.zmq_expose == false)' \
  'tari|.tari.grpc_expose == false' \
  'litecoin|.litecoin.rpc_expose == false' \
  'dogecoin|.dogecoin.rpc_expose == false' \
  'bitcoincash|.bitcoincash.rpc_expose == false' \
  'zcash|.zcash.rpc_expose == false'; do
  install_case=$((install_case + 1))
  node=${interface_case%%|*}
  closed=${interface_case#*|}
  jq -n '{
    bitcoin:{enabled:false,rpc_expose:true},
    monero:{enabled:false,rpc_expose:true,zmq_expose:true},
    tari:{enabled:false,grpc_expose:true},
    litecoin:{enabled:false,rpc_expose:true},
    dogecoin:{enabled:false,rpc_expose:true},
    bitcoincash:{enabled:false,rpc_expose:true},
    zcash:{enabled:false,rpc_expose:true}
  }' >"$config/config.json"
  jq -S . "$config/config.json" >"$state/.approved-config.json"
  cp "$state/.env" "$state/.approved.env"
  install_id=$(printf '%032x' "$((0xa0 + install_case))")
  request "{\"action\":\"install\",\"id\":\"$install_id\",\"node\":\"$node\",\"requested_at\":1000}"
  BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
  jq -e --arg node "$node" ".[\$node].enabled == true and .[\$node].active == true and ($closed)" "$config/config.json" >/dev/null ||
    fail "$node install did not close every optional LAN interface"
  [ "$(status .state)" = applied ] || fail "$node install status was not applied"
  if [ "$node" = zcash ]; then
    jq -e '.zcash.address_index == true' "$config/config.json" >/dev/null || fail "zcash install did not turn on the address index"
  else
    jq -e '.zcash.address_index == null' "$config/config.json" >/dev/null || fail "$node install set the zcash address index"
  fi
done

jq -n '{bitcoin:{enabled:false},zcash:{enabled:false,address_index:false}}' >"$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
cp "$state/.env" "$state/.approved.env"
request '{"action":"install","id":"a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1","node":"zcash","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
jq -e '.zcash.enabled == true and .zcash.address_index == false' "$config/config.json" >/dev/null || fail "zcash install overrode an explicit address_index false"
jq -e 'has("unsupported_nodes") | not' "$dashboard_state/node-control.capability" >/dev/null || fail "a CPU with BMI2 and ADX was marked unsupported"

jq -n '{bitcoin:{enabled:false},zcash:{enabled:false}}' >"$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
cp "$state/.env" "$state/.approved.env"
request '{"action":"install","id":"a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3","node":"zcash","requested_at":1000}'
NODE_CONTROL_CPU_FLAGS="fpu sse4_2 avx2 bmi1 bmi2" BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
jq -e '.unsupported_nodes == ["zcash"]' "$dashboard_state/node-control.capability" >/dev/null || fail "a CPU without ADX did not mark Zcash unsupported"
[ "$(status '.state + " " + .error_code')" = "rejected unsupported_cpu" ] || fail "Zcash install on a CPU without ADX was not rejected"
jq -e '.zcash.enabled == false' "$config/config.json" >/dev/null || fail "a rejected Zcash install still changed the config"
NODE_CONTROL_ARCH=aarch64 NODE_CONTROL_CPU_FLAGS='' BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
jq -e 'has("unsupported_nodes") | not' "$dashboard_state/node-control.capability" >/dev/null || fail "an ARM host was checked for x86 CPU features"

mkdir -p "$state/zdata"; : >"$state/zdata/old.db"
jq -n --arg d "$state/zdata" '{bitcoin:{enabled:false},zcash:{enabled:false,data_dir:$d}}' >"$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
cp "$state/.env" "$state/.approved.env"
request '{"action":"install","id":"a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2","node":"zcash","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
jq -e '.zcash.enabled == true and .zcash.address_index == null' "$config/config.json" >/dev/null || fail "zcash reinstall over a used data dir did not fall back to index off"

jq -n '{bitcoin:{enabled:false},monero:{enabled:false,node_password:"moneropassword1234567890123456"}}' >"$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.20\n' >"$state/.env"
cp "$state/.env" "$state/.approved.env"
custom_monero_dir="$(cd "$tmp" && pwd -P)/Application Support/nodes/monero"
request "$(jq -nc --arg data_dir "$custom_monero_dir" '{action:"install",data_dir:$data_dir,id:"b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0",interfaces:[{enabled:true,host_port:28081,id:"monero_rpc"},{enabled:false,host_port:18083,id:"monero_zmq"}],node:"monero",requested_at:1000}')"
BITCOIN_STACK_PACKAGE_MANAGED=brew NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
custom_monero_real=$(cd "$custom_monero_dir" && pwd -P)
jq -e --arg data_dir "$custom_monero_real" '.monero.enabled and .monero.active and .monero.data_dir==$data_dir and .monero.rpc_expose and .monero.rpc_bind_ip=="192.168.1.20" and .monero.rpc_port==28081 and (.monero.zmq_expose|not)' "$config/config.json" >/dev/null ||
  fail "install options were not persisted"
[ -d "$custom_monero_dir" ] || fail "custom data folder was not prepared"

jq -n '{bitcoin:{enabled:false},zcash:{enabled:false,node_password:"zcashpassword1234567890123456"}}' >"$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.20\n' >"$state/.env"
cp "$state/.env" "$state/.approved.env"
unsafe_data_dir="$tmp/nonempty-zcash"
mkdir -p "$unsafe_data_dir"
: >"$unsafe_data_dir/existing-chain-data"
before_unsafe=$(shasum -a 256 "$config/config.json")
request "$(jq -nc --arg data_dir "$unsafe_data_dir" '{action:"install",data_dir:$data_dir,id:"b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2",interfaces:[],node:"zcash",requested_at:1000}')"
BITCOIN_STACK_PACKAGE_MANAGED=brew NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
[ "$(status .error_code)" = data_path_invalid ] || fail "non-empty custom data folder was accepted"
[ "$(shasum -a 256 "$config/config.json")" = "$before_unsafe" ] || fail "unsafe data folder changed configuration"

empty_target="$tmp/empty-target"
mkdir "$empty_target"
linked_data_dir="$tmp/linked-zcash"
ln -s "$empty_target" "$linked_data_dir"
request "$(jq -nc --arg data_dir "$linked_data_dir" '{action:"install",data_dir:$data_dir,id:"b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3",interfaces:[],node:"zcash",requested_at:1000}')"
BITCOIN_STACK_PACKAGE_MANAGED=brew NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
[ "$(status .error_code)" = data_path_invalid ] || fail "symbolic-link data folder was accepted"

request "$(jq -nc --arg data_dir "$config/node-data" '{action:"install",data_dir:$data_dir,id:"b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4",interfaces:[],node:"zcash",requested_at:1000}')"
BITCOIN_STACK_PACKAGE_MANAGED=brew NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
[ "$(status .error_code)" = data_path_invalid ] || fail "config-overlapping data folder was accepted"

jq -n '{bitcoin:{enabled:false},litecoin:{enabled:false,node_password:"litecoinpassword1234567890123456"}}' >"$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.20\n' >"$state/.env"
cp "$state/.env" "$state/.approved.env"
before_conflict=$(shasum -a 256 "$config/config.json")
request '{"action":"install","data_dir":"/srv/nodes/litecoin","id":"b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1","interfaces":[{"enabled":true,"host_port":29332,"id":"litecoin_rpc"}],"node":"litecoin","requested_at":1000}'
FAKE_PORT_CHECK_RESULT=port_in_use:litecoin_rpc BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = port_in_use ] || fail "install port conflict lacked typed status"
[ "$(status .port)" = litecoin_rpc ] || fail "install port conflict lost its interface"
[ "$(shasum -a 256 "$config/config.json")" = "$before_conflict" ] || fail "install port conflict changed configuration"

# --- migrate ---
jq '.bitcoincash.enabled=true | .bitcoincash.active=true' "$config/config.json" >"$config/.config.tmp"
mv "$config/.config.tmp" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
mkdir -p "$root/data/bitcoincash"
printf 'chain' >"$root/data/bitcoincash/blocks.dat"
request '{"action":"migrate","data_dir":"/nonexistent-root-elsewhere","id":"80808080808080808080808080808080","node":"tari","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
[ "$(status .error_code)" = not_installed ] || fail "migrate of an uninstalled node was accepted"

request "{\"action\":\"migrate\",\"data_dir\":\"$root/data/bitcoincash\",\"id\":\"81818181818181818181818181818181\",\"node\":\"bitcoincash\",\"requested_at\":1000}"
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
[ "$(status .error_code)" = data_path_invalid ] || fail "migrate into the current folder was accepted: $(status .state) $(status .error_code) $(status .message)"

resolved_tmp=$(cd "$tmp" && pwd -P)
newdisk=$resolved_tmp/newdisk/bitcoincash
request "{\"action\":\"migrate\",\"data_dir\":\"$newdisk\",\"id\":\"82828282828282828282828282828282\",\"node\":\"bitcoincash\",\"requested_at\":1000}"
NODE_CONTROL_FORCE_MIGRATE_MODE=rename BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "same-disk migrate was not applied: $(status .message)"
[ "$(jq -r .bitcoincash.data_dir "$config/config.json")" = "$newdisk" ] || fail "migrate did not update the config data_dir"
[ -f "$newdisk/blocks.dat" ] || fail "migrate did not move the data"
[ ! -e "$root/data/bitcoincash" ] || fail "rename migrate left the source behind"
grep -qx 'stop bitcoincash' "$tmp/stack.log" || fail "migrate did not stop the node"
grep -qx 'apply' "$tmp/stack.log" || fail "migrate did not apply the new config"
[ ! -e "$state/node-control/install-transaction" ] || fail "successful migrate left transaction state"

seconddisk=$resolved_tmp/seconddisk/bitcoincash
request "{\"action\":\"migrate\",\"data_dir\":\"$seconddisk\",\"id\":\"83838383838383838383838383838383\",\"node\":\"bitcoincash\",\"requested_at\":1000}"
FAKE_STACK_FAIL_APPLY_ONCE=1 FAKE_STACK_FAIL_MARKER=$tmp/.migrate-fail-marker NODE_CONTROL_FORCE_MIGRATE_MODE=copy BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
[ "$(status .state)" = failed ] || fail "failed migrate did not report failure: $(status .state) $(status .error_code) $(status .message)"
[ "$(jq -r .bitcoincash.data_dir "$config/config.json")" = "$newdisk" ] || fail "failed migrate did not restore the config"
[ -f "$newdisk/blocks.dat" ] || fail "failed migrate lost the source data"
[ ! -e "$seconddisk" ] || fail "failed migrate left a partial copy behind"

thirddisk=$resolved_tmp/thirddisk/bitcoincash
request "{\"action\":\"migrate\",\"data_dir\":\"$thirddisk\",\"id\":\"84848484848484848484848484848484\",\"node\":\"bitcoincash\",\"requested_at\":1000}"
NODE_CONTROL_FORCE_MIGRATE_MODE=copy BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "copy migrate was not applied: $(status .message)"
[ -f "$thirddisk/blocks.dat" ] || fail "copy migrate did not copy the data"
[ ! -e "$newdisk" ] || fail "copy migrate did not remove the old copy"
[ ! -e "$dashboard_state/node-control.progress" ] || fail "finished migrate left the progress file behind"

install_id=85
for case in pruned:true archival:null off:false; do
  jq --arg case "${case%%:*}" '.bitcoin={"enabled":false} | if $case=="archival" then .bitcoin.prune_mb=0 elif $case=="off" then .bitcoin.utxo_index=false else . end' "$config/config.json" >"$tmp/config.next"
  mv "$tmp/config.next" "$config/config.json"
  jq -S . "$config/config.json" >"$state/.approved-config.json"
  install_id=$((install_id + 1))
  request "{\"action\":\"install\",\"id\":\"${install_id}858585858585858585858585858585\",\"node\":\"bitcoin\",\"requested_at\":1000}"
  BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
  [ "$(status .state)" = applied ] || fail "bitcoin install (${case%%:*}) was not applied: $(status .message)"
  [ "$(jq -c .bitcoin.utxo_index "$config/config.json")" = "${case#*:}" ] || fail "a ${case%%:*} bitcoin install set utxo_index to $(jq -c .bitcoin.utxo_index "$config/config.json")"
done

jq '.litecoin={"enabled":false,"utxo_index":false}' "$config/config.json" >"$tmp/config.next"
mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"install","id":"8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a","node":"litecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "litecoin reinstall was not applied: $(status .message)"
[ "$(jq -c .litecoin.utxo_index "$config/config.json")" = false ] || fail "a Litecoin reinstall turned an explicitly off index back on"

echo "PASS: test_node_control.sh"
