#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/native_required_fields.sh

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

check() {
  local chain=$1 json=$2 want=$3 got
  printf '%s' "$json" >"$work/status.json"
  got=$(native_required_missing "$chain" "$work/status.json")
  [ "$got" = "$want" ] || { echo "FAIL: $chain want missing '$want', got '$got'" >&2; exit 1; }
}

check monero '{"fees_available":true,"mempool_txs_available":true}' ""
check monero '{"fees_available":true,"mempool_txs_avail":true}' "mempool_txs_available"
check monero '{"fees_available":false,"mempool_txs_available":true}' "fees_available"
check tari '{"connections_available":true,"mempool_txs_available":true}' ""
check tari '{"connection_available":true,"mempool_txs_available":true}' "connections_available"
check zcash '{"fees_available":true,"connections_available":true}' ""
check zcash '{}' "fees_available connections_available"
check litecoin '{}' ""
echo "PASS: native-node required-field checker catches missing, renamed and false fields"
