#!/bin/sh
set -eu
umask 077
setting() {
  printenv "${NODE_ENV_PREFIX}_$1" || {
    echo "${NODE_ENV_PREFIX}_$1 is not set" >&2
    exit 1
  }
}
rpc_user=$(setting RPC_USER)
rpc_password=$(setting RPC_PASSWORD)
prune=$(setting PRUNE)
data=${NODE_DATA_DIR:-/data}
onion_address() {
  waited=0
  while [ ! -s "${ONION_HOSTNAME:-}" ]; do
    [ "$waited" -lt 120 ] || return 1
    waited=$((waited + 1))
    sleep 1
  done
  grep -Eqx '[a-z2-7]{56}\.onion' "$ONION_HOSTNAME" && cat "$ONION_HOSTNAME"
}
# Tor exits cannot resolve some seeders, so ask a DNS-over-HTTPS resolver through Tor instead.
seed_peers() {
  octet='(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])'
  tries=0
  while [ "$tries" -lt 3 ]; do
    tries=$((tries + 1))
    peers=$(for host in $SEED_DOH_HOSTS; do
      curl -q -s --noproxy '' --proto =https --max-time 20 --socks5-hostname "$TOR_PROXY" \
        -H 'accept: application/dns-json' "https://cloudflare-dns.com/dns-query?name=$host&type=A" || true
    done | grep -oE '"data":"[0-9.]+"' | cut -d'"' -f4 | grep -Ex "($octet\.){3}$octet" |
      grep -vE '^(0|10|127)\.|^169\.254\.|^172\.(1[6-9]|2[0-9]|3[01])\.|^192\.168\.|^100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.|^192\.0\.[02]\.|^198\.(1[89]|51\.100)\.|^203\.0\.113\.|^(22[4-9]|2[3-5][0-9])\.' |
      sort -u | awk 'BEGIN { srand() } { print rand() "\t" $0 }' | sort | cut -f2 | head -8)
    if [ -n "$peers" ]; then
      echo "Resolved $(echo "$peers" | wc -l | tr -d ' ') seed peers through Tor." >&2
      echo "$peers" | sed "s/\$/:$SEED_PORT/; s/^/addnode=/"
      return
    fi
    [ "$tries" -lt 3 ] && sleep 10
  done
  echo "No seed peers resolved through Tor; starting from the built-in seeds." >&2
}
seeds=
if [ "${TOR_CLEARNET:-1}" = 1 ] && [ -n "${SEED_DOH_HOSTS:-}" ] && [ ! -e "$data/.seed-lookup-done" ]; then
  seeds=$(seed_peers)
  [ -z "$seeds" ] || touch "$data/.seed-lookup-done" 2>/dev/null || true
fi
{
  cat <<CONF
server=1
disablewallet=1
rpcbind=0.0.0.0
rpcallowip=172.29.0.0/24
rpcuser=$rpc_user
rpcpassword=$rpc_password
prune=$prune
proxy=$TOR_PROXY
onion=$TOR_PROXY
discover=0
upnp=0
CONF
  if [ "${TOR_CLEARNET:-1}" = 1 ] && [ -n "${SEED_DOH_HOSTS:-}" ]; then
    echo dnsseed=0
    [ -z "$seeds" ] || echo "$seeds"
  elif [ "${TOR_CLEARNET:-1}" = 1 ]; then
    echo dnsseed=1
  else
    printf 'onlynet=onion\ndnsseed=0\n'
  fi
  if [ "${TOR_INBOUND:-0}" = 1 ] && [ -n "${ONION_BIND:-}" ]; then
    printf 'listen=1\nlistenonion=0\nbind=127.0.0.1\nbind=%s=onion\n' "$ONION_BIND"
    if onion=$(onion_address); then
      echo "externalip=$onion"
    else
      echo "Tor has not published this node's onion address; not advertising it." >&2
    fi
  else
    echo listen=0
  fi
  [ "$NODE_BIN" != bitcoin ] || echo natpmp=0
} >"/tmp/$NODE_BIN.conf"
if [ "$(printenv "${NODE_ENV_PREFIX}_WATCH_WALLET" || true)" = 1 ]; then
  grep -v '^disablewallet=1$' "/tmp/$NODE_BIN.conf" >"/tmp/$NODE_BIN.conf.new"
  mv "/tmp/$NODE_BIN.conf.new" "/tmp/$NODE_BIN.conf"
fi
exec "${NODE_BIN}d" -datadir="$data" -conf="/tmp/$NODE_BIN.conf"
