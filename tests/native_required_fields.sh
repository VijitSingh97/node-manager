# shellcheck shell=bash
native_required_fields() {
  case "$1" in
    monero) echo "fees_available mempool_txs_available" ;;
    tari) echo "connections_available mempool_txs_available" ;;
    zcash) echo "fees_available connections_available" ;;
  esac
}

native_required_missing() {
  local chain=$1 file=$2 field missing=""
  for field in $(native_required_fields "$chain"); do
    jq -e --arg f "$field" '.[$f] == true' "$file" >/dev/null 2>&1 || missing="$missing $field"
  done
  printf '%s' "${missing# }"
}
