#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  exit 1
}

if ! command -v dpkg-deb >/dev/null; then
  echo "SKIP: test_package.sh (dpkg-deb not installed)"
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export RUNTIME_IMAGE_DIGESTS_FILE=$tmp/runtime-images.json
jq -n '{
  tor:"sha256:1111111111111111111111111111111111111111111111111111111111111111",
  dashboard:"sha256:2222222222222222222222222222222222222222222222222222222222222222",
  monero:"sha256:3333333333333333333333333333333333333333333333333333333333333333",
  "tari-wallet":"sha256:4444444444444444444444444444444444444444444444444444444444444444",
  litecoin:"sha256:5555555555555555555555555555555555555555555555555555555555555555",
  dogecoin:"sha256:6666666666666666666666666666666666666666666666666666666666666666",
  bitcoincash:"sha256:7777777777777777777777777777777777777777777777777777777777777777"
}' >"$RUNTIME_IMAGE_DIGESTS_FILE"
RUNTIME_IMAGE_DIGESTS_FILE=$tmp/missing.json SOURCE_DATE_EPOCH=1700000000 scripts/build-deb.sh >/dev/null 2>&1 &&
  fail "package build accepted a missing runtime digest file"
jq 'del(.tor)' "$RUNTIME_IMAGE_DIGESTS_FILE" >"$tmp/invalid-runtime-images.json"
RUNTIME_IMAGE_DIGESTS_FILE=$tmp/invalid-runtime-images.json SOURCE_DATE_EPOCH=1700000000 scripts/build-deb.sh >/dev/null 2>&1 &&
  fail "package build accepted an incomplete runtime digest file"

source_version=$(cat VERSION)
unset PACKAGE_VERSION
SOURCE_DATE_EPOCH=1700000000 scripts/build-deb.sh >/dev/null
default_deb="dist/node-manager_${source_version}_all.deb"
cp "$default_deb" "$tmp/default-first.deb"
SOURCE_DATE_EPOCH=1700000000 scripts/build-deb.sh >/dev/null
cmp -s "$tmp/default-first.deb" "$default_deb" || fail "default package build is not reproducible"

export PACKAGE_VERSION=1.31.1-0pr125.abcdef0
SOURCE_DATE_EPOCH=1700000000 scripts/build-deb.sh >/dev/null
deb="dist/node-manager_${PACKAGE_VERSION}_all.deb"
cp "$deb" "$tmp/first.deb"
SOURCE_DATE_EPOCH=1700000000 scripts/build-deb.sh >/dev/null
cmp -s "$tmp/first.deb" "$deb" || fail "package build is not reproducible"

dpkg-deb -c "$deb" >"$tmp/contents"
[ "$(dpkg-deb -f "$deb" Version)" = "$PACKAGE_VERSION" ] || fail "package version override was ignored"
for invalid in 1.0+candidate candidate; do
  invalid_deb="dist/node-manager_${invalid}_all.deb"
  rm -f "$invalid_deb"
  PACKAGE_VERSION=$invalid SOURCE_DATE_EPOCH=1700000000 scripts/build-deb.sh >/dev/null 2>&1 && fail "invalid package version was accepted: $invalid"
  [ ! -e "$invalid_deb" ] || fail "invalid package version produced an artifact: $invalid"
done
for required in \
  ./usr/bin/node-manager \
  ./usr/lib/node-manager/stack \
  ./usr/lib/node-manager/configure.sh \
  ./usr/lib/node-manager/docker-compose.yml \
  ./usr/lib/node-manager/docker-compose.tari-wallet.yml \
  ./usr/lib/node-manager/docker-compose.rpc.yml \
  ./usr/lib/node-manager/docker-compose.monero-rpc.yml \
  ./usr/lib/node-manager/docker-compose.monero-zmq.yml \
  ./usr/lib/node-manager/docker-compose.tari-grpc.yml \
  ./usr/lib/node-manager/docker-compose.litecoin-rpc.yml \
  ./usr/lib/node-manager/docker-compose.dogecoin-rpc.yml \
  ./usr/lib/node-manager/docker-compose.bitcoincash-rpc.yml \
  ./usr/lib/node-manager/docker-compose.zcash-rpc.yml \
  ./usr/lib/node-manager/docker-compose.utxo-index.yml \
  ./usr/lib/node-manager/runtime-images.json \
  ./usr/lib/node-manager/build/dashboard/main.go \
  ./usr/lib/node-manager/build/dashboard/login.go \
  ./usr/lib/node-manager/build/dashboard/sessions.go \
  ./usr/lib/node-manager/build/dashboard/index.html \
  ./usr/lib/node-manager/build/dashboard/login.html \
  ./usr/lib/node-manager/build/dashboard/static/login.css \
  ./usr/lib/node-manager/build/dashboard/static/session.js \
  ./usr/lib/node-manager/build/dashboard/static/theme-init.js \
  ./usr/lib/node-manager/build/monero/Dockerfile \
  ./usr/lib/node-manager/build/monero/wallet-entrypoint.sh \
  ./usr/lib/node-manager/build/monero/wallet-healthcheck.sh \
  ./usr/lib/node-manager/build/tari/config.toml \
  ./usr/lib/node-manager/build/tari-wallet/Dockerfile \
  ./usr/lib/node-manager/build/tari-wallet/launcher.go \
  ./usr/lib/node-manager/build/tari-wallet/config.toml \
  ./usr/lib/node-manager/build/bitcoin-family/Dockerfile \
  ./usr/lib/node-manager/build/bitcoin-family/entrypoint.sh \
  ./usr/lib/node-manager/build/bitcoin-family/healthcheck.sh \
  ./usr/lib/node-manager/build/zcash/zakurad.toml \
  ./usr/lib/node-manager/build/zcash/healthcheck.sh \
  ./usr/lib/node-manager/build/tor/torrc \
  ./usr/lib/node-manager/node-resource-profile \
  ./usr/lib/node-manager/node-port-control \
  ./usr/lib/node-manager/node-control \
  ./usr/lib/systemd/system/node-manager-resource-profile.service \
  ./usr/lib/systemd/system/node-manager-resource-profile.timer \
  ./usr/lib/systemd/system/node-manager-port-control.service \
  ./usr/lib/systemd/system/node-manager-port-control.timer \
  ./usr/lib/systemd/system/node-manager-node-control.service \
  ./usr/lib/systemd/system/node-manager-node-control.timer \
  ./usr/share/doc/node-manager/README.md \
  ./usr/share/doc/node-manager/docs/Caddyfile.example \
  ./usr/share/doc/node-manager/docs/nodes.md \
  ./usr/share/doc/node-manager/docs/privacy.md; do
  grep -q " $required$" "$tmp/contents" || fail "package is missing $required"
done
if grep -Eq '/(tests|parity|experiments|images|__pycache__|\.pytest_cache)/|_test\.go$|\.test\.mjs$|\.proto$|package\.json$|/\.coverage$|\.pyc$|docs/releasing\.md$|\.(jpg|jpeg)$' "$tmp/contents"; then
  fail "package contains development-only files"
fi
if grep -Eq ' \./(etc|var)/' "$tmp/contents"; then
  fail "package owns mutable system configuration or state"
fi
depends=$(dpkg-deb -f "$deb" Depends)
printf '%s' "$depends" | grep -q 'docker-ce | docker.io' || fail "Docker Engine dependency alternatives are missing"
printf '%s' "$depends" | grep -q 'curl' || fail "curl dependency is missing"
printf '%s' "$depends" | grep -q 'util-linux' || fail "resource profile lock dependency is missing"
printf '%s' "$depends" | grep -q 'iproute2' || fail "port listener inspection dependency is missing"
printf '%s' "$depends" | grep -q 'docker-compose-plugin (>= 2.33.1) | docker-compose-v2 (>= 2.33.1) | docker-compose (>= 2.33.1)' || fail "Compose v2 dependency alternatives are missing minimum versions"

mkdir "$tmp/control"
dpkg-deb -e "$deb" "$tmp/control"
for hook in preinst prerm postrm; do
  [ ! -e "$tmp/control/$hook" ] || fail "package contains lifecycle hook $hook"
done
[ -x "$tmp/control/postinst" ] || fail "package is missing its post-upgrade activation hook"
grep -q 'dpkg --compare-versions.* gt ' "$tmp/control/postinst" || fail "postinst does not reject reinstalls and downgrades"
grep -q 'env -i' "$tmp/control/postinst" || fail "postinst passes the package-manager environment to Docker"
grep -q '/usr/bin/node-manager package-upgrade' "$tmp/control/postinst" || fail "postinst does not use the package activation command"
if grep -q '@VERSION@' "$tmp/control/postinst"; then
  fail "postinst package version placeholder was not rendered"
fi

postinst_fixture=$tmp/postinst-fixture
postinst_state=$tmp/postinst-state
postinst_helper=$tmp/postinst-helper
postinst_log=$tmp/postinst.log
mkdir "$postinst_state"
printf 'ready\n' >"$postinst_state/.env"
printf 'ready\n' >"$postinst_state/.approved-config.json"
printf 'ready\n' >"$postinst_state/.approved.env"
sed \
  -e "s|state=/var/lib/node-manager|state=$postinst_state|" \
  -e "s|/usr/bin/node-manager package-upgrade|$postinst_helper package-upgrade|" \
  "$tmp/control/postinst" >"$postinst_fixture"
chmod 755 "$postinst_fixture"
cat >"$postinst_helper" <<'EOF'
#!/bin/sh
printf 'command=%s docker_host=%s compose_file=%s stack_root=%s home=%s path=%s\n' \
  "$*" "${DOCKER_HOST-unset}" "${COMPOSE_FILE-unset}" "${BITCOIN_STACK_ROOT-unset}" "$HOME" "$PATH" >>"@POSTINST_LOG@"
[ ! -e "@POSTINST_FAIL@" ] || exit 42
EOF
sed -i.bak -e "s|@POSTINST_LOG@|$postinst_log|g" -e "s|@POSTINST_FAIL@|$tmp/postinst-fail|g" "$postinst_helper"
rm -f "$postinst_helper.bak"
chmod 755 "$postinst_helper"

"$postinst_fixture" configure
[ ! -e "$postinst_log" ] || fail "fresh install invoked package activation"
"$postinst_fixture" configure "$PACKAGE_VERSION"
[ ! -e "$postinst_log" ] || fail "package reinstall invoked activation"
"$postinst_fixture" configure 9.99.0
[ ! -e "$postinst_log" ] || fail "package downgrade invoked activation"
DOCKER_HOST=ssh://hostile COMPOSE_FILE=/hostile BITCOIN_STACK_ROOT=/hostile HOME=/hostile PATH=/hostile \
  "$postinst_fixture" configure 1.0.0
grep -q 'command=package-upgrade docker_host=unset compose_file=unset stack_root=unset home=/root path=/usr/sbin:/usr/bin:/sbin:/bin' "$postinst_log" ||
  fail "postinst did not sanitize its activation environment"
: >"$postinst_log"
touch "$tmp/postinst-fail"
"$postinst_fixture" configure 1.0.0 || fail "postinst failure blocked package configuration"
grep -q 'command=package-upgrade' "$postinst_log" || fail "postinst did not invoke activation on upgrade"

printf 'STACK_ROOT=/usr/lib/bitcoin-starter-stack\nSTACK_ASSET_ROOT=/usr/lib/bitcoin-starter-stack\n' >"$postinst_state/.env"
printf 'STACK_ROOT=/usr/lib/bitcoin-starter-stack\nREADY=1\n' >"$postinst_state/.approved.env"
"$postinst_fixture" configure 1.0.0
grep -q '^STACK_ROOT=/usr/lib/node-manager$' "$postinst_state/.env" || fail "postinst did not migrate STACK_ROOT in .env"
grep -q '^STACK_ASSET_ROOT=/usr/lib/node-manager$' "$postinst_state/.env" || fail "postinst did not migrate STACK_ASSET_ROOT in .env"
grep -q '^STACK_ROOT=/usr/lib/node-manager$' "$postinst_state/.approved.env" || fail "postinst did not migrate STACK_ROOT in .approved.env"
grep -q '^READY=1$' "$postinst_state/.approved.env" || fail "postinst env migration dropped unrelated lines"

root=$tmp/root
dpkg-deb -x "$deb" "$root"
[ "$(cat "$root/usr/lib/node-manager/VERSION")" = "$PACKAGE_VERSION" ] || fail "payload VERSION does not match package metadata"
packaged_main=$root/usr/lib/node-manager/docker-compose.yml
packaged_tari=$root/usr/lib/node-manager/docker-compose.tari-wallet.yml
total_pinned=0
for service in tor dashboard monero tari-wallet litecoin dogecoin bitcoincash; do
  digest=$(jq -r --arg service "$service" '.[$service]' "$RUNTIME_IMAGE_DIGESTS_FILE")
  expected=1
  [ "$service" != monero ] || expected=2
  count=$({ grep -hF "ghcr.io/vijitsingh97/node-manager-$service:$PACKAGE_VERSION@$digest" "$packaged_main" "$packaged_tari" || true; } | wc -l | tr -d ' ')
  [ "$count" = "$expected" ] || fail "package did not bind $service to its published digest"
  total_pinned=$((total_pinned + count))
done
[ "$total_pinned" = 8 ] || fail "package runtime digest set is incomplete"
if grep -hEq 'ghcr.io/vijitsingh97/node-manager-[^:]+:\$\{STACK_VERSION' "$packaged_main" "$packaged_tari"; then
  fail "package retained a mutable first-party runtime tag"
fi
awk '$1 == "image:" && ($2 ~ /^bitcoin\/bitcoin:/ || $2 ~ /^ghcr.io\/tari-project\// || $2 ~ /^zakuracore\/zakura:/) { print $2 }' docker-compose.yml |
  while IFS= read -r image; do
    grep -Fq "image: $image" "$packaged_main" || fail "package changed external image pin $image"
  done
privacy=$root/usr/share/doc/node-manager/docs/privacy.md
grep -q "^expected='72FD656754BD7534E274360A37070C2730C0B4EA'$" "$privacy" || fail "published feed fingerprint is missing"
grep -q 'node-manager.asc' "$privacy" || fail "feed key download is missing"
grep -q -- '--proxy' "$privacy" && fail "feed key download forces a host proxy"
grep -q -- 'gpg --batch --show-keys --with-colons' "$privacy" || fail "feed key fingerprint is not verified"
grep -q 'primary_keys++' "$privacy" || fail "feed key check does not reject extra primary keys"
grep -Fq "1:\$expected" "$privacy" || fail "feed key check does not bind the only primary key to the fingerprint"
if [ "$(id -u)" -ne 0 ]; then
  echo "PASS: test_package.sh artifact checks"
  echo "SKIP: test_package.sh lifecycle checks require root; the dedicated package CI job runs them"
  exit 0
fi
fake=$tmp/fake-bin
mkdir "$fake"
cat >"$fake/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
if [ "${1:-}" = ps ] && [ "${2:-}" = -aq ]; then
  requested_service=
  for arg in "$@"; do
    case "$arg" in label=com.docker.compose.service=*) requested_service=${arg##*=} ;; esac
  done
  if [ -n "${FAKE_EXISTING_CONFIG:-}" ]; then
    existing_service=${FAKE_EXISTING_SERVICE:-tor}
    [ "$requested_service" != "$existing_service" ] || printf 'existing-id\n'
  fi
  exit 0
fi
if [ "${1:-}" = inspect ]; then
  [ -n "${FAKE_EXISTING_CONFIG:-}" ] || exit 1
  printf '%s\n' "$FAKE_EXISTING_CONFIG"
fi
if [ "${FAKE_SOURCE_VOLUMES:-}" = 1 ] && [ "${1:-}" = compose ]; then
  case " $* " in
    *' --project-name node-manager-apt '*) ;;
    *) exit 88 ;;
  esac
fi
case " $* " in
  *' config --images '*) printf 'example.invalid/stack:test\n' ;;
  *' image inspect '*) [ "${FAKE_IMAGES_MISSING:-0}" != 1 ] || [ -f "$FAKE_DOCKER_LOG.images-ready" ]; exit ;;
  *' pull '*) touch "$FAKE_DOCKER_LOG.images-ready" ;;
esac
EOF
chmod +x "$fake/docker"
cat >"$fake/systemctl" <<'EOF'
#!/bin/sh
printf 'systemctl %s\n' "$*" >>"$FAKE_DOCKER_LOG"
EOF
chmod +x "$fake/systemctl"

home=$tmp/home
config_dir=$tmp/etc/node-manager
state_dir=$tmp/var/lib/node-manager
data_root=$state_dir/data
log=$tmp/docker.log
wrapper=$root/usr/bin/node-manager
runtime=$root/usr/lib/node-manager
export HOME=$home BITCOIN_STACK_CONFIG_DIR=$config_dir BITCOIN_STACK_STATE_DIR=$state_dir BITCOIN_STACK_DATA_DIR=$data_root
export BITCOIN_STACK_ROOT=$runtime FAKE_DOCKER_LOG=$log PATH=$fake:$PATH

"$wrapper" help | grep -q 'Usage: node-manager' || fail "package command help is wrong"
[ ! -e "$log" ] || fail "help invoked Docker"
out=$("$wrapper" upgrade)
printf '%s' "$out" | grep -q 'sudo apt install --only-upgrade node-manager' || fail "upgrade omitted APT instructions"
[ ! -e "$log" ] || fail "package upgrade invoked Docker"

: >"$log"
init_out=$(BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.116 FAKE_IMAGES_MISSING=1 "$wrapper" init)
[ "$(jq -r '.bitcoin.enabled' "$config_dir/config.json")" = true ] || fail "package init did not default to Bitcoin"
[ "$(jq -r '.bitcoin.resource_profile' "$config_dir/config.json")" = compact ] || fail "package init did not select the compact Bitcoin profile"
dashboard_password=$(jq -r '.dashboard.password' "$config_dir/config.json")
[[ "$dashboard_password" =~ ^[a-f0-9]{32}$ ]] || fail "package init did not generate a 32-character dashboard password"
[ "$(stat -c '%a' "$config_dir/config.json" 2>/dev/null || stat -f '%Lp' "$config_dir/config.json")" = 600 ] || fail "package init config is not mode 600"
[ "$(jq -r '.dashboard.bind_ip' "$config_dir/config.json")" = auto ] || fail "package init did not preserve automatic LAN binding"
[ "$(jq -r '.dashboard.launch_at_boot' "$config_dir/config.json")" = false ] || fail "package init enabled launch at boot"
[ "$(jq -r '.bitcoin.data_dir // empty' "$config_dir/config.json")" = "" ] || fail "package init stored the default FHS data path"
jq -e '(.bitcoin | has("node_password") | not)' "$config_dir/config.json" >/dev/null || fail "package init stored generated credentials in config"
printf '%s' "$init_out" | grep -q "Dashboard — username: admin  password: $dashboard_password" || fail "package init did not print its generated dashboard credential"
printf '%s' "$init_out" | grep -q 'Bitcoin RPC — username: admin  password:' || fail "package init did not print the Bitcoin credentials"
printf '%s' "$init_out" | grep -q 'Monero RPC' && fail "package init printed credentials for an unselected node"
printf '%s' "$init_out" | grep -qE 'Dashboard password \(|Nodes to run \(|data directory \[|Download missing containers' && fail "package init still prompts for setup answers"
grep -q -- 'compose .* pull' "$log" || fail "package init did not pull missing images"
grep -q -- 'compose .* up -d --pull never --no-build' "$log" || fail "package init start allows implicit pulls or builds"
grep -q 'systemctl enable --now node-manager-resource-profile.timer' "$log" || fail "package init did not start the resource profile timer"
grep -q 'systemctl enable --now node-manager-port-control.timer' "$log" || fail "package init did not start the LAN port-control timer"
grep -q 'systemctl enable --now node-manager-node-control.timer' "$log" || fail "package init did not start the node-control timer"

env_file=$state_dir/.env
[ -f "$env_file" ] || fail "generated state is outside the package state directory"
grep -q "^DASHBOARD_PASSWORD=$dashboard_password$" "$env_file" || fail "fresh package install did not retain dashboard authentication"
grep -q '^DASHBOARD_BIND_IP=192.168.1.116$' "$env_file" || fail "fresh package install did not use the detected LAN address"
grep -q '^BITCOIN_RPC_USER=admin$' "$env_file" || fail "fresh package install did not generate the Bitcoin RPC user"
bitcoin_password=$(sed -n 's/^BITCOIN_RPC_PASSWORD=//p' "$env_file")
printf '%s' "$bitcoin_password" | grep -qE '^[0-9a-f]{48}$' || fail "fresh package install did not generate a Bitcoin RPC password"
grep -q "^BITCOIN_DATA_DIR=$data_root/bitcoin$" "$env_file" || fail "data path is outside the package data directory"
grep -q '^BITCOIN_DBCACHE=128$' "$env_file" || fail "fresh package install dbcache default is not 128 MB"
grep -q '^BITCOIN_PRUNE=10000$' "$env_file" || fail "fresh package install is not pruned to 10000 MB"
grep -q '^BITCOIN_RPC_EXPOSE=0$' "$env_file" || fail "fresh package install exposed RPC"
grep -q '^MONERO_ENABLED=0$' "$env_file" || fail "fresh package install enabled Monero"
grep -q '^COMPOSE_PROFILES=bitcoin$' "$env_file" || fail "fresh package install did not select Bitcoin"
grep -q '^BITCOIN_RESOURCE_PROFILE=compact$' "$env_file" || fail "package init did not enable the compact Bitcoin profile"
grep -q '^MONERO_RESOURCE_PROFILE=off$' "$env_file" || fail "package init enabled the compact profile for an unselected node"
grep -q '^BITCOIN_STEADY_CPUS=1$' "$env_file" || fail "package init did not render the Bitcoin steady CPU default"
grep -q '^BITCOIN_STEADY_MEM_LIMIT_MB=2048$' "$env_file" || fail "package init did not render the Bitcoin steady memory default"
grep -q '^STACK_RESTART_POLICY=on-failure:5$' "$env_file" || fail "fresh package install enabled launch at boot"
grep -q '^COMPOSE_FILE=docker-compose.yml$' "$env_file" || fail "fresh package install enabled an optional Compose override"
grep -q "^STACK_ROOT=$runtime$" "$env_file" || fail "immutable payload path was not rendered"
wallets=$(sed -n 's/^WATCH_WALLETS_B64=//p' "$env_file" | base64 -d)
[ "$wallets" = '[]' ] || fail "fresh pruned package seeded a wallet it cannot scan"
[ -d "$data_root/bitcoin" ] || fail "data directory was not created"
[ "$(stat -c %u "$data_root/bitcoin")" = 1000 ] || fail "data directory is not owned by the container uid"
[ "$(stat -c %a "$data_root/bitcoin")" = 700 ] || fail "data directory permissions are not private"
[ "$(stat -c %u "$data_root/zcash")" = 10001 ] || fail "Zcash data directory is not owned by the Zakura uid"
[ "$(stat -c %g "$data_root/zcash")" = 1000 ] || fail "Zcash data directory is not readable by the dashboard group"
[ "$(stat -c %a "$data_root/zcash")" = 750 ] || fail "Zcash data directory permissions block its runtime handoff"
[ ! -e "$home" ] || fail "package wrote configuration or data under HOME"
[ ! -e "$runtime/.env" ] || fail "package wrote state into /usr/lib payload"
grep -q -- "--project-directory $state_dir" "$log" || fail "Compose project directory is not package state"
grep -q '^COMPOSE_PROJECT_NAME=node-manager-apt$' "$env_file" || fail "fresh package did not persist its Compose identity"
grep -q -- "--project-name node-manager-apt" "$log" || fail "package Compose project is not isolated from source installs"
grep -q -- "--env-file $env_file" "$log" || fail "Compose did not use package state env"
grep -q -- "-f $runtime/docker-compose.yml up -d" "$log" || fail "Compose did not use immutable payload"
grep -q -- "-f $runtime/docker-compose.rpc.yml" "$log" && fail "default package startup loaded the RPC override"

preset_config_dir=$tmp/preset/etc/node-manager
preset_state_dir=$tmp/preset/var/lib/node-manager
preset_data_root=$preset_state_dir/data
mkdir -p "$preset_config_dir"
jq -nc --arg data_dir "$preset_data_root/custom-monero" \
  '{bitcoin:{enabled:false},monero:{enabled:true,data_dir:$data_dir,node_password:"presetmoneropassword123456789012"},dashboard:{password:"presetdashboardpassword",bind_ip:"192.168.1.118",launch_at_boot:true}}' \
  >"$preset_config_dir/config.json"
cp "$preset_config_dir/config.json" "$tmp/preset-config.before"
: >"$log"
BITCOIN_STACK_CONFIG_DIR=$preset_config_dir BITCOIN_STACK_STATE_DIR=$preset_state_dir BITCOIN_STACK_DATA_DIR=$preset_data_root \
  "$wrapper" init >/dev/null
cmp -s "$tmp/preset-config.before" "$preset_config_dir/config.json" || fail "package init replaced an explicit preset config"
grep -q '^COMPOSE_PROFILES=monero$' "$preset_state_dir/.env" || fail "package init replaced a preset node selection"
grep -q '^DASHBOARD_PASSWORD=presetdashboardpassword$' "$preset_state_dir/.env" || fail "package init replaced a preset dashboard password"
grep -q '^MONERO_RPC_PASSWORD=presetmoneropassword123456789012$' "$preset_state_dir/.env" || fail "package init replaced a preset node credential"
grep -q '^STACK_RESTART_POLICY=unless-stopped$' "$preset_state_dir/.env" || fail "package init replaced a preset launch setting"

sed -i 's/^BITCOIN_DBCACHE=.*/BITCOIN_DBCACHE=512/' "$env_file"
"$wrapper" apply >/dev/null
grep -q '^BITCOIN_DBCACHE=512$' "$env_file" || fail "existing package dbcache was replaced"

rm -f "$env_file"
printf '%s\n' '{"bitcoin":{"rpc_expose":true,"rpc_bind_ip":"192.168.1.117","rpc_port":18332},"dashboard":{"password":"packagetestdashboardpassword"},"wallets":[{"name":"Configured","key":"34xp4vRoCGJym3xR7yCVPFHoCNxv4Twseo"}]}' >"$config_dir/config.json"
: >"$log"
"$wrapper" apply >/dev/null
grep -q '^BITCOIN_PRUNE=10000$' "$env_file" || fail "unrelated fresh config disabled the package pruning default"
grep -q '^BITCOIN_RPC_EXPOSE=1$' "$env_file" || fail "package did not enable requested LAN RPC exposure"
grep -q '^COMPOSE_FILE=docker-compose.yml:docker-compose.rpc.yml$' "$env_file" || fail "package did not select the RPC Compose override"
wallets=$(sed -n 's/^WATCH_WALLETS_B64=//p' "$env_file" | base64 -d)
[ "$(printf '%s' "$wallets" | jq -r '.[0].name')" = Configured ] || fail "explicit wallet configuration was replaced on a pruned package"
grep -q -- "-f $runtime/docker-compose.yml -f $runtime/docker-compose.rpc.yml up -d" "$log" || fail "package did not load the RPC Compose override from immutable payload"

: >"$log"
FAKE_SOURCE_VOLUMES=1 "$wrapper" apply >/dev/null || fail "stale source volumes blocked the isolated package project"
grep -q -- "--project-name node-manager-apt" "$log" || fail "package could reuse source-managed named volumes"

printf '%s\n' '{"bitcoin":{"prune_mb":0,"rpc_expose":true,"rpc_bind_ip":"192.168.1.117","rpc_port":18332}}' >"$config_dir/config.json"
: >"$log"
"$wrapper" apply >/dev/null
grep -q '^BITCOIN_PRUNE=0$' "$env_file" || fail "explicit full-node configuration was replaced by the package default"
grep -q '^BITCOIN_RPC_EXPOSE=1$' "$env_file" || fail "package did not enable requested LAN RPC exposure"
grep -q '^COMPOSE_FILE=docker-compose.yml:docker-compose.rpc.yml$' "$env_file" || fail "package did not select the RPC Compose override"
grep -q '"prune_mb":0' "$config_dir/config.json" || fail "package rewrote explicit configuration"
grep -q -- "-f $runtime/docker-compose.yml -f $runtime/docker-compose.rpc.yml up -d" "$log" || fail "package did not load the RPC Compose override from immutable payload"
rm -f "$config_dir/config.json"
"$wrapper" apply >/dev/null
grep -q '^BITCOIN_PRUNE=0$' "$env_file" || fail "existing full-node state was converted to pruning"

: >"$log"
FAKE_EXISTING_CONFIG=/source/node-manager/docker-compose.yml "$wrapper" up >"$tmp/existing.out" 2>&1 && fail "package startup replaced a source-managed container"
grep -q 'belongs to another installation' "$tmp/existing.out" || fail "existing deployment refusal is unclear"
grep -q 'compose .* up ' "$log" && fail "existing deployment refusal reached Compose up"

: >"$log"
FAKE_EXISTING_SERVICE=tari-wallet FAKE_EXISTING_CONFIG=/source/node-manager/docker-compose.yml \
  "$wrapper" up >"$tmp/existing-tari-wallet.out" 2>&1 && fail "package startup replaced a source-managed Tari wallet container"
grep -q 'Existing tari-wallet container belongs to another installation' "$tmp/existing-tari-wallet.out" || fail "Tari wallet deployment refusal is unclear"
grep -q 'compose .* up ' "$log" && fail "Tari wallet deployment refusal reached Compose up"

printf '%s\n' '{"dashboard":{"control":true}}' >"$config_dir/config.json"
rm -f "$log"
"$wrapper" apply >"$tmp/control.out" 2>&1 || fail "APT mode rejected legacy dashboard.control"
grep -q '^DASHBOARD_CONTROL=' "$env_file" && fail "legacy dashboard.control was rendered"

bad=$tmp/not-a-directory
: >"$bad"
BITCOIN_STACK_CONFIG_DIR=$bad "$wrapper" help >"$tmp/bad.out" 2>&1 && fail "non-directory config path was accepted"
grep -q 'is not a directory' "$tmp/bad.out" || fail "bad package path error is unclear"

rm -rf "$root"
[ -f "$env_file" ] || fail "package removal simulation deleted user state"
echo "PASS: test_package.sh"
