#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
tmp=$(mktemp -d)
service_pid=
trap 'if [ -n "$service_pid" ]; then kill "$service_pid" 2>/dev/null || true; wait "$service_pid" 2>/dev/null || true; fi; rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $1"; exit 1; }
copy_assets() {
  target=$1
  for file in build/bitcoin/bitcoin.conf build/tari/config.toml build/tari/entrypoint.sh \
    build/zcash/zakurad.toml build/zcash/zakurad-address-index.toml build/zcash/healthcheck.sh; do
    mkdir -p "$target/${file%/*}"
    cp "$root/$file" "$target/$file"
  done
}
mkdir -p "$tmp/bin" "$tmp/payload" "$tmp/user home"
copy_assets "$tmp/payload"
cat >"$tmp/bin/uname" <<'SH'
#!/bin/sh
printf '%s\n' "$HOST_TEST_OS"
SH
cat >"$tmp/payload/stack" <<'SH'
#!/bin/sh
printf '%s\n' "$BITCOIN_STACK_CONFIG_DIR" "$BITCOIN_STACK_STATE_DIR" "$BITCOIN_STACK_DATA_DIR" "$BITCOIN_STACK_PACKAGE_MANAGED"
SH
chmod +x "$tmp/bin/uname" "$tmp/payload/stack"
export PATH="$tmp/bin:$PATH"
export HOME="$tmp/user home"
export BITCOIN_STACK_ROOT="$tmp/payload"
export BITCOIN_STACK_PACKAGE_MANAGED=brew
export HOST_TEST_OS=Darwin
unset BITCOIN_STACK_HOME BITCOIN_STACK_CONFIG_DIR BITCOIN_STACK_STATE_DIR BITCOIN_STACK_DATA_DIR
paths=$(scripts/node-manager help)
expected=$(printf '%s\n' "$HOME/Library/Application Support/node-manager/config" "$HOME/Library/Application Support/node-manager/state" "$HOME/Library/Application Support/node-manager/data" brew)
[ "$paths" = "$expected" ] || fail "Mac paths do not preserve spaces or use per-user directories"
export HOST_TEST_OS=Linux XDG_CONFIG_HOME="$tmp/xdg config" XDG_DATA_HOME="$tmp/xdg data"
paths=$(scripts/node-manager help)
expected=$(printf '%s\n' "$XDG_CONFIG_HOME/node-manager" "$XDG_DATA_HOME/node-manager/state" "$XDG_DATA_HOME/node-manager/data" brew)
[ "$paths" = "$expected" ] || fail "Linux Homebrew paths do not honor XDG"
BITCOIN_STACK_CONFIG_DIR=relative scripts/node-manager help >/dev/null 2>&1 && fail "relative state path was accepted"
export BITCOIN_STACK_PACKAGE_MANAGED=1
export BITCOIN_STACK_CONFIG_DIR="$tmp/apt/config" BITCOIN_STACK_STATE_DIR="$tmp/apt/state"
export BITCOIN_STACK_DATA_DIR="$tmp/apt/data"
paths=$(scripts/node-manager help)
expected=$(printf '%s\n' "$BITCOIN_STACK_CONFIG_DIR" "$BITCOIN_STACK_STATE_DIR" "$BITCOIN_STACK_DATA_DIR" 1)
[ "$paths" = "$expected" ] || fail "APT wrapper contract changed"

export BITCOIN_STACK_ROOT=$root BITCOIN_STACK_PACKAGE_MANAGED=brew
export HOST_TEST_OS=Darwin
export BITCOIN_STACK_CONFIG_DIR="$tmp/live config" BITCOIN_STACK_STATE_DIR="$tmp/live state"
export BITCOIN_STACK_DATA_DIR="$tmp/live data"
mkdir -p "$BITCOIN_STACK_CONFIG_DIR" "$BITCOIN_STACK_STATE_DIR" "$BITCOIN_STACK_DATA_DIR"
printf '{"dashboard":{"password":"hostinstalltest"},"wallets":[]}\n' >"$BITCOIN_STACK_CONFIG_DIR/config.json"
"$root/configure.sh" >/dev/null
grep -q '^DASHBOARD_PORT=8080$' "$BITCOIN_STACK_STATE_DIR/.env" || fail "Homebrew defaults to a privileged dashboard port"
printf '{"dashboard":{"password":"hostinstalltest","port":8083},"wallets":[]}\n' >"$BITCOIN_STACK_CONFIG_DIR/config.json"
"$root/configure.sh" >/dev/null
grep -q '^DASHBOARD_PORT=8083$' "$BITCOIN_STACK_STATE_DIR/.env" || fail "custom dashboard port did not render"
for port in 0 65536 '"08080"' '"8083:80"'; do
  printf '{"dashboard":{"password":"hostinstalltest","port":%s}}\n' "$port" >"$BITCOIN_STACK_CONFIG_DIR/config.json"
  "$root/configure.sh" >/dev/null 2>&1 && fail "invalid dashboard port accepted: $port"
done
printf '{"dashboard":{"password":"hostinstalltest","port":8332,"bind_ip":"192.168.1.8"},"bitcoin":{"rpc_expose":true,"rpc_bind_ip":"192.168.1.8"}}\n' >"$BITCOIN_STACK_CONFIG_DIR/config.json"
"$root/configure.sh" >/dev/null 2>&1 && fail "custom dashboard/RPC port collision accepted"

nested="$tmp/unrelated-repo/Cellar/payload"
mkdir -p "$nested"
git -C "$tmp/unrelated-repo" init -q
cp "$root/configure.sh" "$nested/configure.sh"
printf '9.8.7\n' >"$nested/VERSION"
printf '{"dashboard":{"password":"hostinstalltest"},"wallets":[]}\n' >"$BITCOIN_STACK_CONFIG_DIR/config.json"
BITCOIN_STACK_ROOT="$nested" "$nested/configure.sh" >/dev/null
grep -q '^STACK_VERSION=9.8.7$' "$BITCOIN_STACK_STATE_DIR/.env" || fail "packaged payload inherited an unrelated Git checkout version"
if [ "$(/usr/bin/uname -s)" = Darwin ]; then
  BITCOIN_STACK_ROOT="$nested" /bin/bash "$nested/configure.sh" >/dev/null || fail "Mac system Bash cannot render configuration"
fi

payload="$tmp/managed payload"
mkdir -p "$payload/scripts"
copy_assets "$payload"
cp "$root/stack" "$payload/stack"
cat >"$payload/configure.sh" <<'SH'
#!/bin/sh
printf 'configure\n' >>"$HOST_TEST_LOG"
policy=on-failure:5
[ "$(jq -r '.dashboard.launch_at_boot == true' "$BITCOIN_STACK_CONFIG_DIR/config.json")" != true ] || policy=unless-stopped
printf 'COMPOSE_FILE=docker-compose.yml\nSTACK_RESTART_POLICY=%s\n' "$policy" >"$BITCOIN_STACK_STATE_DIR/.env"
SH
for helper in node-control node-port-control node-resource-profile; do
  # shellcheck disable=SC2016
  printf '#!/bin/sh\nprintf "%%s\\n" "%s" >>"$HOST_TEST_LOG"\n' "$helper" >"$payload/scripts/$helper"
done
cat >"$tmp/bin/docker" <<'SH'
#!/bin/sh
case "$1" in
  info) test -f "$HOST_TEST_DOCKER_READY" ;;
  ps) exit 0 ;;
  inspect) exit 1 ;;
  compose) shift; printf 'compose %s\n' "$*" >>"$HOST_TEST_LOG" ;;
  exec) exit 0 ;;
  *) echo "unexpected Docker action" >&2; exit 1 ;;
esac
SH
chmod +x "$payload/configure.sh" "$payload/scripts/"* "$tmp/bin/docker"
export BITCOIN_STACK_ROOT=$payload HOST_TEST_LOG="$tmp/host.log" HOST_TEST_DOCKER_READY="$tmp/docker-ready"
rm -f "$BITCOIN_STACK_CONFIG_DIR/config.json" "$BITCOIN_STACK_STATE_DIR/.env"
"$root/scripts/node-manager" host-service >"$tmp/service.log" 2>&1 &
service_pid=$!
sleep 1
[ ! -f "$HOST_TEST_LOG" ] || fail "service did work without saved configuration"
printf '{"dashboard":{"launch_at_boot":true}}\n' >"$BITCOIN_STACK_CONFIG_DIR/config.json"
touch "$HOST_TEST_DOCKER_READY"
for ((attempt=0; attempt<15; attempt++)); do
  if [ -f "$HOST_TEST_LOG" ] && grep -q '^node-resource-profile$' "$HOST_TEST_LOG"; then break; fi
  sleep 1
done
[ -f "$HOST_TEST_LOG" ] || { cat "$tmp/service.log"; fail "host service did not start configured nodes"; }
for action in configure node-control node-port-control node-resource-profile; do
  grep -q "^$action$" "$HOST_TEST_LOG" || { cat "$tmp/service.log"; fail "host service did not run $action"; }
done
grep -q '^compose .* up ' "$HOST_TEST_LOG" || { cat "$tmp/service.log"; fail "host service did not start configured nodes"; }
"$root/scripts/node-manager" host-service >/dev/null 2>&1 && fail "host service allowed duplicate supervision"
kill "$service_pid"
wait "$service_pid"
service_pid=

: >"$HOST_TEST_LOG"
rm -f "$BITCOIN_STACK_STATE_DIR/.env" "$BITCOIN_STACK_STATE_DIR/.approved.env" "$BITCOIN_STACK_STATE_DIR/.approved-config.json"
printf '{"dashboard":{"launch_at_boot":false}}\n' >"$BITCOIN_STACK_CONFIG_DIR/config.json"
"$root/scripts/node-manager" host-service >"$tmp/service-off.log" 2>&1 &
service_pid=$!
for ((attempt=0; attempt<15; attempt++)); do
  if [ -f "$HOST_TEST_LOG" ] && grep -q '^node-control$' "$HOST_TEST_LOG"; then break; fi
  sleep 1
done
grep -q '^node-control$' "$HOST_TEST_LOG" || { cat "$tmp/service-off.log"; fail "host controls stopped when launch at login was off"; }
! grep -q '^compose .* up ' "$HOST_TEST_LOG" || fail "launch-at-login off started application containers"
kill "$service_pid"
wait "$service_pid"
service_pid=

"$root/scripts/node-manager" upgrade >"$tmp/upgrade.log"
grep -q 'brew upgrade node-manager' "$tmp/upgrade.log" || fail "Homebrew upgrade uses source checkout"
configure_before=$(grep -c '^configure$' "$HOST_TEST_LOG")
"$root/scripts/node-manager" pull >/dev/null
configure_after=$(grep -c '^configure$' "$HOST_TEST_LOG")
[ "$configure_after" -eq "$((configure_before + 1))" ] || fail "pull reused stale image tags instead of rendering the installed version"
grep -q 'node-manager pull' "$tmp/upgrade.log" || fail "Homebrew upgrade instructions omit new images"
echo "PASS: test_host_install.sh"
