#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=tests/lib.sh
. tests/lib.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
root=$tmp/root
state=$tmp/state
dash=$tmp/dash
mkdir -p "$root/scripts" "$state" "$dash"
cp scripts/node-control scripts/helper-common.sh "$root/scripts/"
cat >"$tmp/docker" <<'EOS'
#!/usr/bin/env bash
set -eu
case "$1" in
  ps)
    case "$*" in
      *'{{.State}}'*)
        printf 'dashboard dashboard-id running %s\n' "${FAKE_CONFIG_FILES:-$FAKE_ROOT/docker-compose.yml}"
        IFS='|' read -r running health _ <"$FAKE_INSPECT" || true
        [ "${FAKE_NO_TOR:-0}" = 1 ] || printf 'tor tor-id %s %s/docker-compose.yml Up 9 minutes (%s)\n' "$([ "$running" = true ] && echo running || echo exited)" "$FAKE_ROOT" "${health/starting/health: starting}"
        ;;
      *service=dashboard*) printf 'dashboard-id\n' ;;
      *service=tor*) [ "${FAKE_NO_TOR:-0}" = 1 ] || printf 'tor-id\n' ;;
    esac ;;
  inspect)
    case "$*" in *config_files*) printf '%s\n' "${FAKE_CONFIG_FILES:-$FAKE_ROOT/docker-compose.yml}"; exit 0 ;; esac
    printf 'inspect %s\n' "$*" >>"$FAKE_ACTIONS.inspect"
    [ "${FAKE_INSPECT_FAIL:-0}" != 1 ] || exit 1
    cat "$FAKE_INSPECT" ;;
  restart)
    printf 'restart %s\n' "$*" >>"$FAKE_ACTIONS"
    [ "${FAKE_RESTART_FAIL:-0}" != 1 ] || exit 1 ;;
  logs) printf 'fake tor log line\n' ;;
  exec)
    shift
    [ "${1:-}" != -i ] || shift
    if [ "${1:-}" = -u ]; then shift 2; fi
    if [ "$1" = tor-id ]; then
      if [ "$2" = tor-health ]; then printf 'orconn fake-snapshot-body\n'; exit 0; fi
      [ "${FAKE_TOUCH_FAIL:-0}" != 1 ] || exit 1
      printf '%s %s\n' "$2" "$*" >>"$FAKE_ACTIONS"
      exit 0
    fi
    [ "$1" = dashboard-id ] || exit 1
    [ "${FAKE_DASH_FAIL:-0}" != 1 ] || exit 1
    shift
    exec sh -c "${3//\/state/$FAKE_DASH_STATE}" ;;
  *) exit 1 ;;
esac
EOS
chmod +x "$tmp/docker" "$root/scripts/node-control"
export BITCOIN_STACK_ROOT=$root BITCOIN_STACK_STATE_DIR=$state BITCOIN_STACK_CONFIG_DIR=$state DOCKER_BIN=$tmp/docker
: >"$state/.env"
export FAKE_ROOT=$root FAKE_DASH_STATE=$dash FAKE_ACTIONS=$tmp/actions FAKE_INSPECT=$tmp/inspect

T0=1800000000
wd=$state/node-control/tor-watchdog
snaps=$state/node-control/tor-snapshots
fail_up='tor: probe-failed for 245s bootstrap=100 liveness=up orconns=3'
fail_off='tor: offline for 3700s bootstrap=100 liveness=down orconns=0'
fail_nolink='tor: probe-failed for 245s bootstrap=100 liveness=up orconns=0'
fail_noctl='tor: probe-failed for 245s control=unavailable'
okline='tor: ok via www.torproject.org'

inspect() {
  local running=$1 status=$2 line=$3
  printf '%s|%s|\n--\n%s\n--\n' "$running" "$status" "$line" >"$FAKE_INSPECT"
}
tick() {
  if [ "${KEEP_GATE:-0}" != 1 ] && [ -f "$wd" ]; then gate_clear; fi
  NODE_CONTROL_NOW_EPOCH=$1 "$root/scripts/node-control" || true
}
count() { local n; n=$(grep -c "$1" "$2" 2>/dev/null) || n=0; echo "${n:-0}"; }
restarts() { count '^restart' "$FAKE_ACTIONS"; }
touches() { count '^touch' "$FAKE_ACTIONS"; }
events() { count 'Host: ' "$dash/events.log"; }
snapshots() { find "$snaps" -type f 2>/dev/null | wc -l | tr -d ' '; }
reset() {
  rm -rf "$state/node-control" "$dash/events.log"
  : >"$FAKE_ACTIONS"
  rm -f "$FAKE_ACTIONS.inspect"
  export FAKE_NO_TOR=0 FAKE_INSPECT_FAIL=0 FAKE_TOUCH_FAIL=0 FAKE_DASH_FAIL=0 FAKE_RESTART_FAIL=0
  unset FAKE_CONFIG_FILES
}
expect_idle() {
  [ "$(restarts)" = 0 ] || fail "$1: restarted Tor"
  [ "$(touches)" = 0 ] || fail "$1: reset Tor state"
  [ "$(events)" = 0 ] || fail "$1: wrote an event"
}
event_has() { grep -qF -- "$1" "$dash/events.log" || fail "$2: event lacks '$1': $(cat "$dash/events.log" 2>/dev/null)"; }
gate_clear() { sed -i.bak '/^last_gate=/d' "$wd" && rm -f "$wd.bak"; }

reset
inspect true healthy "$okline"
tick "$T0"
expect_idle "healthy with no incident"
[ "$(count '^inspect' "$FAKE_ACTIONS.inspect")" = 0 ] || fail "a healthy Tor with no incident was inspected"
[ ! -d "$snaps" ] || fail "snapshot taken while healthy"

for status_line in "healthy|tor: probe-failed for 30s bootstrap=100 liveness=up orconns=3" "healthy|tor: bootstrapping uptime=30s bootstrap=50 liveness=up orconns=1" "unhealthy|tor: bootstrapping uptime=30s bootstrap=50" "unhealthy|" "unhealthy|nc: connect failed" "unhealthy|tor: okay" "starting|$fail_up" "healthy|$fail_up"; do
  reset
  inspect true "${status_line%%|*}" "${status_line#*|}"
  tick "$T0"
  expect_idle "'$status_line'"
done
reset
inspect false unhealthy "$fail_up"
tick "$T0"
expect_idle "stopped container"
inspect true unhealthy "$fail_up"
FAKE_INSPECT_FAIL=1 tick $((T0 + 60))
expect_idle "inspect failure"
FAKE_NO_TOR=1 tick $((T0 + 90))
expect_idle "missing container"
reset
FAKE_CONFIG_FILES=/elsewhere/docker-compose.yml tick "$T0"
expect_idle "tor of another stack"
inspect true unhealthy "$fail_up"
exec 8>"$state/.config.lock"
flock -n 8
tick "$T0"
expect_idle "config lock held"
exec 8>&-
tick "$T0"
[ "$(restarts)" = 1 ] || fail "Tor was not restarted once the config lock was free"

reset
inspect true unhealthy "$fail_up"
mkdir -p "$state/node-control"
: >"$wd.disabled"
tick "$T0"
expect_idle "disable file"
rm -f "$wd.disabled"
tick "$T0"
[ "$(restarts)" = 1 ] || fail "removing the disable file did not re-enable the watchdog"

reset
inspect true unhealthy "$fail_up"
tick "$T0"
[ "$(restarts)" = 1 ] || fail "first unhealthy poll did not restart Tor once"
grep -qx 'restart restart -t 30 tor-id' "$FAKE_ACTIONS" || fail "restart is not of the tor container only with a 30 s timeout"
[ "$(touches)" = 0 ] || fail "first step reset state"
[ "$(events)" = 1 ] || fail "first step wrote $(events) events"
grep -Eq '^[0-9T:-]+Z Host: Tor is not working \(probes through Tor failed for several minutes; Tor holds relay connections that may be stale, so the cause is not clear yet\); restarting Tor$' "$dash/events.log" || fail "first event text or shape is wrong: $(cat "$dash/events.log")"
[ "$(snapshots)" = 1 ] || fail "no snapshot before the restart"
[ "$(stat -c %a "$snaps"/* 2>/dev/null || stat -f %Lp "$snaps"/*)" = 600 ] || fail "snapshot is not root-only"
[ "$(stat -c %a "$snaps" 2>/dev/null || stat -f %Lp "$snaps")" = 700 ] || fail "snapshot directory is not private"
snapfile=$(find "$snaps" -type f | head -n 1)
grep -q 'fake-snapshot-body' "$snapfile" || fail "snapshot lacks control data"
grep -q 'fake tor log line' "$snapfile" || fail "snapshot lacks log lines"
grep -qF "$fail_up" "$snapfile" || fail "snapshot lacks control data, log lines or the last health output"
[ "$(stat -c %a "$wd" 2>/dev/null || stat -f %Lp "$wd")" = 600 ] || fail "watchdog state is not private"
KEEP_GATE=1
tick $((T0 + 10))
[ "$(restarts)" = 1 ] || fail "poll inside the 30 s gate acted"
tick $((T0 + 29))
[ "$(sed -n 's/^last_gate=//p' "$wd")" = "$T0" ] || fail "a poll 29 s after the last one passed the gate"
tick $((T0 + 31))
[ "$(sed -n 's/^last_gate=//p' "$wd")" = $((T0 + 31)) ] || fail "a poll 31 s after the last one was held by the gate"
KEEP_GATE=0
tick $((T0 + 479))
[ "$(restarts)" = 1 ] && [ "$(touches)" = 0 ] || fail "acted 479 s after the restart"
tick $((T0 + 480))
[ "$(touches)" = 1 ] && [ "$(restarts)" = 2 ] || fail "guard reset did not run once 480 s after the restart"
[ "$(sed -n 2p "$FAKE_ACTIONS")" = 'touch tor-id touch /var/lib/tor/.reset-state' ] || fail "reset flag was not set before the restart"
[ "$(snapshots)" = 2 ] || fail "no snapshot before the reset"
event_has "Tor is still not working after a restart while the network is up; clearing Tor's saved state (guards and learned circuit timing) and restarting Tor once more" "reset"
[ "$(sed -n 's/^reset_ts=//p' "$wd")" = $((T0 + 480)) ] || fail "reset time was not recorded after the restart"
tick $((T0 + 480 + 479))
[ "$(restarts)" = 2 ] && [ "$(events)" = 2 ] || fail "acted 479 s after the reset"
tick $((T0 + 480 + 480))
[ "$(restarts)" = 2 ] && [ "$(touches)" = 1 ] || fail "ladder restarted or reset a third time"
[ "$(events)" = 3 ] || fail "pause wrote $(events) events in total"
event_has 'Tor is still not working after a restart and a guard state reset (Tor is connected to relays but builds no working circuits, so Tor looks stuck); automatic recovery is paused until Tor works again' "pause"
for i in 1 2 3 4 5 6; do tick $((T0 + 5000 * i)); done
[ "$(restarts)" = 2 ] && [ "$(touches)" = 1 ] && [ "$(events)" = 3 ] || fail "paused ladder acted again"
inspect true healthy "tor: probe-failed for 10s bootstrap=100 liveness=up orconns=3"
tick $((T0 + 40000))
[ "$(events)" = 3 ] || fail "a healthy status without a successful probe ended the incident"
inspect true healthy "$okline"
tick $((T0 + 40100))
[ "$(events)" = 4 ] || fail "recovery wrote $(events) events in total"
event_has 'Host: Tor working again after 672 min (restarted Tor; reset guard state; paused)' "recovery"
grep -Eq '^(rung|incident_start|actions)=' "$wd" && fail "recovery did not clear the incident"
tick $((T0 + 40200))
[ "$(events)" = 4 ] || fail "recovery event repeated"
inspect true unhealthy "$fail_up"
tick $((T0 + 40300))
[ "$(restarts)" = 3 ] || fail "a new incident after recovery was not handled"

reset
inspect true unhealthy "nc: connect failed"
tick "$T0"
expect_idle "unrecognised unhealthy output, first sight"
tick $((T0 + 599))
expect_idle "unrecognised unhealthy output for 599 s"
tick $((T0 + 600))
[ "$(restarts)" = 1 ] || fail "a container unhealthy for 10 minutes with unrecognised output was not restarted"
event_has "Tor's own health check fails before the circuit probe runs" "unrecognised output"
inspect true healthy "$okline"
tick $((T0 + 700))
event_has 'Tor working again after 11 min (restarted Tor)' "unrecognised output recovery"
inspect true unhealthy "nc: connect failed"
tick $((T0 + 800))
tick $((T0 + 1000))
[ "$(restarts)" = 1 ] || fail "the unrecognised-output clock was not reset by a healthy status"

reset
inspect true unhealthy "$fail_off"
tick "$T0"
[ "$(restarts)" = 1 ] || fail "offline incident did not restart Tor once"
event_has 'Tor reports the network down, so the host looks offline); restarting Tor' "offline start"
tick $((T0 + 480))
[ "$(restarts)" = 1 ] && [ "$(touches)" = 0 ] || fail "offline incident reset guard state"
[ "$(events)" = 2 ] || fail "offline pause wrote $(events) events in total"
event_has 'Tor is still not working after a restart (Tor reports the network down, so the host looks offline); automatic recovery is paused until Tor works again' "offline pause"
for i in $(seq 1 40); do tick $((T0 + 480 + 600 * i)); done
[ "$(restarts)" = 1 ] && [ "$(touches)" = 0 ] && [ "$(events)" = 2 ] || fail "a long outage looped: $(restarts) restarts, $(events) events"
inspect true healthy "$okline"
tick $((T0 + 30000))
event_has 'Tor working again after 561 min (restarted Tor; paused)' "offline recovery"

reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nrestart_ts=%s\nincident_start=%s\nactions=restarted Tor\n' "$T0" "$T0" "$T0" >"$wd"
inspect true unhealthy 'tor: offline for 245s bootstrap=95 liveness=down orconns=4'
tick $((T0 + 480))
[ "$(touches)" = 1 ] || fail "a liveness flap with relay connections blocked the guard reset"

reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nrestart_ts=%s\nincident_start=%s\nactions=restarted Tor\n' "$T0" "$T0" "$T0" >"$wd"
inspect true unhealthy "$fail_nolink"
tick $((T0 + 480))
[ "$(touches)" = 0 ] && [ "$(restarts)" = 0 ] || fail "reset ran with no relay connection"
event_has 'Tor holds no connection to any relay' "no relay"
reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nrestart_ts=%s\nincident_start=%s\n' "$T0" "$T0" "$T0" >"$wd"
inspect true unhealthy "$fail_noctl"
tick $((T0 + 480))
[ "$(touches)" = 0 ] || fail "reset ran without control context"
event_has 'Tor does not answer on its control socket' "no control"
reset
inspect true unhealthy "$fail_noctl"
tick "$T0"
[ "$(restarts)" = 1 ] || fail "an unreachable control socket blocked the first restart"

reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nreset_ts=%s\nrestart_ts=%s\nincident_start=%s\n' "$T0" $((T0 + 480 - 604799)) "$T0" "$T0" >"$wd"
inspect true unhealthy "$fail_up"
tick $((T0 + 480))
[ "$(touches)" = 0 ] && [ "$(restarts)" = 0 ] || fail "second reset within 7 days was not blocked"
event_has 'guard state was already reset in the last 7 days' "reset lockout"
reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nreset_ts=%s\nrestart_ts=%s\nincident_start=%s\n' "$T0" $((T0 + 480 - 604800)) "$T0" "$T0" >"$wd"
inspect true unhealthy "$fail_up"
tick $((T0 + 480))
[ "$(touches)" = 1 ] && [ "$(restarts)" = 1 ] || fail "reset exactly 7 days old was blocked"
reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nreset_ts=%s\nrestart_ts=%s\nincident_start=%s\n' "$T0" $((T0 + 90000)) "$T0" "$T0" >"$wd"
inspect true unhealthy "$fail_up"
tick $((T0 + 480))
[ "$(touches)" = 1 ] || fail "a reset time in the future blocked the reset"

reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nrestart_ts=%s\nincident_start=%s\n' "$T0" "$T0" "$T0" >"$wd"
inspect true unhealthy "$fail_up"
FAKE_TOUCH_FAIL=1 tick $((T0 + 480))
[ "$(restarts)" = 0 ] || fail "restart ran after the reset flag failed"
grep -q '^reset_ts=' "$wd" && fail "reset lockout started although the flag was not set"
event_has 'its guard state could not be reset' "touch failure"
reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nrestart_ts=%s\nincident_start=%s\n' "$T0" "$T0" "$T0" >"$wd"
inspect true unhealthy "$fail_up"
FAKE_RESTART_FAIL=1 tick $((T0 + 480))
grep -q '^reset_ts=' "$wd" && fail "reset lockout started although the restart failed"
grep -qx 'rm tor-id rm -f /var/lib/tor/.reset-state' "$FAKE_ACTIONS" || fail "failed restart left the reset flag behind"

reset
mkdir -p "$state/node-control"
printf 'restart_ts=%s %s %s\n' $((T0 - 20000)) $((T0 - 10000)) $((T0 - 5000)) >"$wd"
inspect true unhealthy "$fail_up"
tick "$T0"
[ "$(restarts)" = 0 ] || fail "fourth restart in six hours was not blocked"
[ "$(events)" = 1 ] || fail "blocked restart wrote $(events) events"
event_has '3 automatic restarts in the last 6 hours did not hold' "restart cap"
reset
mkdir -p "$state/node-control"
printf 'restart_ts=%s %s %s\n' $((T0 - 30000)) $((T0 - 25000)) $((T0 - 22000)) >"$wd"
inspect true unhealthy "$fail_up"
tick "$T0"
[ "$(restarts)" = 1 ] || fail "restarts older than six hours counted against the cap"
reset
mkdir -p "$state/node-control"
printf 'restart_ts=%s %s %s\n' $((T0 - 21600)) $((T0 - 100)) $((T0 - 50)) >"$wd"
inspect true unhealthy "$fail_up"
tick "$T0"
[ "$(restarts)" = 1 ] || fail "a restart exactly six hours old counted against the cap"
reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nrestart_ts=%s\nincident_start=%s\n' "$T0" "$T0" "$T0" >"$wd"
inspect true unhealthy 'tor: probe-failed for 245s bootstrap=100 liveness=up orconns=1'
tick $((T0 + 480))
[ "$(touches)" = 1 ] || fail "a single relay connection did not allow the guard reset"

reset
mkdir -p "$state/node-control"
printf 'rung=1\nlast_action=%s\nrestart_ts=%s %s %s\nincident_start=%s\n' "$T0" $((T0 - 100)) $((T0 - 50)) "$T0" "$T0" >"$wd"
inspect true unhealthy "$fail_up"
tick $((T0 + 480))
[ "$(touches)" = 0 ] && [ "$(restarts)" = 0 ] || fail "reset restart ignored the restart cap"

reset
for i in 1 2 3 4 5; do
  inspect true unhealthy "$fail_up"
  now=$((T0 + 1000 * i))
  tick "$now"
  tick $((now + 480))
  inspect true healthy "$okline"
  tick $((now + 960))
done
[ "$(restarts)" = 3 ] || fail "flapping Tor was restarted $(restarts) times in under two hours"

reset
mkdir -p "$snaps"
for i in 1 2 3 4 5; do printf 'old\n' >"$snaps/tor-$((T0 - 1000 * i)).txt"; done
inspect true unhealthy "$fail_up"
tick "$T0"
[ "$(snapshots)" = 5 ] || fail "snapshots were not capped at 5: $(snapshots)"
[ -e "$snaps/tor-$T0.txt" ] && [ ! -e "$snaps/tor-$((T0 - 5000)).txt" ] || fail "the oldest snapshot was not the one dropped"
for i in 1 2 3; do tick $((T0 + 480 * i)); done
[ "$(snapshots)" = 5 ] || fail "snapshots exceeded 5"

reset
inspect true unhealthy "$fail_up"
export FAKE_DASH_FAIL=1
tick "$T0"
[ "$(restarts)" = 1 ] || fail "restart waited for the dashboard"
[ ! -e "$dash/events.log" ] || fail "event was written while the dashboard exec failed"
grep -q '^pending_event=.*Z Host: Tor is not working' "$wd" || fail "failed event was not kept"
tick $((T0 + 480))
[ "$(grep -c '^pending_event=' "$wd")" = 2 ] || fail "second event was not queued behind the first"
export FAKE_DASH_FAIL=0
awk '/^pending_event=/ && !done { done = 1; sub(/^pending_event=[^ ]*Z Host: /, "pending_event=2030-01-02T03:04:05Z Host: ") } { print }' "$wd" >"$wd.seed" && mv "$wd.seed" "$wd"
tick $((T0 + 520))
[ "$(events)" = 2 ] || fail "queued events were not delivered"
[ "$(sed -n 1p "$dash/events.log" | grep -c 'Tor is not working')" = 1 ] && [ "$(sed -n 2p "$dash/events.log" | grep -c 'clearing Tor'\''s saved state')" = 1 ] || fail "events were delivered out of order"
[ "$(sed -n 1p "$dash/events.log" | cut -c1-26)" = "2030-01-02T03:04:05Z Host:" ] || fail "delivered event lost its queued timestamp"
grep -q '^pending_event=' "$wd" && fail "delivered events stayed queued"
grep -vq 'Z Host: ' "$dash/events.log" && fail "event line does not start with a timestamp and Host:"

echo "PASS: test_tor_watchdog.sh"
