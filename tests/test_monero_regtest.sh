#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."

if [ "${RUN_MONERO_REGTEST:-0}" != 1 ]; then
  echo "SKIP: test_monero_regtest.sh (set RUN_MONERO_REGTEST=1)"
  exit 0
fi

for tool in docker go openssl; do
  command -v "$tool" >/dev/null || { echo "FAIL: $tool is required" >&2; exit 1; }
done

run_id="$$-$(date +%s)"
image="monero-regtest-$run_id"
network="monero-regtest-$run_id"
work=$(mktemp -d)
user=regtest
password=$(openssl rand -hex 16)
containers=("daemon-$run_id" "wallet-$run_id" "signer-$run_id")

cleanup() {
  status=$?
  trap - EXIT
  if [ "$status" != 0 ]; then
    for c in "${containers[@]}"; do docker logs --tail 40 "$c" >&2 2>&1 || true; done
  fi
  docker rm -f "${containers[@]}" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  docker image rm "$image" >/dev/null 2>&1 || true
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT

docker build -q -t "$image" build/monero >/dev/null
docker network create "$network" >/dev/null

docker run -d --name "daemon-$run_id" --network "$network" --network-alias monerod -p 127.0.0.1::18085 \
  --entrypoint monerod "$image" --non-interactive --regtest --offline --fixed-difficulty 1 --data-dir /data \
  --rpc-bind-ip 0.0.0.0 --rpc-bind-port 18085 --confirm-external-bind --rpc-login "$user:$password" \
  --disable-rpc-ban --no-igd --check-updates disabled >/dev/null

wallet_rpc() {
  docker run -d --name "$1-$run_id" --network "$network" -p 127.0.0.1::18082 -e USER_NAME="$user" -e PASS="$password" \
    --entrypoint sh "$image" -c '
      umask 077
      mkdir -p /wallets/.shared-ringdb
      cat >/tmp/wallet-rpc.conf <<CONF
wallet-dir=/wallets
shared-ringdb-dir=/wallets/.shared-ringdb
daemon-address=http://monerod:18085
daemon-login=$USER_NAME:$PASS
trusted-daemon=1
rpc-bind-ip=0.0.0.0
rpc-bind-port=18082
confirm-external-bind=1
rpc-login=$USER_NAME:$PASS
disable-rpc-ban=1
allow-mismatched-daemon-version=1
CONF
      exec monero-wallet-rpc --non-interactive --config-file=/tmp/wallet-rpc.conf' >/dev/null
}
wallet_rpc wallet
wallet_rpc signer

port_of() { docker port "$1-$run_id" "$2/tcp" | head -n1 | sed 's/.*://'; }
daemon_port=$(port_of daemon 18085)
wallet_port=$(port_of wallet 18082)
signer_port=$(port_of signer 18082)

for port in "$daemon_port" "$wallet_port" "$signer_port"; do
  ready=0
  for _ in $(seq 1 60); do
    if printf 'user = "%s:%s"\ndigest\nsilent\nfail\nmax-time = 5\nheader = "Content-Type: application/json"\n' "$user" "$password" |
      curl --config - --data-binary '{"jsonrpc":"2.0","id":"0","method":"get_version"}' "http://127.0.0.1:$port/json_rpc" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [ "$ready" = 1 ] || { echo "FAIL: RPC on port $port did not come up" >&2; exit 1; }
done

(
  cd build/dashboard
  MONERO_REGTEST=1 MONERO_REGTEST_USER="$user" MONERO_REGTEST_PASSWORD="$password" \
    MONERO_REGTEST_DAEMON_URL="http://127.0.0.1:$daemon_port" \
    MONERO_REGTEST_WALLET_URL="http://127.0.0.1:$wallet_port" \
    MONERO_REGTEST_SIGNER_URL="http://127.0.0.1:$signer_port" \
    go test -count=1 -v -run '^TestMoneroWatchRegtest$' . 2>&1 | tee "$work/go-test.log"
)
grep -q -- '--- PASS: TestMoneroWatchRegtest' "$work/go-test.log" || { echo "FAIL: Monero regtest did not pass (skipped?)" >&2; exit 1; }
! grep -q -- '--- SKIP' "$work/go-test.log" || { echo "FAIL: Monero regtest was skipped" >&2; exit 1; }
echo "PASS: Monero watch wallet against regtest monerod and monero-wallet-rpc"
