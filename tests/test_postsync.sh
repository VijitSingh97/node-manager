#!/usr/bin/env bash
# Post-sync assertions against an ALREADY RUNNING, ALREADY SYNCED stack —
# the behaviors a fresh-datadir e2e can never reach: out of IBD, at the
# chain tip, live onion-only peers, mempool + fee estimates flowing, and
# the dashboard/metrics rendering synced stats. Read-only (RPC + HTTP GETs),
# safe to run against a production node. Self-skips unless requested:
#   RUN_POSTSYNC=1 bash tests/test_postsync.sh
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -z "${RUN_POSTSYNC:-}" ]; then
  echo "SKIP: test_postsync.sh (set RUN_POSTSYNC=1; needs a running, synced stack)"
  exit 0
fi

fail() {
  echo "FAIL: $1"
  exit 1
}
bcli() { docker compose exec -T bitcoin bitcoin-cli -datadir=/data "$@"; }

info=$(bcli getblockchaininfo) || fail "bitcoind RPC unreachable"
echo "$info" | grep -q '"initialblockdownload": false' || fail "node is still in initial block download"
blocks=$(echo "$info" | grep -o '"blocks": *[0-9]*' | grep -o '[0-9]*')
headers=$(echo "$info" | grep -o '"headers": *[0-9]*' | grep -o '[0-9]*')
[ "$((headers - blocks))" -le 2 ] || fail "blocks ($blocks) lag headers ($headers) — not at the tip"

peers=$(bcli getpeerinfo)
n=$(echo "$peers" | grep -c '"id":' || true)
[ "$n" -ge 1 ] || fail "no peers connected"
clearnet=$(echo "$peers" | grep '"addr":' | grep -v '\.onion' || true)
[ -z "$clearnet" ] || fail "non-onion peer(s) connected: $clearnet"

bcli getmempoolinfo | grep -q '"size"' || fail "no mempool info"
bcli estimatesmartfee 6 | grep -q '"feerate"' || fail "no fee estimate on a synced node"

# Dashboard (auth-aware: use the password from .env when one is set)
pass=$(sed -n 's/^DASHBOARD_PASSWORD=//p' .env 2>/dev/null || true)
body=$(curl -sf ${pass:+-u "admin:$pass"} localhost:80) || fail "dashboard unreachable"
grep -q "Sync Progress" <<<"$body" || fail "dashboard not rendering live stats"
grep -qi "Initializing" <<<"$body" && fail "dashboard stuck on the loading page"
curl -sf ${pass:+-u "admin:$pass"} localhost:80/metrics | grep -q "bitcoin_node_up 1" ||
  fail "metrics missing bitcoin_node_up 1"

echo "PASS: test_postsync.sh (height $blocks, $n onion peer(s))"
