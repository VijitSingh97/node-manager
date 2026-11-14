#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

if [[ ${RUN_NATIVE_WALLET_SMOKE:-} != 1 ]]; then
  echo "SKIP: set RUN_NATIVE_WALLET_SMOKE=1 to run native regtest wallet proofs"
  exit 0
fi

for command in docker python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "missing required command: $command" >&2
    exit 1
  fi
done

timeout_seconds=${NATIVE_WALLET_TIMEOUT:-240}
if [[ ! $timeout_seconds =~ ^[0-9]+$ ]] || ((timeout_seconds < 30 || timeout_seconds > 900)); then
  echo "NATIVE_WALLET_TIMEOUT must be an integer from 30 to 900" >&2
  exit 1
fi

case ${NATIVE_WALLET_CHAIN:-all} in
  all) chains=(litecoin bitcoincash) ;;
  litecoin | bitcoincash) chains=("${NATIVE_WALLET_CHAIN}") ;;
  *) echo "NATIVE_WALLET_CHAIN must be all, litecoin, or bitcoincash" >&2; exit 1 ;;
esac

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
run_id="${$}-$(date +%s)"
project="native-wallet-${run_id}"
work=$(mktemp -d "${TMPDIR:-/tmp}/${project}.XXXXXX")
active_container=
active_network=
active_volume=
active_image=
active_runner=
active_runner_image=
cleanup_failed=0

bounded() {
  local limit=$1
  shift
  python3 -c 'import subprocess,sys
try:
 result=subprocess.run(sys.argv[2:],timeout=int(sys.argv[1]))
except subprocess.TimeoutExpired:
 sys.exit(124)
sys.exit(result.returncode)' "$limit" "$@"
}

bounded_log() {
  local limit=$1 output=$2
  shift 2
  python3 - "$limit" "$output" "$@" <<'PY'
import subprocess
import sys

limit = int(sys.argv[1])
with open(sys.argv[2], "wb") as output:
    try:
        result = subprocess.run(sys.argv[3:], stdout=output, stderr=subprocess.STDOUT, timeout=limit)
    except subprocess.TimeoutExpired:
        sys.exit(124)
sys.exit(result.returncode)
PY
}

resource_exists() {
  local kind=$1 name=$2
  bounded 10 docker "$kind" inspect "$name" >/dev/null 2>&1
}

cleanup_active() {
  local failed=0
  if [[ -n $active_runner ]]; then
    bounded 20 docker rm -f "$active_runner" >/dev/null 2>&1 || failed=1
    if resource_exists container "$active_runner"; then failed=1; fi
  fi
  if [[ -n $active_container ]]; then
    bounded 10 docker logs "$active_container" >"$work/${active_container}.log" 2>&1 || true
    bounded 20 docker rm -f "$active_container" >/dev/null 2>&1 || failed=1
    if resource_exists container "$active_container"; then failed=1; fi
  fi
  if [[ -n $active_network ]]; then
    bounded 20 docker network rm "$active_network" >/dev/null 2>&1 || failed=1
    if resource_exists network "$active_network"; then failed=1; fi
  fi
  if [[ -n $active_volume ]]; then
    bounded 20 docker volume rm "$active_volume" >/dev/null 2>&1 || failed=1
    if resource_exists volume "$active_volume"; then failed=1; fi
  fi
  if [[ -n $active_image ]]; then
    bounded 20 docker image rm "$active_image" >/dev/null 2>&1 || failed=1
    if resource_exists image "$active_image"; then failed=1; fi
  fi
  if [[ -n $active_runner_image ]]; then
    bounded 20 docker image rm "$active_runner_image" >/dev/null 2>&1 || failed=1
    if resource_exists image "$active_runner_image"; then failed=1; fi
  fi
  active_runner=
  active_runner_image=
  active_container=
  active_network=
  active_volume=
  active_image=
  if ((failed)); then cleanup_failed=1; return 1; fi
}

finish() {
  local status=$?
  trap - EXIT
  cleanup_active || true
  if ((cleanup_failed)); then status=1; fi
  if ((status == 0)); then
    rm -rf "$work"
  else
    chmod -R u=rwX,go= "$work" 2>/dev/null || true
    echo "native wallet smoke failed; private artifacts: $work; project: $project" >&2
  fi
  exit "$status"
}
trap finish EXIT

public_fixture_hashes() {
  python3 - <<'PY'
for byte in (1, 2, 3):
    print((bytes([byte]) * 20).hex())
PY
}

rpc_call() {
  local method=$1 params=$2 body
  body=$(printf '{"jsonrpc":"1.0","id":"native-wallet","method":"%s","params":%s}' "$method" "$params")
  printf 'silent\nshow-error\nmax-time = 10\nuser = "%s:%s"\nheader = "Content-Type: application/json"\nurl = "http://127.0.0.1:%s"\n' "$rpc_user" "$rpc_password" "$rpc_port" |
    bounded 15 docker exec -i "$active_container" curl --config - --data-binary "$body"
}

rpc_result() {
  local response=$1
  python3 -c 'import json,sys; value=json.loads(sys.stdin.read()); sys.exit("native RPC returned an error") if value.get("error") else print(json.dumps(value["result"], separators=(",", ":")))' <<<"$response"
}

wait_for_rpc() {
  local deadline=$((SECONDS + 60)) response running
  while ((SECONDS < deadline)); do
    if response=$(rpc_call getblockchaininfo '[]' 2>/dev/null) && rpc_result "$response" >/dev/null 2>&1; then return 0; fi
    running=$(bounded 10 docker container inspect --format '{{.State.Running}}' "$active_container" 2>/dev/null || true)
    if [[ $running != true ]]; then
      echo "native daemon exited before RPC became ready" >&2
      return 1
    fi
    sleep 1
  done
  echo "native RPC did not become ready within 60 seconds" >&2
  return 1
}

fixture_address() {
  local hash=$1 response result address
  response=$(rpc_call decodescript "[\"76a914${hash}88ac\"]")
  result=$(rpc_result "$response")
  address=$(python3 -c 'import json,sys; value=json.loads(sys.stdin.read()); addresses=value.get("addresses", []); print(value.get("address") or (addresses[0] if addresses else ""))' <<<"$result")
  response=$(rpc_call validateaddress "[\"$address\"]")
  result=$(rpc_result "$response")
  python3 -c 'import json,sys; value=json.loads(sys.stdin.read()); sys.exit("public fixture validation failed: valid=%s script=%s canonical=%s" % (bool(value.get("isvalid")), bool(value.get("scriptPubKey")), bool(value.get("address")))) if not value.get("isvalid") or not value.get("scriptPubKey") or not value.get("address") else print(value["address"])' <<<"$result"
}

assert_native_scan() {
  local address=$1 expected=$2 response result
  response=$(rpc_call scantxoutset "[\"start\",[\"addr($address)\"]]")
  result=$(rpc_result "$response")
  python3 -c 'import decimal,json,sys; value=json.loads(sys.stdin.read(), parse_float=decimal.Decimal); expected=decimal.Decimal(sys.argv[1]); actual=decimal.Decimal(value["total_amount"]); sys.exit("native scan mismatch: success=%s height=%s amount=%s" % (value.get("success"), value.get("height"), actual)) if not value.get("success") or value.get("height") != 101 or actual != expected else None' "$expected" <<<"$result"
}

run_chain() {
  local chain=$1 context daemon rpc_port host_port conf subnet now funded zero error_address fixtures runner_file runner_context
  local address_one address_two address_three
  fixtures=$(public_fixture_hashes)
  address_one=$(sed -n '1p' <<<"$fixtures")
  address_two=$(sed -n '2p' <<<"$fixtures")
  address_three=$(sed -n '3p' <<<"$fixtures")
  subnet="172.31.$((100 + (${#chain} * 3))).0/24"
  active_container="${project}-${chain}"
  active_network="${project}-${chain}-net"
  active_volume="${project}-${chain}-data"
  active_image="${project}-${chain}:native-wallet"
  active_runner="${project}-${chain}-runner"
  active_runner_image="${project}-${chain}-runner:native-wallet"
  context=$root/build/bitcoin-family
  if [[ $chain == litecoin ]]; then daemon=litecoind; rpc_port=19443; else daemon=bitcoind; rpc_port=18443; fi
  host_port=$(python3 -c 'import socket; server=socket.socket(); server.bind(("127.0.0.1", 0)); print(server.getsockname()[1]); server.close()')
  conf="$work/${chain}.conf"
  runner_context="$work/${chain}-runner"
  runner_file="$runner_context/Dockerfile"

  cat >"$conf" <<EOF
regtest=1
server=1
disablewallet=1
listen=0
dnsseed=0
discover=0
txindex=0
printtoconsole=1
[regtest]
rpcbind=0.0.0.0
rpcallowip=$subnet
rpcuser=$rpc_user
rpcpassword=$rpc_password
EOF
  chmod 0644 "$conf"

  mkdir "$runner_context"
  cp "$root"/build/dashboard/*.go "$root/build/dashboard/go.mod" "$root/build/dashboard/go.sum" "$runner_context/"
  cat >"$runner_file" <<'EOF'
FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
EOF
  chmod 0644 "$runner_file"

  bounded_log "$timeout_seconds" "$work/${chain}-build.log" docker build --tag "$active_image" --target "$chain" "$context"
  bounded_log "$timeout_seconds" "$work/${chain}-runner-build.log" docker build --tag "$active_runner_image" "$runner_context"
  bounded 30 docker network create --internal --subnet "$subnet" "$active_network" >"$work/${chain}-network.id"
  bounded 30 docker volume create "$active_volume" >"$work/${chain}-volume.id"
  bounded 30 docker run --detach --name "$active_container" --network "$active_network" --mount "type=volume,src=$active_volume,dst=/data" --mount "type=bind,src=$conf,dst=/tmp/regtest.conf,readonly" --publish "127.0.0.1:${host_port}:${rpc_port}" --entrypoint "$daemon" "$active_image" -datadir=/data -conf=/tmp/regtest.conf >"$work/${chain}-container.id"
  wait_for_rpc

  funded=$(fixture_address "$address_one")
  zero=$(fixture_address "$address_two")
  error_address=$(fixture_address "$address_three")
  now=$(date +%s)
  rpc_result "$(rpc_call setmocktime "[$((now - 120))]")" >/dev/null
  rpc_result "$(rpc_call generatetoaddress "[100,\"$funded\"]")" >/dev/null
  rpc_result "$(rpc_call setmocktime "[$((now - 1))]")" >/dev/null
  rpc_result "$(rpc_call generatetoaddress "[1,\"$funded\"]")" >/dev/null
  assert_native_scan "$funded" 5050.00000000
  assert_native_scan "$zero" 0.00000000

  bounded "$timeout_seconds" docker run --name "$active_runner" --network "$active_network" \
    --env NATIVE_WALLET_INTEGRATION=1 \
    --env NATIVE_WALLET_CHAIN="$chain" \
    --env NATIVE_WALLET_RPC_URL="http://${active_container}:${rpc_port}" \
    --env NATIVE_WALLET_RPC_USER="$rpc_user" \
    --env NATIVE_WALLET_RPC_PASSWORD="$rpc_password" \
    --env NATIVE_WALLET_FUNDED_ADDRESS="$funded" \
    --env NATIVE_WALLET_ZERO_ADDRESS="$zero" \
    --env NATIVE_WALLET_ERROR_ADDRESS="$error_address" \
    "$active_runner_image" /bin/sh -ec "go test -v -count=1 -run '^TestNativeFamilyWatchHTTP$' -timeout '${timeout_seconds}s' . | tee /tmp/native-wallet-test.log; grep -q -- '--- PASS: TestNativeFamilyWatchHTTP' /tmp/native-wallet-test.log"

  cleanup_active
  echo "PASS: $chain native regtest wallet HTTP proof"
}

rpc_user=nativewallet
rpc_password=$(python3 -c 'import secrets; print(secrets.token_hex(24))')
cd "$root"
for chain in "${chains[@]}"; do run_chain "$chain"; done
