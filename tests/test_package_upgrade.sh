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
FAKE_CURRENT_VERSION=$(cat VERSION)
export FAKE_CURRENT_VERSION
systemctl_log=$tmp/systemctl.log
running=$tmp/running
printf '%s\n' tor bitcoin dashboard >"$running"

cat >"$fake/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
case "${1:-}" in
  info) [ "${FAKE_DOCKER_DOWN:-0}" != 1 ] ;;
  images)
    printf 'ghcr.io/vijitsingh97/node-manager-dashboard:ancient img001\nghcr.io/vijitsingh97/node-manager-tor:%s img002\nghcr.io/other/thing:1.0 img003\nghcr.io/vijitsingh97/node-manager-monero:<none> img004\nghcr.io/other/thing:<none> img005\nghcr.io/vijitsingh97/node-manager-litecoin:1.39.0 img010\nghcr.io/vijitsingh97/node-manager-litecoin:<none> img006\nzakuracore/zakura:1.8.0 img012\n' "${FAKE_CURRENT_VERSION:-}" |
      if [ "${3:-}" = '{{.Repository}}' ]; then sed 's/:[^:]* [^ ]*$//'; else cat; fi
    ;;
  pull)
    [ "${FAKE_EXTRA_PULL_FAIL:-0}" != 1 ] && printf '%s\n' "$2" >>"$FAKE_DOCKER_LOG.pulled"
    ;;
  ps)
    case " $* " in *' --format '*)
      [ "${FAKE_PS_FAIL_SERVICE:-}" = "" ] || exit 70
      while read -r service; do
        config=$BITCOIN_STACK_ROOT/docker-compose.yml
        [ "${FAKE_FOREIGN_SERVICE:-}" != "$service" ] || config=/foreign/docker-compose.yml
        printf '%s|%s-id|%s|%s\n' "$service" "$service" "$config" "$BITCOIN_STACK_STATE_DIR"
        [ "${FAKE_DUPLICATE_SERVICE:-}" != "$service" ] || printf '%s|%s-duplicate-id|%s|%s\n' "$service" "$service" "$config" "$BITCOIN_STACK_STATE_DIR"
      done <"$FAKE_RUNNING_FILE"
      exit 0 ;;
    esac
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
    service=${4%-id}
    case "${3:-}" in
      *State.Running*) grep -qx "$service" "$FAKE_RUNNING_FILE" 2>/dev/null && printf 'true\n' || printf 'false\n' ;;
    esac
    ;;
  image)
    for ref; do :; done
    ! grep -qxF "$ref" "$FAKE_DOCKER_LOG.pulled" 2>/dev/null || { case "$ref" in *litecoin*) printf 'sha256:img006deadbeef\n' ;; esac; exit 0; }
    for absent in ${FAKE_IMAGE_ABSENT:-}; do
      case "$ref" in *"$absent"*) exit 1 ;; esac
    done
    case "$ref" in *litecoin*) printf 'sha256:img006deadbeef\n' ;; esac
    exit 0 ;;
  volume) [ "${2:-}" != inspect ] || [ -z "${FAKE_VOLUME_MOUNTPOINT:-}" ] || printf '%s\n' "$FAKE_VOLUME_MOUNTPOINT" ;;
  compose)
    case " $* " in
      *' pull '*)
        [ "${FAKE_PULL_FAIL:-0}" != 1 ] || exit 71
        if [ -n "${FAKE_PULL_FAILURES:-}" ]; then
          failed=$(cat "$FAKE_DOCKER_LOG.pull-failures" 2>/dev/null || echo 0)
          if [ "$failed" -lt "$FAKE_PULL_FAILURES" ]; then echo $((failed + 1)) >"$FAKE_DOCKER_LOG.pull-failures"; exit 71; fi
        fi
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
case "$*" in 'is-enabled --quiet node-manager-'*'.timer') [ "${FAKE_TIMERS_DISABLED:-0}" != 1 ] ;; esac
EOF
chmod +x "$fake/systemctl"
cat >"$fake/timeout" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_TIMEOUT_LOG"
while [ "${1#-}" != "$1" ]; do shift; done
shift
exec "$@"
EOF
chmod +x "$fake/timeout"
cat >"$fake/sleep" <<'EOF'
#!/bin/sh
case "$*" in 15 | 30) printf '%s\n' "$*" >>"$FAKE_SLEEP_LOG"; exit 0 ;; esac
exec /bin/sleep "$@"
EOF
chmod +x "$fake/sleep"
timeout_log=$tmp/timeout.log
sleep_log=$tmp/sleep.log
export PATH="$fake:$PATH" FAKE_DOCKER_LOG=$log FAKE_RUNNING_FILE=$running
export FAKE_SYSTEMCTL_LOG=$systemctl_log FAKE_TIMEOUT_LOG=$timeout_log FAKE_SLEEP_LOG=$sleep_log
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
grep -q '^rmi ghcr.io/vijitsingh97/node-manager-dashboard:ancient$' "$log" || fail "superseded image was not pruned"
grep -q "^rmi ghcr.io/vijitsingh97/node-manager-tor:$FAKE_CURRENT_VERSION\$" "$log" && fail "current image was pruned"
grep -q '^rmi ghcr.io/other/thing:1.0$' "$log" && fail "a foreign image was pruned"
grep -q '^volume prune -f$' "$log" || fail "stranded anonymous volumes were not pruned"
grep -q '^rmi img004$' "$log" || fail "our dangling image was not pruned by id"
grep -q '^rmi img005$' "$log" && fail "a foreign dangling image was pruned"
[ "$(cat "$tmp/etc/config.json")" = "$pending" ] || fail "package upgrade applied pending configuration"
[ ! -e "$tmp/custom-do-not-create" ] || fail "package upgrade ran first-boot data setup"
grep -q '^null$' "$tmp/state/.approved-config.json" || fail "package upgrade replaced a legacy approved-config sentinel"
grep -q "^BITCOIN_DATA_DIR=$custom_data$" "$tmp/state/.env" || fail "package upgrade replaced a custom data path"
grep -q '^COMPOSE_PROFILES=bitcoin$' "$tmp/state/.env" || fail "package upgrade replaced the approved node selection"
[ "$(grep -o ' pull [a-z-]*$' "$log" | tr '\n' ' ')" = ' pull tor  pull bitcoin  pull dashboard ' ] || fail "package upgrade did not pull the running subset one image at a time"
grep -q 'compose .* up -d --pull never --no-build --no-deps tor bitcoin dashboard' "$log" || fail "package upgrade did not recreate the running subset"
grep -q '^STACK_VERSION=' "$tmp/state/.env" || fail "package upgrade did not render the installed version"
grep -q '^enable --now node-manager-resource-profile.timer$' "$systemctl_log" || fail "package upgrade did not enable post-sync resource controls"
grep -q '^enable --now node-manager-port-control.timer node-manager-port-control.path$' "$systemctl_log" || fail "package upgrade did not enable LAN port controls"
grep -q '^enable --now node-manager-node-control.timer node-manager-node-control.path$' "$systemctl_log" || fail "package upgrade did not enable node controls"
grep -q 'node-manager-host-stats.timer' "$systemctl_log" && fail "package upgrade enabled host readings without the hostd binary"
printf '#!/bin/sh\n' >"$tmp/hostd" && chmod +x "$tmp/hostd"
: >"$systemctl_log"
BITCOIN_STACK_HOSTD="$tmp/hostd" ./stack package-upgrade >/dev/null
grep -q '^enable --now node-manager-host-stats.timer$' "$systemctl_log" || fail "package upgrade did not enable host readings with the hostd binary installed"

custom_volume=/srv/docker/volumes/node-manager-apt_dashboard_state/_data
BITCOIN_STACK_SYSTEMD_DIR=$tmp/systemd FAKE_VOLUME_MOUNTPOINT=$custom_volume ./stack package-upgrade >/dev/null
[ "$(cat "$tmp/systemd/node-manager-node-control.path.d/volume.conf")" = "$(printf '[Path]\nPathChanged=%s/node-control.request' "$custom_volume")" ] ||
  fail "a custom Docker data root was not added to the node-control request watch"
[ "$(cat "$tmp/systemd/node-manager-port-control.path.d/volume.conf")" = "$(printf '[Path]\nPathChanged=%s/port-control.request\nPathChanged=%s/credential-control.request' "$custom_volume" "$custom_volume")" ] ||
  fail "a custom Docker data root was not added to the port-control request watch"
BITCOIN_STACK_SYSTEMD_DIR=$tmp/systemd FAKE_VOLUME_MOUNTPOINT=/var/lib/docker/volumes/node-manager-apt_dashboard_state/_data ./stack package-upgrade >/dev/null
[ ! -e "$tmp/systemd/node-manager-node-control.path.d/volume.conf" ] && [ ! -e "$tmp/systemd/node-manager-port-control.path.d/volume.conf" ] ||
  fail "the request watch kept a custom path after the volume moved back to the default"

sed "s|^PATH=.*|PATH=$fake:/usr/bin:/bin|" packaging/postinst >"$tmp/postinst"
: >"$systemctl_log"
sh "$tmp/postinst" configure 1.0.0 || fail "postinst failed before package activation"
{ grep -qx 'daemon-reload' "$systemctl_log" && grep -qx 'enable --now node-manager-node-control.path' "$systemctl_log" &&
  grep -qx 'enable --now node-manager-port-control.path' "$systemctl_log"; } ||
  fail "an upgrade that stopped before package activation left the request watches off: $(cat "$systemctl_log")"
: >"$systemctl_log"
FAKE_TIMERS_DISABLED=1 sh "$tmp/postinst" configure 1.0.0
grep -q '^enable' "$systemctl_log" && fail "postinst enabled a request watch whose timer is off"

: >"$log"
printf '%s\n' tor dashboard >"$running"
./stack package-upgrade >/dev/null
[ "$(grep -o ' pull [a-z-]*$' "$log" | tr '\n' ' ')" = ' pull tor  pull dashboard ' ] || fail "package upgrade pulled a stopped node"
grep -q ' up -d --pull never --no-build --no-deps tor dashboard' "$log" || fail "package upgrade started a stopped node"

: >"$log"
printf '%s\n' tor bitcoin dashboard >"$running"
FAKE_RUNNING_AFTER_PULL=dashboard ./stack package-upgrade >/dev/null
grep -q ' up -d --pull never --no-build --no-deps dashboard' "$log" || fail "package upgrade restarted a service stopped during pull"
grep -q ' up -d --pull never --no-build --no-deps tor' "$log" && fail "package upgrade restarted Tor after it was stopped during pull"

: >"$log"
printf '%s\n' tor bitcoin dashboard >"$running"
rm -f "$log.pulled"
FAKE_IMAGE_ABSENT="litecoin dogecoin zakura" ./stack package-upgrade >"$tmp/extra-pull.out" 2>&1 || fail "package upgrade failed with not-running node images to refresh"
grep -q "^pull ghcr.io/vijitsingh97/node-manager-litecoin:$FAKE_CURRENT_VERSION\$" "$log" || fail "an inactive node with an older image did not get its current image"
grep -q '^pull zakuracore/zakura:1.8.9@sha256:' "$log" || fail "an upstream-image node with an older image did not get its current image"
[ "$(grep -c '^pull ' "$log")" = 2 ] || fail "a node with no image of its repository was pulled: $(grep '^pull ' "$log")"
grep -q ' up -d --pull never --no-build --no-deps tor bitcoin dashboard' "$log" || fail "an extra pull changed which services were recreated"
grep -q 'up -d.*litecoin' "$log" && fail "an extra pull started the inactive node"
grep -q '^rmi img006$' "$log" && fail "the cleanup removed the current image of a not-running node"
grep -q '^rmi ghcr.io/vijitsingh97/node-manager-litecoin:1.39.0$' "$log" || fail "the cleanup kept the older image of a not-running node"
[ "$(grep -o ' pull [a-z-]*$' "$log" | tr '\n' ' ')" = ' pull tor  pull bitcoin  pull dashboard ' ] || fail "the extra pulls changed the running pulls"
[ "$(grep -n ' up -d --pull never' "$log" | head -n 1 | cut -d: -f1)" -lt "$(grep -n '^pull ' "$log" | head -n 1 | cut -d: -f1)" ] || fail "the extra pulls ran before the running services were recreated"

: >"$log"
rm -f "$log.pulled"
FAKE_EXTRA_PULL_FAIL=1 FAKE_IMAGE_ABSENT="litecoin" ./stack package-upgrade >"$tmp/extra-pull-fail.out" 2>&1 || fail "a failing extra pull failed the upgrade"
grep -q "^WARN  could not pull ghcr.io/vijitsingh97/node-manager-litecoin:$FAKE_CURRENT_VERSION for a node that is not running" "$tmp/extra-pull-fail.out" || fail "a failing extra pull did not warn"
grep -q ' up -d --pull never --no-build --no-deps tor bitcoin dashboard' "$log" || fail "a failing extra pull stopped the running services from being recreated"
grep -q '^rmi img006$' "$log" && fail "the cleanup removed the only image of a node whose extra pull failed"
grep -q '^rmi ghcr.io/vijitsingh97/node-manager-litecoin:1.39.0$' "$log" && fail "the cleanup removed the only tagged image of a node whose extra pull failed"
[ ! -e "$tmp/state/.package-upgrade-pending" ] || fail "a failing extra pull left the activation pending"

before_env=$(cksum "$tmp/state/.env")
before_approved=$(cksum "$tmp/state/.approved.env")
: >"$log"
printf '%s\n' tor bitcoin dashboard >"$running"
: >"$sleep_log"
FAKE_PULL_FAIL=1 ./stack package-upgrade >/dev/null 2>"$tmp/pull-failure.log" && fail "package upgrade accepted a pull failure"
[ "$(cksum "$tmp/state/.env")" = "$before_env" ] || fail "pull failure changed the runtime environment"
[ "$(cksum "$tmp/state/.approved.env")" = "$before_approved" ] || fail "pull failure changed the approved environment"
grep -q ' up -d ' "$log" && fail "pull failure touched running containers"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "pull failure did not leave a retry marker"
[ "$(grep -o ' pull [a-z-]*$' "$log" | tr '\n' ' ')" = ' pull tor  pull tor  pull tor ' ] ||
  fail "a failing image pull was not tried exactly 3 times before activation stopped"
[ "$(tr '\n' ' ' <"$sleep_log")" = '15 30 ' ] || fail "image pull retries did not back off 15 s then 30 s"
[ "$(grep -c '^Pulling the tor image failed after [0-9]* s (attempt [123] of 3)\.$' "$tmp/pull-failure.log")" = 3 ] ||
  fail "pull failures did not log each attempt's duration"
./stack doctor >"$tmp/doctor.out" 2>&1 || true
grep -q "^WARN  Node Manager $FAKE_CURRENT_VERSION is installed, but its containers were not updated; retry with: sudo node-manager package-upgrade\$" "$tmp/doctor.out" ||
  fail "doctor did not report the pending activation"

: >"$log"
: >"$timeout_log"
rm -f "$log.pull-failures"
FAKE_PULL_FAILURES=2 ./stack package-upgrade >"$tmp/pull-retry.log" 2>&1 || fail "package upgrade gave up on an image pull that succeeded on its third try"
[ "$(grep -o ' pull [a-z-]*$' "$log" | tr '\n' ' ')" = ' pull tor  pull tor  pull tor  pull bitcoin  pull dashboard ' ] ||
  fail "a transient pull failure was not retried for that image only"
grep -q ' up -d --pull never --no-build --no-deps tor bitcoin dashboard' "$log" || fail "package upgrade did not activate after a retried pull"
[ ! -e "$tmp/state/.package-upgrade-pending" ] || fail "a successful retry left the activation pending"
grep -q '^Pulled the tor image in [0-9]* s (attempt 3 of 3)\.$' "$tmp/pull-retry.log" || fail "a retried pull did not log its duration"
grep -q '^Pulled the dashboard image in [0-9]* s (attempt 1 of 3)\.$' "$tmp/pull-retry.log" || fail "a pull did not log its duration"
[ "$(grep -c '^--foreground --kill-after=30 600 docker compose .* pull [a-z-]*$' "$timeout_log")" = 5 ] || fail "an image pull ran without its 600 s bound"

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
grep -q 'sudo node-manager up tor bitcoin dashboard' "$up_failure" || fail "recreation failure omitted the interrupted service recovery command"
grep -q 'sudo node-manager package-upgrade' "$up_failure" || fail "recreation failure omitted the activation retry command"

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
FAKE_DUPLICATE_SERVICE=tor ./stack package-upgrade >/dev/null 2>&1 && fail "package upgrade accepted duplicate project service identities"
grep -q ' pull ' "$log" && fail "duplicate project identity downloaded images"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "duplicate project identity did not leave a retry marker"

: >"$log"
: >"$running"
./stack package-upgrade >/dev/null
grep -q ' pull ' "$log" && fail "stopped stack downloaded images"
[ ! -e "$tmp/state/.package-upgrade-pending" ] || fail "a stopped stack kept a pending activation that package-upgrade cannot clear"
grep -q ' up -d ' "$log" && fail "stopped stack was started"

: >"$log"
printf '%s\n' tor dashboard >"$running"
FAKE_DOCKER_DOWN=1 ./stack package-upgrade >/dev/null 2>&1 && fail "package upgrade ignored unavailable Docker"
grep -q ' pull ' "$log" && fail "Docker-down path attempted a pull"
[ -f "$tmp/state/.package-upgrade-pending" ] || fail "Docker-down path did not leave a retry marker"

expected_tor=$(grep '^TOR_' "$tmp/state/.approved.env" | sort)
[ "$(grep -c '^TOR_' "$tmp/state/.approved.env")" = 3 ] || fail "configure.sh did not render the three Tor network keys"
sed -i.bak '/^TOR_/d' "$tmp/state/.approved.env"
rm -f "$tmp/state/.approved.env.bak"
printf '%s\n' tor bitcoin dashboard >"$running"
./stack package-upgrade >/dev/null
[ "$(grep '^TOR_' "$tmp/state/.env" | sort)" = "$expected_tor" ] || fail "package upgrade did not derive the Tor network keys configure.sh renders"

sed -i.bak '/^TOR_/d' "$tmp/state/.approved.env"
rm -f "$tmp/state/.approved.env.bak"
printf '%s\n' '{"network":{"inbound_connections":false}}' >"$tmp/state/.approved-config.json"
./stack package-upgrade >/dev/null
grep -q '^TOR_INBOUND=0$' "$tmp/state/.env" || fail "package upgrade ignored an approved inbound_connections false"
grep -q '^TOR_ONION_NODES=$' "$tmp/state/.env" || fail "package upgrade published node onions with inbound off"
[ "$(grep -c '^TOR_' "$tmp/state/.env")" = 3 ] || fail "package upgrade duplicated the Tor network keys"

mkdir -p "$tmp/state/data/bitcoin-old"
head -c 2097152 /dev/zero >"$tmp/state/data/bitcoin-old/blk00000.dat"
./stack doctor >"$tmp/doctor.out" 2>&1 || true
grep -q 'WARN  orphaned data directory .*bitcoin-old (2 MiB)' "$tmp/doctor.out" || fail "doctor missed the orphaned data directory"

cat >"$fake/ifaddrs" <<'EOF'
#!/bin/sh
for address in $FAKE_HOST_ADDRESSES; do printf '2: eth0    inet %s/24 brd 192.168.1.255 scope global eth0\n' "$address"; done
EOF
chmod +x "$fake/ifaddrs"
sed -i.bak -e 's/^DASHBOARD_BIND_IP=.*/DASHBOARD_BIND_IP=192.168.1.20/' -e 's/^BITCOIN_RPC_EXPOSE=.*/BITCOIN_RPC_EXPOSE=1/' \
  -e 's/^BITCOIN_RPC_BIND_IP=.*/BITCOIN_RPC_BIND_IP=192.168.1.21/' "$tmp/state/.env"
rm -f "$tmp/state/.env.bak"
FAKE_HOST_ADDRESSES="127.0.0.1 192.168.1.20 192.168.1.21" IP_BIN=$fake/ifaddrs IFCONFIG_BIN=$fake/ifaddrs ./stack doctor >"$tmp/doctor.out" 2>&1 || true
! grep -q 'is not on this host' "$tmp/doctor.out" || fail "doctor flagged addresses the host still has"
FAKE_HOST_ADDRESSES="127.0.0.1 192.168.1.30" IP_BIN=$fake/ifaddrs IFCONFIG_BIN=$fake/ifaddrs ./stack doctor >"$tmp/doctor.out" 2>&1 || true
grep -q '^WARN  DASHBOARD_BIND_IP 192.168.1.20 is not on this host' "$tmp/doctor.out" || fail "doctor missed a dashboard address the host lost"
grep -q '^WARN  BITCOIN_RPC_BIND_IP 192.168.1.21 is not on this host' "$tmp/doctor.out" || fail "doctor missed a LAN listener address the host lost"
printf '192.168.1.22\n' >"$tmp/state/.paused-listeners"
FAKE_HOST_ADDRESSES="127.0.0.1 192.168.1.20 192.168.1.21" IP_BIN=$fake/ifaddrs IFCONFIG_BIN=$fake/ifaddrs ./stack doctor >"$tmp/doctor.out" 2>&1 || true
grep -q '^WARN  LAN listeners on 192.168.1.22 are paused' "$tmp/doctor.out" || fail "doctor did not report paused LAN listeners"
rm -f "$tmp/state/.paused-listeners"

echo "PASS: test_package_upgrade.sh"
