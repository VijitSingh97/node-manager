#!/usr/bin/env bash
# Maintainer script: how long after a new block does a watched wallet first refresh (#236)?
# Matches each bitcoind UpdateTip to the first dashboard refresh at that height or higher.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/measure-block-latency.sh --since TIME [--until TIME] [--bitcoin CONTAINER] [--dashboard CONTAINER]
                                        [--bitcoin-log FILE] [--dashboard-log FILE]

TIME is UTC, like 2026-11-15T06:30:00Z. Run it on the host with docker access.
Containers default to the single running Compose bitcoin and dashboard services.
--bitcoin-log reads a bitcoind debug.log copy and --dashboard-log a copy of
`docker logs --timestamps` output instead of docker. No addresses are printed.
EOF
}

since='' until='' bitcoin='' dashboard='' bitcoin_log='' dashboard_log=''
while [ $# -gt 0 ]; do
  case "$1" in
  --since | --until | --bitcoin | --dashboard | --bitcoin-log | --dashboard-log)
    [ $# -ge 2 ] || {
      usage >&2
      exit 1
    }
    case "$1" in
    --since) since=$2 ;;
    --until) until=$2 ;;
    --bitcoin) bitcoin=$2 ;;
    --dashboard) dashboard=$2 ;;
    --bitcoin-log) bitcoin_log=$2 ;;
    --dashboard-log) dashboard_log=$2 ;;
    esac
    shift 2
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 1
    ;;
  esac
done
time_re='^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$'
[[ "$since" =~ $time_re ]] || {
  echo "--since needs a UTC time like 2026-11-15T06:30:00Z." >&2
  exit 1
}
[ -z "$until" ] || [[ "$until" =~ $time_re ]] || {
  echo "--until needs a UTC time like 2026-11-15T19:00:00Z." >&2
  exit 1
}
until=${until:-9999-12-31T23:59:59Z}

service_container() {
  local ids
  ids=$(docker ps -q --filter "label=com.docker.compose.service=$1")
  [ -n "$ids" ] && [ "$(wc -l <<<"$ids")" = 1 ] || {
    echo "Need exactly one running $1 container; pass --$1 NAME." >&2
    return 1
  }
  printf '%s' "$ids"
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
if [ -n "$bitcoin_log" ]; then
  cp "$bitcoin_log" "$work/bitcoin.log"
else
  bitcoin=${bitcoin:-$(service_container bitcoin)}
  docker exec "$bitcoin" grep -h UpdateTip: /data/debug.log >"$work/bitcoin.log"
fi
if [ -n "$dashboard_log" ]; then
  cp "$dashboard_log" "$work/dashboard.log"
else
  dashboard=${dashboard:-$(service_container dashboard)}
  docker logs --timestamps --since "$since" "$dashboard" >"$work/dashboard.log" 2>&1
fi

{
  grep 'UpdateTip:' "$work/bitcoin.log" | sed -n 's/^\([0-9T:Z.-]*\) .*UpdateTip:.* height=\([0-9]*\) .*/B \1 \2/p'
  sed -n 's/^\([0-9T:Z.-]*\) .*watch index: outcome=ok .* height=\([0-9]*\) .*/R index \1 \2/p;
    s/^\([0-9T:Z.-]*\) .*watch block update: .* height=\([0-9]*\) .*/R block \1 \2/p' "$work/dashboard.log"
  head -n 1 "$work/dashboard.log" | sed -n 's/^\([0-9T:Z.-]*\) .*/F \1/p'
} | awk -v since="$since" -v until="$until" '
function epoch(ts,   y, m, d, era, yoe, doy, doe) {
  y = substr(ts, 1, 4) + 0; m = substr(ts, 6, 2) + 0; d = substr(ts, 9, 2) + 0
  if (m <= 2) y--
  era = int(y / 400); yoe = y - era * 400
  doy = int((153 * (m + (m > 2 ? -3 : 9)) + 2) / 5) + d - 1
  doe = yoe * 365 + int(yoe / 4) - int(yoe / 100) + doy
  return (era * 146097 + doe - 719468) * 86400 + substr(ts, 12, 2) * 3600 + substr(ts, 15, 2) * 60 + substr(ts, 18, 2)
}
function sorted(src, n, dst,   i, j, v) {
  for (i = 1; i <= n; i++) {
    v = src[i]
    for (j = i - 1; j >= 1 && dst[j] > v; j--) dst[j + 1] = dst[j]
    dst[j + 1] = v
  }
}
function report(label, vals, n,   s, i, p50, p90) {
  if (n == 0) { printf "%-34s %6d\n", label, 0; return }
  sorted(vals, n, s)
  i = int(n * 0.5); if (i < n * 0.5) i++
  p50 = s[i]
  i = int(n * 0.9); if (i < n * 0.9) i++
  p90 = s[i]
  printf "%-34s %6d %8.1f %8.1f %8.1f\n", label, n, p50, p90, s[n]
}
BEGIN { lo = epoch(since); hi = epoch(until) }
$1 == "B" { t = epoch($2); if (t >= lo && t <= hi) { nb++; bt[nb] = t; bh[nb] = $3 + 0 } }
$1 == "F" { first = epoch($2) }
$1 == "R" { nr++; rk[nr] = $2; rt[nr] = epoch($3); rh[nr] = $4 + 0 }
END {
  for (i = 1; i <= nr; i++) for (j = i + 1; j <= nr; j++) if (rt[j] < rt[i]) {
    x = rt[i]; rt[i] = rt[j]; rt[j] = x; x = rh[i]; rh[i] = rh[j]; rh[j] = x; x = rk[i]; rk[i] = rk[j]; rk[j] = x
  }
  for (b = 1; b <= nb; b++) {
    for (r = 1; r <= nr; r++) {
      if (rt[r] >= bt[b] && rh[r] >= bh[b]) {
        d = rt[r] - bt[b]
        if (rk[r] == "index") ni++; else nk++
        if (rk[r] == "index") iv[ni] = d; else kv[nk] = d
        no++; ov[no] = d
        break
      }
    }
  }
  if (nb == 0) {
    print "No UpdateTip lines parsed in the window; check --since, --bitcoin and the bitcoind log timestamp format." > "/dev/stderr"
    exit 1
  }
  if (first > lo + 60) print "Warning: dashboard logs start after --since (log rotation?); early blocks may show no refresh, so the figures are biased." > "/dev/stderr"
  printf "Blocks seen: %d, matched to a refresh: %d, with no refresh yet: %d\n\n", nb, no, nb - no
  printf "%-34s %6s %8s %8s %8s\n", "First refresh after a block", "Blocks", "Median s", "p90 s", "Max s"
  report("Served by the index", iv, ni)
  report("Fell back to the block update", kv, nk)
  report("All", ov, no)
  if (nr == 0) {
    print "No dashboard refresh lines parsed; is the index on and the dashboard log window right?" > "/dev/stderr"
    exit 1
  }
}'
