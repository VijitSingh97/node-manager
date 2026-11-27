#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "${RUN_TARI_WALLET_NATIVE_SMOKE:-0}" != 1 ]; then
  echo "SKIP: set RUN_TARI_WALLET_NATIVE_SMOKE=1 to run the native Tari watch-wallet proof"
  exit 0
fi

for tool in docker go jq python3 openssl; do
  command -v "$tool" >/dev/null || { echo "FAIL: $tool is required for the requested native proof"; exit 1; }
done

run_id=$(openssl rand -hex 6)
image="tari-wallet-native-$run_id"
previous_image="tari-wallet-native-previous-$run_id"
container="tari-wallet-native-$run_id"
control_volume="tari-wallet-native-control-$run_id"
data_volume="tari-wallet-native-data-$run_id"
db_copy=$(mktemp)
previous_dockerfile=$(mktemp)
probe_copy="build/dashboard/tari_grpc_probe_$run_id.go"
gate_copy="build/dashboard/tari_probe_gate_$run_id.go"
cleanup() {
  docker container rm -f "$container" >/dev/null 2>&1 || true
  docker volume rm "$control_volume" "$data_volume" >/dev/null 2>&1 || true
  docker image rm "$image" "$previous_image" >/dev/null 2>&1 || true
  rm -f "$db_copy" "$previous_dockerfile" "$probe_copy" "$gate_copy"
}
trap cleanup EXIT

sed 's|^FROM ghcr.io/tari-project/minotari_console_wallet:.*$|FROM ghcr.io/tari-project/minotari_console_wallet:v6.1.0-mainnet@sha256:e20d5cf93f33254dcd07e8c9a405ee48d20721e14fee81140319dd41ecd277f0|' \
  build/tari-wallet/Dockerfile >"$previous_dockerfile"
grep -Fqx 'FROM ghcr.io/tari-project/minotari_console_wallet:v6.1.0-mainnet@sha256:e20d5cf93f33254dcd07e8c9a405ee48d20721e14fee81140319dd41ecd277f0' \
  "$previous_dockerfile"
docker build -q -f "$previous_dockerfile" -t "$previous_image" . >/dev/null
docker build -q -f build/tari-wallet/Dockerfile -t "$image" . >/dev/null
docker volume create "$control_volume" >/dev/null
docker volume create "$data_volume" >/dev/null
docker run --rm -v "$control_volume:/tari-wallet" -v "$data_volume:/tari-wallet/private" --entrypoint sh "$image" -c \
  'mkdir -p /tari-wallet/control /tari-wallet/private/archive; chown -R 1000:1000 /tari-wallet; chmod -R 700 /tari-wallet'
docker run -d --name "$container" --read-only -p 127.0.0.1::18143 \
  --tmpfs /tmp:uid=1000,gid=1000,mode=0700 -v "$control_volume:/tari-wallet" -v "$data_volume:/tari-wallet/private" \
  -e TARI_WALLET_PASSWORD=nativeproofdatabasepassword \
  -e TARI_WALLET_RPC_USER=nativeproofuser \
  -e TARI_WALLET_RPC_PASSWORD=nativeproofrpcpassword "$previous_image" >/dev/null

for _ in $(seq 1 20); do
  docker exec "$container" test -r /tari-wallet/control/capability.json && break
  sleep 1
done
docker exec "$container" sh -c \
  'test "$(id -u)" = 1000; test ! -e /tari-wallet/private/data; test ! -e /tari-wallet/identity.json; test "$(stat -c %a /tari-wallet/control/capability.json)" = 600'

view_key=0100000000000000000000000000000000000000000000000000000000000000
public_spend=d09dfde45e45456b7a8935fecfc0ebea431548d105d2f098a488820526395a61
wallet_id=11111111111111111111111111111111
jq -nc --arg id "$wallet_id" --arg spend "$public_spend" --arg view "$view_key" \
  '{id:$id,action:"create",wallet_id:$id,requested_at:1,name:"Native proof",public_spend_key:$spend,view_key:$view,birthday_days:0}' |
  docker run --rm -i -v "$control_volume:/tari-wallet" --entrypoint sh "$image" -c \
    'umask 077; cat >/tari-wallet/control/request.json; chown 1000:1000 /tari-wallet/control/request.json'

state=
for _ in $(seq 1 140); do
  state=$(docker exec "$container" sh -c \
    'test -r /tari-wallet/control/status.json && sed -n "s/.*\"state\":\"\([^\"]*\)\".*/\1/p" /tari-wallet/control/status.json' 2>/dev/null || true)
  case "$state" in ready | error) break ;; esac
  sleep 1
done
[ "$state" = ready ] || { echo "FAIL: native Tari wallet did not reach ready"; exit 1; }

host_port=$(docker port "$container" 18143/tcp | sed 's/.*://')
cp tests/tari_grpc_probe.go "$probe_copy"
cp tests/tari_probe_gate.go "$gate_copy"
(
  cd build/dashboard
  TARI_PROOF_ENDPOINT="127.0.0.1:$host_port" TARI_PROOF_USER=nativeproofuser TARI_PROOF_PASSWORD=nativeproofrpcpassword \
    go run "$(basename "$probe_copy")" "$(basename "$gate_copy")" node-stack-tari-wallet.pb.go node-stack-tari-wallet_grpc.pb.go
)

inspect=$(docker inspect "$container")
logs=$(docker logs "$container" 2>&1)
! grep -Fq "$view_key" <<<"$inspect" || { echo "FAIL: view key entered Docker metadata"; exit 1; }
! grep -Fq "$view_key" <<<"$logs" || { echo "FAIL: view key entered container logs"; exit 1; }
docker exec "$container" sh -c \
  'test ! -e /tari-wallet/control/request.json; test -s /tari-wallet/private/data/wallet.db; test -s /tari-wallet/identity.json; test "$(stat -c %a /tari-wallet/identity.json)" = 600'
docker run --rm -v "$control_volume:/tari-wallet" --entrypoint sh "$image" -c \
  'test ! -e /tari-wallet/private/data/wallet.db; test ! -e /tari-wallet/private/archive'
docker cp "$container:/tari-wallet/private/data/wallet.db" "$db_copy" >/dev/null
DB_COPY="$db_copy" VIEW_KEY="$view_key" python3 - <<'PY'
import json
import os
import sqlite3

with sqlite3.connect(os.environ["DB_COPY"]) as db:
    raw = db.execute("select value from wallet_settings where key='WalletType'").fetchone()[0]
wallet_type = json.loads(raw)["ProvidedKeys"]
assert wallet_type["private_spend_key"] is None
assert wallet_type["private_comms_key"] is None
assert os.environ["VIEW_KEY"] in raw
PY

docker stop -t 25 "$container" >/dev/null
docker rm "$container" >/dev/null
docker run -d --name "$container" --read-only -p 127.0.0.1::18143 \
  --tmpfs /tmp:uid=1000,gid=1000,mode=0700 -v "$control_volume:/tari-wallet" -v "$data_volume:/tari-wallet/private" \
  -e TARI_WALLET_PASSWORD=nativeproofdatabasepassword \
  -e TARI_WALLET_RPC_USER=nativeproofuser \
  -e TARI_WALLET_RPC_PASSWORD=nativeproofrpcpassword "$image" >/dev/null
for _ in $(seq 1 30); do
  state=$(docker exec "$container" sh -c \
    'test -r /tari-wallet/control/status.json && sed -n "s/.*\"state\":\"\([^\"]*\)\".*/\1/p" /tari-wallet/control/status.json' 2>/dev/null || true)
  [ "$state" = ready ] && break
  sleep 1
done
[ "$state" = ready ] || { echo "FAIL: v6.1 watch wallet did not restart under the current release"; exit 1; }
docker exec "$container" sh -c \
  'test "$(stat -c %a /tari-wallet/private/data)" = 700; test "$(stat -c %a /tari-wallet/private/data/wallet.db)" = 600'
rm -f "$db_copy"
docker cp "$container:/tari-wallet/private/data/wallet.db" "$db_copy" >/dev/null
DB_COPY="$db_copy" VIEW_KEY="$view_key" python3 - <<'PY'
import json
import os
import sqlite3

with sqlite3.connect(os.environ["DB_COPY"]) as db:
    assert db.execute("pragma journal_mode").fetchone()[0] == "wal"
    raw = db.execute("select value from wallet_settings where key='WalletType'").fetchone()[0]
wallet_type = json.loads(raw)["ProvidedKeys"]
assert wallet_type["private_spend_key"] is None
assert wallet_type["private_comms_key"] is None
assert os.environ["VIEW_KEY"] in raw
PY
host_port=$(docker port "$container" 18143/tcp | sed 's/.*://')
(
  cd build/dashboard
  TARI_PROOF_ENDPOINT="127.0.0.1:$host_port" TARI_PROOF_USER=nativeproofuser TARI_PROOF_PASSWORD=nativeproofrpcpassword \
    go run "$(basename "$probe_copy")" "$(basename "$gate_copy")" node-stack-tari-wallet.pb.go node-stack-tari-wallet_grpc.pb.go
)

operation_id=22222222222222222222222222222222
jq -nc --arg id "$operation_id" --arg wallet "$wallet_id" \
  '{id:$id,action:"remove",wallet_id:$wallet,requested_at:2}' |
  docker run --rm -i -v "$control_volume:/tari-wallet" --entrypoint sh "$image" -c \
    'umask 077; cat >/tari-wallet/control/request.json; chown 1000:1000 /tari-wallet/control/request.json'
for _ in $(seq 1 30); do
  state=$(docker exec "$container" sh -c \
    'sed -n "s/.*\"state\":\"\([^\"]*\)\".*/\1/p" /tari-wallet/control/status.json' 2>/dev/null || true)
  [ "$state" = removed ] && break
  [ "$state" = error ] && break
  sleep 1
done
[ "$state" = removed ] || { echo "FAIL: native Tari wallet was not archived"; exit 1; }
docker exec "$container" sh -c \
  'test ! -e /tari-wallet/private/data; test ! -e /tari-wallet/identity.json; test -s /tari-wallet/private/archive/11111111111111111111111111111111/data/wallet.db; test "$(stat -c %a /tari-wallet/private/archive/11111111111111111111111111111111)" = 700; test "$(stat -c %a /tari-wallet/private/archive/11111111111111111111111111111111/identity.json)" = 600; test "$(stat -c %a /tari-wallet/private/archive/11111111111111111111111111111111/data/wallet.db)" = 600'

docker stop -t 25 "$container" >/dev/null
docker run --rm -v "$control_volume:/tari-wallet" -v "$data_volume:/tari-wallet/private" --entrypoint sh "$image" -c \
  'mv /tari-wallet/private/archive/11111111111111111111111111111111 /tari-wallet/private/archive/preserved-proof; cp /tari-wallet/private/archive/preserved-proof/identity.json /tari-wallet/identity.json; chown 1000:1000 /tari-wallet/identity.json; chmod 600 /tari-wallet/identity.json'
docker start "$container" >/dev/null
for _ in $(seq 1 20); do
  state=$(docker run --rm -v "$control_volume:/tari-wallet" --entrypoint sh "$image" -c \
    'test -r /tari-wallet/control/status.json && sed -n "s/.*\"state\":\"\([^\"]*\)\".*/\1/p" /tari-wallet/control/status.json' 2>/dev/null || true)
  running=$(docker inspect "$container" --format '{{.State.Running}}' 2>/dev/null || true)
  [ "$state" = error ] && [ "$running" = false ] && break
  sleep 1
done
[ "$state" = error ] && [ "$running" = false ] || { echo "FAIL: missing database did not stop the supervisor"; exit 1; }
docker run --rm -v "$control_volume:/tari-wallet" -v "$data_volume:/tari-wallet/private" --entrypoint sh "$image" -c \
  'test ! -e /tari-wallet/private/data; test ! -e /tari-wallet/control/capability.json'

inspect=$(docker inspect "$container")
logs=$(docker logs "$container" 2>&1)
! grep -Fq "$view_key" <<<"$inspect" || { echo "FAIL: view key entered Docker metadata"; exit 1; }
! grep -Fq "$view_key" <<<"$logs" || { echo "FAIL: view key entered container logs"; exit 1; }

echo "PASS: native Tari watch wallet upgrades from v6.1 to the current release, remains spend-incapable, archives privately, and fails closed without its database"
echo "NOTE: Tari v6.2.0 stores its private view key in owner-only SQLite metadata; this proof is not a funded pruned-node balance qualification"
