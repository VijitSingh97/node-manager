#!/bin/sh
set -eu

: "${MONERO_RPC_USER:?missing MONERO_RPC_USER}"
: "${MONERO_RPC_PASSWORD:?missing MONERO_RPC_PASSWORD}"
: "${TOR_PROXY:?missing TOR_PROXY}"

umask 077
config=/tmp/monerod.conf
cat >"$config" <<EOF
data-dir=/data
log-file=/data/bitmonero.log
log-level=0
prune-blockchain=1
sync-pruned-blocks=1
proxy=socks5://$TOR_PROXY
tx-proxy=tor,$TOR_PROXY,disable_noise
pad-transactions=1
disable-dns-checkpoints=1
check-updates=disabled
hide-my-port=1
no-igd=1
in-peers=0
out-peers=12
rpc-bind-ip=0.0.0.0
rpc-bind-port=18085
rpc-restricted-bind-ip=0.0.0.0
rpc-restricted-bind-port=18081
confirm-external-bind=1
rpc-login=$MONERO_RPC_USER:$MONERO_RPC_PASSWORD
zmq-pub=tcp://0.0.0.0:18083
EOF

exec monerod --non-interactive --config-file="$config"
