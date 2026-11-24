#!/usr/bin/env bash
# Release tooling: changelog extraction edge cases and release.sh preflight.
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  exit 1
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for development_file in AGENTS.md CHANGELOG.md README.md SECURITY.md config.example.json .dockerignore llms.txt docs tools \
  build/dashboard build/hostd build/monero build/tor build/bitcoin-family build/tari-wallet; do
  git check-attr export-ignore -- "$development_file" | grep -Fq ': export-ignore: set' ||
    fail "$development_file is not excluded from release archives"
done

git check-attr export-ignore -- llms.txt unlisted-root-file.txt | grep -vq ': export-ignore: set' &&
  fail "new top-level files are not excluded from release archives by default"

git archive --worktree-attributes HEAD >"$tmp/release.tar"
tar -tf "$tmp/release.tar" >"$tmp/contents"
for required in stack configure.sh LICENSE VERSION docker-compose.yml docker-compose.rpc.yml \
  docker-compose.monero-rpc.yml docker-compose.monero-zmq.yml docker-compose.tari-wallet.yml docker-compose.tari-grpc.yml \
  docker-compose.litecoin-rpc.yml docker-compose.dogecoin-rpc.yml docker-compose.bitcoincash-rpc.yml docker-compose.zcash-rpc.yml \
  docker-compose.utxo-index.yml docker-compose.litecoin-utxo-index.yml docker-compose.bitcoincash-utxo-index.yml docker-compose.dogecoin-utxo-index.yml \
  scripts/node-manager scripts/node-control scripts/node-port-control scripts/node-resource-profile scripts/helper-common.sh \
  build/bitcoin/bitcoin.conf build/tari/config.toml build/tari/entrypoint.sh \
  build/zcash/zakurad.toml build/zcash/zakurad-address-index.toml build/zcash/healthcheck.sh; do
  grep -Fxq "$required" "$tmp/contents" || fail "release archive is missing $required"
done
grep -Evx 'stack|configure\.sh|LICENSE|VERSION|docker-compose[a-z.-]*\.yml|scripts/|scripts/(node-(manager|control|port-control|resource-profile)|helper-common\.sh)|build/|build/(bitcoin|tari|zcash)/|build/bitcoin/bitcoin\.conf|build/tari/(config\.toml|entrypoint\.sh)|build/zcash/(zakurad\.toml|zakurad-address-index\.toml|healthcheck\.sh)' \
  "$tmp/contents" >"$tmp/unexpected" || true
[ ! -s "$tmp/unexpected" ] || fail "release archive includes files outside the runtime set: $(tr '\n' ' ' <"$tmp/unexpected")"
grep -Eq '^docs(/|$)|\.go$' "$tmp/contents" && fail "release archive includes docs or Go sources"

# shellcheck disable=SC2016 # the compose variable names are literal text
sed -n 's|.*${STACK_ASSET_ROOT:-\.}/\(.*\):/.*|\1|p' docker-compose*.yml | sed 's|${ZCASH_TOML:-zakurad.toml}|zakurad.toml|' | sort -u >"$tmp/mounts"
[ -s "$tmp/mounts" ] || fail "no compose asset mounts were found"
while IFS= read -r mount; do
  grep -Eq "^$mount(/|$)" "$tmp/contents" || fail "release archive is missing compose asset $mount"
  grep -Fq "$mount" scripts/build-deb.sh || fail "Debian package does not install compose asset $mount"
done <"$tmp/mounts"

# --- release-notes.sh ---

cat >"$tmp/CHANGELOG.md" <<'EOF'
# Changelog

## [2.0.0] - 2026-08-01

### Added

- Two point oh.

## [1.9.0] - 2026-07-01

### Fixed

- One point nine fix.

[2.0.0]: https://example.com/2.0.0
EOF

# Extracts exactly one section, not the next one and not the link footer
notes=$(scripts/release-notes.sh 2.0.0 "$tmp/CHANGELOG.md")
echo "$notes" | grep -q "Two point oh" || fail "notes missing section content"
echo "$notes" | grep -q "One point nine" && fail "notes bled into the next section"
echo "$notes" | grep -q "example.com" && fail "notes include the link footer"

# Middle section extracts cleanly too
scripts/release-notes.sh 1.9.0 "$tmp/CHANGELOG.md" | grep -q "One point nine fix" ||
  fail "middle section not extracted"

# Missing version is an error, not empty output
scripts/release-notes.sh 3.0.0 "$tmp/CHANGELOG.md" >/dev/null 2>&1 &&
  fail "missing version did not fail"

# Version needing regex escaping (dots must not match arbitrary chars)
cat >"$tmp/CHANGELOG.md" <<'EOF'
## [1x0x0] - decoy

- Decoy.

## [1.0.0] - real

- Real.
EOF
scripts/release-notes.sh 1.0.0 "$tmp/CHANGELOG.md" | grep -q "Real" ||
  fail "dotted version matched the wrong section"

# --- release.sh preflight (in a throwaway git repo) ---

repo="$tmp/repo"
mkdir -p "$repo/scripts"
cp scripts/release.sh scripts/release-notes.sh "$repo/scripts/"
cd "$repo"
git init -q
git -c user.email=t@t -c user.name=t commit -q --allow-empty -m init

# Invalid VERSION blocks
echo "not-semver" >VERSION
cat >CHANGELOG.md <<'EOF'
## [9.9.9] - test

- Test release.
EOF
git add -A && git -c user.email=t@t -c user.name=t commit -qm files
scripts/release.sh --dry-run >/dev/null 2>&1 && fail "invalid semver was accepted"

# Happy path dry-run: passes, prints notes, tags nothing
echo "9.9.9" >VERSION
git add -A && git -c user.email=t@t -c user.name=t commit -qm version
out=$(scripts/release.sh --dry-run)
echo "$out" | grep -q "Preflight OK: v9.9.9" || fail "dry-run preflight did not pass"
echo "$out" | grep -q "Test release" || fail "dry-run did not preview notes"
git rev-parse -q --verify refs/tags/v9.9.9 >/dev/null && fail "dry-run created a tag"

# Dirty tree blocks
echo dirty >dirty.txt
scripts/release.sh --dry-run >/dev/null 2>&1 && fail "dirty tree was accepted"
rm dirty.txt

# Existing tag blocks
git tag v9.9.9
scripts/release.sh --dry-run >/dev/null 2>&1 && fail "existing tag was accepted"
git tag -d v9.9.9 >/dev/null

# Missing changelog section blocks
echo "8.8.8" >VERSION
git add -A && git -c user.email=t@t -c user.name=t commit -qm bump
scripts/release.sh --dry-run >/dev/null 2>&1 && fail "missing changelog section was accepted"

echo "PASS: test_release.sh"
