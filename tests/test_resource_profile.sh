#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  exit 1
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fake=$tmp/fake-docker
log=$tmp/docker.log
caps=$tmp/caps
mkdir "$caps"

cat >"$fake" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"

if [ "$1" = compose ]; then
  shift
  while [ "$1" != ps ] && [ "$1" != exec ] && [ "$1" != stop ] && [ "$1" != start ]; do shift; done
  command=$1
  shift
  case "$command" in
    ps)
      [ "$1" = -q ]
      case "$2" in
        bitcoin) [ "${FAKE_BITCOIN_RUNNING:-1}" = 1 ] && printf '%s\n' "${FAKE_BITCOIN_ID:-btc-id}" ;;
        monero) [ "${FAKE_MONERO_RUNNING:-1}" = 1 ] && printf '%s\n' "${FAKE_MONERO_ID:-xmr-id}" ;;
        litecoin) printf '%s\n' "${FAKE_LITECOIN_ID:-ltc-id}" ;;
        dogecoin) printf '%s\n' "${FAKE_DOGECOIN_ID:-doge-id}" ;;
        bitcoincash) printf '%s\n' "${FAKE_BITCOINCASH_ID:-bch-id}" ;;
        tari) printf '%s\n' tari-id ;;
        zcash) printf '%s\n' zcash-id ;;
        dashboard) printf '%s\n' dashboard-id ;;
      esac
      ;;
    exec)
      [ "$1" = -T ]
      shift
      if [ "$1" = -u ]; then
        [ "$2" = 10001:1000 ]
        shift 2
      fi
      case "$1" in
        bitcoin)
          if [ "${FAKE_BITCOIN_RPC_FAIL:-0}" = 1 ]; then exit 1; fi
          if [ "${FAKE_BITCOIN_SYNCED:-0}" = 1 ]; then
            printf '%s\n' "{\"initialblockdownload\":false,\"blocks\":100,\"headers\":100,\"time\":${FAKE_BITCOIN_TIP:-$RESOURCE_NOW_EPOCH},\"warnings\":${FAKE_BITCOIN_WARNINGS:-[]}}"
          else
            printf '%s\n' "{\"initialblockdownload\":true,\"blocks\":50,\"headers\":100,\"time\":${RESOURCE_NOW_EPOCH},\"warnings\":[]}"
          fi
          ;;
        monero)
          if [ "${FAKE_MONERO_RPC_FAIL:-0}" = 1 ]; then exit 1; fi
          if [ "${FAKE_MONERO_SYNCED:-0}" = 1 ]; then
            printf '%s\n' '{"result":{"status":"OK","synchronized":true,"busy_syncing":false,"height":100,"target_height":100}}'
            printf '%s\n' "{\"result\":{\"status\":\"OK\",\"block_header\":{\"timestamp\":${FAKE_MONERO_TIP:-$RESOURCE_NOW_EPOCH}}}}"
          else
            printf '%s\n' '{"result":{"status":"OK","synchronized":false,"busy_syncing":true,"height":50,"target_height":100}}'
            printf '%s\n' "{\"result\":{\"status\":\"OK\",\"block_header\":{\"timestamp\":${RESOURCE_NOW_EPOCH}}}}"
          fi
          ;;
        litecoin | dogecoin | bitcoincash)
          service=$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]')
          eval "rpc_fail=\${FAKE_${service}_RPC_FAIL:-0}"
          eval "tip=\${FAKE_${service}_TIP:-$RESOURCE_NOW_EPOCH}"
          [ "$rpc_fail" != 1 ] || exit 1
          printf '%s\n' "{\"initialblockdownload\":false,\"blocks\":100,\"headers\":100,\"mediantime\":${tip},\"warnings\":[] }"
          ;;
        zcash)
          if printf '%s' "$*" | grep -q getblockheader; then
            printf '%s\n' "{\"result\":{\"time\":${RESOURCE_NOW_EPOCH}},\"error\":null}"
          elif [ "${FAKE_ZCASH_SYNCED:-0}" = 1 ]; then
            printf '%s\n' '{"result":{"blocks":100,"headers":100,"estimatedheight":100,"verificationprogress":1.0,"bestblockhash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","pruned":true},"error":null}'
          else
            printf '%s\n' '{"result":{"blocks":50,"headers":100,"estimatedheight":100,"verificationprogress":0.5,"bestblockhash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","pruned":true},"error":null}'
          fi
          ;;
      esac
      ;;
    stop | start) exit 0 ;;
  esac
  exit 0
fi

case "$1" in
  inspect)
    id=${4:-}
    case "$3" in
      *State.Health*)
        case "$id" in
          btc-*) printf '%s\n' "${FAKE_BITCOIN_HEALTH:-healthy}" ;;
          xmr-*) printf '%s\n' "${FAKE_MONERO_HEALTH:-healthy}" ;;
          *) printf '%s\n' healthy ;;
        esac
        ;;
      *HostConfig*)
        case "$id" in
          btc-*) configured=${FAKE_BITCOIN_LIMITS:-} ;;
          xmr-*) configured=${FAKE_MONERO_LIMITS:-} ;;
          *) configured= ;;
        esac
        if [ -n "$configured" ]; then
          printf '%s\n' "$configured"
        elif [ -f "$FAKE_CAPS/$id" ]; then
          cat "$FAKE_CAPS/$id"
        else
          printf '%s\n' '0 0 0'
        fi
        ;;
      *State.ExitCode*) printf '%s\n' "${FAKE_EXIT_CODE:-0}" ;;
    esac
    ;;
  stats)
    if [ "${3:-}" = --no-trunc ]; then
      printf 'cid-monero\tbitcoin-starter-stack-apt-monero-1\t170.00%%\t3.2GiB / 8GiB\ncid-dash\tbitcoin-starter-stack-apt-dashboard-1\t8.00%%\t50MiB / 8GiB\n'
      exit 0
    fi
    id=${5:-}
    case "$id" in
      btc-*) printf '%s\n' "${FAKE_BITCOIN_MEMORY:-700MiB} / 8GiB" ;;
      xmr-*) printf '%s\n' "${FAKE_MONERO_MEMORY:-600MiB} / 8GiB" ;;
      ltc-* | doge-* | bch-* | tari-* | zcash-*) printf '%s\n' "600MiB / 8GiB" ;;
    esac
    ;;
  update)
    cpus=$3
    if [ "$#" = 4 ]; then
      id=$4
      nanos=$(awk -v value="$cpus" 'BEGIN { printf "%.0f\n", value * 1000000000 }')
      [ "$nanos" = 0 ] && exit 0
      printf '%s 0 0\n' "$nanos" >"$FAKE_CAPS/$id"
      exit 0
    fi
    memory=${5%m}
    swap=${7%m}
    id=$8
    if [ "${FAKE_UPDATE_FAIL:-0}" != 1 ]; then
      nanos=$(awk -v value="$cpus" 'BEGIN { printf "%.0f\n", value * 1000000000 }')
      printf '%s %s %s\n' "$nanos" "$((memory * 1024 * 1024))" "$((swap * 1024 * 1024))" >"$FAKE_CAPS/$id"
    fi
    ;;
  exec)
    if [ "${2:-}" = -i ]; then
      case "$*" in
        *events.log*) cat >>"${FAKE_EVENTS_OUT:-/dev/null}" ;;
        *) cat >"${FAKE_HOST_STATS_OUT:-/dev/null}" ;;
      esac
    else
      case "$*" in
        *notification_settings.json*) printf '%s' "${FAKE_SETTINGS_JSON:-}" ;;
        *) [ "${FAKE_TARI_SYNCED:-0}" = 1 ] ;;
      esac
    fi
    ;;
  info)
    case "${3:-}" in
      '{{.NCPU}}') printf '4\n' ;;
      '{{.MemTotal}}') printf '8197607424\n' ;;
      '{{.DockerRootDir}}') printf '/var/lib/docker\n' ;;
    esac
    ;;
esac
EOF
chmod +x "$fake" scripts/node-resource-profile tests/test_resource_profile.sh

new_case() {
  case_root=$tmp/$1
  mkdir -p "$case_root/root" "$case_root/state"
  : >"$case_root/root/docker-compose.yml"
  : >"$log"
  rm -f "$caps"/*
  cat >"$case_root/state/.env" <<'EOF'
BITCOIN_RESOURCE_PROFILE=off
MONERO_RESOURCE_PROFILE=off
BITCOIN_DBCACHE=128
BITCOIN_STEADY_CPUS=1
BITCOIN_STEADY_MEM_LIMIT_MB=2048
MONERO_STEADY_CPUS=1
MONERO_STEADY_MEM_LIMIT_MB=2048
TARI_RESOURCE_PROFILE=off
TARI_STEADY_CPUS=1
TARI_STEADY_MEM_LIMIT_MB=4096
LITECOIN_RESOURCE_PROFILE=off
LITECOIN_STEADY_CPUS=1
LITECOIN_STEADY_MEM_LIMIT_MB=4096
DOGECOIN_RESOURCE_PROFILE=off
DOGECOIN_STEADY_CPUS=1
DOGECOIN_STEADY_MEM_LIMIT_MB=4096
BITCOINCASH_RESOURCE_PROFILE=off
BITCOINCASH_STEADY_CPUS=1
BITCOINCASH_STEADY_MEM_LIMIT_MB=4096
ZCASH_RESOURCE_PROFILE=off
ZCASH_STEADY_CPUS=1
ZCASH_STEADY_MEM_LIMIT_MB=4096
EOF
  export BITCOIN_STACK_ROOT=$case_root/root
  export BITCOIN_STACK_STATE_DIR=$case_root/state
  export DOCKER_BIN=$fake FAKE_DOCKER_LOG=$log FAKE_CAPS=$caps
  export FAKE_BITCOIN_ID=btc-id FAKE_MONERO_ID=xmr-id
  export FAKE_DOGECOIN_ID=doge-id FAKE_BITCOINCASH_ID=bch-id
  export FAKE_BITCOIN_RUNNING=1 FAKE_MONERO_RUNNING=1
  export FAKE_BITCOIN_SYNCED=0 FAKE_MONERO_SYNCED=0
  export FAKE_TARI_SYNCED=0
  export FAKE_ZCASH_SYNCED=0
  export FAKE_BITCOIN_HEALTH=healthy FAKE_MONERO_HEALTH=healthy
  export FAKE_BITCOIN_MEMORY=700MiB FAKE_MONERO_MEMORY=600MiB
  export FAKE_BITCOIN_RPC_FAIL=0 FAKE_MONERO_RPC_FAIL=0 FAKE_UPDATE_FAIL=0
  export FAKE_BITCOIN_LIMITS='' FAKE_MONERO_LIMITS='' FAKE_EXIT_CODE=0
  unset FAKE_BITCOIN_TIP FAKE_BITCOIN_WARNINGS FAKE_MONERO_TIP
  unset FAKE_LITECOIN_TIP FAKE_DOGECOIN_TIP FAKE_BITCOINCASH_TIP
  unset FAKE_LITECOIN_RPC_FAIL FAKE_DOGECOIN_RPC_FAIL FAKE_BITCOINCASH_RPC_FAIL
  export RESOURCE_NOW_EPOCH=100000
}

run_profile() {
  scripts/node-resource-profile
}

tick_profile() {
  RESOURCE_NOW_EPOCH=$((RESOURCE_NOW_EPOCH + 60))
  export RESOURCE_NOW_EPOCH
  run_profile
}

new_case disabled
run_profile
! grep -qE '(^|[[:space:]])(update|stop|start|restart)([[:space:]]|$)' "$log" || fail "disabled profiles mutated containers"

new_case bitcoin-sync
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
tick_profile
tick_profile
tick_profile
! grep -q '^update ' "$log" || fail "syncing Bitcoin was capped"
[ "$(cut -d '|' -f 1-3 "$case_root/state/resource-profile/bitcoin")" = 'btc-id|0|pending' ] || fail "syncing Bitcoin did not reset stability"

export FAKE_BITCOIN_SYNCED=1
run_profile
run_profile
run_profile
! grep -q '^update ' "$log" || fail "immediate Bitcoin checks counted as sustained readiness"
tick_profile
! grep -q '^update ' "$log" || fail "Bitcoin was capped before three stable checks"
tick_profile
grep -q '^update --cpus 1 --memory 2048m --memory-swap 2048m btc-id$' "$log" || fail "Bitcoin cap is wrong"
[ "$(cut -d '|' -f 1-3 "$case_root/state/resource-profile/bitcoin")" = 'btc-id|3|capped' ] || fail "Bitcoin capped state was not saved"
updates=$(grep -c '^update ' "$log")
tick_profile
[ "$(grep -c '^update ' "$log")" = "$updates" ] || fail "an already capped Bitcoin container was updated again"

new_case additional-nodes
perl -pi -e 's/LITECOIN_RESOURCE_PROFILE=off/LITECOIN_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
tick_profile; tick_profile; tick_profile
grep -q '^update --cpus 1 --memory 4096m --memory-swap 4096m ltc-id$' "$log" || fail "Litecoin compact cap is wrong"
grep -q 'litecoin-cli -datadir=/data -conf=/tmp/litecoin.conf getblockchaininfo' "$log" || fail "Litecoin sync check missed its private config"

new_case dogecoin-bitcoincash
perl -pi -e 's/DOGECOIN_RESOURCE_PROFILE=off/DOGECOIN_RESOURCE_PROFILE=compact/; s/DOGECOIN_STEADY_CPUS=1/DOGECOIN_STEADY_CPUS=0.75/; s/DOGECOIN_STEADY_MEM_LIMIT_MB=4096/DOGECOIN_STEADY_MEM_LIMIT_MB=3072/; s/BITCOINCASH_RESOURCE_PROFILE=off/BITCOINCASH_RESOURCE_PROFILE=compact/; s/BITCOINCASH_STEADY_CPUS=1/BITCOINCASH_STEADY_CPUS=1.25/; s/BITCOINCASH_STEADY_MEM_LIMIT_MB=4096/BITCOINCASH_STEADY_MEM_LIMIT_MB=3584/' "$case_root/state/.env"
export FAKE_DOGECOIN_ID=doge-custom FAKE_BITCOINCASH_ID=bch-custom
export FAKE_DOGECOIN_TIP=1 FAKE_BITCOINCASH_TIP=1
tick_profile; tick_profile; tick_profile
! grep -q '^update ' "$log" || fail "additional node with a stale tip was capped"
export FAKE_DOGECOIN_TIP=$RESOURCE_NOW_EPOCH FAKE_BITCOINCASH_TIP=$RESOURCE_NOW_EPOCH
tick_profile; tick_profile; tick_profile
grep -q '^update --cpus 0.75 --memory 3072m --memory-swap 3072m doge-custom$' "$log" || fail "Dogecoin custom cap targeted the wrong container or limits"
grep -q '^update --cpus 1.25 --memory 3584m --memory-swap 3584m bch-custom$' "$log" || fail "Bitcoin Cash custom cap targeted the wrong container or limits"
grep -q 'dogecoin-cli -datadir=/data -conf=/tmp/dogecoin.conf getblockchaininfo' "$log" || fail "Dogecoin sync check missed its private config"
grep -q 'bitcoin-cli -datadir=/data -conf=/tmp/bitcoin.conf getblockchaininfo' "$log" || fail "Bitcoin Cash sync check missed its private config"

new_case tari
perl -pi -e 's/TARI_RESOURCE_PROFILE=off/TARI_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_TARI_SYNCED=1
tick_profile; tick_profile; tick_profile
grep -q '^update --cpus 1 --memory 4096m --memory-swap 4096m tari-id$' "$log" || {
  cat "$log"
  cat "$case_root/state/resource-profile/tari" 2>/dev/null || true
  fail "Tari compact cap is wrong"
}
grep -q 'stat -c %Y' "$log" || fail "Tari readiness does not require a fresh marker"
grep -q 'stat -c %u' "$log" || fail "Tari readiness does not verify marker ownership"

new_case zcash
perl -pi -e 's/ZCASH_RESOURCE_PROFILE=off/ZCASH_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
tick_profile; tick_profile; tick_profile
! grep -q '^update ' "$log" || fail "syncing Zcash was capped"
export FAKE_ZCASH_SYNCED=1
tick_profile; tick_profile; tick_profile
grep -q '^update --cpus 1 --memory 4096m --memory-swap 4096m zcash-id$' "$log" || fail "Zcash compact cap is wrong"
grep -q 'exec -T -u 10001:1000 zcash sh -c' "$log" || fail "Zcash readiness did not use the native cookie owner"
grep -q '/data/.cookie' "$log" || fail "Zcash readiness did not read the native RPC cookie"
grep -q -- '--user' "$log" || fail "Zcash readiness did not authenticate native RPC"

new_case legacy-defaults
perl -ni -e 'print unless /STEADY_/' "$case_root/state/.env"
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1
tick_profile; tick_profile; tick_profile
grep -q '^update --cpus 1 --memory 2048m --memory-swap 2048m btc-id$' "$log" || fail "legacy environment did not use the default compact limits"

new_case recreate
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1
tick_profile; tick_profile; tick_profile
export FAKE_BITCOIN_ID=btc-new
tick_profile; tick_profile
[ "$(grep -c '^update ' "$log")" = 1 ] || fail "recreated Bitcoin was capped before stability checks"
tick_profile
grep -q '^update --cpus 1 --memory 2048m --memory-swap 2048m btc-new$' "$log" || fail "recreated Bitcoin was not capped"

new_case unhealthy
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1 FAKE_BITCOIN_HEALTH=unhealthy
tick_profile; tick_profile; tick_profile
! grep -q '^update ' "$log" || fail "unhealthy Bitcoin was capped"

new_case cache
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/; s/BITCOIN_DBCACHE=128/BITCOIN_DBCACHE=1900/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1
tick_profile; tick_profile; tick_profile
! grep -q '^update ' "$log" || fail "high-dbcache Bitcoin was capped"
! grep -q ' restart bitcoin$' "$log" || fail "high-dbcache Bitcoin was restarted"
grep -q '|blocked|' "$case_root/state/resource-profile/bitcoin" || fail "high-dbcache Bitcoin was not marked blocked"

new_case memory
perl -pi -e 's/MONERO_RESOURCE_PROFILE=off/MONERO_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_MONERO_SYNCED=1 FAKE_MONERO_MEMORY=1900MiB
tick_profile; tick_profile; tick_profile
[ "$(grep -c ' stop monero$' "$log")" = 1 ] || fail "high-memory Monero did not stop once"
grep -q ' start monero$' "$log" || fail "high-memory Monero was not restarted"
tick_profile; tick_profile; tick_profile
[ "$(grep -c ' stop monero$' "$log")" = 1 ] || fail "high-memory Monero restarted more than once"
! grep -q '^update ' "$log" || fail "high-memory Monero was capped"
grep -q '|blocked|' "$case_root/state/resource-profile/monero" || fail "high-memory Monero was not marked blocked"
export FAKE_MONERO_MEMORY=600MiB
tick_profile
grep -q '^update --cpus 1 --memory 2048m --memory-swap 2048m xmr-id$' "$log" || fail "Monero was not capped after memory became safe"

new_case custom-limits
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/; s/BITCOIN_STEADY_CPUS=1/BITCOIN_STEADY_CPUS=0.5/; s/BITCOIN_STEADY_MEM_LIMIT_MB=2048/BITCOIN_STEADY_MEM_LIMIT_MB=1536/; s/MONERO_RESOURCE_PROFILE=off/MONERO_RESOURCE_PROFILE=compact/; s/MONERO_STEADY_CPUS=1/MONERO_STEADY_CPUS=1.5/; s/MONERO_STEADY_MEM_LIMIT_MB=2048/MONERO_STEADY_MEM_LIMIT_MB=3072/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1 FAKE_MONERO_SYNCED=1
tick_profile; tick_profile; tick_profile
grep -q '^update --cpus 0.5 --memory 1536m --memory-swap 1536m btc-id$' "$log" || fail "custom Bitcoin limits were not applied"
grep -q '^update --cpus 1.5 --memory 3072m --memory-swap 3072m xmr-id$' "$log" || fail "custom Monero limits were not applied"
perl -pi -e 's/BITCOIN_STEADY_CPUS=0.5/BITCOIN_STEADY_CPUS=0.75/; s/BITCOIN_STEADY_MEM_LIMIT_MB=1536/BITCOIN_STEADY_MEM_LIMIT_MB=1792/' "$case_root/state/.env"
export FAKE_BITCOIN_ID=btc-edited
tick_profile; tick_profile
! grep -q '^update .* btc-edited$' "$log" || fail "edited Bitcoin cap was applied before sustained readiness"
tick_profile
grep -q '^update --cpus 0.75 --memory 1792m --memory-swap 1792m btc-edited$' "$log" || fail "edited Bitcoin cap was not applied to its recreated container"

new_case invalid-limit
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/; s/BITCOIN_STEADY_CPUS=1/BITCOIN_STEADY_CPUS=zero/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1
run_profile >/dev/null 2>&1 && fail "invalid rendered CPU limit was accepted"

new_case rpc-failure
perl -pi -e 's/MONERO_RESOURCE_PROFILE=off/MONERO_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_MONERO_SYNCED=1 FAKE_MONERO_RPC_FAIL=1
tick_profile; tick_profile; tick_profile
! grep -q '^update ' "$log" || fail "Monero was capped after RPC failures"

new_case stale-tip
perl -pi -e 's/MONERO_RESOURCE_PROFILE=off/MONERO_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_MONERO_SYNCED=1 FAKE_MONERO_TIP=1
tick_profile; tick_profile; tick_profile
! grep -q '^update ' "$log" || fail "Monero with a stale tip was capped"

new_case warning
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1 FAKE_BITCOIN_WARNINGS='["test warning"]'
tick_profile; tick_profile; tick_profile
! grep -q '^update ' "$log" || fail "Bitcoin reporting a warning was capped"

new_case update-failure
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1 FAKE_UPDATE_FAIL=1
tick_profile; tick_profile
if tick_profile >/dev/null 2>&1; then
  fail "an unverified Docker update was accepted"
fi

new_case existing-limit
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1 FAKE_BITCOIN_LIMITS='500000000 1073741824 1073741824'
tick_profile; tick_profile; tick_profile
! grep -q '^update ' "$log" || fail "an operator-set lower limit was increased"
grep -q '|blocked|' "$case_root/state/resource-profile/bitcoin" || fail "operator-set limits were not marked blocked"

new_case memory-units
perl -pi -e 's/BITCOIN_RESOURCE_PROFILE=off/BITCOIN_RESOURCE_PROFILE=compact/; s/MONERO_RESOURCE_PROFILE=off/MONERO_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_BITCOIN_SYNCED=1 FAKE_MONERO_SYNCED=1
export FAKE_BITCOIN_MEMORY=1073741824B FAKE_MONERO_MEMORY=1048576KiB
tick_profile; tick_profile; tick_profile
grep -q '^update --cpus 1 --memory 2048m --memory-swap 2048m btc-id$' "$log" || fail "byte memory usage was parsed incorrectly"
grep -q '^update --cpus 1 --memory 2048m --memory-swap 2048m xmr-id$' "$log" || fail "KiB memory usage was parsed incorrectly"

new_case dirty-stop
perl -pi -e 's/MONERO_RESOURCE_PROFILE=off/MONERO_RESOURCE_PROFILE=compact/' "$case_root/state/.env"
export FAKE_MONERO_SYNCED=1 FAKE_MONERO_MEMORY=1900MiB FAKE_EXIT_CODE=137
tick_profile; tick_profile; tick_profile
grep -q ' stop monero$' "$log" || fail "high-memory Monero was not stopped"
grep -q ' start monero$' "$log" || fail "Monero was not restarted after a dirty stop"
! grep -q '^update ' "$log" || fail "Monero was capped after a dirty stop"
grep -q '|blocked|' "$case_root/state/resource-profile/monero" || fail "dirty Monero stop was not marked blocked"

grep -q '^ProtectSystem=strict$' packaging/systemd/node-manager-resource-profile.service || fail "systemd service is not filesystem-hardened"
grep -q '^ProtectHome=read-only$' packaging/systemd/node-manager-resource-profile.service || fail "service cannot read node data dirs under /home"
grep -q '^RestrictAddressFamilies=AF_UNIX$' packaging/systemd/node-manager-resource-profile.service || fail "systemd service can use public network sockets"
grep -q '^Persistent=true$' packaging/systemd/node-manager-resource-profile.timer || fail "systemd timer does not catch up after reboot"

new_case host-stats
mkdir -p "$case_root/state/data/bitcoin" "$case_root/state/data/tari"
head -c 2097152 /dev/zero >"$case_root/state/data/bitcoin/blk00000.dat"
export FAKE_HOST_STATS_OUT=$tmp/host-stats-payload.json
run_profile
unset FAKE_HOST_STATS_OUT
[ -s "$tmp/host-stats-payload.json" ] || fail "collector wrote no host stats payload"
jq -e '.cpus == 4 and .nodes.monero.cpu_pct > 40 and .nodes.bitcoin.disk_bytes > 0 and .nodes.bitcoin.cpu_pct == 0 and (.nodes | has("tari") | not) and .nodes.stack.disk_bytes > 0 and .nodes.stack.disk_bytes < 10000000 and (.host_cpu_pct // 0) >= 0 and (.host_mem_bytes // 0) >= 0' "$tmp/host-stats-payload.json" >/dev/null || fail "host stats payload lost disk bytes or kept an empty placeholder dir"
jq -e '.nodes.monero.mem_bytes > 3000000000' "$tmp/host-stats-payload.json" >/dev/null || fail "docker-stats memory fallback was lost"
mkdir -p "$case_root/cgroup/system.slice/docker-cid-monero.scope"
printf 'anon 1234567890\nfile 99\n' >"$case_root/cgroup/system.slice/docker-cid-monero.scope/memory.stat"
export RESOURCE_CGROUP_ROOT=$case_root/cgroup FAKE_HOST_STATS_OUT=$tmp/host-stats-payload.json
run_profile
unset RESOURCE_CGROUP_ROOT FAKE_HOST_STATS_OUT
jq -e '.nodes.monero.mem_bytes == 1234567890' "$tmp/host-stats-payload.json" >/dev/null || fail "cgroup anon memory did not override docker stats"

new_case thermal
cat >>"$case_root/state/.env" <<'ENVEOF'
THERMAL_LIMIT_C=85
THERMAL_RESUME_C=75
ENVEOF
mkdir -p "$case_root/thermal"
printf '90000\n' >"$case_root/thermal/temp"
export RESOURCE_THERMAL_GLOB="$case_root/thermal/temp" FAKE_EVENTS_OUT=$tmp/thermal-events.log
export FAKE_SETTINGS_JSON='{"thermal_throttle":true}'
run_profile
grep -q '^2000000000 ' "$caps/xmr-id" || fail "hot pass did not halve monero from unlimited"
[ "$(cat "$case_root/state/thermal-throttle/monero")" = "xmr-id 0" ] || fail "original cap missing its container id"
tick_profile
grep -q '^1000000000 ' "$caps/xmr-id" || fail "second hot pass did not halve again"
printf '70000\n' >"$case_root/thermal/temp"
tick_profile
grep -q '^4000000000 ' "$caps/xmr-id" || fail "cool pass did not restore uncapped monero to every host CPU"
[ ! -f "$case_root/state/thermal-throttle/monero" ] || fail "throttle state was not cleared"
grep -q 'thermal throttle at 90C' "$tmp/thermal-events.log" || fail "throttle event line missing"
grep -q 'thermal throttle released (cooled to 70C)' "$tmp/thermal-events.log" || fail "release event line missing"

printf '90000\n' >"$case_root/thermal/temp"
run_profile
grep -q '^2000000000 ' "$caps/xmr-id" || fail "re-throttle did not halve monero"
export FAKE_SETTINGS_JSON='{"thermal_throttle":false}'
run_profile
grep -q '^4000000000 ' "$caps/xmr-id" || fail "disabling the throttle in settings did not restore monero"
grep -q 'thermal throttle released (throttle disabled)' "$tmp/thermal-events.log" || fail "disable release event missing"
export FAKE_SETTINGS_JSON='{"thermal_throttle":true}'
run_profile
grep -q '^2000000000 ' "$caps/xmr-id" || fail "throttle did not re-engage for recreate case"
export FAKE_MONERO_ID=xmr-id-2
printf '70000\n' >"$case_root/thermal/temp"
run_profile
[ ! -f "$case_root/state/thermal-throttle/monero" ] || fail "recreated container kept stale throttle state"
[ ! -f "$caps/xmr-id-2" ] || fail "stale original restored onto recreated container"
export FAKE_MONERO_ID=xmr-id
unset RESOURCE_THERMAL_GLOB FAKE_EVENTS_OUT FAKE_SETTINGS_JSON

echo "PASS: test_resource_profile.sh"
