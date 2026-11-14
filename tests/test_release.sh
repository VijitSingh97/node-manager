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

for development_file in AGENTS.md docs/developer-bot.md docs/adr tools; do
  git check-attr export-ignore -- "$development_file" | grep -Fq ': export-ignore: set' ||
    fail "$development_file is not excluded from release archives"
done

git archive --worktree-attributes HEAD >"$tmp/release.tar"
tar -tf "$tmp/release.tar" >"$tmp/contents"
runtime_helpers=
git cat-file -e HEAD:scripts/node-port-control 2>/dev/null && runtime_helpers="scripts/node-port-control scripts/node-control"
for required in stack configure.sh docker-compose.yml docker-compose.rpc.yml config.example.json VERSION \
  .dockerignore .gitignore README.md CHANGELOG.md LICENSE SECURITY.md \
  build/dashboard/Dockerfile build/dashboard/go.mod build/dashboard/main.go build/dashboard/settings.go \
  build/dashboard/auth.go build/dashboard/credentials.go build/dashboard/login.go build/dashboard/sessions.go \
  build/dashboard/index.html build/dashboard/login.html build/dashboard/THIRD_PARTY_NOTICES \
  build/dashboard/static/credentials.js build/dashboard/static/login.css build/dashboard/static/session.js \
  build/dashboard/static/theme-init.js build/dashboard/static/watch.js \
  build/dashboard/static/prices.js build/dashboard/static/settings.js build/tor/Dockerfile build/tor/entrypoint.sh build/tor/torrc \
  build/monero/Dockerfile build/monero/entrypoint.sh build/monero/healthcheck.sh \
  build/monero/wallet-entrypoint.sh build/monero/wallet-healthcheck.sh \
  build/tari/entrypoint.sh build/tari/config.toml \
  build/tari-wallet/Dockerfile build/tari-wallet/go.mod build/tari-wallet/launcher.go build/tari-wallet/config.toml \
  build/bitcoin-family/Dockerfile build/bitcoin-family/entrypoint.sh build/bitcoin-family/healthcheck.sh \
  build/zcash/zakurad.toml build/zcash/healthcheck.sh \
  docker-compose.monero-rpc.yml docker-compose.monero-zmq.yml docker-compose.tari-grpc.yml \
  docker-compose.litecoin-rpc.yml docker-compose.dogecoin-rpc.yml docker-compose.bitcoincash-rpc.yml docker-compose.zcash-rpc.yml docker-compose.utxo-index.yml \
  build/dashboard/static/nodes.js docs/nodes.md $runtime_helpers \
  docs/Caddyfile.example docs/getting-started.md docs/configuration.md docs/operations.md docs/watch-only.md docs/privacy.md; do
  grep -Fxq "$required" "$tmp/contents" || fail "release archive is missing $required"
done
if grep -Ev '^scripts/$|^scripts/(node-port-control|node-control|node-resource-profile|node-manager)$' "$tmp/contents" | grep -Eq '(^|/)(tests|parity|experiments|images)(/|$)|(_test\.go|\.test\.mjs|\.proto|\.pyc)$|^(\.github/|\.gitattributes$|\.gitleaks\.toml$|ruff\.toml$|AGENTS\.md$|CONTRIBUTING\.md$|scripts/|packaging/|tools/|docs/(developer-bot|releasing)\.md$|docs/adr/|build/dashboard/package\.json$)'; then
  fail "release archive includes development-only files"
fi

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
