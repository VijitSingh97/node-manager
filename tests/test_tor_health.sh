#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=tests/lib.sh
. tests/lib.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
cat >"$tmp/bin/nc" <<'EOS'
#!/bin/sh
case " $* " in
*" -U "*)
  cat >/dev/null
  [ "${FAKE_CTL_FAIL:-0}" != 1 ] || exit 1
  cat "$FAKE_CTL_REPLY"
  ;;
*)
  printf '%s\n' "$*" >>"$FAKE_PROBE_LOG"
  [ -z "${FAKE_NC_HANG:-}" ] || sleep "$FAKE_NC_HANG"
  for arg; do host=$prev; prev=$arg; done
  case " $FAKE_NC_OK " in *" $host "*) exit 0 ;; esac
  exit 1
  ;;
esac
EOS
chmod +x "$tmp/bin/nc"
printf '\001\002\377' >"$tmp/cookie"
export PATH="$tmp/bin:$PATH" TOR_COOKIE=$tmp/cookie TOR_HEALTH_STATE=$tmp/state TOR_CONTROL_SOCKET=$tmp/sock
export FAKE_CTL_REPLY=$tmp/reply FAKE_PROBE_LOG=$tmp/probes FAKE_NC_OK=

reply() {
  local progress=$1 liveness=$2 orconn=$3
  printf '250 OK\r\n'
  printf '250-status/bootstrap-phase=NOTICE BOOTSTRAP PROGRESS=%s TAG=done SUMMARY="Done"\r\n250 OK\r\n' "$progress"
  printf '250-network-liveness=%s\r\n250 OK\r\n' "$liveness"
  if [ -n "$orconn" ]; then printf '250+orconn-status=\r\n%s\r\n.\r\n250 OK\r\n' "$orconn"; else printf '250-orconn-status=\r\n250 OK\r\n'; fi
  printf '250 closing connection\r\n'
}
connected=$'$AAAA~guard CONNECTED\r\n$BBBB~two CONNECTED\r\n$CCCC~three LAUNCHED\r\n$DDDD~four CONNECTED'
check() {
  rc=0
  out=$(TOR_HEALTH_NOW=$1 build/tor/tor-health.sh) || rc=$?
}
expect() {
  [ "$rc" = "$1" ] || fail "$3: exit $rc, wanted $1 ($out)"
  case $out in "tor: $2"*) ;; *) fail "$3: output '$out' does not start with 'tor: $2'" ;; esac
}
fresh() { rm -f "$tmp/state" "$FAKE_PROBE_LOG"; }

fresh
reply 100 up "$connected" >"$FAKE_CTL_REPLY"
FAKE_NC_OK="www.torproject.org check.torproject.org" check 1000
expect 0 "ok via www.torproject.org" "first destination reachable"
[ "$out" = "tor: ok via www.torproject.org" ] || fail "a working Tor was probed through its control socket: $out"
[ "$(cat "$FAKE_PROBE_LOG")" = "-z -w 20 -X 5 -x 127.0.0.1:9050 www.torproject.org 443" ] || fail "probe is not one SOCKS5 stream to port 443 of the first host: $(cat "$FAKE_PROBE_LOG")"

fresh
FAKE_NC_OK="api.github.com" check 1000
expect 0 "ok via api.github.com" "third destination reachable"
fresh
FAKE_NC_OK="check.torproject.org" check 1000
expect 0 "ok via check.torproject.org" "second destination reachable"
[ "$(awk '{print $(NF-1)}' "$FAKE_PROBE_LOG" | tr '\n' ' ')" = "www.torproject.org check.torproject.org " ] || fail "destinations were not tried in order"
FAKE_NC_OK="" TOR_PROBE_HOSTS="a.example b.example c.example" check 1001
[ "$(wc -l <"$FAKE_PROBE_LOG" | tr -d " ")" = 5 ] || fail "a failing probe did not try every configured destination once"

fresh
export FAKE_NC_OK=
check 1000; expect 0 "probe-failed for 0s bootstrap=100 liveness=up orconns=3" "first failure"
check 1239; expect 0 "probe-failed for 239s" "failing under 4 minutes"
check 1240; expect 1 "probe-failed for 240s" "failing for 4 minutes"
check 1500; expect 1 "probe-failed for 500s" "still failing"
FAKE_NC_OK=www.torproject.org check 1530; expect 1 "probe-failed for 0s" "one success does not clear a failing Tor"
FAKE_NC_OK=www.torproject.org check 1560; expect 1 "probe-failed" "two successes do not clear it"
FAKE_NC_OK=www.torproject.org check 1590; expect 0 "ok via www.torproject.org" "three successes in a row clear it"
for t in 1620 1650 1680 1710; do FAKE_NC_OK=www.torproject.org check "$t"; done
expect 0 "ok" "steady after recovery"
check 1740; expect 0 "probe-failed for 0s" "one failure after recovery is reported but not unhealthy"
check 1770; check 1800; check 1830; check 1860
expect 0 "probe-failed for 120s" "a new run of failures starts a new count"

fresh
export FAKE_NC_OK=
for t in 1000 1030 1060; do check "$t"; done
FAKE_NC_OK=www.torproject.org check 1090
expect 0 "ok" "one success among three failures is below the minimum sample count"
check 1120; expect 1 "probe-failed for 0s bootstrap=100 liveness=up orconns=3 probes_ok=1/5" "a brown-out of one success in five probes is unhealthy"
for t in 1150 1180; do FAKE_NC_OK=www.torproject.org check "$t"; done
expect 1 "probe-failed for 0s bootstrap=100 liveness=up orconns=3" "two successes in a row do not clear a brown-out"
FAKE_NC_OK=www.torproject.org check 1210
expect 0 "ok via www.torproject.org" "three successes in a row clear a brown-out"
check 1240; expect 0 "probe-failed for 0s" "old failures are dropped once cleared, so one failure is not unhealthy"

fresh
export FAKE_NC_OK=
for t in 1000 1090 1180; do check "$t"; done
FAKE_NC_OK=www.torproject.org check 1210
FAKE_NC_OK=www.torproject.org check 1240
expect 0 "ok via www.torproject.org" "recovering from a short outage is not a brown-out"

fresh
for t in 1000 1030 1060 1090 1120 1150 1180 1210; do
  if [ "$t" = 1090 ] || [ "$t" = 1180 ]; then check "$t"; else FAKE_NC_OK=www.torproject.org check "$t"; fi
done
expect 0 "ok" "an occasional failure among successes stays healthy"
fresh
for t in 1000 1030 1060 1090; do check "$t"; done
FAKE_NC_OK=www.torproject.org check 1700
expect 0 "ok" "failures older than the 10 minute window are forgotten"
check 1730; check 1760; check 1790; check 1820; check 1850
expect 1 "probe-failed" "a window with only failures is unhealthy"

fresh
reply 100 down "" >"$FAKE_CTL_REPLY"
check 1000; expect 0 "offline for 0s bootstrap=100 liveness=down orconns=0" "offline label"
check 1240; expect 0 "offline for 240s" "offline for 4 minutes is not a failure"
check 3599; expect 0 "offline for 2599s" "a long outage is not a failure"
check 4600; expect 1 "offline for 3600s" "a Tor that says offline for an hour is treated as stuck"
reply 100 up "$connected" >"$FAKE_CTL_REPLY"
check 4630; expect 0 "probe-failed for 0s" "failures during the outage do not count once the link is back"
check 4869; expect 0 "probe-failed for 239s" "the stall timer starts when the link returns"
check 4870; expect 1 "probe-failed for 240s" "a stall after the link returns is detected"

fresh
reply 100 down "$connected" >"$FAKE_CTL_REPLY"
check 1000; expect 0 "probe-failed for 0s" "liveness down with relay connections is a probe failure"
check 1240; expect 1 "probe-failed for 240s" "liveness down with relay connections can fail"

fresh
reply 50 up "" >"$FAKE_CTL_REPLY"
check 1000; expect 0 "bootstrapping uptime=0s bootstrap=50" "young bootstrap is neutral"
check 1599; expect 0 "bootstrapping uptime=599s" "bootstrap neutral just under 10 minutes"
check 1600; expect 0 "probe-failed for 0s bootstrap=50" "bootstrap stuck at 10 minutes starts counting"
check 1840; expect 1 "probe-failed for 240s" "stuck bootstrap fails 4 minutes after the neutral window"

fresh
reply 100 up "$connected" >"$FAKE_CTL_REPLY"
check 1000; check 1100
expect 0 "probe-failed for 100s" "a bootstrapped Tor with a failing probe is not neutral while young"

fresh
printf '515 Authentication failed\r\n' >"$FAKE_CTL_REPLY"
FAKE_NC_OK=www.torproject.org check 1000
expect 0 "ok via www.torproject.org" "a rejected control login never fails a working Tor"
FAKE_NC_OK=www.torproject.org FAKE_CTL_FAIL=1 check 1001
expect 0 "ok via www.torproject.org" "a dead control socket never fails a working Tor"
printf 'first_seen 1\nfail_since 0\n' >"$tmp/state"
FAKE_CTL_FAIL=1 check 1002; expect 0 "probe-failed for 0s control=unavailable" "failing probe without control"
FAKE_CTL_FAIL=1 check 1242; expect 1 "probe-failed for 240s control=unavailable" "failing probe without control exits 1"
rm -f "$tmp/state"
TOR_COOKIE=$tmp/missing FAKE_NC_OK=www.torproject.org check 1243
expect 0 "ok via www.torproject.org" "a missing cookie never fails a working Tor"

fresh
printf 'first_seen abc\nfail_since -5\n' >"$tmp/state"
reply 100 up "$connected" >"$FAKE_CTL_REPLY"
check 5000; expect 0 "probe-failed for 0s" "garbage state is treated as fresh"
printf 'first_seen 1\nfail_since 9999999999\n' >"$tmp/state"
check 5001; expect 0 "probe-failed for 0s" "a failure time in the future is ignored"

fresh
FAKE_NC_OK=www.torproject.org check 1000
reply 100 up "$connected" >"$FAKE_CTL_REPLY"
out=$(TOR_HEALTH_NOW=1001 build/tor/tor-health.sh snapshot)
case $out in *"bootstrap=100 liveness=up orconns=3"*) ;; *) fail "snapshot lacks the context line: $out" ;; esac
case $out in *"orconn \$AAAA~guard CONNECTED"*) ;; *) fail "snapshot lacks orconn lines: $out" ;; esac
{
  printf '250 OK\r\n250+circuit-status=\r\n'
  printf '%s BUILT %s PURPOSE=GENERAL\r\n' 1 "\$A~g" 2 "\$A~g"
  printf '3 LAUNCHED PURPOSE=GENERAL\r\n.\r\n250 OK\r\n'
  printf '250-status/circuit-established=0\r\n250-dormant=0\r\n250+entry-guards=\r\n%s up\r\n.\r\n250 OK\r\n' "\$G~one"
} >"$FAKE_CTL_REPLY"
out=$(build/tor/tor-health.sh snapshot)
case $out in *"info 250-status/circuit-established=0"*"info 250-dormant=0"*"guard \$G~one up"*) ;; *) fail "snapshot lacks circuit-established, dormant or guard lines: $out" ;; esac
case $out in *"circuits BUILT 2"*"circuits LAUNCHED 1"* | *"circuits LAUNCHED 1"*"circuits BUILT 2"*) ;; *) fail "snapshot lacks circuit counts: $out" ;; esac

fresh
reply 100 up "$connected" >"$FAKE_CTL_REPLY"
start=$SECONDS
FAKE_NC_HANG=20 TOR_PROBE_TIMEOUT=1 check 1000
[ $((SECONDS - start)) -lt 10 ] || fail "a hanging SOCKS handshake was not bounded by the probe timeout"
expect 0 "probe-failed for 0s" "hung probes are reported as failures"

echo "PASS: test_tor_health.sh"
