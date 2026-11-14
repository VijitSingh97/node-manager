#!/bin/sh
"$NODE_BIN-cli" -datadir=/data -conf="/tmp/$NODE_BIN.conf" getblockchaininfo >/dev/null
