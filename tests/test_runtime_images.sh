#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
fail() { echo "FAIL: $1"; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
cat >"$tmp/bin/docker" <<'SH'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$IMAGE_TEST_LOG"
case "${IMAGE_TEST_CASE:-valid}" in
  failure) exit 1 ;;
  malformed) printf 'not json\n' ;;
  missing-platform) printf '{"digest":"sha256:%064d","manifests":[{"platform":{"os":"linux","architecture":"amd64"}}]}\n' 0 ;;
  bad-digest) printf '{"digest":"not-a-digest","manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}}]}\n' ;;
  *) printf '{"digest":"sha256:%064d","manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}}]}\n' 0 ;;
esac
SH
chmod 755 "$tmp/bin/docker"
export IMAGE_TEST_LOG="$tmp/calls"
PATH="$tmp/bin:$PATH" scripts/resolve-runtime-images.sh 1.32.0 >"$tmp/images.json"
jq -e 'keys == (["tor","dashboard","monero","tari-wallet","litecoin","dogecoin","bitcoincash"]|sort) and all(.[]; test("^sha256:[a-f0-9]{64}$"))' "$tmp/images.json" >/dev/null || fail "runtime digest map is incomplete"
[ "$(wc -l <"$IMAGE_TEST_LOG" | tr -d ' ')" = 7 ] || fail "not every runtime image was inspected"
grep -Fq 'node-manager-tari-wallet:1.32.0' "$IMAGE_TEST_LOG" || fail "runtime resolver used the wrong release tag"
for scenario in failure malformed missing-platform bad-digest; do
  if IMAGE_TEST_CASE="$scenario" PATH="$tmp/bin:$PATH" scripts/resolve-runtime-images.sh 1.32.0 >"$tmp/rejected.json" 2>/dev/null; then
    fail "runtime resolver accepted $scenario"
  fi
  [ ! -s "$tmp/rejected.json" ] || fail "failed resolution emitted a partial digest map"
done
PATH="$tmp/bin:$PATH" scripts/resolve-runtime-images.sh invalid >/dev/null 2>&1 && fail "invalid image tag accepted"
echo "PASS: test_runtime_images.sh"
