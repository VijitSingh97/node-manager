#!/bin/sh
sock=${TOR_CONTROL_SOCKET:-/var/lib/tor/control/sock}
cookie=${TOR_COOKIE:-/var/lib/tor/control_auth_cookie}
state=${TOR_HEALTH_STATE:-/tmp/tor-health.state}
hosts=${TOR_PROBE_HOSTS:-www.torproject.org check.torproject.org api.github.com}
fail_after=${TOR_FAIL_AFTER:-240}
probe_timeout=${TOR_PROBE_TIMEOUT:-20}
neutral_for=${TOR_NEUTRAL_FOR:-600}
window=${TOR_WINDOW:-600}
min_samples=${TOR_MIN_SAMPLES:-5}
clear_after=${TOR_CLEAR_AFTER:-3}
offline_max=${TOR_OFFLINE_MAX:-3600}
now=${TOR_HEALTH_NOW:-$(date +%s)}

control() {
  hex=$(od -An -v -tx1 "$cookie" 2>/dev/null | tr -d ' \n')
  [ -n "$hex" ] || return 1
  {
    printf 'AUTHENTICATE %s\r\n' "$hex"
    for query in "$@"; do printf 'GETINFO %s\r\n' "$query"; done
    printf 'QUIT\r\n'
  } | nc -U -w 5 "$sock" 2>/dev/null
}

context() {
  reply=$(control status/bootstrap-phase network-liveness orconn-status | tr -d '\r') || reply=
  case $reply in
  250*) ;;
  *)
    echo "control=unavailable"
    return 0
    ;;
  esac
  printf '%s\n' "$reply" | awk '
    /PROGRESS=/ { match($0, /PROGRESS=[0-9]+/); progress = substr($0, RSTART + 9, RLENGTH - 9) }
    /^250-network-liveness=/ { live = $0; sub(/^[^=]*=/, "", live) }
    / CONNECTED/ && !/^250/ { conns++ }
    END {
      if (progress == "") progress = "?"
      if (live == "") live = "?"
      printf "bootstrap=%s liveness=%s orconns=%d\n", progress, live, conns
    }'
}

if [ "${1:-}" = snapshot ]; then
  context
  control status/bootstrap-phase network-liveness orconn-status circuit-status status/circuit-established status/enough-dir-info dormant entry-guards | tr -d '\r' | awk '
    /^250[-+ ](status\/circuit-established|status\/enough-dir-info|dormant)=/ { print "info " $0; next }
    /^250\+entry-guards=/ { section = "guards"; next }
    /^250\+orconn-status=/ { section = "orconn"; next }
    /^250\+circuit-status=/ { section = "circuit"; next }
    /^\.$/ { section = ""; next }
    section == "orconn" { print "orconn " $0 }
    section == "guards" { print "guard " $0 }
    section == "circuit" { count[$2]++ }
    END { for (s in count) print "circuits " s " " count[s] }'
  exit 0
fi

number() { case $1 in '' | *[!0-9]*) echo 0 ;; *) echo "$1" ;; esac; }
get() { sed -n "s/^$1 //p" "$state" 2>/dev/null | head -n 1; }
first_seen=$(number "$(get first_seen)")
off_since=$(number "$(get off_since)")
was_bad=$(number "$(get bad)")
[ "$first_seen" -ne 0 ] && [ "$first_seen" -le "$now" ] || first_seen=$now
[ "$off_since" -le "$now" ] || off_since=0
hist=$(sed -n 's/^p //p' "$state" 2>/dev/null | awk -v now="$now" -v w="$window" '$1 <= now && now - $1 < w && ($2 == 0 || $2 == 1)')
save() {
  {
    printf 'first_seen %s\noff_since %s\nbad %s\n' "$first_seen" "$off_since" "$bad"
    printf '%s\n' "$hist" | sed '/^$/d; s/^/p /'
  } >"$state.tmp" 2>/dev/null && mv -f "$state.tmp" "$state" 2>/dev/null || true
}

reached=
for host in $hosts; do
  if timeout "$probe_timeout" nc -z -w "$probe_timeout" -X 5 -x 127.0.0.1:9050 "$host" 443 >/dev/null 2>&1; then
    reached=$host
    break
  fi
done

ctx=$(context)
bad=$was_bad
uptime=$((now - first_seen))
case $ctx in *"bootstrap=100"*) bootstrapped=1 ;; *) bootstrapped=0 ;; esac
if [ -z "$reached" ] && [ "$uptime" -lt "$neutral_for" ] && [ "$bootstrapped" = 0 ]; then
  save
  echo "tor: bootstrapping uptime=${uptime}s $ctx"
  exit 0
fi

# A host with no relay connection cannot be helped by a restart, so offline time never counts as failure.
case $ctx in *"liveness=down"*"orconns=0"*) offline=1 ;; *) offline=0 ;; esac
if [ -z "$reached" ] && [ "$offline" = 1 ]; then
  [ "$off_since" -ne 0 ] || off_since=$now
  hist=
  bad=0
  save
  duration=$((now - off_since))
  echo "tor: offline for ${duration}s $ctx"
  [ "$duration" -lt "$offline_max" ]
  exit
fi
off_since=0

result=0
[ -z "$reached" ] || result=1
hist=$(printf '%s\n%s %s\n' "$hist" "$now" "$result" | sed '/^$/d')
stats=$(printf '%s\n' "$hist" | awk -v now="$now" '
  { n++; s += $2; if ($2 == 1) { streak++; start = 0 } else { streak = 0; if (!start) start = $1 } }
  END { printf "%d %d %d %d\n", n, s, streak, start ? now - start : 0 }')
read -r samples ok streak run <<EOF
$stats
EOF
bad=0
if [ "$was_bad" = 1 ]; then
  if [ "$streak" -lt "$clear_after" ]; then bad=1; else hist=$(printf '%s\n' "$hist" | tail -n "$streak"); fi
else
  [ "$run" -lt "$fail_after" ] || bad=1
  { [ -n "$reached" ] || [ "$samples" -lt "$min_samples" ] || [ $((ok * 2)) -ge "$samples" ]; } || bad=1
fi
save
if [ "$bad" = 0 ] && [ -n "$reached" ]; then
  echo "tor: ok via $reached $ctx"
  exit 0
fi
echo "tor: probe-failed for ${run}s $ctx probes_ok=$ok/$samples"
[ "$bad" = 0 ]
