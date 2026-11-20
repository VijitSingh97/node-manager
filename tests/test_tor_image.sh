#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() { echo "FAIL: $1"; exit 1; }
if ! docker info >/dev/null 2>&1; then
  [ "${CI:-}" != true ] || fail "Docker is required in CI"
  echo "SKIP: test_tor_image.sh (Docker not available)"
  exit 0
fi
name=tor-image-test-$$
image=$name:test
cleanup() {
  docker rm -f "$name" "$name-old" >/dev/null 2>&1 || true
  docker volume rm "$name" "$name-old" >/dev/null 2>&1 || true
  docker rmi "$image" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker build -q -t "$image" build/tor >/dev/null || fail "tor image did not build"

wait_ok() {
  local out=
  for _ in $(seq 1 40); do
    out=$(docker exec "$1" tor-health 2>&1) || true
    case $out in "tor: ok"*) echo "$out"; return 0 ;; esac
    sleep 5
  done
  echo "last tor-health output: $out"
  docker logs "$1" 2>&1 | tail -n 20
  case $out in *"bootstrap=100"*) return 1 ;; *"bootstrap="*) return 2 ;; *) return 1 ;; esac
}
expect_ok() {
  local rc=0
  wait_ok "$1" || rc=$?
  [ "$rc" != 2 ] || [ "${CI:-}" != true ] || fail "Tor could not bootstrap in CI"
  [ "$rc" != 2 ] || { echo "SKIP: test_tor_image.sh (Tor could not bootstrap here, no Internet?)"; exit 0; }
  [ "$rc" = 0 ] || fail "$2"
}

docker run -d --name "$name" --health-cmd /usr/local/bin/tor-health --health-interval 5s --health-retries 1 -v "$name:/var/lib/tor" "$image" >/dev/null
expect_ok "$name" "healthcheck never reached ok on a fresh tor_data volume"
[ "$(docker inspect -f '{{.State.Running}}' "$name")" = true ] || fail "Tor did not stay running"
docker exec "$name" sh -c 'tor-health >/dev/null; nc -z 127.0.0.1 9050' || fail "SocksPort is not listening"
sleep 6
template='{{.State.Running}}|{{.State.Health.Status}}|{{range .State.Health.Log}}{{.Output}}{{"\n--\n"}}{{end}}'
case $(docker inspect "$name" --format "$template") in "true|"*"|tor: "*) ;; *) fail "the watchdog's docker inspect template no longer parses" ;; esac
[ "$(docker exec "$name" stat -c %a /var/lib/tor/control)" = 700 ] || fail "control directory is not private"
docker logs "$name" 2>&1 | grep -q 'control connection' && fail "control connections are logged at notice level"
docker exec "$name" test -e /tmp/tor-health.state || fail "the health probe kept no state"
docker restart -t 30 "$name" >/dev/null
sleep 2
docker exec "$name" test ! -e /tmp/tor-health.state || fail "health checker state survived a container restart"
expect_ok "$name" "healthcheck never reached ok after a restart"
docker exec "$name" touch /var/lib/tor/.reset-state
docker restart -t 30 "$name" >/dev/null
sleep 2
docker exec "$name" test ! -e /var/lib/tor/.reset-state || fail "reset flag survived the restart"
expect_ok "$name" "healthcheck never reached ok after a guard state reset"

docker volume create "$name-old" >/dev/null
docker run --rm -v "$name-old:/var/lib/tor" --entrypoint sh "$image" -c 'timeout -s INT 30 tor --DataDirectory /var/lib/tor --SocksPort 0 --ControlPort 0 >/dev/null 2>&1 || true; mkdir -p /var/lib/tor/bitcoin_onion'
docker run --rm -v "$name-old:/var/lib/tor" --entrypoint sh "$image" -c 'test ! -e /var/lib/tor/control' || fail "the pre-v2 volume already has a control directory"
docker run -d --name "$name-old" -v "$name-old:/var/lib/tor" "$image" >/dev/null
expect_ok "$name-old" "healthcheck never reached ok on a tor_data volume from before the control socket"
[ "$(docker exec "$name-old" stat -c %a /var/lib/tor/control)" = 700 ] || fail "control directory was not created with a private mode on an existing volume"

echo "PASS: test_tor_image.sh"
