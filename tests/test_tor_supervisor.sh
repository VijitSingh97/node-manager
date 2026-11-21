#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  exit 1
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fake=$tmp/bin
mkdir "$fake"

cat >"$fake/tor" <<'EOF'
#!/bin/sh
printf '%s\n' "$$" >"$FAKE_TOR_PID_FILE"
trap 'exit 0' TERM INT
while :; do sleep 1; done
EOF

cat >"$fake/socat" <<'EOF'
#!/bin/sh
printf '%s\n' "$$" >"$FAKE_SOCAT_PID_FILE"
trap 'exit 0' TERM INT
while :; do sleep 1; done
EOF
chmod +x "$fake/tor" "$fake/socat"

entrypoint=$tmp/entrypoint.sh
sed \
  -e "s|/etc/tor/torrc|$tmp/torrc.source|g" \
  -e "s|/tmp/torrc|$tmp/torrc.rendered|g" \
  -e "s|/var/lib/tor|$tmp/lib/tor|g" \
  -e "s|/tmp/tor-health|$tmp/tor-health|g" \
  build/tor/entrypoint.sh >"$entrypoint"
chmod +x "$entrypoint"
printf 'SocksPort 9050\n' >"$tmp/torrc.source"

[ "$(PATH="$fake:/usr/bin:/bin" command -v tor)" = "$fake/tor" ] || fail "test could reach a real Tor executable"
[ "$(PATH="$fake:/usr/bin:/bin" command -v socat)" = "$fake/socat" ] || fail "test could reach a real socat executable"

start_supervisor() {
  : >"$tmp/tor.pid"
  : >"$tmp/socat.pid"
  PATH="$fake:/usr/bin:/bin" \
    FAKE_TOR_PID_FILE="$tmp/tor.pid" \
    FAKE_SOCAT_PID_FILE="$tmp/socat.pid" \
    "$entrypoint" &
  supervisor_pid=$!
  for _ in $(seq 1 50); do
    [ -s "$tmp/tor.pid" ] && [ -s "$tmp/socat.pid" ] && return
    kill -0 "$supervisor_pid" 2>/dev/null || fail "Tor supervisor exited during test setup"
    sleep 0.1
  done
  fail "Tor supervisor children did not start"
}

wait_status() {
  set +e
  wait "$supervisor_pid"
  supervisor_status=$?
  set -e
}

mkdir -p "$tmp/lib/tor/bitcoin_onion" "$tmp/lib/tor-onions"
printf 'guards\n' >"$tmp/lib/tor/state"
printf 'key\n' >"$tmp/lib/tor/bitcoin_onion/hs_ed25519_secret_key"

printf 'first_seen 1\nfail_since 2\n' >"$tmp/tor-health.state"
start_supervisor
[ ! -e "$tmp/tor-health.state" ] || fail "stale health checker state survived a Tor start"
[ -f "$tmp/lib/tor/state" ] || fail "Tor state moved without a reset request"
kill -TERM "$supervisor_pid"
wait_status

: >"$tmp/lib/tor/.reset-state"
start_supervisor
[ ! -e "$tmp/lib/tor/state" ] || fail "reset request did not move the Tor state aside"
[ ! -e "$tmp/lib/tor/state.reset" ] || fail "reset request kept the old guard list on disk"
[ ! -e "$tmp/lib/tor/.reset-state" ] || fail "reset request flag was not removed"
[ -f "$tmp/lib/tor/bitcoin_onion/hs_ed25519_secret_key" ] || fail "reset request touched the onion keys"
kill -TERM "$supervisor_pid"
wait_status
: >"$tmp/lib/tor/.reset-state"
start_supervisor
[ ! -e "$tmp/lib/tor/.reset-state" ] || fail "reset request without a state file did not clear its flag"
kill -TERM "$supervisor_pid"
wait_status

start_supervisor
kill -TERM "$(cat "$tmp/tor.pid")"
wait_status
[ "$supervisor_status" -ne 0 ] || fail "clean Tor child exit did not fail the supervisor"

start_supervisor
kill -TERM "$(cat "$tmp/socat.pid")"
wait_status
[ "$supervisor_status" -ne 0 ] || fail "clean relay child exit did not fail the supervisor"

start_supervisor
kill -TERM "$supervisor_pid"
wait_status
[ "$supervisor_status" -eq 0 ] || fail "intentional supervisor shutdown returned failure"

echo "PASS: test_tor_supervisor.sh"
