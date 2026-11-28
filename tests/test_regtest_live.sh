#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "${RUN_REGTEST_LIVE:-0}" != 1 ]; then
  echo "SKIP: test_regtest_live.sh (set RUN_REGTEST_LIVE=1 and REGTEST_CHAIN=<chain>)"
  exit 0
fi

chain=${REGTEST_CHAIN:-}
case "$chain" in
  bitcoin)
    tests=(TestParseUtxoRegtestLive TestUtxoIngestRegtestLive TestResolveWalletScriptsRegtestLive TestUtxoFollowerRegtestLive
      TestUtxoFollowerRegtestReorg TestUtxoSeedRegtestLive TestUtxoManagerAppliesNewBlocksAtOnceRegtestLive
      TestPrunedWalletIndexMatchesScantxoutsetRegtestLive TestUtxoStorageFullTmpfsLive TestUtxoP2PKRegtestLive)
    image=$(sed -n 's/.*image: \(bitcoin\/bitcoin:[^ ]*\).*/\1/p' docker-compose.yml | head -n 1)
    test -n "$image"
    docker pull "$image"
    export UTXO_REGTEST=1
    ;;
  litecoin)
    tests=(TestUtxoLitecoinMWEBRegtestLive TestUtxoP2PKLitecoinRegtestLive)
    export LTC_REGTEST_IMAGE=node-manager-regtest-litecoin
    ;;
  bitcoincash)
    tests=(TestUtxoBitcoinCashRawReplayRegtestLive TestUtxoBitcoinCashChainstateSeedRegtestLive TestUtxoP2PKBitcoinCashRegtestLive)
    export BCH_REGTEST_IMAGE=node-manager-regtest-bitcoincash
    ;;
  dogecoin)
    tests=(TestUtxoDogecoinChainstateSeedRegtestLive)
    export DOGE_REGTEST_IMAGE=node-manager-regtest-dogecoin
    ;;
  *)
    echo "FAIL: REGTEST_CHAIN must be bitcoin, litecoin, bitcoincash, or dogecoin" >&2
    exit 1
    ;;
esac

if [ "$chain" != bitcoin ]; then
  docker build --target "$chain" -t "node-manager-regtest-$chain" build/bitcoin-family
fi

out=$(mktemp)
trap 'rm -f "$out"' EXIT
pattern="^($(IFS='|'; echo "${tests[*]}"))\$"
status=0
(cd build/dashboard && go test -count=1 -v -timeout 15m -run "$pattern" .) 2>&1 | tee "$out" || status=$?
if [ "$status" != 0 ]; then
  echo "FAIL: go test exited $status" >&2
  exit 1
fi
for name in "${tests[@]}"; do
  if ! grep -q -- "^--- PASS: $name " "$out"; then
    echo "FAIL: $name did not pass (a skip counts as a failure here)" >&2
    exit 1
  fi
done
echo "PASS: ${#tests[@]} live regtest test(s) for $chain"
