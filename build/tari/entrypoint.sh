#!/bin/sh
set -eu
transport=tcp_tor
[ "${TOR_CLEARNET:-1}" = 1 ] || transport=tor
methods=${TARI_GRPC_EXTRA_METHODS:-}
case $methods in *[!a-z_,]*) echo "TARI_GRPC_EXTRA_METHODS may hold only a-z, _ and commas." >&2; exit 1 ;; esac
extra=
IFS=,
for method in $methods; do [ -z "$method" ] || extra="$extra, \"$method\""; done
unset IFS
sed -e "s/__PRUNING_HORIZON__/$TARI_PRUNING_HORIZON/" -e "s/__TRANSPORT__/$transport/" -e "s/__GRPC_EXTRA_METHODS__/$extra/" /var/tari/config/config.toml >/var/tari/node/runtime-config.toml
export TARI_CONFIG=/var/tari/node/runtime-config.toml
exec start_tari_app.sh --disable-splash-screen --non-interactive-mode
