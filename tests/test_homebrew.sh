#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  exit 1
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
archive="$tmp/bitcoin-starter-stack.tar.gz"
payload="$tmp/bitcoin-starter-stack"
mkdir -p "$payload/build/dashboard" "$payload/build/bitcoin" "$payload/build/tari" "$payload/build/tari-wallet" "$payload/build/zcash" "$payload/scripts"
for file in stack configure.sh VERSION docker-compose.yml docker-compose.tari-wallet.yml; do
  printf 'fixture\n' >"$payload/$file"
done
printf 'fixture\n' >"$payload/build/dashboard/Dockerfile"
for file in build/bitcoin/bitcoin.conf build/tari/config.toml build/tari/entrypoint.sh \
  build/tari-wallet/Dockerfile build/tari-wallet/go.mod build/tari-wallet/launcher.go build/tari-wallet/config.toml \
  build/zcash/zakurad.toml build/zcash/healthcheck.sh; do
  printf 'fixture %s\n' "$file" >"$payload/$file"
done
for file in bitcoin-stack node-control node-port-control node-resource-profile; do
  printf 'fixture\n' >"$payload/scripts/$file"
done
tar -czf "$archive" -C "$tmp" bitcoin-starter-stack
url="file://$archive"
formula="$tmp/bitcoin-starter-stack.rb"

PACKAGE_VERSION=9.8.7 scripts/build-homebrew-formula.sh --url "$url" --output "$formula" "$archive" >/dev/null
ruby -c "$formula" >/dev/null || fail "generated formula is not valid Ruby"
checksum=$(shasum -a 256 "$archive" | awk '{print $1}')
grep -Fq 'version "9.8.7"' "$formula" || fail "formula version was not rendered"
grep -Fq "url \"$url\"" "$formula" || fail "formula local URL was not rendered"
grep -Fq "sha256 \"$checksum\"" "$formula" || fail "formula checksum was not rendered"
grep -Eq '@(VERSION|RELEASE_URL|SHA256)@' "$formula" && fail "formula contains an unresolved placeholder"

second="$tmp/second.rb"
PACKAGE_VERSION=9.8.7 scripts/build-homebrew-formula.sh --url "$url" --output "$second" "$archive" >/dev/null
cmp -s "$formula" "$second" || fail "formula generation is not reproducible"

PACKAGE_VERSION=invalid scripts/build-homebrew-formula.sh --url "$url" --output "$tmp/invalid.rb" "$archive" >/dev/null 2>&1 &&
  fail "invalid version was accepted"
scripts/build-homebrew-formula.sh --url http://example.invalid/archive --output "$tmp/http.rb" "$archive" >/dev/null 2>&1 &&
  fail "insecure release URL was accepted"
scripts/build-homebrew-formula.sh --output "$tmp/missing.rb" "$tmp/missing.tar.gz" >/dev/null 2>&1 &&
  fail "missing archive was accepted"
printf 'wrong payload\n' >"$tmp/wrong"
tar -czf "$tmp/wrong.tar.gz" -C "$tmp" wrong
PACKAGE_VERSION=9.8.7 scripts/build-homebrew-formula.sh --url "file://$tmp/wrong.tar.gz" \
  --output "$tmp/wrong.rb" "$tmp/wrong.tar.gz" >/dev/null 2>&1 &&
  fail "archive without the runtime payload was accepted"

template=Formula/bitcoin-starter-stack.rb.in
grep -Fq 'BITCOIN_STACK_PACKAGE_MANAGED=brew' "$template" || fail "launcher does not select Homebrew mode"
grep -Fq '#{opt_libexec}/scripts/bitcoin-stack' "$template" || fail "launcher bypasses the package wrapper"
grep -Fq 'depends_on "openssl@3"' "$template" || fail "formula does not install OpenSSL"
grep -Fq 'depends_on "iproute2"' "$template" || fail "Linuxbrew formula does not install iproute2"
grep -Fq 'formula_opt_bin("openssl@3")' "$template" || fail "launcher does not select formula OpenSSL"
grep -Fq 'Formula["iproute2"].opt_sbin' "$template" || fail "Linuxbrew launcher omits iproute2 sbin"
grep -Fq 'Library/Application Support/bitcoin-starter-stack' docs/homebrew.md || fail "macOS data path is not documented"
grep -Fq 'XDG_CONFIG_HOME' docs/homebrew.md || fail "Linuxbrew config does not follow XDG"
grep -Fq 'XDG_DATA_HOME' docs/homebrew.md || fail "Linuxbrew data does not follow XDG"
grep -Fq 'run [opt_bin/"bitcoin-stack", "host-service"]' "$template" || fail "brew service does not use host-service"
grep -Fq 'bitcoin-stack init' "$template" || fail "formula does not explain explicit setup"
grep -Fq 'Setup starts the Homebrew host supervisor automatically.' "$template" || fail "formula caveats describe manual supervisor setup"
grep -Fq 'STACK_RESTART_POLICY=on-failure:5' "$template" || fail "formula test omits the launch-at-login default"
grep -Fq '"password" => "formulatestpassword"' "$template" || fail "formula test omits its required dashboard password"
grep -Fq 'DASHBOARD_PASSWORD=formulatestpassword' "$template" || fail "formula test does not verify its rendered dashboard password"
grep -Fq 'assert_equal preset_config, config_path.read' "$template" || fail "formula test does not preserve preset config"
grep -Fq 'pipe_output("#{bin}/bitcoin-stack init"' "$template" && fail "formula test can start Docker or register a real service"
grep -Eq 'post_install|system .*bitcoin-stack.*(init|up)|system .*docker' "$template" &&
  fail "formula installation has a lifecycle side effect"

formula_config="$tmp/formula-config/config.json"
formula_state="$tmp/formula-state"
formula_data="$tmp/formula-data"
mkdir -p "${formula_config%/*}" "$formula_state" "$formula_data"
printf '%s\n' '{"bitcoin":{"enabled":true,"resource_profile":"compact"},"dashboard":{"password":"formulatestpassword","bind_ip":"auto","launch_at_boot":false}}' >"$formula_config"
formula_config_before=$(cat "$formula_config")
BITCOIN_STACK_ROOT=$PWD BITCOIN_STACK_PACKAGE_MANAGED=brew \
  BITCOIN_STACK_CONFIG_DIR="${formula_config%/*}" BITCOIN_STACK_STATE_DIR="$formula_state" \
  BITCOIN_STACK_DATA_DIR="$formula_data" BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.116 \
  ./configure.sh >/dev/null
[ "$(cat "$formula_config")" = "$formula_config_before" ] || fail "formula configure fixture was rewritten"
grep -q '^DASHBOARD_PASSWORD=formulatestpassword$' "$formula_state/.env" || fail "formula configure fixture did not render dashboard authentication"
grep -q '^DASHBOARD_BIND_IP=192\.168\.1\.116$' "$formula_state/.env" || fail "formula configure fixture did not use the package bind address"
grep -q '^COMPOSE_PROFILES=bitcoin$' "$formula_state/.env" || fail "formula configure fixture did not enable Bitcoin"
grep -q '^BITCOIN_RESOURCE_PROFILE=compact$' "$formula_state/.env" || fail "formula configure fixture lost its resource profile"
grep -q '^STACK_RESTART_POLICY=on-failure:5$' "$formula_state/.env" || fail "formula configure fixture changed its restart policy"

runtime="$tmp/runtime"
home="$tmp/home"
mkdir -p "$runtime/build/bitcoin" "$runtime/build/tari" "$runtime/build/zcash" "$home"
cp scripts/bitcoin-stack "$runtime/bitcoin-stack"
for file in build/bitcoin/bitcoin.conf build/tari/config.toml build/tari/entrypoint.sh \
  build/zcash/zakurad.toml build/zcash/healthcheck.sh; do
  cp "$file" "$runtime/$file"
done
cat >"$runtime/stack" <<'EOF'
#!/bin/sh
printf '%s\n' "$BITCOIN_STACK_ASSET_ROOT"
EOF
chmod +x "$runtime/stack"
case "$(uname -s)" in
  Darwin) expected_base="$home/Library/Application Support/bitcoin-starter-stack/state/package-assets" ;;
  Linux) expected_base="$home/xdg-data/bitcoin-starter-stack/state/package-assets" ;;
  *) fail "unsupported test platform" ;;
esac
asset_root=$(HOME="$home" XDG_CONFIG_HOME="$home/xdg-config" XDG_DATA_HOME="$home/xdg-data" \
  BITCOIN_STACK_ROOT="$runtime" BITCOIN_STACK_PACKAGE_MANAGED=brew "$runtime/bitcoin-stack" help)
case "$asset_root" in "$expected_base"/*) ;; *) fail "package assets were staged outside private state" ;; esac
asset_name=${asset_root##*/}
[ "${#asset_name}" -eq 64 ] || fail "package asset directory is not content-addressed"
for file in build/bitcoin/bitcoin.conf build/tari/config.toml build/tari/entrypoint.sh \
  build/zcash/zakurad.toml build/zcash/healthcheck.sh; do
  cmp -s "$runtime/$file" "$asset_root/$file" || fail "staged asset differs: $file"
done
old_root=$asset_root
printf 'changed fixture\n' >"$runtime/build/bitcoin/bitcoin.conf"
asset_root=$(HOME="$home" XDG_CONFIG_HOME="$home/xdg-config" XDG_DATA_HOME="$home/xdg-data" \
  BITCOIN_STACK_ROOT="$runtime" BITCOIN_STACK_PACKAGE_MANAGED=brew "$runtime/bitcoin-stack" help)
[ "$asset_root" != "$old_root" ] || fail "changed assets reused the previous content address"
grep -Fq 'changed fixture' "$asset_root/build/bitcoin/bitcoin.conf" || fail "changed asset was not staged"
grep -Fq 'changed fixture' "$old_root/build/bitcoin/bitcoin.conf" && fail "upgrade mutated an existing staged asset"
mv "$runtime/build/zcash/healthcheck.sh" "$runtime/build/zcash/healthcheck.sh.missing"
HOME="$home" XDG_CONFIG_HOME="$home/xdg-config" XDG_DATA_HOME="$home/xdg-data" \
  BITCOIN_STACK_ROOT="$runtime" BITCOIN_STACK_PACKAGE_MANAGED=brew "$runtime/bitcoin-stack" help >/dev/null 2>&1 &&
  fail "wrapper accepted an incomplete runtime payload"
mv "$runtime/build/zcash/healthcheck.sh.missing" "$runtime/build/zcash/healthcheck.sh"
[ "$(grep -c 'STACK_ASSET_ROOT' docker-compose.yml)" -eq 4 ] || fail "compose does not use the staged asset root"
grep -Fq "STACK_ASSET_ROOT=\$asset_root" configure.sh || fail "configure does not persist the staged asset root"

echo "PASS: test_homebrew.sh"
