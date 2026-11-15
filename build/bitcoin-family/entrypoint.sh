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
CONF
  if [ "${TOR_CLEARNET:-1}" = 1 ]; then
    echo dnsseed=1
  else
    printf 'onlynet=onion\ndnsseed=0\n'
  fi
  if [ "${TOR_INBOUND:-0}" = 1 ] && [ -n "${ONION_BIND:-}" ]; then
    printf 'listen=1\nlistenonion=1\ntorcontrol=%s:9051\nbind=127.0.0.1\nbind=%s=onion\n' "${TOR_PROXY%:*}" "$ONION_BIND"
  else
    echo listen=0
  fi
} >"/tmp/$NODE_BIN.conf"
if [ "$(printenv "${NODE_ENV_PREFIX}_WATCH_WALLET" || true)" = 1 ]; then
  grep -v '^disablewallet=1$' "/tmp/$NODE_BIN.conf" >"/tmp/$NODE_BIN.conf.new"
  mv "/tmp/$NODE_BIN.conf.new" "/tmp/$NODE_BIN.conf"
fi
exec "${NODE_BIN}d" -datadir=/data -conf="/tmp/$NODE_BIN.conf"
