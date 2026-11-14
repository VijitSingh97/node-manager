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
cat >"/tmp/$NODE_BIN.conf" <<CONF
server=1
disablewallet=1
rpcbind=0.0.0.0
rpcallowip=172.29.0.0/24
rpcuser=$rpc_user
rpcpassword=$rpc_password
prune=$prune
proxy=$TOR_PROXY
onion=$TOR_PROXY
onlynet=onion
listen=0
dnsseed=0
discover=0
CONF
if [ "$(printenv "${NODE_ENV_PREFIX}_WATCH_WALLET" || true)" = 1 ]; then
  grep -v '^disablewallet=1$' "/tmp/$NODE_BIN.conf" >"/tmp/$NODE_BIN.conf.new"
  mv "/tmp/$NODE_BIN.conf.new" "/tmp/$NODE_BIN.conf"
fi
exec "${NODE_BIN}d" -datadir=/data -conf="/tmp/$NODE_BIN.conf"
