#!/bin/sh
set -eu
transport=tcp_tor
[ "${TOR_CLEARNET:-1}" = 1 ] || transport=tor
sed -e "s/__PRUNING_HORIZON__/$TARI_PRUNING_HORIZON/" -e "s/__TRANSPORT__/$transport/" /var/tari/config/config.toml >/var/tari/node/runtime-config.toml
export TARI_CONFIG=/var/tari/node/runtime-config.toml
exec start_tari_app.sh --disable-splash-screen --non-interactive-mode
