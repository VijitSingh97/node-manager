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
cp scripts/node-control scripts/helper-common.sh "$root/scripts/"
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
[ "${1:-}" != --detect-bind-ip ] || { printf '%s\n' "$FAKE_DETECTED_BIND_IP"; exit 0; }
jq -e 'type=="object"' "$BITCOIN_STACK_CONFIG_DIR/config.json" >/dev/null
zdir=$(jq -r 'select(.zcash.address_index == true) | .zcash.data_dir // empty' "$BITCOIN_STACK_CONFIG_DIR/config.json")
[ -z "$zdir" ] || [ ! -e "$zdir/old.db" ] || exit 1
bdir=$(jq -r 'select(.bitcoincash.prune_mb == 0) | .bitcoincash.data_dir // empty' "$BITCOIN_STACK_CONFIG_DIR/config.json")
[ -z "$bdir" ] || [ ! -e "$bdir/blocks/index" ] || [ -e "$bdir/blocks/blk00000.dat" ] || exit 1
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
[ "${1:-}" != up ] || [ "${BITCOIN_STACK_HELPER_START:-}" != 1 ] || printf 'helper-start\n' >>"$FAKE_STACK_LOG"
if [ "${1:-}" = up ] && [ "${BITCOIN_STACK_CONFIG_LOCK_HELD:-0}" != 1 ]; then
  exec 9>"$BITCOIN_STACK_STATE_DIR/.config.lock"
  flock -n 9 || {
    printf 'up-blocked-on-config-lock\n' >>"$FAKE_STACK_LOG"
    exit 75
  }
fi
if [ "${1:-}" = up ] && [ -f "$BITCOIN_STACK_STATE_DIR/.approved-config.json" ] && [ -f "$BITCOIN_STACK_CONFIG_DIR/config.json" ] &&
  ! jq -S . "$BITCOIN_STACK_CONFIG_DIR/config.json" | cmp -s - "$BITCOIN_STACK_STATE_DIR/.approved-config.json"; then
  printf 'up-with-unapproved-config\n' >>"$FAKE_STACK_LOG"
fi
if [ "${1:-}" = stop ] && [ -f "$BITCOIN_STACK_STATE_DIR/.approved-config.json" ] && [ -f "$BITCOIN_STACK_CONFIG_DIR/config.json" ] &&
  jq -S . "$BITCOIN_STACK_CONFIG_DIR/config.json" | cmp -s - "$BITCOIN_STACK_STATE_DIR/.approved-config.json"; then
  printf 'stop-with-change-already-approved\n' >>"$FAKE_STACK_LOG"
fi
if [ "${1:-}" = utxo-chainstate-copy ]; then
  [ -z "${FAKE_CLOCK:-}" ] || echo $(($(cat "$FAKE_CLOCK") + 300)) >"$FAKE_CLOCK"
  [ "${FAKE_COPY_STATUS:-0}" = 0 ] || exit "$FAKE_COPY_STATUS"
fi
[ "${FAKE_STACK_INTERRUPT_ON_PULL:-0}" != 1 ] || [ "${1:-}" != pull ] || {
  kill -KILL "$PPID"
  exit 1
}
[ "${FAKE_STACK_FAIL_ONCE:-0}" != 1 ] || [ "${1:-}" = approve-rendered-config ] || {
  if [ ! -e "$FAKE_STACK_FAIL_MARKER" ]; then
    : >"$FAKE_STACK_FAIL_MARKER"
    [ -z "${FAKE_STACK_STDERR:-}" ] || printf '%s\n' "$FAKE_STACK_STDERR" >&2
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
[ -z "${FAKE_STACK_PULL_PROGRESS:-}" ] || [ "${1:-}" != pull ] || printf '%s\n' "$FAKE_STACK_PULL_PROGRESS" >&2
[ "${FAKE_STACK_FAIL_CMD:-}" != "${1:-}" ] || {
  [ -z "${FAKE_STACK_STDERR:-}" ] || printf '%s\n' "$FAKE_STACK_STDERR" >&2
  exit 1
}
[ "${FAKE_STACK_FAIL:-0}" != 1 ] || exit 1
[ "${1:-}" != apply ] || [ ! -f "${FAKE_RECREATE_TO:-/nonexistent}" ] || cp "$FAKE_RECREATE_TO" "$FAKE_SERVICE_IDS"
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
[ -z "${FAKE_CALL_LOG:-}" ] || printf '%s\n' "$*" >>"$FAKE_CALL_LOG"
if [ "$1" = ps ]; then
  case "$*" in *'{{.State}}'*)
    [ "${FAKE_PS_FAIL:-0}" != 1 ] || exit 1
    [ "${FAKE_DASHBOARD_MISSING:-0}" = 1 ] || printf 'dashboard dashboard-id %s %s/docker-compose.yml\n' "${FAKE_DASHBOARD_STATE:-running}" "$BITCOIN_STACK_ROOT"
    [ ! -f "${FAKE_SERVICE_IDS:-/nonexistent}" ] || awk -v state="${FAKE_START_STATES:+exited}" '{ print $1, $2, (state == "" ? "running" : state) }' "$FAKE_SERVICE_IDS"
    exit 0 ;;
  esac
  case "$*" in *--format*) [ ! -f "${FAKE_SERVICE_IDS:-/nonexistent}" ] || cat "$FAKE_SERVICE_IDS"; exit 0 ;; esac
  case "$*" in *--filter\ volume=*)
    [ "${FAKE_VOLUME_IN_USE:-0}" != 1 ] || printf 'container-using-volume\n'
    exit 0 ;;
  esac
  all="$*"
  fake_service=${all##*com.docker.compose.service=}
  case " ${FAKE_CONTAINERS:-} " in *" ${fake_service%% *} "*) printf '%s-ctr\n' "${fake_service%% *}" ;; esac
  case "$*" in *com.docker.compose.service=dashboard*) [ "${FAKE_DASHBOARD_MISSING:-0}" = 1 ] || printf 'dashboard-id\n' ;; esac
  case "$*" in *com.docker.compose.service=bitcoin* | *com.docker.compose.service=litecoin* | *com.docker.compose.service=dogecoin*)
    all="$*"
    [ "${FAKE_NODE_RUNNING:-0}" != 1 ] || printf '%s-node\n' "${all##*com.docker.compose.service=}" ;;
  esac
  exit 0
fi
if [ "$1" = inspect ]; then
  case "$3" in
    '{{.Id}}') [ "${FAKE_DASHBOARD_MISSING:-0}" = 1 ] || printf 'dashboard-id\n' ;;
    '{{.State.Status}}|{{.State.Error}}') awk -v id="$4" '$1 == id { sub(/^[^ ]+ /, ""); print }' "${FAKE_START_STATES:-/nonexistent}" 2>/dev/null || true ;;
    *State.Error*)
      for id in "${@:4}"; do
        awk -v id="$id" '$1 == id { sub(/^[^ ]+ /, ""); print substr(id, 1, length(id) - 4) "|" $0 }' "${FAKE_START_STATES:-/nonexistent}" 2>/dev/null || true
      done ;;
    *StartedAt*) printf '%s\n' "${FAKE_NODE_STARTED_AT:-}" ;;
    *RestartPolicy*) printf '%s/docker-compose.yml|on-failure|3\n' "$BITCOIN_STACK_ROOT" ;;
    *project.config_files*) printf '%s/docker-compose.yml\n' "$BITCOIN_STACK_ROOT" ;;
  esac
  exit 0
fi
[ "$1" != rm ] || {
  printf 'docker %s\n' "$*" >>"$FAKE_DOCKER_LOG"
  exit 0
}
[ "$1" != volume ] || {
  printf 'docker %s\n' "$*" >>"$FAKE_DOCKER_LOG"
  case "$2" in
    ls) cat "$FAKE_VOLUMES" 2>/dev/null || true ;;
    rm)
      [ "${FAKE_VOLUME_RM_FAIL:-0}" != 1 ] || exit 1
      grep -vxF "$3" "$FAKE_VOLUMES" >"$FAKE_VOLUMES.next" || true
      mv "$FAKE_VOLUMES.next" "$FAKE_VOLUMES" ;;
  esac
  exit 0
}
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
if [ -n "${FAKE_EXEC_FAIL_ONCE:-}" ] && [ ! -e "$FAKE_EXEC_FAIL_ONCE" ]; then
  : >"$FAKE_EXEC_FAIL_ONCE"
  exit 1
fi
if [ -n "${FAKE_EXEC_ERROR:-}" ]; then
  printf 'Error response from daemon: %s\n' "$FAKE_EXEC_ERROR" >&2
  exit 1
fi
shift
[ "${1:-}" != -i ] || shift
if [ "${1:-}" = -u ]; then shift 2; fi
if [ "${1%-node}" != "$1" ] && [ "$2" = sh ] && [ "$3" = -c ]; then
  exec sh -c "${4//\/data/$FAKE_NODE_DATA/${1%-node}}"
fi
[ "$1" = dashboard-id ]
shift
if [ "$1" = sh ] && [ "$2" = -c ]; then
  case "$3" in *node-delete.*) [ "${FAKE_HANDOFF_FAIL:-0}" != 1 ] || exit 1 ;; esac
  case "$3" in *'wc -c < /state/node-control.processing'*)
    if [ "${FAKE_INTAKE_EXEC_FAIL:-0}" = 1 ]; then
      mv "$FAKE_DASH_STATE/node-control.request" "$FAKE_DASH_STATE/node-control.processing" 2>/dev/null || true
      head -c 20 "$FAKE_DASH_STATE/node-control.processing"
      exit 125
    fi ;;
  esac
  command=${3//\/state/$FAKE_DASH_STATE}
  exec sh -c "$command"
fi
args=()
for arg in "$@"; do args+=("${arg//\/state/$FAKE_DASH_STATE}"); done
exec "${args[@]}"
EOF
chmod +x "$tmp/docker"
cat >"$tmp/ifaddrs" <<'EOF'
#!/bin/sh
for address in $FAKE_HOST_ADDRESSES; do printf '2: eth0    inet %s/%s brd 192.168.1.255 scope global eth0\n' "$address" "${FAKE_HOST_PREFIX:-24}"; done
EOF
cat >"$tmp/ifconfig" <<'EOF'
#!/bin/sh
for address in $FAKE_HOST_ADDRESSES; do printf 'en0: flags=8863<UP>\n\tinet %s netmask 0xffffff00 broadcast 192.168.1.255\n' "$address"; done
EOF
chmod +x "$tmp/ifaddrs" "$tmp/ifconfig"
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
export FAKE_DOCKER_LOG=$tmp/stack.log FAKE_VOLUMES=$tmp/volumes FAKE_CONTAINERS='' FAKE_VOLUME_IN_USE=0 FAKE_VOLUME_RM_FAIL=0
export NODE_CONTROL_ARCH=x86_64 NODE_CONTROL_CPU_FLAGS="fpu sse4_2 avx2 bmi1 bmi2 adx"
export IP_BIN=$tmp/ifaddrs IFCONFIG_BIN=$tmp/ifconfig FAKE_HOST_ADDRESSES="127.0.0.1 192.168.1.20" FAKE_DETECTED_BIND_IP=192.168.1.20

request() { rm -f "$dashboard_state"/*utxo-seed.request; printf '%s\n' "$1" >"$dashboard_state/node-control.request"; }
status() { jq -r "$1" "$dashboard_state/node-control.status"; }

request '{"id":"11111111111111111111111111111111","node":"tari","active":false,"confirmation":"deactivate","requested_at":1000}'
"$root/scripts/node-control"
config_real=$(cd "$config" && pwd -P)
jq -e --arg config_path "$config_real/config.json" --arg bitcoin_path "$root/data/bitcoin" '
  .config_path==$config_path and .data_paths.bitcoin==$bitcoin_path and .data_paths.monero!=null
' "$dashboard_state/node-control.capability" >/dev/null || fail "host catalog paths were not published"
[ "$(jq -r .tari.active "$config/config.json")" = false ] || fail "Tari was not deactivated"
grep -qx 'stop tari-wallet tari' "$tmp/stack.log" || fail "Tari wallet was not stopped before its node"
! grep -qx stop-with-change-already-approved "$tmp/stack.log" || fail "deactivation was approved before the node stopped"
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
grep -qx helper-start "$tmp/stack.log" || fail "activation did not mark its start as the helper's"
[ "$(jq -r .tari.active "$state/.approved-config.json")" = true ] || fail "node state did not advance approved config"
! grep -qx up-blocked-on-config-lock "$tmp/stack.log" || fail "activation waited on the config lock it already holds"
! grep -qx up-with-unapproved-config "$tmp/stack.log" || fail "activation started the node from the old approved config"

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

request '{"id":"5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a","node":"bitcoin","active":true,"confirmation":"","requested_at":1000}'
FAKE_INTAKE_EXEC_FAIL=1 "$root/scripts/node-control"
[ -f "$dashboard_state/node-control.processing" ] || fail "a request read cut off partway lost the request"
[ "$(status .id)" != 5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a ] || fail "a failed request read rejected the request"
"$root/scripts/node-control"
[ "$(status .id):$(status .state)" = 5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a:applied ] || fail "a request was not applied after a failed read"

request '{"id":"44444444444444444444444444444444","node":"monero","active":false,"confirmation":"deactivate","requested_at":1000}'
FAKE_STACK_FAIL_ONCE=1 "$root/scripts/node-control"
[ "$(jq -r .monero.active "$config/config.json")" = true ] || fail "failed apply did not restore config"
[ "$(status .error_code)" = apply_failed ] || fail "failed apply lacked typed status"
! grep -qx up-with-unapproved-config "$tmp/stack.log" || fail "rollback restarted the node from the failed change"
grep -vx approve-rendered-config "$tmp/stack.log" | tail -n 2 | head -n 1 | grep -qx 'stop monero-wallet monero' || fail "failed Monero apply was not attempted"
grep -vx approve-rendered-config "$tmp/stack.log" | tail -n 1 | grep -qx 'up monero monero-wallet' || fail "partial Monero stop was not restored"
[ "$(jq -r .monero.active "$state/.approved-config.json")" = true ] || fail "failed apply left the change approved"

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

activation_id=0
power() {
  activation_id=$((activation_id + 1))
  request "$(printf '{"id":"c%031d","node":"%s","active":%s,"confirmation":"%s","requested_at":1000}' "$activation_id" "$1" "$2" "$([ "$2" = true ] && printf '' || printf deactivate)")"
  "$root/scripts/node-control"
}
line_of() { grep -nx -- "$1" "$tmp/stack.log" | head -n 1 | cut -d: -f1; }
no_activation_errors() { ! compgen -G "$state/node-control/.activate-errors.*" >/dev/null; }

: >"$tmp/stack.log"
power litecoin false
[ "$(status .state)" = applied ] || fail "Litecoin was not deactivated"
! grep -q '^pull' "$tmp/stack.log" || fail "deactivation pulled images"
: >"$tmp/stack.log"
power litecoin true
[ "$(status .state)" = applied ] || fail "Litecoin was not activated: $(cat "$dashboard_state/node-control.status")"
[ "$(grep -c '^pull' "$tmp/stack.log")" = 1 ] || fail "activation did not pull exactly once"
[ "$(line_of 'pull --policy missing litecoin')" -lt "$(line_of 'up litecoin')" ] || fail "activation did not pull its image before starting it"
[ -z "$(status '.error_code // ""')" ] && [ -z "$(status '.detail // ""')" ] || fail "a successful activation reported an error"
no_activation_errors || fail "a successful activation left its error file"
power monero false
: >"$tmp/stack.log"
power monero true
[ "$(status .state)" = applied ] || fail "Monero was not activated"
grep -qx 'pull --policy missing monero monero-wallet' "$tmp/stack.log" || fail "Monero activation did not pull both of its images"
[ "$(line_of 'pull --policy missing monero monero-wallet')" -lt "$(line_of 'up monero monero-wallet')" ] || fail "Monero activation did not pull before starting"

power litecoin false
: >"$tmp/stack.log"
FAKE_STACK_FAIL_CMD=pull FAKE_STACK_STDERR="$(printf 'Pulling litecoin\n\033[31mError response from daemon: manifest unknown\033[0m\n')" power litecoin true
[ "$(status .state):$(status .error_code)" = failed:image_unavailable ] || fail "a failed pull lacked its typed status: $(cat "$dashboard_state/node-control.status")"
[ "$(status .message)" = 'Node image could not be downloaded; prior state restored.' ] || fail "a failed pull had the wrong message"
[ "$(status .detail)" = 'Error response from daemon: manifest unknown' ] || fail "a failed pull did not report its last error: $(status .detail)"
[ "$(jq -r .litecoin.active "$config/config.json")" = false ] || fail "a failed pull did not restore config"
[ "$(jq -r .litecoin.active "$state/.approved-config.json")" = false ] || fail "a failed pull left the change approved"
! grep -q '^up ' "$tmp/stack.log" || fail "a failed pull still tried to start the node"
grep -q 'Host: Litecoin activation failed: Error response from daemon: manifest unknown$' "$dashboard_state/events.log" || fail "a failed pull was not logged as a host event"
no_activation_errors || fail "a failed pull left its error file"

: >"$tmp/stack.log"
FAKE_STACK_FAIL_CMD=up FAKE_STACK_STDERR='Error response from daemon: port is already allocated' power litecoin true
[ "$(status .state):$(status .error_code)" = failed:apply_failed ] || fail "a failed start lacked its typed status: $(cat "$dashboard_state/node-control.status")"
[ "$(status .detail)" = 'Error response from daemon: port is already allocated' ] || fail "a failed start did not report its last error"
grep -qx 'pull --policy missing litecoin' "$tmp/stack.log" || fail "a failed start skipped the pull"
[ "$(jq -r .litecoin.active "$config/config.json")" = false ] || fail "a failed start did not restore config"
grep -q 'Host: Litecoin activation failed: Error response from daemon: port is already allocated$' "$dashboard_state/events.log" || fail "a failed start was not logged as a host event"
no_activation_errors || fail "a failed start left its error file"

FAKE_STACK_PULL_PROGRESS='Container litecoin Pulled' FAKE_STACK_FAIL_CMD=up power litecoin true
[ "$(status .error_code)" = apply_failed ] || fail "a silent failed start lacked its typed status"
[ -z "$(status '.detail // ""')" ] || fail "a successful pull's progress was reported as the failure: $(status .detail)"
! grep -q 'Container litecoin Pulled' "$dashboard_state/events.log" || fail "a successful pull's progress reached the event log"

FAKE_STACK_FAIL_CMD=pull FAKE_STACK_STDERR='invalid rpc password abc123' power litecoin true
[ "$(status .error_code)" = image_unavailable ] || fail "a secret-looking pull failure lacked its typed status"
[ -z "$(status '.detail // ""')" ] || fail "a failure line naming a password was reported"
! grep -q 'abc123' "$dashboard_state/events.log" || fail "a failure line naming a password reached the event log"
grep -q 'Host: Litecoin activation failed$' "$dashboard_state/events.log" || fail "a failure without a safe detail was not logged"

power litecoin true
[ "$(status .state)" = applied ] || fail "Litecoin was not activated again"

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

printf 'STACK_RESTART_POLICY=on-failure:5\n' >>"$state/.env"
request '{"action":"launch_at_boot","enabled":true,"id":"76767676767676767676767676767676","requested_at":1000}'
FAKE_DOCKER_UPDATE_FAIL_ONCE=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = apply_failed ] || fail "failed launch policy update lacked typed status"
[ "$(jq -r '.dashboard.launch_at_boot // false' "$config/config.json")" = false ] || fail "failed launch policy update changed config"
grep -q '^update --restart=on-failure:3 dashboard-id$' "$tmp/stack.log" || fail "failed launch policy update did not restore retry count"
printf 'DASHBOARD_PASSWORD=testpassword\nSTACK_RESTART_POLICY=on-failure:5\n' >"$state/.env"
request '{"action":"launch_at_boot","enabled":true,"id":"75757575757575757575757575757575","requested_at":1000}'
FAKE_DOCKER_UPDATE_FAIL=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = rollback_failed ] || fail "failed launch policy rollback lacked typed status"
[ "$(jq -r '.dashboard.launch_at_boot // false' "$config/config.json")" = false ] || fail "failed launch policy rollback changed config"
printf 'DASHBOARD_PASSWORD=testpassword\nSTACK_RESTART_POLICY=on-failure:5\n' >"$state/.env"
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

jq 'del(.dashboard.launch_at_boot)' "$config/config.json" >"$tmp/config.apt" && mv "$tmp/config.apt" "$config/config.json"
printf 'DASHBOARD_PASSWORD=testpassword\nSTACK_RESTART_POLICY=unless-stopped\n' >"$state/.env"
jq -S . "$config/config.json" >"$state/.approved-config.json"
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(jq -r .boot_enabled "$dashboard_state/node-control.capability")" = true ] || fail "an APT host with no launch key did not report launch at boot from its saved policy"
boot_case() {
  jq -S --argjson key "$1" 'if $key == null then del(.dashboard.launch_at_boot) else .dashboard.launch_at_boot=$key end' "$config/config.json" >"$state/.approved-config.json"
  if [ -n "$2" ]; then printf 'DASHBOARD_PASSWORD=testpassword\nSTACK_RESTART_POLICY=%s\n' "$2" >"$state/.env"; else printf 'DASHBOARD_PASSWORD=testpassword\n' >"$state/.env"; fi
  BITCOIN_STACK_PACKAGE_MANAGED=${4:-1} NODE_CONTROL_OS=${5:-Linux} "$root/scripts/node-control"
  [ "$(jq -r .boot_enabled "$dashboard_state/node-control.capability")" = "$3" ] || fail "launch at boot key=$1 policy=$2 reported $(jq -r .boot_enabled "$dashboard_state/node-control.capability"), wanted $3"
}
boot_case true no true
boot_case false unless-stopped false
boot_case null no false
boot_case null on-failure:5 false
boot_case null always true
boot_case null "" true
boot_case null unless-stopped false 0 Linux
request '{"action":"launch_at_boot","enabled":true,"id":"72727272727272727272727272727273","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "an already-enabled launch request blocked the queue"
[ "$(jq -r '.dashboard | has("launch_at_boot")' "$config/config.json")" = false ] || fail "an already-enabled launch request changed config"
request '{"action":"launch_at_boot","enabled":false,"id":"72727272727272727272727272727274","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "an APT host could not turn launch at boot off"
[ "$(jq -r .dashboard.launch_at_boot "$config/config.json")" = false ] || fail "disabling launch at boot on an APT host did not save"

jq -e '.clearnet_over_tor and .inbound_connections' "$dashboard_state/node-control.capability" >/dev/null || fail "network settings did not default on"
request '{"action":"inbound_connections","enabled":true,"id":"71717171717171717171717171717171","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "unchanged network setting blocked the queue"
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
printf 'bitcoin b1\nlitecoin l1\ntor t1\n' >"$tmp/service-ids"
printf 'bitcoin b1\nlitecoin l2\ntor t2\ndashboard d2\n' >"$tmp/service-ids.after"
request '{"action":"inbound_connections","enabled":false,"id":"6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c","requested_at":1000}'
FAKE_SERVICE_IDS=$tmp/service-ids FAKE_RECREATE_TO=$tmp/service-ids.after BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "inbound setting was not applied"
[ "$(status '.restarted|join(",")')" = "dashboard,litecoin,tor" ] || fail "applied status did not list the recreated services: $(status .restarted)"
request '{"action":"inbound_connections","enabled":true,"id":"6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "inbound setting was not restored"
[ "$(status 'has("restarted")')" = false ] || fail "status listed recreated services without a container snapshot"
request '{"action":"clearnet_over_tor","enabled":"no","id":"6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = invalid_request ] || fail "malformed network request was not rejected"

jq -e '.utxo_index == false' "$dashboard_state/node-control.capability" >/dev/null || fail "balance index did not default off"
request '{"action":"utxo_index","enabled":false,"id":"e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "unchanged balance index setting blocked the queue"
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
rm -f "$dashboard_state/utxo-seed.request"
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
request '{"action":"utxo_seed","id":"f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6","node":"zcash","requested_at":1000}'
FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = invalid_request ] || fail "a seed request for an unsupported node was accepted"

# Dogecoin seeds from its chainstate: stack stops the node, copies the chainstate and starts it again before the request is written.
jq '.dogecoin={"enabled":true,"utxo_index":true}' "$tmp/config.keep" >"$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
"$root/scripts/node-control"
jq -e '.utxo_index_dogecoin' "$dashboard_state/node-control.capability" >/dev/null || fail "Dogecoin index capability was not published"
: >"$tmp/stack.log"
request '{"action":"utxo_seed","id":"fbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfb","node":"dogecoin","requested_at":1000}'
mkdir -p "$tmp/clockbin"
cat >"$tmp/clockbin/date" <<EOF
#!/bin/sh
if [ "\$*" = +%s ]; then cat "\$FAKE_CLOCK"; else exec $(command -v date) "\$@"; fi
EOF
chmod +x "$tmp/clockbin/date"
echo 1000 >"$tmp/clock"
FAKE_CLOCK=$tmp/clock PATH="$tmp/clockbin:$PATH" FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .node)" = applied:dogecoin ] || { cat "$dashboard_state/node-control.status"; fail "the Dogecoin seed was not prepared"; }
grep -qx 'utxo-chainstate-copy dogecoin' "$tmp/stack.log" || fail "the Dogecoin seed did not copy the chainstate"
jq -e '.id=="fbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfb"' "$dashboard_state/dogecoin-utxo-seed.request" >/dev/null || fail "the Dogecoin seed request was not written under its prefix"
jq -e '.requested_at==1300' "$dashboard_state/dogecoin-utxo-seed.request" >/dev/null || fail "the Dogecoin seed request was stamped before the copy, so a slow stop leaves it stale"
jq -e '.mode=="chainstate"' "$dashboard_state/dogecoin-utxo-seed.request" >/dev/null || fail "the Dogecoin seed request did not name its chainstate mode"
[ ! -e "$FAKE_NODE_DATA/dogecoin/utxo-seed.dat.incomplete" ] || fail "a seed pipe was made for a chainstate seed"
rm -f "$dashboard_state/dogecoin-utxo-seed.request"
request '{"action":"utxo_seed","id":"fcfcfcfcfcfcfcfcfcfcfcfcfcfcfcfc","node":"dogecoin","requested_at":1000}'
FAKE_COPY_STATUS=2 FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = failed:apply_failed ] && [ ! -e "$dashboard_state/dogecoin-utxo-seed.request" ] || { cat "$dashboard_state/node-control.status"; fail "a failed chainstate copy still requested a seed"; }
status .message | grep -q 'the node was started again' || fail "a failed chainstate copy did not say the node was started again"
request '{"action":"utxo_seed","id":"c7c7c7c7c7c7c7c7c7c7c7c7c7c7c7c7","node":"dogecoin","requested_at":1000}'
FAKE_COPY_STATUS=3 FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = failed:apply_failed ] && [ ! -e "$dashboard_state/dogecoin-utxo-seed.request" ] || { cat "$dashboard_state/node-control.status"; fail "a copy whose node did not start again still requested a seed"; }
status .message | grep -q 'did not start again' || fail "a copy whose node did not start again said otherwise"
request '{"action":"utxo_seed","id":"c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9","node":"dogecoin","requested_at":1000}'
FAKE_COPY_STATUS=130 FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = failed:apply_failed ] && [ ! -e "$dashboard_state/dogecoin-utxo-seed.request" ] || { cat "$dashboard_state/node-control.status"; fail "an interrupted copy still requested a seed"; }
status .message | grep -q 'interrupted' || fail "an interrupted copy was reported as a node that did not start"
request '{"action":"utxo_seed","id":"c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8","node":"dogecoin","requested_at":1000}'
FAKE_COPY_STATUS=1 FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = rejected:validation_failed ] && [ ! -e "$dashboard_state/dogecoin-utxo-seed.request" ] || { cat "$dashboard_state/node-control.status"; fail "a refused chainstate copy was not reported as refused"; }
status .message | grep -q 'Nothing was changed' || fail "a refused chainstate copy did not say nothing was changed"

# Bitcoin Cash seeds the same way, so a pruned node can have its index.
jq '.bitcoincash={"enabled":true,"utxo_index":true}' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
"$root/scripts/node-control"
: >"$tmp/stack.log"
echo 1000 >"$tmp/clock"
request '{"action":"utxo_seed","id":"b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5","node":"bitcoincash","requested_at":1000}'
FAKE_CLOCK=$tmp/clock PATH="$tmp/clockbin:$PATH" FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .node)" = applied:bitcoincash ] || { cat "$dashboard_state/node-control.status"; fail "the Bitcoin Cash seed was not prepared"; }
grep -qx 'utxo-chainstate-copy bitcoincash' "$tmp/stack.log" || fail "the Bitcoin Cash seed did not copy the chainstate"
jq -e '.id=="b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5" and .mode=="chainstate" and .requested_at==1300' "$dashboard_state/bitcoincash-utxo-seed.request" >/dev/null || fail "the Bitcoin Cash seed request was not written with its prefix, mode and post-copy stamp"
[ ! -e "$FAKE_NODE_DATA/bitcoincash/utxo-seed.dat.incomplete" ] || fail "a seed pipe was made for a Bitcoin Cash chainstate seed"
rm -f "$dashboard_state/bitcoincash-utxo-seed.request"
request '{"action":"utxo_seed","id":"b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6","node":"bitcoincash","requested_at":1000}'
FAKE_COPY_STATUS=3 FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = failed:apply_failed ] && [ ! -e "$dashboard_state/bitcoincash-utxo-seed.request" ] || { cat "$dashboard_state/node-control.status"; fail "a Bitcoin Cash copy whose node did not start again still requested a seed"; }
jq 'del(.bitcoincash)' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"

# A seed never starts beside another one or just after the node started, and neither case stops the node.
cp "$config/config.json" "$tmp/config.busy"
jq '.dogecoin={"enabled":true,"utxo_index":true} | del(.litecoin)' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
"$root/scripts/node-control"
: >"$tmp/stack.log"
printf '{"seed_state":"seeding"}\n' >"$dashboard_state/litecoin-utxo-seed.json"
request '{"action":"utxo_seed","id":"d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1","node":"dogecoin","requested_at":1000}'
FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = rejected:busy ] || { cat "$dashboard_state/node-control.status"; fail "a seed started while another index was seeding"; }
! grep -q 'utxo-chainstate-copy' "$tmp/stack.log" || fail "the node was stopped for a seed that was refused as busy"
rm -f "$dashboard_state/litecoin-utxo-seed.json"
request '{"action":"utxo_seed","id":"d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2","node":"dogecoin","requested_at":1000}'
printf '{}\n' >"$dashboard_state/utxo-seed.request"
FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = busy ] || fail "a seed started while another seed request was waiting for the dashboard"
rm -f "$dashboard_state/utxo-seed.request"
request '{"action":"utxo_seed","id":"d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3","node":"dogecoin","requested_at":1000}'
FAKE_NODE_STARTED_AT=1970-01-01T00:16:00.123456789Z FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = rejected:recently_started ] || { cat "$dashboard_state/node-control.status"; fail "a seed started just after the node started"; }
! grep -q 'utxo-chainstate-copy' "$tmp/stack.log" || fail "the node was stopped for a seed that was refused as too soon"
request '{"action":"utxo_seed","id":"d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4","node":"dogecoin","requested_at":1000}'
FAKE_NODE_STARTED_AT=1970-01-01T00:00:01.000000000Z FAKE_NODE_RUNNING=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || { cat "$dashboard_state/node-control.status"; fail "a node that started long ago was refused"; }
rm -f "$dashboard_state/dogecoin-utxo-seed.request"
request '{"action":"utxo_index","node":"litecoin","enabled":true,"id":"d5d5d5d5d5d5d5d5d5d5d5d5d5d5d5d5","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = rejected:validation_failed ] || fail "an index for a node that is not enabled was accepted"
[ "$(status .detail)" = "Balance index setting requires Litecoin to be enabled." ] || fail "the refusal reason was lost: $(status '.detail // ""')"
jq -e '.install_index_supported == true and .utxo_index_blocked.litecoin == "The balance index requires Litecoin to be enabled." and (.utxo_index_blocked | has("dogecoin") | not)' "$dashboard_state/node-control.capability" >/dev/null || fail "the capability did not publish the refused index options"
cp "$tmp/config.busy" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"

# A finished seed's copy is removed, but not while the dashboard may still be reading it.
doge_copy=$tmp/doge-data/utxo-seed-chainstate
mkdir -p "$doge_copy"
cp "$state/.env" "$tmp/env.keep"
printf 'DOGECOIN_ENABLED=1\nDOGECOIN_DATA_DIR=%s\n' "$tmp/doge-data" >>"$state/.env"
"$root/scripts/node-control"
[ -d "$doge_copy" ] || fail "a fresh chainstate copy was removed before the dashboard could take its request"
touch -t 202001010000 "$doge_copy"
printf '{"seed_state":"seeding"}\n' >"$dashboard_state/dogecoin-utxo-seed.json"
"$root/scripts/node-control"
[ -d "$doge_copy" ] || fail "the chainstate copy was removed while its seed was running"
printf '{"seed_state":"ok"}\n' >"$dashboard_state/dogecoin-utxo-seed.json"
: >"$dashboard_state/dogecoin-utxo-seed.request"
"$root/scripts/node-control"
[ -d "$doge_copy" ] || fail "the chainstate copy was removed while its request was pending"
rm -f "$dashboard_state/dogecoin-utxo-seed.request"
FAKE_DASHBOARD_MISSING=1 "$root/scripts/node-control" || true
[ -d "$doge_copy" ] || fail "the chainstate copy was removed while the dashboard did not answer"
mv "$dashboard_state/dogecoin-utxo-seed.json" "$tmp/doge-seed.json"
"$root/scripts/node-control"
[ ! -e "$doge_copy" ] || fail "a copy left by an interrupted copy, with no seed state at all, was kept"
mkdir -p "$doge_copy"
touch -t 202001010000 "$doge_copy"
mv "$tmp/doge-seed.json" "$dashboard_state/dogecoin-utxo-seed.json"
"$root/scripts/node-control"
[ ! -e "$doge_copy" ] || fail "the chainstate copy of a finished seed was kept"
cp "$tmp/env.keep" "$state/.env"
rm -f "$dashboard_state/dogecoin-utxo-seed.json"

bch_copy=$tmp/bch-data/utxo-seed-chainstate
mkdir -p "$bch_copy"
touch -t 202001010000 "$bch_copy"
printf 'BITCOINCASH_ENABLED=1\nBITCOINCASH_DATA_DIR=%s\n' "$tmp/bch-data" >>"$state/.env"
printf '{"seed_state":"seeding"}\n' >"$dashboard_state/bitcoincash-utxo-seed.json"
"$root/scripts/node-control"
[ -d "$bch_copy" ] || fail "the Bitcoin Cash chainstate copy was removed while its seed was running"
printf '{"seed_state":"ok"}\n' >"$dashboard_state/bitcoincash-utxo-seed.json"
"$root/scripts/node-control"
[ ! -e "$bch_copy" ] || fail "the Bitcoin Cash chainstate copy of a finished seed was kept"
cp "$tmp/env.keep" "$state/.env"
rm -f "$dashboard_state/bitcoincash-utxo-seed.json"

jq '.dogecoin.enabled=false | .dogecoin.utxo_index=false' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_index","enabled":true,"id":"fdfdfdfdfdfdfdfdfdfdfdfdfdfdfdfd","node":"dogecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = rejected:validation_failed ] || fail "the Dogecoin index was turned on for a disabled node"
jq '.dogecoin.enabled=true' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_index","enabled":true,"id":"fefefefefefefefefefefefefefefefe","node":"dogecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] && [ "$(jq -r .dogecoin.utxo_index "$config/config.json")" = true ] || fail "the Dogecoin index was not turned on for a pruned node"
cp "$tmp/config.keep" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"

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
FAKE_STACK_STDERR="$(printf 'Pulling dogecoin\n\033[31mError response from daemon: manifest unknown\033[0m\n')" FAKE_STACK_FAIL_ONCE=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(jq -r .dogecoin.enabled "$config/config.json")" = false ] || { cat "$config/config.json" "$dashboard_state/node-control.status"; fail "failed install left Dogecoin configured"; }
[ "$(status .error_code)" = apply_failed ] || fail "failed install lacked typed status"
[ "$(status .detail)" = 'Error response from daemon: manifest unknown' ] || fail "failed install did not report its last error: $(status .detail)"
grep -q 'Host: Dogecoin install failed: Error response from daemon: manifest unknown$' "$dashboard_state/events.log" || fail "failed install was not logged as a host event"
[ "$(shasum -a 256 "$state/.approved-config.json" "$state/.approved.env")" = "$approved_before" ] || fail "failed install advanced approved snapshots"
grep -qx 'stop dogecoin' "$tmp/stack.log" || fail "failed install did not stop its partial service"
rm -f "$FAKE_STACK_FAIL_MARKER"
request '{"action":"install","id":"79797979797979797979797979797979","node":"dogecoin","requested_at":1000}'
FAKE_STACK_STDERR='invalid rpc password abc123' FAKE_STACK_FAIL_ONCE=1 BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = apply_failed ] || fail "second failed install lacked typed status"
[ -z "$(status '.detail // ""')" ] || fail "a failure line naming a password was reported"
! grep -q 'abc123' "$dashboard_state/events.log" || fail "a failure line naming a password reached the event log"

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

index_install() {
  mkdir -p "$tmp/index-data"
  jq -n '{bitcoin:{enabled:false},litecoin:{enabled:false},dogecoin:{enabled:false},bitcoincash:{enabled:false},monero:{enabled:false}}' >"$config/config.json"
  jq -S . "$config/config.json" >"$state/.approved-config.json"
  cp "$state/.env" "$state/.approved.env"
  request "$(jq -nc --arg id "$1" --arg node "$2" --arg dir "$(cd "$tmp" && pwd -P)/index-data/$2" --argjson index "$3" '{action:"install",data_dir:$dir,id:$id,interfaces:[],node:$node,requested_at:1000} + if $index == null then {} else {utxo_index:$index} end')"
  BITCOIN_STACK_PACKAGE_MANAGED=brew NODE_CONTROL_OS=Darwin "$root/scripts/node-control"
}
index_install c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1 dogecoin false
[ "$(status .state)" = applied ] && [ "$(jq -r .dogecoin.utxo_index "$config/config.json")" = false ] || { cat "$dashboard_state/node-control.status"; fail "an install that unticked the balance index still turned it on"; }
index_install c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2 bitcoincash true
[ "$(jq -r '[.bitcoincash.utxo_index, (.bitcoincash.prune_mb // "default")] | join(" ")' "$config/config.json")" = "true default" ] || fail "a Bitcoin Cash install with the index did not stay pruned with the index on"
index_install c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3 litecoin null
[ "$(jq -r .litecoin.utxo_index "$config/config.json")" = true ] || fail "an install without the option lost the default index"
index_install c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4 bitcoin true
[ "$(jq -r .bitcoin.utxo_index "$config/config.json")" = true ] || fail "a Bitcoin install with the index did not turn it on"
index_install c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5 monero true
[ "$(status .error_code)" = invalid_request ] || fail "an index option for a chain without an index was accepted"
request '{"action":"install","data_dir":"/x/y","id":"c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6","interfaces":[],"node":"dogecoin","requested_at":1000,"utxo_index":"yes"}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .error_code)" = invalid_request ] || fail "a non-boolean index option was accepted"

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

flat=$tmp/flat
mkdir -p "$flat"
cp scripts/node-control scripts/helper-common.sh "$root/scripts/node-port-control" "$root/stack" "$root/configure.sh" "$root/docker-compose.yml" "$flat/"
request '{"action":"install","data_dir":"/srv/nodes/litecoin","id":"0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a","interfaces":[{"enabled":true,"host_port":29332,"id":"litecoin_rpc"}],"node":"litecoin","requested_at":1000}'
BITCOIN_STACK_ROOT=$flat FAKE_PORT_CHECK_RESULT=port_in_use:litecoin_rpc BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$flat/node-control"
[ "$(status .error_code)" = port_in_use ] || fail "flat package layout could not run the port check"
no_common=$tmp/no-common
mkdir -p "$no_common"
for helper in node-control node-port-control node-resource-profile; do
  cp "scripts/$helper" "$no_common/"
  if "$no_common/$helper" >/dev/null 2>"$tmp/no-common.err"; then fail "$helper ran without helper-common.sh"; fi
  grep -q 'helper-common.sh is missing' "$tmp/no-common.err" || fail "$helper did not say helper-common.sh is missing"
done
[ "$("$no_common/node-port-control" --check-ports '[]' 2>/dev/null || true)" = port_check_failed ] || fail "port check without helper-common.sh did not fail closed"
seen_ring=$tmp/seen-ring
for n in $(seq 1 300); do (. scripts/helper-common.sh && remember_seen_id "$seen_ring" "id-$n"); done
[ "$(wc -l <"$seen_ring" | tr -d ' ')" = 256 ] && [ "$(head -n 1 "$seen_ring")" = id-45 ] && [ "$(tail -n 1 "$seen_ring")" = id-300 ] || fail "seen ring did not keep the newest 256 ids"
[ "$(stat -c %a "$seen_ring" 2>/dev/null || stat -f %Lp "$seen_ring")" = 600 ] || fail "seen ring is not mode 600"
for line in 'rpcpassword=hunter2' 'Authorization: Bearer abc' 'connect to http://user:pw@host failed' "key $(printf 'a%.0s' $(seq 1 40))" 'my cookie here' 'my onion here' 'pub1abc' 'api key x' 'apikey x' 'seed x' 'token x' 'secret x' 'login x' 'mnemonic x' 'private x' 'phrase x' 'prv x'; do
  printf 'earlier line\n%s\n' "$line" >"$tmp/redact.log"
  [ "$(. scripts/helper-common.sh && redact_last_line fallback <"$tmp/redact.log")" = fallback ] || fail "redact_last_line kept a secret-looking line from stdin: $line"
  [ "$(. scripts/helper-common.sh && redact_last_line fallback "$tmp/redact.log")" = fallback ] || fail "redact_last_line kept a secret-looking line from a file: $line"
done
long=$(printf 'word %.0s' $(seq 1 60))
printf '%s\n' "$long" >"$tmp/redact.log"
[ "$(. scripts/helper-common.sh && redact_last_line fallback <"$tmp/redact.log" | tr -d '\n' | wc -c | tr -d ' ')" = 240 ] || fail "redact_last_line did not cap at 240 characters"
printf '\033[31mboom\033[0m\n' >"$tmp/redact.log"
[ "$(. scripts/helper-common.sh && redact_last_line fallback <"$tmp/redact.log")" = boom ] || fail "redact_last_line kept an ANSI escape"
printf 'Error response from daemon: port is already allocated\n' >"$tmp/redact.log"
[ "$(. scripts/helper-common.sh && redact_last_line fallback <"$tmp/redact.log")" = 'Error response from daemon: port is already allocated' ] || fail "redact_last_line dropped a safe line"
request '{"action":"install","id":"1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b","node":"litecoin","requested_at":1000}'
BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "Litecoin install was not applied: $(status .message)"
[ "$(jq -r .litecoin.steady_mem_limit_mb "$config/config.json")" = 2048 ] || fail "dashboard-installed Litecoin did not get the 2048 MiB default"

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

bch_install() {
  jq --argjson bch "$1" '.bitcoincash=$bch' "$config/config.json" >"$tmp/config.next"
  mv "$tmp/config.next" "$config/config.json"
  jq -S . "$config/config.json" >"$state/.approved-config.json"
  request "{\"action\":\"install\",\"id\":\"$2\",\"node\":\"bitcoincash\",\"requested_at\":1000}"
  BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"
  [ "$(status .state)" = applied ] || fail "bitcoin cash install $2 was not applied: $(status .message)"
}
bch_fresh=$resolved_tmp/bch-fresh
bch_install "{\"enabled\":false,\"data_dir\":\"$bch_fresh\"}" 8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b
[ "$(jq -c '[.bitcoincash.prune_mb, .bitcoincash.utxo_index]' "$config/config.json")" = '[null,true]' ] || fail "a fresh Bitcoin Cash install did not default to a pruned node with its balance index on"
bch_pruned=$resolved_tmp/bch-pruned
mkdir -p "$bch_pruned/blocks/index"
bch_install "{\"enabled\":false,\"data_dir\":\"$bch_pruned\"}" 8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c
[ "$(jq -c '.bitcoincash | [has("prune_mb"), has("utxo_index")]' "$config/config.json")" = '[false,true]' ] || fail "a Bitcoin Cash install over a pruned chain was not pruned with its index on"
bch_install "{\"enabled\":false,\"prune_mb\":10000,\"data_dir\":\"$bch_fresh\"}" 8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d
[ "$(jq -c '[.bitcoincash.prune_mb, .bitcoincash.utxo_index]' "$config/config.json")" = '[10000,true]' ] || fail "a Bitcoin Cash install overrode an explicit prune_mb"
jq -e '.utxo_index_bitcoincash == true' "$dashboard_state/node-control.capability" >/dev/null || fail "Bitcoin Cash index capability was not published on after a pruned install"
jq '.bitcoincash.utxo_index=false' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
"$root/scripts/node-control"
jq -e '.utxo_index_bitcoincash == false' "$dashboard_state/node-control.capability" >/dev/null || fail "Bitcoin Cash index capability was not published off"
request '{"action":"utxo_index","enabled":true,"id":"8e8e8e8e8e8e8e8e8e8e8e8e8e8e8e8e","node":"bitcoincash","requested_at":1000}'
"$root/scripts/node-control"
[ "$(status .state)" = applied ] || fail "the Bitcoin Cash index was refused for a pruned node: $(status .message)"
[ "$(jq -r .bitcoincash.utxo_index "$config/config.json")" = true ] || fail "the Bitcoin Cash index was not turned on for a pruned node"
jq -e '.utxo_index_bitcoincash' "$dashboard_state/node-control.capability" >/dev/null || fail "Bitcoin Cash index capability was stale after turning it on"
jq '.bitcoincash.prune_mb=0' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_index","enabled":false,"id":"8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f","node":"bitcoincash","requested_at":1000}'
"$root/scripts/node-control"
request '{"action":"utxo_index","enabled":true,"id":"9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a","node":"bitcoincash","requested_at":1000}'
"$root/scripts/node-control"
[ "$(status .state)" = applied ] && [ "$(jq -r .bitcoincash.utxo_index "$config/config.json")" = true ] || fail "the Bitcoin Cash index was not turned on again for an archival node: $(status .message)"
jq '.bitcoincash.enabled=false | .bitcoincash.utxo_index=false' "$config/config.json" >"$tmp/config.next" && mv "$tmp/config.next" "$config/config.json"
jq -S . "$config/config.json" >"$state/.approved-config.json"
request '{"action":"utxo_index","enabled":true,"id":"9b9b9b9b9b9b9b9b9b9b9b9b9b9b9b9b","node":"bitcoincash","requested_at":1000}'
"$root/scripts/node-control"
[ "$(status .state):$(status .error_code)" = rejected:validation_failed ] || fail "the Bitcoin Cash index was turned on for a node that is not enabled: $(status .message)"
# --- delete ---
export BITCOIN_STACK_PROJECT_NAME=ds
run_nc() { BITCOIN_STACK_PACKAGE_MANAGED=1 NODE_CONTROL_OS=Linux "$root/scripts/node-control"; }
del_base=$resolved_tmp/deletecase
del_seq=0
del_request() {
  del_seq=$((del_seq + 1))
  del_id=$(printf '%032x' "$((0xd0 + del_seq))")
  request "{\"action\":\"delete\",\"confirmation\":\"${2-$1}\",\"id\":\"$del_id\",\"node\":\"$1\",\"requested_at\":1000}"
}
write_config() {
  jq -S . "$1" >"$config/config.json.next"
  mv "$config/config.json.next" "$config/config.json"
  jq -S . "$config/config.json" >"$state/.approved-config.json"
  cp "$state/.env" "$state/.approved.env"
}
delete_setup() {
  rm -rf "$del_base"
  for node in bitcoin litecoin monero tari zcash; do
    mkdir -p "$del_base/$node/sub"
    printf 'chain' >"$del_base/$node/blocks.dat"
    : >"$del_base/$node/sub/.hidden"
  done
  printf 'DASHBOARD_PASSWORD=testpassword\n' >"$state/.env"
  jq -n --arg d "$del_base" '{
    bitcoin:{enabled:true,active:true,utxo_index:true,prune_mb:5000,data_dir:($d+"/bitcoin")},
    wallets:[{name:"Savings",key:"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"}],
    monero:{enabled:true,active:false,data_dir:($d+"/monero"),node_password:"moneropassword1234567890123456"},
    tari:{enabled:true,active:true,data_dir:($d+"/tari")},
    litecoin:{enabled:true,active:false,utxo_index:true,data_dir:($d+"/litecoin"),node_password:"litecoinpassword1234567890123456"},
    zcash:{enabled:true,active:false,data_dir:($d+"/zcash")},
    dashboard:{launch_at_boot:false}
  }' >"$tmp/config.delete"
  write_config "$tmp/config.delete"
  printf '%s\n' ds_utxo_index ds_utxo_index_litecoin ds_monero_wallets ds_tari_wallet ds_tari_wallet_data ds_unrelated >"$FAKE_VOLUMES"
  : >"$tmp/stack.log"
  for leftover in events.log node-delete.bitcoin node-delete.litecoin node-delete.monero node-delete.tari utxo-seed.json litecoin-utxo-seed.json; do
    rm -f "$dashboard_state/$leftover"
  done
  rm -f "$state/node-control/delete-pending.json" "$FAKE_STACK_FAIL_MARKER"
  rm -rf "$state/resource-profile" "$state/thermal-throttle"
  mkdir -p "$state/resource-profile" "$state/thermal-throttle"
  for marker in litecoin monero monero-wallet bitcoin failing-litecoin failed-litecoin; do : >"$state/resource-profile/$marker"; done
  : >"$state/thermal-throttle/litecoin"
  printf '{"seed_state":"seeded"}\n' >"$dashboard_state/utxo-seed.json"
  printf '{"seed_state":"seeded"}\n' >"$dashboard_state/litecoin-utxo-seed.json"
  config_before=$(jq -S -c . "$config/config.json")
}
delete_touched_nothing() {
  [ "$(jq -S -c . "$config/config.json")" = "$config_before" ] || fail "$1: the config changed"
  ! grep -qE '^docker (volume rm|rm) ' "$tmp/stack.log" || fail "$1: containers or volumes were removed"
  for node in bitcoin litecoin monero tari zcash; do
    [ -f "$del_base/$node/blocks.dat" ] && [ -e "$del_base/$node/sub/.hidden" ] || fail "$1: $node data was touched"
  done
  [ ! -e "$dashboard_state/node-delete.litecoin" ] && [ ! -e "$state/node-control/delete-pending.json" ] || fail "$1: a deletion marker was left"
  ! grep -q deleted "$dashboard_state/events.log" 2>/dev/null || fail "$1: a deleted event was logged"
}
volume_removals() { grep -c '^docker volume rm ' "$tmp/stack.log" || true; }

delete_setup
jq -e '.delete_supported == true' "$dashboard_state/node-control.capability" >/dev/null || fail "delete support was not published"
del_request tari
run_nc
[ "$(status .action):$(status .state):$(status .error_code):$(status .node)" = delete:rejected:node_active:tari ] || fail "an active node was deleted: $(cat "$dashboard_state/node-control.status")"
delete_touched_nothing "active node"

delete_setup
del_request litecoin wrong
run_nc
[ "$(status .action):$(status .error_code)" = delete:invalid_request ] || fail "a wrong confirmation was accepted"
delete_touched_nothing "wrong confirmation"
delete_setup
request '{"action":"delete","id":"d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1","node":"litecoin","requested_at":1000}'
run_nc
[ "$(status .error_code)" = invalid_request ] || fail "a delete without a confirmation was accepted"
delete_touched_nothing "missing confirmation"
delete_setup
request '{"action":"delete","confirmation":"litecoin","data_dir":"/","id":"d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2","node":"litecoin","requested_at":1000}'
run_nc
[ "$(status .error_code)" = invalid_request ] || fail "a delete carrying a data folder was accepted"
delete_touched_nothing "smuggled data folder"

delete_setup
jq '.zcash.enabled=false' "$config/config.json" >"$tmp/config.delete"
write_config "$tmp/config.delete"
config_before=$(jq -S -c . "$config/config.json")
del_request zcash
run_nc
[ "$(status .error_code)" = not_installed ] || fail "an uninstalled node was deleted"
delete_touched_nothing "not installed"

delete_setup
jq -n --arg d "$del_base" '{bitcoin:{enabled:true,active:false,data_dir:($d+"/bitcoin")}}' >"$tmp/config.delete"
write_config "$tmp/config.delete"
config_before=$(jq -S -c . "$config/config.json")
del_request bitcoin
run_nc
[ "$(status .error_code)" = last_node ] || fail "the last installed node was deleted: $(cat "$dashboard_state/node-control.status")"
delete_touched_nothing "last node"
jq -n '{bitcoin:{enabled:true,active:false},litecoin:{enabled:false}}' >"$tmp/config.delete"
write_config "$tmp/config.delete"
del_request bitcoin
run_nc
[ "$(status .error_code)" = last_node ] || fail "an uninstalled node counted as another installed node"

state_real=$(cd "$state" && pwd -P)
root_real=$(cd "$root" && pwd -P)
config_real=$(cd "$config" && pwd -P)
mkdir -p "$state_real/node-control" "$root_real/scripts" "$config_real/protected"
ln -s "$del_base/monero" "$tmp/data-link"
for unsafe in "$tmp/data-link" "$state_real" "$HOME" / "$del_base" "$del_base/monero" "$del_base/litecoin/" "$del_base/./litecoin" /var/lib/docker \
  "$state_real/node-control" "$root_real/scripts" "$config_real/protected" \
  "$root_real/missing-folder" "$state_real/missing-folder" "$del_base/monero/missing-folder"; do
  delete_setup
  jq --arg d "$unsafe" '.litecoin.data_dir=$d' "$config/config.json" >"$tmp/config.delete"
  write_config "$tmp/config.delete"
  config_before=$(jq -S -c . "$config/config.json")
  del_request litecoin
  FAKE_CONTAINERS=litecoin run_nc
  [ "$(status .state):$(status .error_code)" = rejected:data_path_invalid ] || fail "unsafe data folder $unsafe was accepted: $(cat "$dashboard_state/node-control.status")"
  delete_touched_nothing "unsafe data folder $unsafe"
  [ -d "$state_real" ] && [ -f "$state/.env" ] && [ -f "$config/config.json" ] || fail "unsafe data folder $unsafe damaged the stack"
done

if [ "$(id -u)" != 0 ]; then
  delete_setup
  chmod 555 "$del_base/litecoin"
  del_request litecoin
  FAKE_CONTAINERS=litecoin run_nc
  [ "$(status .state):$(status .error_code)" = rejected:data_path_invalid ] || fail "a data folder the host cannot write was accepted"
  chmod 755 "$del_base/litecoin"
  delete_touched_nothing "read-only data folder"
fi

delete_setup
del_request litecoin
FAKE_STACK_FAIL_APPLY_ONCE=1 FAKE_CONTAINERS=litecoin run_nc
[ "$(status .state):$(status .error_code)" = failed:apply_failed ] || fail "a failed apply was not reported: $(cat "$dashboard_state/node-control.status")"
delete_touched_nothing "failed apply"
[ "$(grep -c '^apply$' "$tmp/stack.log")" -ge 2 ] || fail "a failed delete did not re-apply the prior config"
cmp -s <(jq -S . "$config/config.json") "$state/.approved-config.json" || fail "a failed delete left the approved config behind"
delete_setup
del_request litecoin
FAKE_STACK_FAIL_APPLY=1 FAKE_CONTAINERS=litecoin run_nc
[ "$(status .state):$(status .error_code)" = failed:rollback_failed ] || fail "a failed rollback was not reported"
delete_touched_nothing "failed rollback"

delete_setup
del_request litecoin
FAKE_CONTAINERS=litecoin run_nc
[ "$(status .action):$(status .state):$(status .node)" = delete:applied:litecoin ] || fail "delete was not applied: $(cat "$dashboard_state/node-control.status")"
[ "$(status .message)" = "Node deleted." ] || fail "delete status message is wrong"
[ -z "$(ls -A "$del_base/litecoin" 2>/dev/null)" ] || fail "delete left data behind, hidden files included"
jq -e '.litecoin == null' "$config/config.json" >/dev/null || fail "delete left the node's config section"
jq -e '.litecoin == null' "$state/.approved-config.json" >/dev/null || fail "delete left the node in the approved config"
[ "$(jq -S -c 'del(.litecoin)' <<<"$config_before")" = "$(jq -S -c . "$config/config.json")" ] || fail "delete changed another part of the config"
for node in bitcoin monero tari zcash; do
  [ -f "$del_base/$node/blocks.dat" ] && [ -e "$del_base/$node/sub/.hidden" ] || fail "deleting Litecoin touched $node data"
done
{ [ "$(volume_removals)" = 1 ] && grep -qx 'docker volume rm ds_utxo_index_litecoin' "$tmp/stack.log"; } || fail "Litecoin's balance index volume was not removed alone"
{ grep -qx 'ds_utxo_index' "$FAKE_VOLUMES" && grep -qx 'ds_monero_wallets' "$FAKE_VOLUMES" && grep -qx 'ds_unrelated' "$FAKE_VOLUMES"; } || fail "another node's volume was removed"
! grep -qx 'ds_utxo_index_litecoin' "$FAKE_VOLUMES" || fail "the Litecoin volume is still listed"
grep -qx 'docker rm -f litecoin-ctr' "$tmp/stack.log" || fail "the node's container was not removed"
apply_line=$(grep -nx apply "$tmp/stack.log" | head -n 1 | cut -d: -f1)
remove_line=$(grep -nE '^docker (rm|volume rm) ' "$tmp/stack.log" | head -n 1 | cut -d: -f1)
[ -n "$apply_line" ] && [ -n "$remove_line" ] && [ "$apply_line" -lt "$remove_line" ] || fail "data was deleted before the config was applied"
[ ! -e "$state/resource-profile/litecoin" ] && [ ! -e "$state/resource-profile/failing-litecoin" ] && [ ! -e "$state/resource-profile/failed-litecoin" ] && [ ! -e "$state/thermal-throttle/litecoin" ] || fail "delete left resource-profile markers"
[ -e "$state/resource-profile/monero" ] && [ -e "$state/resource-profile/bitcoin" ] || fail "delete cleared another node's markers"
[ ! -e "$dashboard_state/litecoin-utxo-seed.json" ] && [ -e "$dashboard_state/utxo-seed.json" ] || fail "delete did not clear exactly the Litecoin seed state"
[ -e "$dashboard_state/node-delete.litecoin" ] || fail "the dashboard was not told to drop the node's wallets"
[ ! -e "$dashboard_state/node-delete.bitcoin" ] && [ ! -e "$dashboard_state/node-delete.monero" ] || fail "delete marked another node's wallets"
tail -n 1 "$dashboard_state/events.log" | grep -qE '^[0-9T:-]+Z Host: Litecoin deleted$' || fail "the Host event was not logged: $(cat "$dashboard_state/events.log" 2>/dev/null)"
[ ! -e "$state/node-control/delete-pending.json" ] || fail "delete left its recovery marker"
! grep -rqE 'litecoinpassword|moneropassword' "$dashboard_state/events.log" "$dashboard_state/node-control.status" "$tmp/stack.log" || fail "a credential reached a log, event or status"
del_request litecoin
run_nc
[ "$(status .error_code)" = not_installed ] || fail "a deleted node could be deleted twice"
request "{\"action\":\"delete\",\"confirmation\":\"litecoin\",\"id\":\"$del_id\",\"node\":\"litecoin\",\"requested_at\":1000}"
run_nc
[ "$(status .error_code)" = replayed ] || fail "a replayed delete was accepted"

delete_setup
jq '.litecoin.enabled=false' "$config/config.json" >"$tmp/config.delete"
write_config "$tmp/config.delete"
del_request monero
FAKE_CONTAINERS="monero monero-wallet litecoin" run_nc
[ "$(status .state)" = applied ] || fail "Monero delete was not applied: $(cat "$dashboard_state/node-control.status")"
{ grep -qx 'docker rm -f monero-wallet-ctr' "$tmp/stack.log" && grep -qx 'docker rm -f monero-ctr' "$tmp/stack.log"; } || fail "Monero's node and wallet containers were not removed"
! grep -q 'litecoin-ctr' "$tmp/stack.log" || fail "another node's container was removed"
{ [ "$(volume_removals)" = 1 ] && grep -qx 'docker volume rm ds_monero_wallets' "$tmp/stack.log"; } || fail "the Monero wallet volume was not removed alone"
jq -e '.monero == null and .tari.enabled == true' "$config/config.json" >/dev/null || fail "Monero delete changed the wrong config"
[ -z "$(ls -A "$del_base/monero" 2>/dev/null)" ] && [ -f "$del_base/tari/blocks.dat" ] || fail "Monero delete touched the wrong data"
[ -e "$dashboard_state/node-delete.monero" ] || fail "Monero wallets were not marked for removal"
tail -n 1 "$dashboard_state/events.log" | grep -q 'Host: Monero deleted$' || fail "Monero's Host event is wrong"

delete_setup
jq '.tari.active=false' "$config/config.json" >"$tmp/config.delete"
write_config "$tmp/config.delete"
del_request tari
FAKE_CONTAINERS="tari tari-wallet" run_nc
[ "$(status .state)" = applied ] || fail "Tari delete was not applied"
{ [ "$(volume_removals)" = 2 ] && grep -qx 'docker volume rm ds_tari_wallet' "$tmp/stack.log" && grep -qx 'docker volume rm ds_tari_wallet_data' "$tmp/stack.log"; } || fail "the Tari wallet volumes were not removed"
{ grep -qx 'docker rm -f tari-wallet-ctr' "$tmp/stack.log" && grep -qx 'docker rm -f tari-ctr' "$tmp/stack.log"; } || fail "Tari's containers were not removed"

delete_setup
mkdir -p "$del_base/bitcoincash" && printf 'chain' >"$del_base/bitcoincash/blocks.dat"
jq --arg d "$del_base" '.bitcoincash={enabled:true,active:false,prune_mb:0,utxo_index:true,data_dir:($d+"/bitcoincash")}' "$config/config.json" >"$tmp/config.delete"
write_config "$tmp/config.delete"
printf '%s\n' ds_utxo_index_bitcoincash >>"$FAKE_VOLUMES"
printf '{"seed_state":"seeded"}\n' >"$dashboard_state/bitcoincash-utxo-seed.json"
del_request bitcoincash
FAKE_CONTAINERS=bitcoincash run_nc
[ "$(status .state)" = applied ] || fail "Bitcoin Cash delete was not applied: $(cat "$dashboard_state/node-control.status")"
{ [ "$(volume_removals)" = 1 ] && grep -qx 'docker volume rm ds_utxo_index_bitcoincash' "$tmp/stack.log"; } || fail "Bitcoin Cash's balance index volume was not removed alone"
[ ! -e "$dashboard_state/bitcoincash-utxo-seed.json" ] && [ -e "$dashboard_state/litecoin-utxo-seed.json" ] || fail "delete did not clear exactly the Bitcoin Cash replay state"
tail -n 1 "$dashboard_state/events.log" | grep -q 'Host: Bitcoin Cash deleted$' || fail "Bitcoin Cash's Host event is wrong"

delete_setup
mkdir -p "$del_base/dogecoin" && printf 'chain' >"$del_base/dogecoin/blocks.dat"
jq --arg d "$del_base" '.dogecoin={enabled:true,active:false,utxo_index:true,data_dir:($d+"/dogecoin")}' "$config/config.json" >"$tmp/config.delete"
write_config "$tmp/config.delete"
printf '%s\n' ds_utxo_index_dogecoin >>"$FAKE_VOLUMES"
del_request dogecoin
FAKE_CONTAINERS=dogecoin run_nc
[ "$(status .state)" = applied ] || fail "Dogecoin delete was not applied: $(cat "$dashboard_state/node-control.status")"
{ [ "$(volume_removals)" = 1 ] && grep -qx 'docker volume rm ds_utxo_index_dogecoin' "$tmp/stack.log"; } || fail "Dogecoin's balance index volume was not removed alone"

delete_setup
jq '.bitcoin.active=false' "$config/config.json" >"$tmp/config.delete"
write_config "$tmp/config.delete"
del_request bitcoin
run_nc
[ "$(status .state)" = applied ] || fail "Bitcoin delete was not applied: $(cat "$dashboard_state/node-control.status")"
jq -e '.bitcoin == {"enabled":false} and .wallets == null and .litecoin.enabled == true and .dashboard.launch_at_boot == false' "$config/config.json" >/dev/null || fail "Bitcoin delete left settings or wallets behind, or hurt another section: $(cat "$config/config.json")"
{ [ "$(volume_removals)" = 1 ] && grep -qx 'docker volume rm ds_utxo_index' "$tmp/stack.log"; } || fail "the Bitcoin balance index volume was not removed alone"
[ -z "$(ls -A "$del_base/bitcoin" 2>/dev/null)" ] && [ -f "$del_base/litecoin/blocks.dat" ] || fail "Bitcoin delete touched the wrong data"
[ -e "$dashboard_state/node-delete.bitcoin" ] && [ ! -e "$dashboard_state/utxo-seed.json" ] && [ -e "$dashboard_state/litecoin-utxo-seed.json" ] || fail "Bitcoin delete handled dashboard state wrongly"

delete_setup
del_request litecoin
FAKE_VOLUME_IN_USE=1 FAKE_CONTAINERS=litecoin run_nc
[ "$(status .state):$(status .error_code)" = failed:delete_incomplete ] || fail "a volume still in use was not reported: $(cat "$dashboard_state/node-control.status")"
jq -e '.litecoin == null' "$config/config.json" >/dev/null || fail "an incomplete delete restored the config"
[ "$(volume_removals)" = 0 ] || fail "a volume in use was removed"
[ -z "$(ls -A "$del_base/litecoin" 2>/dev/null)" ] && [ -e "$dashboard_state/node-delete.litecoin" ] || fail "an incomplete delete skipped its other steps"
[ ! -e "$state/node-control/delete-pending.json" ] || fail "an incomplete delete kept its marker"
delete_setup
del_request litecoin
FAKE_VOLUME_RM_FAIL=1 FAKE_CONTAINERS=litecoin run_nc
[ "$(status .error_code)" = delete_incomplete ] || fail "a failed volume removal was not reported"

delete_setup
jq 'del(.litecoin)' "$config/config.json" >"$tmp/config.delete"
write_config "$tmp/config.delete"
jq -nc --arg dir "$del_base/litecoin" '{id:"d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3",node:"litecoin",data_dir:$dir}' >"$state/node-control/delete-pending.json"
request '{"action":"delete","confirmation":"litecoin","id":"d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3","node":"litecoin","requested_at":1000}'
mv "$dashboard_state/node-control.request" "$dashboard_state/node-control.processing"
FAKE_CONTAINERS=litecoin run_nc
[ "$(status .id):$(status .state):$(status .node)" = d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3:applied:litecoin ] || fail "an interrupted delete was not finished: $(cat "$dashboard_state/node-control.status")"
[ -z "$(ls -A "$del_base/litecoin" 2>/dev/null)" ] && [ ! -e "$state/node-control/delete-pending.json" ] && [ ! -e "$dashboard_state/node-control.processing" ] || fail "interrupted delete recovery left work behind"
{ [ -e "$dashboard_state/node-delete.litecoin" ] && grep -qx 'docker volume rm ds_utxo_index_litecoin' "$tmp/stack.log"; } || fail "interrupted delete recovery skipped volumes or wallets"
[ -f "$del_base/monero/blocks.dat" ] || fail "interrupted delete recovery touched another node"
delete_setup
jq 'del(.litecoin)' "$config/config.json" >"$tmp/config.delete"
write_config "$tmp/config.delete"
jq -nc --arg dir "$state_real" '{id:"d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4",node:"litecoin",data_dir:$dir}' >"$state/node-control/delete-pending.json"
request '{"action":"delete","confirmation":"litecoin","id":"d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4","node":"litecoin","requested_at":1000}'
mv "$dashboard_state/node-control.request" "$dashboard_state/node-control.processing"
FAKE_CONTAINERS=litecoin run_nc
[ "$(status .error_code)" = delete_incomplete ] && [ -f "$state/.env" ] && [ -f "$config/config.json" ] || fail "recovery trusted an unsafe data folder from its marker"

delete_setup
del_request litecoin
FAKE_HANDOFF_FAIL=1 FAKE_CONTAINERS=litecoin run_nc
[ "$(status .state):$(status .error_code)" = failed:delete_incomplete ] || fail "a failed wallet handoff was not reported: $(cat "$dashboard_state/node-control.status")"
[ -f "$state/node-control/delete-pending.json" ] && [ ! -e "$dashboard_state/node-delete.litecoin" ] || fail "a failed wallet handoff did not keep the retry marker"
[ -z "$(ls -A "$del_base/litecoin" 2>/dev/null)" ] || fail "a failed wallet handoff skipped the data removal"
FAKE_CONTAINERS=litecoin run_nc
[ "$(status .state)" = applied ] && [ -e "$dashboard_state/node-delete.litecoin" ] && [ ! -e "$state/node-control/delete-pending.json" ] || fail "the wallet handoff was not retried: $(cat "$dashboard_state/node-control.status")"
unset BITCOIN_STACK_PROJECT_NAME

jq '.tari.active=false | .bitcoin.active=true' "$config/config.json" >"$tmp/recover-config.json"
mv "$tmp/recover-config.json" "$config/config.json"
rm -f "$state/node-control/recover" "$dashboard_state/events.log" "$dashboard_state/node-control.request" "$dashboard_state/node-control.processing"
printf 'tor tor-ctr\ntari tari-ctr\ntari-wallet tari-wallet-ctr\nbitcoin bitcoin-ctr\nlitecoin litecoin-ctr\ndogecoin dogecoin-ctr\n' >"$tmp/recover-ids"
cat >"$tmp/start-states" <<'EOF'
tor-ctr unless-stopped|false|failed to set up container networking: driver failed programming external connectivity on endpoint tor (1a56a25): failed to bind host port 192.168.1.20:80/tcp: cannot assign requested address
tari-ctr unless-stopped|false|failed to bind host port 192.168.1.20:18142/tcp: cannot assign requested address
tari-wallet-ctr unless-stopped|false|failed to bind host port 192.168.1.20:18143/tcp: cannot assign requested address
bitcoin-ctr unless-stopped|false|
litecoin-ctr on-failure|false|failed to bind host port 192.168.1.20:9332/tcp: cannot assign requested address
dogecoin-ctr unless-stopped|false|error while creating mount source path '/mnt/chain/dogecoin': mkdir /mnt/chain: read-only file system
EOF
recover_nc() { : >"$tmp/stack.log"; FAKE_SERVICE_IDS=$tmp/recover-ids FAKE_START_STATES=$tmp/start-states run_nc; }
recover_nc
[ "$(grep -c -- '--force-recreate' "$tmp/stack.log")" = 1 ] || fail "only Tor should be recreated after its start failed: $(cat "$tmp/stack.log")"
grep -qx 'up --force-recreate tor' "$tmp/stack.log" || fail "Tor was not recreated after its start failed"
grep -q 'Host: tor failed to start (cannot assign requested address); recreated it.$' "$dashboard_state/events.log" || fail "the recovered start was not logged"
[ ! -s "$state/node-control/recover" ] || fail "a recovered start left a retry entry"
FAKE_STACK_FAIL=1 recover_nc
grep -qx 'tor 1030 30' "$state/node-control/recover" || fail "a failed recovery was not scheduled for a retry"
[ "$(grep -c 'retrying until it starts' "$dashboard_state/events.log")" = 1 ] || fail "a failed recovery was not logged once"
NODE_CONTROL_NOW_EPOCH=1029 FAKE_STACK_FAIL=1 recover_nc
! grep -q -- '--force-recreate' "$tmp/stack.log" || fail "a recovery retried before its backoff"
grep -qx 'tor 1030 30' "$state/node-control/recover" || fail "a waiting recovery lost its retry time"
NODE_CONTROL_NOW_EPOCH=1029 FAKE_PS_FAIL=1 recover_nc
grep -qx 'tor 1030 30' "$state/node-control/recover" || fail "a Docker listing failure dropped the retry schedule"
NODE_CONTROL_NOW_EPOCH=1030 FAKE_STACK_FAIL=1 recover_nc
grep -qx 'tor 1090 60' "$state/node-control/recover" || fail "a repeated recovery failure did not back off"
[ "$(grep -c 'retrying until it starts' "$dashboard_state/events.log")" = 1 ] || fail "a repeated recovery failure was logged again"
printf 'tor 1090 600\n' >"$state/node-control/recover"
NODE_CONTROL_NOW_EPOCH=1090 FAKE_STACK_FAIL=1 recover_nc
grep -qx 'tor 1690 600' "$state/node-control/recover" || fail "the recovery backoff was not capped at 10 minutes"
NODE_CONTROL_NOW_EPOCH=1690 recover_nc
grep -qx 'up --force-recreate tor' "$tmp/stack.log" || fail "a due recovery was not retried"
[ ! -s "$state/node-control/recover" ] || fail "a retried recovery did not clear its entry"
[ "$(grep -c 'recreated it' "$dashboard_state/events.log")" = 2 ] || fail "the retried recovery was not logged"
: >"$tmp/docker-calls"
NODE_CONTROL_NOW_EPOCH=1700 FAKE_CALL_LOG=$tmp/docker-calls recover_nc
heartbeat_line=$(grep -n '^exec -i -u 1000 dashboard-id sh -c' "$tmp/docker-calls" | head -n 1 | cut -d: -f1)
recover_line=$(grep -n '^inspect --format {{index' "$tmp/docker-calls" | head -n 1 | cut -d: -f1)
[ -n "$heartbeat_line" ] && [ -n "$recover_line" ] && [ "$heartbeat_line" -lt "$recover_line" ] ||
  fail "the capability heartbeat waited for container recovery: $(cat "$tmp/docker-calls")"
printf '900.12 1800.00\n' >"$tmp/uptime"
if NODE_CONTROL_UPTIME_FILE=$tmp/uptime FAKE_EXEC_FAIL_ONCE=$tmp/exec-failed run_nc; then
  fail "a run that could not reach the dashboard long after boot reported success"
fi
printf '120.50 240.00\n' >"$tmp/uptime"
NODE_CONTROL_NOW_EPOCH=1800 NODE_CONTROL_UPTIME_FILE=$tmp/uptime FAKE_EXEC_FAIL_ONCE=$tmp/exec-failed-at-boot run_nc ||
  fail "a run right after boot gave up while the dashboard was starting"
[ -e "$tmp/exec-failed-at-boot" ] && [ "$(jq -r .timestamp "$dashboard_state/node-control.capability")" = 1800 ] ||
  fail "a run right after boot did not publish once the dashboard answered"
printf '900.12 1800.00\n' >"$tmp/uptime"
dashboard_files() { find "$dashboard_state" -type f -exec cksum {} + | sort; }
dashboard_before=$(dashboard_files)
for down in 'exited|container dashboard-id is not running' 'running|container dashboard-id is not running' \
  'restarting|Container dashboard-id is restarting, wait until the container is running' 'removing|No such container: dashboard-id'; do
  NODE_CONTROL_NOW_EPOCH=1900 NODE_CONTROL_UPTIME_FILE=$tmp/uptime FAKE_DASHBOARD_STATE=${down%%|*} FAKE_EXEC_ERROR=${down#*|} run_nc 2>"$tmp/stderr" ||
    fail "a run while the dashboard was ${down%%|*} (${down#*|}) failed its unit"
  grep -qF "${down#*|}" "$tmp/stderr" || fail "a skipped run hid the Docker error: $(cat "$tmp/stderr")"
done
[ "$(dashboard_files)" = "$dashboard_before" ] || fail "a run while the dashboard was down wrote dashboard state"
if NODE_CONTROL_UPTIME_FILE=$tmp/uptime FAKE_EXEC_ERROR='Container dashboard-id is paused, unpause the container before exec' run_nc 2>/dev/null; then
  fail "a run that could not reach a paused dashboard reported success"
fi

jq -S . "$config/config.json" >"$state/.approved-config.json"
mkdir -p "$tmp/jq-shim"
printf '#!/bin/sh\nprintf x >>"%s"\nexec "%s" "$@"\n' "$tmp/jq-calls" "$(command -v jq)" >"$tmp/jq-shim/jq"
chmod +x "$tmp/jq-shim/jq"
: >"$tmp/jq-calls"
: >"$tmp/docker-calls"
NODE_CONTROL_NOW_EPOCH=2000 FAKE_CALL_LOG=$tmp/docker-calls PATH="$tmp/jq-shim:$PATH" run_nc
[ "$(jq -r .timestamp "$dashboard_state/node-control.capability")" = 2000 ] || fail "an idle tick did not refresh the host capability"
{ [ "$(grep -c '^[a-z]' "$tmp/docker-calls")" = 2 ] && grep -q '^ps -a ' "$tmp/docker-calls" && grep -q '^exec -i -u 1000 dashboard-id sh -c' "$tmp/docker-calls"; } ||
  fail "an idle tick made more than one listing and one exec: $(cat "$tmp/docker-calls")"
[ "$(wc -c <"$tmp/jq-calls" | tr -d ' ')" -le 4 ] || fail "an idle tick ran jq $(wc -c <"$tmp/jq-calls" | tr -d ' ') times"
request '{"id":"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f","node":"tari","active":true,"confirmation":"","requested_at":2000}'
NODE_CONTROL_NOW_EPOCH=2000 run_nc
[ "$(status .id):$(status .state)" = 7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f:applied ] && [ ! -e "$dashboard_state/node-control.request" ] ||
  fail "a triggered run did not take the waiting request"

timer_seconds() { value=$(sed -n "s/^$1=\([0-9]*\)s$/\1/p" "$2"); printf '%s\n' "${value:-$3}"; }
timer=packaging/systemd/node-manager-node-control.timer
age=$(sed -n 's/^const nodeControlCapabilityAge = \([0-9]*\) \* time.Second$/\1/p' build/dashboard/node_control.go)
interval=$(timer_seconds OnUnitActiveSec "$timer" '')
slack=$(($(timer_seconds AccuracySec "$timer" 60) + $(timer_seconds RandomizedDelaySec "$timer" 0)))
[ -n "$age" ] && [ -n "$interval" ] && [ $((interval + slack + 20)) -le "$age" ] ||
  fail "the node-control heartbeat (${interval:-?} s plus ${slack} s of timer slack) does not refresh the capability 20 s inside the dashboard's ${age:-?} s limit"
[ "$interval" -ge 60 ] || fail "the node-control timer runs more often than its heartbeat needs"
unit=packaging/systemd/node-manager-node-control.path
grep -qx 'Unit=node-manager-node-control.service' "$unit" || fail "the request watch does not start node-control"
grep -qx 'StartLimitBurst=30' packaging/systemd/node-manager-node-control.service || fail "a burst of requests would trip the start limit and stop the request watch"
! grep -q '^PathExists' "$unit" || fail "a level-triggered watch would loop on a request the helper leaves in place"
for project in node-manager-apt bitcoin-starter-stack-apt; do
  grep -qx "PathChanged=/var/lib/docker/volumes/${project}_dashboard_state/_data/node-control.request" "$unit" ||
    fail "the request watch misses the $project dashboard volume"
done
{ grep -qx '      - dashboard_state:/state' docker-compose.yml && grep -q "fresh_project='node-manager-apt'" stack &&
  grep -q 'legacy_project=bitcoin-starter-stack-apt' stack; } || fail "the dashboard volume or APT project name changed; update the request watch"


rm -f "$state/node-control/recover" "$dashboard_state/events.log"
printf 'tor tor-ctr\n' >"$tmp/drift-ids"
printf 'tor-ctr running|\n' >"$tmp/drift-states"
jq '.dashboard.bind_ip="auto" | .bitcoin.rpc_expose=true | .bitcoin.rpc_bind_ip="192.168.1.20" | .monero.zmq_bind_ip="192.168.1.99"' "$state/.approved-config.json" >"$tmp/drift-approved.json"
mv "$tmp/drift-approved.json" "$state/.approved-config.json"
jq '.bitcoin.dbcache_mb=999' "$state/.approved-config.json" >"$config/config.json"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.20\n' >"$state/.env"
drift_nc() {
  : >"$tmp/stack.log"
  NODE_CONTROL_NOW_EPOCH=$1 FAKE_HOST_ADDRESSES=$2 FAKE_DETECTED_BIND_IP=$3 FAKE_CONTAINERS=tor FAKE_START_STATES=$tmp/drift-states run_nc
}
drift_moved() { grep -qx 'up tor dashboard' "$tmp/stack.log"; }
drift_state() { cat "$state/node-control/bind-address" 2>/dev/null || true; }
drift_nc 1000 "127.0.0.1 192.168.1.20" 192.168.1.20
[ "$(drift_state)" = 192.168.1.20 ] || fail "a present address was not remembered"
! drift_moved || fail "a present address was treated as changed"
drift_nc 1000 "127.0.0.1 192.168.1.30" 192.168.1.30
! drift_moved || fail "a new address was acted on without waiting"
drift_nc 1059 "127.0.0.1 192.168.1.30" 192.168.1.30
! drift_moved || fail "a new address was acted on before a minute"
drift_nc 1062 "127.0.0.1 192.168.1.20" 192.168.1.20
drift_nc 1070 "127.0.0.1 192.168.1.30" 192.168.1.30
drift_nc 1125 "127.0.0.1 192.168.1.30" 192.168.1.30
! drift_moved || fail "a brief return of the old address did not restart the wait"
drift_nc 1129 "127.0.0.1 192.168.1.31" 192.168.1.31
drift_nc 1185 "127.0.0.1 192.168.1.31" 192.168.1.31
! drift_moved || fail "a second new address did not restart the wait"
drift_nc 1190 127.0.0.1 127.0.0.1
drift_nc 1300 127.0.0.1 127.0.0.1
[ "$(drift_state)" = 192.168.1.20 ] || fail "a lost network forgot the last LAN address"
! drift_moved || fail "a lost network moved the dashboard to loopback"
drift_nc 1300 "127.0.0.1 192.168.1.30" 192.168.1.30
printf '192.168.1.20 192.168.1.30 \n' >"$state/node-control/bind-address"
drift_nc 1301 "127.0.0.1 192.168.1.30" 192.168.1.30
[ "$(drift_state)" = '192.168.1.20 192.168.1.30 1301' ] || fail "a truncated drift record was not restarted"
drift_nc 1361 "127.0.0.1 192.168.1.30" 192.168.1.30
drift_moved || fail "a lasting address change did not restart the dashboard: $(cat "$tmp/stack.log")"
! grep -Eq '^(apply|approve-rendered-config)' "$tmp/stack.log" || fail "an address change approved the pending config"
[ "$(jq -c '[.bitcoin.rpc_bind_ip, .monero.zmq_bind_ip, .bitcoin.dbcache_mb]' "$config/config.json")" = '["192.168.1.30","192.168.1.99",999]' ] ||
  fail "an address change did not move only the LAN listeners on the old address in the pending config"
[ "$(jq -c '[.bitcoin.rpc_bind_ip, .monero.zmq_bind_ip, .bitcoin.dbcache_mb]' "$state/.approved-config.json")" = '["192.168.1.30","192.168.1.99",null]' ] ||
  fail "an address change did not move only the LAN listeners on the old address in the approved config"
grep -q 'Host: Moved the dashboard from 192.168.1.20 to 192.168.1.30, this host.s current LAN address. Moved LAN listeners on the same LAN with it: bitcoin RPC.$' "$dashboard_state/events.log" ||
  fail "the move did not name the moved listeners: $(cat "$dashboard_state/events.log")"
[ ! -e "$state/.paused-listeners" ] || fail "a listener on the same LAN was paused"
drift_nc 1400 "127.0.0.1 192.168.1.30" 192.168.1.30
! drift_moved || fail "a failed move was retried without a change"
[ "$(grep -c 'Moved' "$dashboard_state/events.log")" = 1 ] || fail "a failed move was logged again"

rm -f "$dashboard_state/events.log"
printf '192.168.1.20\n' >"$state/node-control/bind-address"
for drift_config in "$config/config.json" "$state/.approved-config.json"; do
  jq '.bitcoin.rpc_bind_ip="192.168.1.20"' "$drift_config" >"$tmp/drift-config.json"
  mv "$tmp/drift-config.json" "$drift_config"
done
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.30\n' >"$state/.env"
printf 'tor-ctr exited|failed to bind host port 192.168.1.20:8332/tcp: cannot assign requested address\n' >"$tmp/drift-states"
drift_nc 2000 "127.0.0.1 192.168.1.30" 192.168.1.30
NODE_CONTROL_OS=Darwin FAKE_SERVICE_IDS=$tmp/drift-ids drift_nc 2060 "127.0.0.1 192.168.1.30" 192.168.1.30
drift_moved || fail "LAN listeners left behind by an earlier render were not moved"
[ "$(jq -r .bitcoin.rpc_bind_ip "$state/.approved-config.json")" = 192.168.1.30 ] || fail "a listener left behind kept the old address"
grep -q 'Host: Moved the dashboard from 192.168.1.20 to 192.168.1.30, this host.s current LAN address. Moved LAN listeners on the same LAN with it: bitcoin RPC.$' "$dashboard_state/events.log" ||
  fail "the listener move was not logged: $(cat "$dashboard_state/events.log")"
drift_nc 2070 "127.0.0.1 192.168.1.30" 192.168.1.30
[ "$(drift_state)" = 192.168.1.30 ] || fail "the new address was not remembered after the move"

rm -f "$dashboard_state/events.log"
for drift_config in "$config/config.json" "$state/.approved-config.json"; do
  jq '.bitcoin.rpc_bind_ip="192.168.1.20" | .tari.grpc_expose=true | .tari.grpc_bind_ip="192.168.1.20" | .monero.zmq_expose=true | .monero.zmq_bind_ip="192.168.1.20"' "$drift_config" >"$tmp/drift-config.json"
  mv "$tmp/drift-config.json" "$drift_config"
done
cp "$config/config.json" "$tmp/drift-pending.json"
cp "$state/.approved-config.json" "$tmp/drift-approved-before.json"
printf '192.168.1.20\n' >"$state/node-control/bind-address"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.20\n' >"$state/.env"
printf 'tor-ctr running|\n' >"$tmp/drift-states"
drift_nc 2500 "127.0.0.1 10.0.0.5" 10.0.0.5
drift_nc 2560 "127.0.0.1 10.0.0.5" 10.0.0.5
drift_moved || fail "a move to another network did not restart the dashboard"
cmp -s "$config/config.json" "$tmp/drift-pending.json" || fail "listeners followed the host onto another network in config.json"
cmp -s "$state/.approved-config.json" "$tmp/drift-approved-before.json" || fail "listeners followed the host onto another network in the approved config"
[ "$(cat "$state/.paused-listeners")" = 192.168.1.20 ] || fail "listeners on a lost network were not paused"
grep -q 'Host: Moved the dashboard from 192.168.1.20 to 10.0.0.5, this host.s current LAN address. Paused LAN listeners whose address is not on this network: bitcoin RPC, monero ZMQ, tari gRPC. Re-point them from Settings to use this network.$' "$dashboard_state/events.log" ||
  fail "the pause did not name the paused listeners: $(cat "$dashboard_state/events.log")"
[ "$(grep -c 'Host:' "$dashboard_state/events.log")" = 1 ] || fail "a move to another network logged more than one event"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=10.0.0.5\n' >"$state/.env"
drift_nc 2600 "127.0.0.1 10.0.0.5" 10.0.0.5
! drift_moved || fail "a paused listener was published while its address was gone"
[ -s "$state/.paused-listeners" ] || fail "a paused listener was dropped while its address was gone"
drift_nc 2610 "127.0.0.1 10.0.0.5 192.168.1.20" 10.0.0.5
! drift_moved || fail "a paused listener was published the moment its address returned"
drift_nc 2669 "127.0.0.1 10.0.0.5 192.168.1.20" 10.0.0.5
! drift_moved || fail "a paused listener was published before its address held for a minute"
drift_nc 2670 "127.0.0.1 10.0.0.5 192.168.1.20" 10.0.0.5
drift_moved || fail "a paused listener was not published again when its address returned"
[ ! -e "$state/.paused-listeners" ] || fail "a published listener stayed paused"
grep -q 'Host: Published LAN listeners again now that their address is back: bitcoin RPC, monero ZMQ, tari gRPC.$' "$dashboard_state/events.log" ||
  fail "publishing again was not logged"

printf '192.168.1.20\n' >"$state/node-control/bind-address"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.20\n' >"$state/.env"
FAKE_HOST_PREFIX=32 drift_nc 2700 "127.0.0.1 192.168.1.30" 192.168.1.30
FAKE_HOST_PREFIX=32 drift_nc 2760 "127.0.0.1 192.168.1.30" 192.168.1.30
[ "$(jq -r .bitcoin.rpc_bind_ip "$state/.approved-config.json")" = 192.168.1.20 ] || fail "a /32 address was treated as the same LAN"
[ "$(cat "$state/.paused-listeners")" = 192.168.1.20 ] || fail "listeners were not paused next to a /32 address"
rm -f "$state/.paused-listeners"

rm -f "$dashboard_state/events.log"
for drift_config in "$config/config.json" "$state/.approved-config.json"; do
  jq '.bitcoin.rpc_expose=false | .tari.grpc_expose=false | .monero.zmq_expose=false' "$drift_config" >"$tmp/drift-config.json"
  mv "$tmp/drift-config.json" "$drift_config"
done
printf '192.168.1.20\n' >"$state/node-control/bind-address"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.30\n' >"$state/.env"
drift_nc 2800 "127.0.0.1 192.168.1.30" 192.168.1.30
drift_nc 2860 "127.0.0.1 192.168.1.30" 192.168.1.30
drift_moved || fail "the dashboard kept its old address after the failed-start recovery re-rendered first"
grep -q 'Host: Moved the dashboard from 192.168.1.20 to 192.168.1.30, this host.s current LAN address.$' "$dashboard_state/events.log" ||
  fail "the dashboard move after an earlier render was not logged: $(cat "$dashboard_state/events.log")"

printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.20\n' >"$state/.env"
rm -f "$state/node-control/bind-address"
printf 'tor-ctr exited|\n' >"$tmp/drift-states"
drift_nc 3000 "127.0.0.1 192.168.1.30" 192.168.1.30
drift_nc 3060 "127.0.0.1 192.168.1.30" 192.168.1.30
! drift_moved || fail "a stopped stack was started by an address change"
[ "$(drift_state)" = 192.168.1.20 ] || fail "a stopped stack forgot the last LAN address"

printf 'tor-ctr running|\n' >"$tmp/drift-states"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=127.0.0.1\n' >"$state/.env"
rm -f "$state/node-control/bind-address" "$dashboard_state/events.log"
drift_nc 3100 "127.0.0.1 192.168.1.30" 192.168.1.30
NODE_CONTROL_OS=Darwin drift_nc 3160 "127.0.0.1 192.168.1.30" 192.168.1.30
drift_moved || fail "a loopback fallback was not moved to the LAN once it appeared"
grep -q 'Host: Moved the dashboard from 127.0.0.1 to 192.168.1.30' "$dashboard_state/events.log" || fail "the loopback move was not logged"

rm -f "$state/node-control/bind-address" "$dashboard_state/events.log"
cp "$config/config.json" "$tmp/drift-pending.json"
jq '.dashboard.bind_ip="192.168.1.20"' "$state/.approved-config.json" >"$tmp/drift-approved.json"
mv "$tmp/drift-approved.json" "$state/.approved-config.json"
printf 'DASHBOARD_PASSWORD=testpassword\nDASHBOARD_BIND_IP=192.168.1.20\n' >"$state/.env"
drift_nc 4000 "127.0.0.1 192.168.1.30" 192.168.1.30
drift_nc 4060 "127.0.0.1 192.168.1.30" 192.168.1.30
drift_nc 4120 "127.0.0.1 192.168.1.30" 192.168.1.30
! drift_moved || fail "a fixed dashboard address was changed"
[ "$(grep -c 'Host: dashboard.bind_ip 192.168.1.20 is not on this host' "$dashboard_state/events.log")" = 1 ] || fail "a missing fixed address was not warned about once"
cmp -s "$config/config.json" "$tmp/drift-pending.json" || fail "a missing fixed address touched the config"
drift_nc 4130 "127.0.0.1 192.168.1.20" 192.168.1.20
[ "$(drift_state)" = 192.168.1.20 ] || fail "a returned fixed address kept its warning record"

[ "$(. scripts/helper-common.sh && node_label bitcoincash && node_label monero)" = "$(printf 'Bitcoin Cash\nMonero')" ] || fail "node_label did not name the nodes"
helper_events=$tmp/helper-events
helper_docker=$tmp/helper-docker
printf '#!/bin/sh\ncat >> "%s"\n' "$helper_events" >"$helper_docker"
chmod +x "$helper_docker"
(. scripts/helper-common.sh && docker_bin=$helper_docker dashboard_id=x host_event "hello" && docker_bin=$helper_docker dashboard_id=x host_event "late" 2030-01-02T03:04:05)
[ "$(sed -n 1p "$helper_events" | sed 's/^[0-9T:-]*Z Host: /Z /')" = "Z hello" ] && [ "$(sed -n 1p "$helper_events" | cut -c20)" = Z ] && [ "$(sed -n 2p "$helper_events")" = "2030-01-02T03:04:05Z Host: late" ] || fail "host_event wrote the wrong event line"
helper_ip=$tmp/helper-ip
printf '#!/bin/sh\necho "2: eth0    inet 192.168.1.20/24 brd 192.168.1.255 scope global eth0"\n' >"$helper_ip"
chmod +x "$helper_ip"
(. scripts/helper-common.sh && ip_bin=$helper_ip host_os=Linux host_has_ipv4 192.168.1.20 && ! ip_bin=$helper_ip host_os=Linux host_has_ipv4 192.168.1.2) || fail "host_has_ipv4 matched the wrong address"
helper_other_ip=$tmp/helper-other-ip
printf '#!/bin/sh\necho "2: eth0    inet 10.9.9.9/24 scope global eth0"\n' >"$helper_other_ip"
chmod +x "$helper_other_ip"
helper_ifconfig=$tmp/helper-ifconfig
printf '#!/bin/sh\necho "\tinet 192.168.1.20 netmask 0xffffff00"\n' >"$helper_ifconfig"
chmod +x "$helper_ifconfig"
(. scripts/helper-common.sh && ip_bin=$helper_other_ip ifconfig_bin=$helper_ifconfig host_os=Darwin host_has_ipv4 192.168.1.20 && ! ip_bin=$helper_other_ip ifconfig_bin=$helper_ifconfig host_os=Darwin host_has_ipv4 192.168.1.2) || fail "host_has_ipv4 did not use ifconfig on Darwin"

echo "PASS: test_node_control.sh"
