#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  [ ! -f "${log:-}" ] || sed -n '1,160p' "$log"
  exit 1
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fake=$tmp/bin
mkdir -p "$fake" "$tmp/etc" "$tmp/state" "$tmp/data"
log=$tmp/docker.log
systemctl_log=$tmp/systemctl.log
running=$tmp/running
printf '%s\n' tor bitcoin dashboard >"$running"

cat >"$fake/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
case "${1:-}" in
  info) [ "${FAKE_DOCKER_DOWN:-0}" != 1 ] ;;
  ps)
    service=
    for arg in "$@"; do case "$arg" in label=com.docker.compose.service=*) service=${arg##*=} ;; esac; done
    [ "${FAKE_PS_FAIL_SERVICE:-}" != "$service" ] || exit 70
    if grep -qx "$service" "$FAKE_RUNNING_FILE" 2>/dev/null; then
      printf '%s-id\n' "$service"
      [ "${FAKE_DUPLICATE_SERVICE:-}" != "$service" ] || printf '%s-duplicate-id\n' "$service"
    fi
    exit 0
    ;;
  inspect)
    format=${3:-}
    id=${4:-}
    service=${id%-id}
    [ "${FAKE_INSPECT_FAIL_SERVICE:-}" != "$service" ] || exit 73
    case "$format" in
      *project.config_files*)
        if [ "${FAKE_FOREIGN_SERVICE:-}" = "$service" ]; then printf '/foreign/docker-compose.yml\n'; else printf '%s/docker-compose.yml\n' "$BITCOIN_STACK_ROOT"; fi
        ;;
      *project.working_dir*) printf '%s\n' "$BITCOIN_STACK_STATE_DIR" ;;
      *State.Running*) grep -qx "$service" "$FAKE_RUNNING_FILE" 2>/dev/null && printf 'true\n' || printf 'false\n' ;;
    esac
    ;;
  image) exit 0 ;;
  compose)
    case " $* " in
      *' pull '*)
        [ "${FAKE_PULL_FAIL:-0}" != 1 ] || exit 71
        if [ -n "${FAKE_RUNNING_AFTER_PULL:-}" ]; then printf '%s\n' $FAKE_RUNNING_AFTER_PULL >"$FAKE_RUNNING_FILE"; fi
        ;;
      *' up -d '*)
        if [ "${FAKE_UP_FAIL_ONCE:-0}" = 1 ] && [ ! -e "$FAKE_DOCKER_LOG.up-failed" ]; then touch "$FAKE_DOCKER_LOG.up-failed"; exit 72; fi
        ;;
    esac
    ;;
esac
EOF
chmod +x "$fake/docker"
cat >"$fake/systemctl" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_SYSTEMCTL_LOG"
EOF
chmod +x "$fake/systemctl"
export PATH="$fake:$PATH" FAKE_DOCKER_LOG=$log FAKE_RUNNING_FILE=$running
export FAKE_SYSTEMCTL_LOG=$systemctl_log
export BITCOIN_STACK_ROOT=$PWD BITCOIN_STACK_CONFIG_DIR=$tmp/etc BITCOIN_STACK_STATE_DIR=$tmp/state BITCOIN_STACK_DATA_DIR=$tmp/data
export BITCOIN_STACK_PACKAGE_MANAGED=apt BITCOIN_STACK_PROJECT_NAME=node-manager-apt

printf '%s\n' '{"bitcoin":{"enabled":true,"resource_profile":"compact"},"dashboard":{"password":"packageupgradetest","bind_ip":"auto","launch_at_boot":false}}' >"$tmp/etc/config.json"
BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.116 ./configure.sh
printf 'null\n' >"$tmp/state/.approved-config.json"
cp "$tmp/state/.env" "$tmp/state/.approved.env"
custom_data=$tmp/custom-do-not-create/bitcoin
sed -i.bak -e 's/^STACK_VERSION=.*/STACK_VERSION=old-release/' -e "s|^BITCOIN_DATA_DIR=.*|BITCOIN_DATA_DIR=$custom_data|" "$tmp/state/.env"
cp "$tmp/state/.env" "$tmp/state/.approved.env"
rm -f "$tmp/state/.env.bak"
export BITCOIN_STACK_PACKAGE_MANAGED=1

printf '%s\n' '{"bitcoin":{"enabled":false},"monero":{"enabled":true}}' >"$tmp/etc/config.json"
pending=$(cat "$tmp/etc/config.json")
: >"$log"
./stack package-upgrade >/dev/null
[ "$(cat "$tmp/etc/config.json")" = "$pending" ] || fail "package upgrade applied pending configuration"
[ ! -e "$tmp/custom-do-not-create" ] || fail "package upgrade ran first-boot data setup"
grep -q '^null$' "$tmp/state/.approved-config.json" || fail "package upgrade replaced a legacy approved-config sentinel"
grep -q "^BITCOIN_DATA_DIR=$custom_data$" "$tmp/state/.env" || fail "package upgrade replaced a custom data path"
grep -q '^COMPOSE_PROFILES=bitcoin$' "$tmp/state/.env" || fail "package upgrade replaced the approved node selection"
grep -q 'compose .* pull tor bitcoin dashboard' "$log" || fail "package upgrade did not pull the running subset"
grep -q 'compose .* up -d --pull never --no-build --no-deps tor bitcoin dashboard' "$log" || fail "package upgrade did not recreate the running subset"
grep -q '^STACK_VERSION=' "$tmp/state/.env" || fail "package upgrade did not render the installed version"
grep -q '^enable --now bitcoin-starter-stack-resource-profile.timer$' "$systemctl_log" || fail "package upgrade did not enable post-sync resource controls"
grep -q '^enable --now bitcoin-starter-stack-port-control.timer$' "$systemctl_log" || fail "package upgrade did not enable LAN port controls"
grep -q '^enable --now bitcoin-starter-stack-node-control.timer$' "$systemctl_log" || fail "package upgrade did not enable node controls"

: >"$log"
printf '%s\n' tor dashboard >"$running"
./stack package-upgrade >/dev/null
grep -q ' pull tor dashboard' "$log" || fail "package upgrade pulled a stopped node"
grep -q ' up -d --pull never --no-build --no-deps tor dashboard' "$log" || fail "package upgrade started a stopped node"

: >"$log"
printf '%s\n' tor bitcoin dashboard >"$running"
FAKE_RUNNING_AFTER_PULL=dashboard ./stack package-upgrade >/dev/null
grep -q ' up -d --pull never --no-build --no-deps dashboard' "$log" || fail "package upgrade restarted a service stopped during pull"
grep -q ' up -d --pull never --no-build --no-deps tor' "$log" && fail "package upgrade restarted Tor after it was stopped during pull"

before_env=$(cksum "$tmp/state/.env")
before_approved=$(cksum "$tmp/state/.approved.env")
: >"$log"
printf '%s\n' tor bitcoin dashboard >"$running"
FAKE_PULL_FAIL=1 ./stack package-upgrade >/dev/null 2>&1 && fail "package upgrade accepted a pull failure"
[ "$(cksum "$tmp/state/.env")" = "$before_env" ] || fail "pull failure changed the runtime environment"
[ "$(cksum "$tmp/state/.approved.env")" = "$before_approved" ] || fail "pull failure changed the approved environment"
grep -q ' up -d ' "$log" && fail "pull failure touched running containers"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "pull failure did not leave a retry marker"

: >"$log"
rm -f "$log.up-failed"
printf '%s\n' tor bitcoin dashboard >"$running"
sed -i.bak 's/^STACK_VERSION=.*/STACK_VERSION=old-release/' "$tmp/state/.env"
sed -i.bak 's/^STACK_VERSION=.*/STACK_VERSION=old-release/' "$tmp/state/.approved.env"
rm -f "$tmp/state/.env.bak" "$tmp/state/.approved.env.bak"
up_failure=$tmp/up-failure.log
FAKE_UP_FAIL_ONCE=1 ./stack package-upgrade >"$up_failure" 2>&1 && fail "package upgrade accepted a recreation failure"
[ "$(grep -c ' up -d ' "$log")" = 1 ] || fail "recreation failure attempted an unsafe image downgrade"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "recreation failure did not leave a retry marker"
[ "$(sed -n 's/^STACK_VERSION=//p' "$tmp/state/.approved.env")" != old-release ] || fail "recreation failure did not preserve the new approved runtime"
grep -q 'sudo bitcoin-stack up tor bitcoin dashboard' "$up_failure" || fail "recreation failure omitted the interrupted service recovery command"
grep -q 'sudo bitcoin-stack package-upgrade' "$up_failure" || fail "recreation failure omitted the activation retry command"

: >"$log"
printf '%s\n' tor dashboard >"$running"
FAKE_FOREIGN_SERVICE=tor ./stack package-upgrade >/dev/null 2>&1 && fail "package upgrade accepted a foreign container"
grep -q ' pull ' "$log" && fail "ownership failure downloaded images"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "ownership failure did not leave a retry marker"

: >"$log"
printf '%s\n' tor dashboard >"$running"
FAKE_PS_FAIL_SERVICE=bitcoin ./stack package-upgrade >/dev/null 2>&1 && fail "package upgrade treated a Docker query failure as a stopped service"
grep -q ' pull ' "$log" && fail "Docker query failure downloaded images"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "Docker query failure did not leave a retry marker"

: >"$log"
printf '%s\n' tor dashboard >"$running"
FAKE_INSPECT_FAIL_SERVICE=tor ./stack package-upgrade >/dev/null 2>&1 && fail "package upgrade accepted an ownership inspection failure"
grep -q ' pull ' "$log" && fail "ownership inspection failure downloaded images"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "ownership inspection failure did not leave a retry marker"

: >"$log"
printf '%s\n' tor dashboard >"$running"
FAKE_DUPLICATE_SERVICE=tor ./stack package-upgrade >/dev/null 2>&1 && fail "package upgrade accepted duplicate project service identities"
grep -q ' pull ' "$log" && fail "duplicate project identity downloaded images"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "duplicate project identity did not leave a retry marker"

: >"$log"
: >"$running"
./stack package-upgrade >/dev/null
grep -q ' pull ' "$log" && fail "stopped stack downloaded images"
grep -q ' up -d ' "$log" && fail "stopped stack was started"

: >"$log"
printf '%s\n' tor dashboard >"$running"
FAKE_DOCKER_DOWN=1 ./stack package-upgrade >/dev/null 2>&1 && fail "package upgrade ignored unavailable Docker"
grep -q ' pull ' "$log" && fail "Docker-down path attempted a pull"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "Docker-down path did not leave a retry marker"

echo "PASS: test_package_upgrade.sh"
