#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  exit 1
}

repo=$PWD
system_path=$PATH
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fake_bin=$tmp/bin
mkdir "$fake_bin"

cat >"$fake_bin/uname" <<'EOF'
#!/bin/sh
printf '%s\n' "${FAKE_UNAME:-Linux}"
EOF
cat >"$fake_bin/readlink" <<'EOF'
#!/bin/sh
set -eu
path=$3
if [ "${FAKE_ESCAPE_NODE:-}" = "${path##*/}" ]; then
  printf '/outside/%s\n' "${path##*/}"
else
  printf '%s\n' "$path"
fi
EOF
cat >"$fake_bin/stat" <<'EOF'
#!/bin/sh
set -eu
format=$2
path=$3
if [ "${FAKE_OWNERS_MATCH:-0}" = 1 ]; then
  if [ "${path##*/}" = zcash ]; then owner=10001:1000; mode=750; else owner=1000:1000; mode=700; fi
else
  owner=1001:1001
  mode=700
fi
mode=${FAKE_DIRECTORY_MODE:-$mode}
case "$format" in
  %u:%g) printf '%s\n' "$owner" ;;
  %a) printf '%s\n' "$mode" ;;
  *) exit 1 ;;
esac
EOF
cat >"$fake_bin/find" <<'EOF'
#!/bin/sh
[ "${FAKE_FIND_FAIL:-0}" != 1 ] || exit 1
exec /usr/bin/find "$@"
EOF
cat >"$fake_bin/sleep" <<'EOF'
#!/bin/sh
if [ "${FAKE_SLEEP_TERMINATE:-0}" = 1 ]; then
  kill -TERM "$PPID"
  exit 0
fi
exec /bin/sleep "$@"
EOF
cat >"$fake_bin/docker" <<'EOF'
#!/bin/sh
set -eu
printf '%s' docker >>"$FAKE_DOCKER_LOG"
for arg in "$@"; do printf ' <%s>' "$arg" >>"$FAKE_DOCKER_LOG"; done
printf '\n' >>"$FAKE_DOCKER_LOG"
case "${1:-}:${2:-}" in
  inspect:*) exit 1 ;;
  image:inspect) [ "${FAKE_IMAGE_MISSING:-0}" != 1 ] ;;
  compose:*)
    case " $* " in
      *' config --images '*) printf '%s\n' tor-image ;;
      *' config --format json '*) printf '%s\n' '{"services":{"tor":{"image":"tor-image"}}}' ;;
    esac
    ;;
  run:*) [ "${FAKE_RUN_FAIL:-0}" != 1 ] ;;
esac
EOF
cat >"$fake_bin/brew" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$FAKE_BREW_LOG"
[ "$*" = "services start node-manager" ]
EOF
cat >"$fake_bin/systemctl" <<'EOF'
#!/bin/sh
exit 1
EOF
cat >"$fake_bin/crontab" <<'EOF'
#!/bin/sh
cat >/dev/null || true
exit 1
EOF
cat >"$fake_bin/pgrep" <<'EOF'
#!/bin/sh
exit 1
EOF
cat >"$fake_bin/setsid" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_HOST_LOG"
EOF
chmod +x "$fake_bin"/*

new_case() {
  case_root=$tmp/$1
  state=$case_root/state
  config=$case_root/config
  data=$case_root/data
  mkdir -p "$state" "$config" "$data"
  for node in bitcoin monero tari litecoin dogecoin bitcoincash zcash; do mkdir "$data/$node"; done
  chmod 700 "$data" "$data"/*
  chmod 750 "$data/zcash"
  cat >"$state/.env" <<EOF
COMPOSE_FILE=docker-compose.yml
BITCOIN_DATA_DIR=$data/bitcoin
MONERO_DATA_DIR=$data/monero
TARI_DATA_DIR=$data/tari
LITECOIN_DATA_DIR=$data/litecoin
DOGECOIN_DATA_DIR=$data/dogecoin
BITCOINCASH_DATA_DIR=$data/bitcoincash
ZCASH_DATA_DIR=$data/zcash
BITCOIN_ENABLED=1
BITCOIN_ACTIVE=1
MONERO_ENABLED=0
TARI_ENABLED=0
LITECOIN_ENABLED=0
DOGECOIN_ENABLED=0
BITCOINCASH_ENABLED=0
ZCASH_ENABLED=0
EOF
  export BITCOIN_STACK_ROOT=$repo BITCOIN_STACK_STATE_DIR=$state BITCOIN_STACK_CONFIG_DIR=$config
  export BITCOIN_STACK_DATA_DIR=$data BITCOIN_STACK_PACKAGE_MANAGED=brew
  export BITCOIN_STACK_PROJECT_NAME=brew-data-test FAKE_DOCKER_LOG=$case_root/docker.log
  export FAKE_BREW_LOG=$case_root/brew.log FAKE_HOST_LOG=$case_root/host.log
  export PATH=$fake_bin:$system_path
  export FAKE_UNAME=Linux FAKE_OWNERS_MATCH=0 FAKE_IMAGE_MISSING=0 FAKE_RUN_FAIL=0 FAKE_FIND_FAIL=0 FAKE_SLEEP_TERMINATE=0
  unset FAKE_ESCAPE_NODE FAKE_DIRECTORY_MODE
  : >"$FAKE_DOCKER_LOG"
  : >"$FAKE_BREW_LOG"
  : >"$FAKE_HOST_LOG"
  [ "$(command -v brew)" = "$fake_bin/brew" ] || fail "test harness can reach the real Homebrew service manager"
  [ "$(command -v setsid)" = "$fake_bin/setsid" ] || fail "test harness can launch a real background service"
}

new_case fresh
./stack up
grep -qx 'services start node-manager' "$FAKE_BREW_LOG" || fail "Homebrew startup did not register the host supervisor"
run_line=$(grep '^docker <run>' "$FAKE_DOCKER_LOG")
for flag in '<--network> <none>' '<--read-only>' '<--security-opt> <no-new-privileges>' \
  '<--cap-drop> <ALL>' '<--cap-add> <CHOWN>' '<--cap-add> <DAC_READ_SEARCH>' '<--cap-add> <FOWNER>' \
  '<--user> <0:0>' '<--entrypoint> </bin/sh>'; do
  grep -Fq "$flag" <<<"$run_line" || fail "transient data initializer omitted $flag"
done
[ "$(grep -o '<--mount>' <<<"$run_line" | wc -l | tr -d ' ')" = 7 ] || fail "initializer did not mount each default node directory separately"
grep -Fq "<type=bind,src=$data/bitcoin,dst=/node-data/bitcoin>" <<<"$run_line" || fail "Bitcoin default directory was not mounted exactly"
grep -Fq '<chown 1000:1000 /node-data/bitcoin' <<<"$run_line" || fail "UID 1000 ownership was not requested"
grep -Fq 'chown 10001:1000 /node-data/zcash' <<<"$run_line" || fail "Zcash ownership was not requested"
grep -Fq 'chmod 750 /node-data/zcash' <<<"$run_line" || fail "Zcash mode was not requested"
images_line=$(grep -n 'config> <--images>' "$FAKE_DOCKER_LOG" | head -1 | cut -d: -f1)
run_number=$(grep -n '^docker <run>' "$FAKE_DOCKER_LOG" | cut -d: -f1)
up_line=$(grep -n 'up> <-d>' "$FAKE_DOCKER_LOG" | cut -d: -f1)
[ "$images_line" -lt "$run_number" ] || fail "ownership initialization ran before image checks"
[ "$run_number" -lt "$up_line" ] || fail "ownership initialization ran after startup"
grep -q '<pull>' "$FAKE_DOCKER_LOG" && fail "ownership initialization pulled an image"

new_case already-owned
export FAKE_OWNERS_MATCH=1
./stack up
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "matching directories invoked the initializer again"
grep -q 'up> <-d>' "$FAKE_DOCKER_LOG" || fail "matching directories prevented startup"

new_case macos
export FAKE_UNAME=Darwin
./stack up
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "macOS invoked the Linux ownership initializer"

new_case source-install
export BITCOIN_STACK_PACKAGE_MANAGED=0
./stack up
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "source install invoked the Linuxbrew initializer"

new_case missing-image
export FAKE_IMAGE_MISSING=1
if ./stack up >"$case_root/output" 2>&1; then fail "startup accepted missing local images"; fi
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "missing image invoked ownership initialization"
grep -q 'up> <-d>' "$FAKE_DOCKER_LOG" && fail "missing image reached startup"
grep -q 'pull' "$case_root/output" || fail "missing image did not explain the offline prerequisite"

new_case nonempty
printf 'existing chain data\n' >"$data/bitcoin/block.dat"
if ./stack up >"$case_root/output" 2>&1; then fail "non-empty mismatched data was changed automatically"; fi
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "non-empty mismatched data invoked chown"
grep -q 'sudo chown -R 1000:1000' "$case_root/output" || fail "manual migration command was not shown"
grep -q 'up> <-d>' "$FAKE_DOCKER_LOG" && fail "ownership mismatch reached startup"

new_case nonempty-wrong-mode
export FAKE_OWNERS_MATCH=1 FAKE_DIRECTORY_MODE=755
printf 'existing chain data\n' >"$data/bitcoin/block.dat"
if ./stack up >"$case_root/output" 2>&1; then fail "non-empty data permissions were changed automatically"; fi
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "non-empty data invoked chmod"
grep -q 'up> <-d>' "$FAKE_DOCKER_LOG" && fail "non-empty permission mismatch reached startup"

new_case unreadable
export FAKE_FIND_FAIL=1
if ./stack up >"$case_root/output" 2>&1; then fail "uninspectable mismatched data was accepted"; fi
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "uninspectable data invoked chown"
grep -q 'cannot inspect' "$case_root/output" || fail "uninspectable data did not fail clearly"
grep -q 'up> <-d>' "$FAKE_DOCKER_LOG" && fail "uninspectable data reached startup"

new_case custom
custom=$case_root/custom-bitcoin
mkdir "$custom"
sed -i.bak "s|^BITCOIN_DATA_DIR=.*|BITCOIN_DATA_DIR=$custom|" "$state/.env"
export FAKE_OWNERS_MATCH=1
./stack up
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "correct custom data was mounted by the initializer"
grep -q 'up> <-d>' "$FAKE_DOCKER_LOG" || fail "correct custom data prevented startup"

new_case custom-mismatched
custom=$case_root/custom-bitcoin
mkdir "$custom"
sed -i.bak "s|^BITCOIN_DATA_DIR=.*|BITCOIN_DATA_DIR=$custom|" "$state/.env"
if ./stack up >"$case_root/output" 2>&1; then fail "mismatched custom data reached startup"; fi
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "custom mismatched data was automatically mounted"
grep -q 'Custom bitcoin data needs owner' "$case_root/output" || fail "custom data migration was not explained"
grep -q 'up> <-d>' "$FAKE_DOCKER_LOG" && fail "mismatched custom data started containers"

new_case symlink
external=$case_root/external
mkdir "$external"
rmdir "$data/bitcoin"
ln -s "$external" "$data/bitcoin"
if ./stack up >"$case_root/output" 2>&1; then fail "symlinked default directory was accepted"; fi
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "symlinked default directory reached initializer"

new_case canonical-escape
export FAKE_ESCAPE_NODE=bitcoin
if ./stack up >"$case_root/output" 2>&1; then fail "canonical path escape was accepted"; fi
grep -q '^docker <run>' "$FAKE_DOCKER_LOG" && fail "canonical path escape reached initializer"

new_case initializer-failure
export FAKE_RUN_FAIL=1
if ./stack up >"$case_root/output" 2>&1; then fail "failed ownership initialization reached startup"; fi
grep -q 'up> <-d>' "$FAKE_DOCKER_LOG" && fail "failed ownership initialization started containers"
grep -q 'could not prepare' "$case_root/output" || fail "initializer failure was not explained"

new_case host-service-initializer-failure
printf '{}\n' >"$config/config.json"
export FAKE_RUN_FAIL=1 FAKE_SLEEP_TERMINATE=1
./stack host-service >"$case_root/output" 2>&1
grep -q 'supervising node controls' "$case_root/output" && fail "host service marked a failed initialization as started"
grep -q 'up> <-d>' "$FAKE_DOCKER_LOG" && fail "host service started containers after initializer failure"

echo "PASS: test_brew_data.sh"
