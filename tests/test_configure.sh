#!/usr/bin/env bash
# configure.sh end-to-end: zero-config auto-generation, .env rendering, validation.
set -euo pipefail
cd "$(dirname "$0")/.."

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export BITCOIN_STACK_DEFAULT_BIND_IP=192.168.50.10
cp configure.sh "$tmp/"

fail() {
  echo "FAIL: $1"
  exit 1
}

# 1. A baseline dashboard password is required before configure renders defaults.
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "configure accepted a missing dashboard password"
printf '%s\n' '{"dashboard":{"password":"configuretest12"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null || fail "configured defaults run failed"
[ -f "$tmp/.config.lock" ] || fail "standalone configure did not use the shared config lock"
grep -q '^BITCOIN_RPC_USER=admin$' "$tmp/.env" || fail "RPC user default is not 'admin'"
grep -qE '^BITCOIN_RPC_PASSWORD=[a-zA-Z0-9]{16,}$' "$tmp/.env" || fail "RPC password not auto-generated"
rpc_password=$(sed -n 's/^BITCOIN_RPC_PASSWORD=//p' "$tmp/.env")
dashboard_password=$(sed -n 's/^DASHBOARD_PASSWORD=//p' "$tmp/.env")
monero_password=$(sed -n 's/^MONERO_RPC_PASSWORD=//p' "$tmp/.env")
monero_wallet_rpc_password=$(sed -n 's/^MONERO_WALLET_RPC_PASSWORD=//p' "$tmp/.env")
monero_wallet_password=$(sed -n 's/^MONERO_WALLET_PASSWORD=//p' "$tmp/.env")
tari_wallet_rpc_password=$(sed -n 's/^TARI_WALLET_RPC_PASSWORD=//p' "$tmp/.env")
tari_wallet_password=$(sed -n 's/^TARI_WALLET_PASSWORD=//p' "$tmp/.env")
if [ "$dashboard_password" != configuretest12 ] || [ -z "$monero_password" ] || [ -z "$monero_wallet_rpc_password" ] || [ -z "$monero_wallet_password" ] || [ -z "$tari_wallet_rpc_password" ] || [ -z "$tari_wallet_password" ]; then
  fail "fresh install password defaults are wrong"
fi
passwords=$(printf '%s\n' "$rpc_password" "$monero_password" "$monero_wallet_rpc_password" "$monero_wallet_password" "$tari_wallet_rpc_password" "$tari_wallet_password" | sort -u | wc -l | tr -d ' ')
[ "$passwords" = "6" ] || fail "fresh install did not separate service passwords"
grep -q '^MONERO_WALLET_RPC_USER=admin$' "$tmp/.env" || fail "Monero wallet RPC user default is not admin"
grep -q '^MONERO_WALLET_RPC_URL=http://172.29.0.31:18082$' "$tmp/.env" || fail "Monero wallet RPC URL is not internal"
grep -q '^TARI_WALLET_RPC_USER=admin$' "$tmp/.env" || fail "Tari wallet RPC user default is not admin"
grep -q '^TARI_WALLET_GRPC_URL=http://172.29.0.37:18143$' "$tmp/.env" || fail "Tari wallet gRPC URL is not internal"
grep -q '^TARI_WALLET_ENABLED=0$' "$tmp/.env" || fail "Tari wallet integration did not follow the Tari default"
grep -q '^COMPOSE_FILE=docker-compose.yml$' "$tmp/.env" || fail "zero-config install enabled an optional compose override"
grep -q '^COMPOSE_PROFILES=bitcoin$' "$tmp/.env" || fail "zero-config install did not select only Bitcoin"
grep -q '^BITCOIN_ENABLED=1$' "$tmp/.env" || fail "Bitcoin default is not enabled"
grep -q '^MONERO_ENABLED=0$' "$tmp/.env" || fail "Monero default is not disabled"
grep -q '^BITCOIN_RPC_EXPOSE=0$' "$tmp/.env" || fail "LAN RPC exposure default is not off"
grep -q '^BITCOIN_RPC_BIND_IP=$' "$tmp/.env" || fail "LAN RPC bind default is not empty"
grep -q '^BITCOIN_RPC_PORT=8332$' "$tmp/.env" || fail "LAN RPC port default not applied"
grep -q '^BITCOIN_DBCACHE=128$' "$tmp/.env" || fail "fresh install dbcache default is not 128 MB"
grep -q '^BITCOIN_PRUNE=10000$' "$tmp/.env" || fail "fresh install prune default is not 10000 MB"
grep -q '^BITCOIN_INBOUND_ONION=0$' "$tmp/.env" || fail "fresh install inbound onion default is not off"
grep -q '^BITCOIN_STEADY_CPUS=1$' "$tmp/.env" || fail "Bitcoin steady CPU default is not 1"
grep -q '^BITCOIN_STEADY_MEM_LIMIT_MB=2048$' "$tmp/.env" || fail "Bitcoin steady memory default is not 2048 MB"
grep -q '^MONERO_STEADY_CPUS=1$' "$tmp/.env" || fail "Monero steady CPU default is not 1"
grep -q '^MONERO_STEADY_MEM_LIMIT_MB=2048$' "$tmp/.env" || fail "Monero steady memory default is not 2048 MB"
grep -q '^LITECOIN_STEADY_MEM_LIMIT_MB=2048$' "$tmp/.env" || fail "Litecoin steady memory default is not 2048 MB"
grep -q '^DOGECOIN_STEADY_MEM_LIMIT_MB=2048$' "$tmp/.env" || fail "Dogecoin steady memory default is not 2048 MB"
grep -q '^BITCOINCASH_STEADY_MEM_LIMIT_MB=2048$' "$tmp/.env" || fail "Bitcoin Cash steady memory default is not 2048 MB"
grep -q '^TARI_STEADY_MEM_LIMIT_MB=4096$' "$tmp/.env" || fail "Tari steady memory default is not 4096 MB"
grep -q '^DASHBOARD_ONION=0$' "$tmp/.env" || fail "fresh install dashboard onion default is not off"
grep -q '^DASHBOARD_PUBLIC_URL=$' "$tmp/.env" || fail "fresh install dashboard public URL default is not empty"
grep -q '^STACK_RESTART_POLICY=on-failure:5$' "$tmp/.env" || fail "fresh install restart policy is not crash-only"
watch_b64=$(sed -n 's/^WATCH_WALLETS_B64=//p' "$tmp/.env")
watch_json=$(printf '%s' "$watch_b64" | openssl base64 -d -A)
printf '%s' "$watch_json" | jq -e 'length == 1 and .[0].name == "Binance (public)" and .[0].key == "34xp4vRoCGJym3xR7yCVPFHoCNxv4Twseo" and .[0].birthday == ""' >/dev/null ||
  fail "zero-config install did not seed the public Binance example"
printf '%s\n' '{"dashboard":{"launch_at_boot":true}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^STACK_RESTART_POLICY=unless-stopped$' "$tmp/.env" || fail "launch-at-boot setting did not render"
rm -f "$tmp/config.json"

# 2. Re-running keeps the same auto-generated credentials AND the rpcauth
#    salt/hash (idempotent). A rotating salt re-derives the hash every run, which
#    changes the bitcoin container's env and needlessly recreates bitcoind on
#    every apply/upgrade (5-min flush each time).
u1=$(sed -n 's/^BITCOIN_RPC_USER=//p' "$tmp/.env")
p1=$(sed -n 's/^BITCOIN_RPC_PASSWORD=//p' "$tmp/.env")
s1=$(sed -n 's/^BITCOIN_RPCAUTH_SALT=//p' "$tmp/.env")
h1=$(sed -n 's/^BITCOIN_RPCAUTH_HASH=//p' "$tmp/.env")
mw1=$(sed -n 's/^MONERO_WALLET_RPC_PASSWORD=//p' "$tmp/.env")
wf1=$(sed -n 's/^MONERO_WALLET_PASSWORD=//p' "$tmp/.env")
tw1=$(sed -n 's/^TARI_WALLET_RPC_PASSWORD=//p' "$tmp/.env")
tf1=$(sed -n 's/^TARI_WALLET_PASSWORD=//p' "$tmp/.env")
(cd "$tmp" && ./configure.sh) >/dev/null
u2=$(sed -n 's/^BITCOIN_RPC_USER=//p' "$tmp/.env")
p2=$(sed -n 's/^BITCOIN_RPC_PASSWORD=//p' "$tmp/.env")
s2=$(sed -n 's/^BITCOIN_RPCAUTH_SALT=//p' "$tmp/.env")
h2=$(sed -n 's/^BITCOIN_RPCAUTH_HASH=//p' "$tmp/.env")
mw2=$(sed -n 's/^MONERO_WALLET_RPC_PASSWORD=//p' "$tmp/.env")
wf2=$(sed -n 's/^MONERO_WALLET_PASSWORD=//p' "$tmp/.env")
tw2=$(sed -n 's/^TARI_WALLET_RPC_PASSWORD=//p' "$tmp/.env")
tf2=$(sed -n 's/^TARI_WALLET_PASSWORD=//p' "$tmp/.env")
if [ "$u1" != "$u2" ] || [ "$p1" != "$p2" ]; then
  fail "credentials changed on re-run (not idempotent)"
fi
if [ "$s1" != "$s2" ] || [ "$h1" != "$h2" ]; then
  fail "rpcauth salt/hash changed on re-run — would recreate bitcoind on every apply/upgrade"
fi
if [ "$mw1" != "$mw2" ] || [ "$wf1" != "$wf2" ]; then
  fail "Monero wallet credentials changed on re-run"
fi
if [ "$tw1" != "$tw2" ] || [ "$tf1" != "$tf2" ]; then
  fail "Tari wallet credentials changed on re-run"
fi

# Existing rendered settings remain authoritative when a new default is added.
legacy="$tmp/legacy"
mkdir "$legacy"
cp configure.sh "$legacy/"
printf 'BITCOIN_RPC_USER=bitcoin\nBITCOIN_RPC_PASSWORD=oldrpc\nDASHBOARD_PASSWORD=olddashboard\nBITCOIN_DBCACHE=3000\nBITCOIN_PRUNE=0\nBITCOIN_INBOUND_ONION=1\nDASHBOARD_ONION=1\n' >"$legacy/.env"
(cd "$legacy" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_RPC_USER=bitcoin$' "$legacy/.env" || fail "existing RPC username was replaced"
grep -q '^BITCOIN_RPC_PASSWORD=oldrpc$' "$legacy/.env" || fail "existing RPC password was replaced"
grep -q '^DASHBOARD_PASSWORD=olddashboard$' "$legacy/.env" || fail "existing dashboard password was forced to match RPC"
grep -q '^BITCOIN_DBCACHE=3000$' "$legacy/.env" || fail "existing dbcache was replaced by the fresh-install default"
grep -q '^BITCOIN_PRUNE=0$' "$legacy/.env" || fail "existing archival node was converted to pruning"
grep -q '^BITCOIN_INBOUND_ONION=1$' "$legacy/.env" || fail "existing inbound onion setting was disabled"
grep -q '^DASHBOARD_ONION=1$' "$legacy/.env" || fail "existing dashboard onion setting was disabled"
grep -q '^STACK_RESTART_POLICY=unless-stopped$' "$legacy/.env" || fail "legacy install had launch at boot disabled"
grep -q '^COMPOSE_PROJECT_NAME=bitcoin-starter-stack$' "$legacy/.env" || fail "legacy source install changed its Compose project"

legacy_missing="$tmp/legacy-missing-prune"
mkdir "$legacy_missing"
cp configure.sh "$legacy_missing/"
printf 'BITCOIN_RPC_USER=bitcoin\nBITCOIN_RPC_PASSWORD=oldrpc\nDASHBOARD_PASSWORD=legacydashboard12\n' >"$legacy_missing/.env"
(cd "$legacy_missing" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_DBCACHE=3000$' "$legacy_missing/.env" || fail "pre-dbcache environment did not retain the legacy 3000 MB default"
grep -q '^BITCOIN_PRUNE=0$' "$legacy_missing/.env" || fail "existing pre-prune environment was converted to pruning"

existing_custom="$tmp/existing-custom-dbcache"
mkdir "$existing_custom"
cp configure.sh "$existing_custom/"
printf 'BITCOIN_RPC_USER=bitcoin\nBITCOIN_RPC_PASSWORD=oldrpc\nDASHBOARD_PASSWORD=legacydashboard12\nBITCOIN_DBCACHE=512\n' >"$existing_custom/.env"
(cd "$existing_custom" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_DBCACHE=512$' "$existing_custom/.env" || fail "custom existing dbcache was replaced"

legacy_limits="$tmp/legacy-limits"
mkdir "$legacy_limits"
cp configure.sh "$legacy_limits/"
printf 'BITCOIN_RPC_USER=bitcoin\nBITCOIN_RPC_PASSWORD=oldrpc\nDASHBOARD_PASSWORD=legacydashboard12\nBITCOIN_STEADY_CPUS=0.75\nBITCOIN_STEADY_MEM_LIMIT_MB=1536\nMONERO_STEADY_CPUS=1.25\nMONERO_STEADY_MEM_LIMIT_MB=3072\n' >"$legacy_limits/.env"
(cd "$legacy_limits" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_STEADY_CPUS=0.75$' "$legacy_limits/.env" || fail "existing Bitcoin steady CPU limit was replaced"
grep -q '^BITCOIN_STEADY_MEM_LIMIT_MB=1536$' "$legacy_limits/.env" || fail "existing Bitcoin steady memory limit was replaced"
grep -q '^MONERO_STEADY_CPUS=1.25$' "$legacy_limits/.env" || fail "existing Monero steady CPU limit was replaced"
grep -q '^MONERO_STEADY_MEM_LIMIT_MB=3072$' "$legacy_limits/.env" || fail "existing Monero steady memory limit was replaced"

echo '{"bitcoin":{"dbcache_mb":"256","prune_mb":0,"inbound_onion":false},"dashboard":{"onion":false}}' >"$legacy/config.json"
(cd "$legacy" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_DBCACHE=256$' "$legacy/.env" || fail "explicit dbcache did not override the existing rendered value"
grep -q '^BITCOIN_PRUNE=0$' "$legacy/.env" || fail "explicit prune_mb=0 was not preserved"
grep -q '^BITCOIN_INBOUND_ONION=0$' "$legacy/.env" || fail "explicit inbound_onion=false was ignored"
grep -q '^DASHBOARD_ONION=0$' "$legacy/.env" || fail "explicit dashboard.onion=false was ignored"

echo '{"dashboard":{"password":""}}' >"$legacy/config.json"
(cd "$legacy" && ./configure.sh) >/dev/null 2>&1 && fail "explicit empty dashboard password was accepted"

# 3. A config.json without credentials still gets auto-generated ones
rm -f "$tmp/.env"
echo '{"bitcoin": {"dbcache_mb": 512}, "dashboard": {"password": "partialconfig12"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -qE '^BITCOIN_RPC_USER=[a-zA-Z0-9]+$' "$tmp/.env" || fail "creds not auto-generated with partial config"
grep -q '^BITCOIN_DBCACHE=512$' "$tmp/.env" || fail "partial config override not applied"

# 4. Refuses special characters in credentials (they'd hit shell/env-file layers)
# shellcheck disable=SC2016 # non-expansion of the backtick is the point
echo '{"bitcoin": {"node_username": "u", "node_password": "p`whoami`"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "special characters in password were accepted"

# 5. Refuses prune targets bitcoind would reject (1-549) and non-numbers
echo '{"bitcoin": {"node_username": "u", "node_password": "p", "prune_mb": 100}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "prune_mb=100 was accepted"
echo '{"bitcoin": {"node_username": "u", "node_password": "p", "prune_mb": "lots"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "non-numeric prune_mb was accepted"

# 6. Refuses special characters in the dashboard password too
echo '{"bitcoin": {"node_username": "u", "node_password": "p"}, "dashboard": {"password": "a b"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "special characters in dashboard password were accepted"

# 7. Protected reverse-proxy mode requires an HTTPS origin, a baseline password,
# and a loopback dashboard bind.
echo '{"bitcoin":{"node_username":"u","node_password":"p"},"dashboard":{"public_url":"https://node.example.com","password":"longenough12","bind_ip":"127.0.0.1"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null || fail "protected dashboard configuration failed"
grep -q '^DASHBOARD_PUBLIC_URL=https://node.example.com$' "$tmp/.env" || fail "dashboard public URL was not rendered"
grep -q '^DASHBOARD_PASSWORD=longenough12$' "$tmp/.env" || fail "protected dashboard password was not rendered"
for invalid in 'http://node.example.com' 'https://node.example.com/' 'https://user:pass@node.example.com' 'https://node.example.com/path' 'https://node.example.com?x=1' 'https://node.example.com#x' 'https://node..example.com' 'https://[::::]' 'https://node.example.com:0' 'https://node.example.com:65536' 'https://node.example.com:70000'; do
  printf '%s\n' "{\"dashboard\":{\"public_url\":\"$invalid\",\"password\":\"longenough12\"}}" >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "invalid public URL was accepted: $invalid"
done
sed -i.bak 's|^DASHBOARD_PUBLIC_URL=.*$|DASHBOARD_PUBLIC_URL=https://stale.example.com|' "$tmp/.env"
rm -f "$tmp/.env.bak"
echo '{"dashboard":{"public_url":"","password":"longenough12"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null || fail "explicit empty public URL did not clear the rendered value"
grep -q '^DASHBOARD_PUBLIC_URL=$' "$tmp/.env" || fail "explicit empty public URL inherited stale .env value"
echo '{"dashboard":{"public_url":"https://node.example.com","password":"longenough12"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null || fail "public URL could not be enabled after explicit clear"
grep -q '^DASHBOARD_PUBLIC_URL=https://node.example.com$' "$tmp/.env" || fail "public URL was not restored after explicit clear"
echo '{"dashboard":{"public_url":null,"password":"longenough12"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "null public URL was accepted"
echo '{"dashboard":{"public_url":"https://node.example.com","password":"short"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "short protected dashboard password was accepted"
echo '{"dashboard":{"public_url":"https://node.example.com","password":"longenough12","bind_ip":"192.168.1.20"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "non-loopback protected dashboard bind was accepted"

# 8. Happy path renders .env with defaults applied
cat >"$tmp/config.json" <<'EOF'
{"bitcoin": {"node_username": "myuser", "node_password": "mypass123"}, "dashboard": {"password": "happyconfig12"}}
EOF
rm -f "$tmp/.env"
(cd "$tmp" && ./configure.sh) >/dev/null

grep -q '^BITCOIN_RPC_USER=myuser$' "$tmp/.env" || fail "username not rendered"
grep -q '^BITCOIN_RPC_PASSWORD=mypass123$' "$tmp/.env" || fail "password not rendered"
grep -q '^BITCOIN_DATA_DIR=./data/bitcoin$' "$tmp/.env" || fail "data_dir default not applied"
grep -q '^BITCOIN_DBCACHE=128$' "$tmp/.env" || fail "dbcache default not applied"
grep -q '^BITCOIN_PRUNE=10000$' "$tmp/.env" || fail "prune default not applied"
grep -q '^BITCOIN_INBOUND_ONION=0$' "$tmp/.env" || fail "inbound_onion should default off"
grep -q '^BITCOIN_BLOCKFILTERINDEX=0$' "$tmp/.env" || fail "blockfilterindex default not applied"
grep -q '^BITCOIN_RPC_EXPOSE=0$' "$tmp/.env" || fail "LAN RPC exposure default not applied"
grep -q '^COMPOSE_FILE=docker-compose.yml$' "$tmp/.env" || fail "default compose file selection is wrong"
grep -q '^COMPOSE_PROJECT_NAME=node-manager-source$' "$tmp/.env" || fail "fresh source install did not use its Node Manager Compose project"
grep -q '^DASHBOARD_PASSWORD=happyconfig12$' "$tmp/.env" || fail "dashboard password baseline was not retained"
grep -qE '^MONERO_RPC_PASSWORD=[a-zA-Z0-9]{16,}$' "$tmp/.env" || fail "Monero RPC password was not independently generated"
grep -qE '^MONERO_WALLET_RPC_PASSWORD=[a-zA-Z0-9]{16,}$' "$tmp/.env" || fail "Monero wallet RPC password was not generated"
grep -qE '^MONERO_WALLET_PASSWORD=[a-zA-Z0-9]{16,}$' "$tmp/.env" || fail "Monero wallet password was not generated"
grep -qE '^TARI_WALLET_RPC_PASSWORD=[a-zA-Z0-9]{16,}$' "$tmp/.env" || fail "Tari wallet RPC password was not generated"
grep -qE '^TARI_WALLET_PASSWORD=[a-zA-Z0-9]{16,}$' "$tmp/.env" || fail "Tari wallet password was not generated"
grep -q '^MONERO_DATA_DIR=./data/monero$' "$tmp/.env" || fail "Monero data_dir default not applied"
grep -q '^DASHBOARD_ONION=0$' "$tmp/.env" || fail "dashboard onion default not applied"
grep -q '^DASHBOARD_BIND_IP=192.168.50.10$' "$tmp/.env" || fail "dashboard did not use the detected private LAN address"
grep -q '^STACK_RESTART_POLICY=on-failure:5$' "$tmp/.env" || fail "fresh config restart policy is not crash-only"
grep -q '^TELEGRAM_BOT_TOKEN=$' "$tmp/.env" || fail "telegram token default not applied"
grep -q '^HEALTHCHECKS_URL=$' "$tmp/.env" || fail "healthchecks default not applied"
grep -q '^ALERT_NEW_BLOCK=0$' "$tmp/.env" || fail "new-block alert default not applied"
grep -q '^ALERT_NEW_BLOCK_MONERO=0$' "$tmp/.env" || fail "Monero new-block alert default not applied"
grep -q '^NODE_NAME=' "$tmp/.env" || fail "node name not rendered"
watch_b64=$(sed -n 's/^WATCH_WALLETS_B64=//p' "$tmp/.env")
printf '%s' "$watch_b64" | openssl base64 -d -A | jq -e '.[0].name == "Binance (public)"' >/dev/null ||
  fail "missing wallets must seed the public Binance example"
[ -d "$tmp/data/bitcoin" ] || fail "data dir not created"
[ -d "$tmp/data/monero" ] || fail "Monero data dir not created"

# An explicit empty roster opts out of the public example.
echo '{"wallets": []}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
watch_b64=$(sed -n 's/^WATCH_WALLETS_B64=//p' "$tmp/.env")
[ "$(printf '%s' "$watch_b64" | openssl base64 -d -A)" = "[]" ] ||
  fail "explicit empty wallets did not opt out"

# A malformed roster fails before replacing a valid rendered environment.
cp "$tmp/.env" "$tmp/env.before"
echo '{"wallets": {"name": "not-an-array"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "non-array wallets were accepted"
cmp -s "$tmp/.env" "$tmp/env.before" || fail "invalid wallets changed .env"

for unsafe_key in \
  'abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about' \
  'xprvPrivateMaterial' \
  'wpkh(xprvPrivateMaterial/0/*)'; do
  jq -n --arg key "$unsafe_key" '{wallets:[{name:"Rejected",key:$key}]}' >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >"$tmp/rejected.log" 2>&1 && fail "private or invalid configured wallet was accepted"
  cmp -s "$tmp/.env" "$tmp/env.before" || fail "rejected public-only wallet changed .env"
  grep -Fq "$unsafe_key" "$tmp/rejected.log" && fail "rejected wallet material leaked into output"
done

# 8. rpcauth is a real salted HMAC of the password (re-derive to check)
salt=$(sed -n 's/^BITCOIN_RPCAUTH_SALT=//p' "$tmp/.env")
hash=$(sed -n 's/^BITCOIN_RPCAUTH_HASH=//p' "$tmp/.env")
echo "$salt" | grep -qE '^[0-9a-f]{32}$' || fail "rpcauth salt is not 32 hex chars: $salt"
expected=$(printf '%s' "mypass123" | openssl dgst -sha256 -hmac "$salt" -r | cut -d' ' -f1)
[ "$hash" = "$expected" ] || fail "rpcauth hash does not verify against the password"

# 9. .env is private
# shellcheck disable=SC2012 # fixed filename, ls is the portable way to read the mode string
perms=$(ls -l "$tmp/.env" | cut -c1-10)
[ "$perms" = "-rw-------" ] || fail ".env permissions are $perms, expected -rw-------"

# 10. Custom values are honored
cat >"$tmp/config.json" <<'EOF'
{"bitcoin": {"node_username": "u", "node_password": "longrpcpassword1234567890", "data_dir": "./elsewhere", "dbcache_mb": 512, "prune_mb": 550, "inbound_onion": true, "rpc_expose": true, "rpc_bind_ip": "192.168.1.118", "rpc_port": 18332},
 "dashboard": {"password": "dashpassword12", "onion": true, "bind_ip": "192.168.1.117", "launch_at_boot": true},
 "notifications": {"telegram_bot_token": "123:abc-DEF", "telegram_chat_id": "-10042", "healthchecks_url": "https://hc-ping.com/uuid", "alert_new_block": true, "alert_new_block_monero": true, "alert_new_block_tari": true, "alert_new_block_litecoin": true, "alert_new_block_dogecoin": true, "alert_new_block_bitcoincash": true, "alert_new_block_zcash": true},
 "wallets": [{"name": "Cold storage", "key": "34xp4vRoCGJym3xR7yCVPFHoCNxv4Twseo", "birthday": "2021-01-01"}]}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_DATA_DIR=./elsewhere$' "$tmp/.env" || fail "custom data_dir not rendered"
grep -q '^BITCOIN_DBCACHE=512$' "$tmp/.env" || fail "custom dbcache not rendered"
grep -q '^BITCOIN_PRUNE=550$' "$tmp/.env" || fail "custom prune not rendered"
grep -q '^BITCOIN_INBOUND_ONION=1$' "$tmp/.env" || fail "inbound_onion=true not rendered as 1"
grep -q '^BITCOIN_RPC_EXPOSE=1$' "$tmp/.env" || fail "rpc_expose=true not rendered as 1"
grep -q '^BITCOIN_RPC_BIND_IP=192.168.1.118$' "$tmp/.env" || fail "RPC LAN bind not rendered"
grep -q '^BITCOIN_RPC_PORT=18332$' "$tmp/.env" || fail "RPC host port not rendered"
grep -q '^COMPOSE_FILE=docker-compose.yml:docker-compose.rpc.yml$' "$tmp/.env" || fail "RPC compose override not enabled"
grep -q '^DASHBOARD_PASSWORD=dashpassword12$' "$tmp/.env" || fail "dashboard password not rendered"
grep -q '^DASHBOARD_ONION=1$' "$tmp/.env" || fail "dashboard onion not rendered"
grep -q '^DASHBOARD_BIND_IP=192.168.1.117$' "$tmp/.env" || fail "dashboard LAN bind not rendered"
grep -q '^STACK_RESTART_POLICY=unless-stopped$' "$tmp/.env" || fail "launch at boot was not rendered"
grep -q '^TELEGRAM_BOT_TOKEN=123:abc-DEF$' "$tmp/.env" || fail "telegram token not rendered"
grep -q '^TELEGRAM_CHAT_ID=-10042$' "$tmp/.env" || fail "telegram chat id not rendered"
grep -q '^HEALTHCHECKS_URL=https://hc-ping.com/uuid$' "$tmp/.env" || fail "healthchecks url not rendered"
grep -q '^ALERT_NEW_BLOCK=1$' "$tmp/.env" || fail "new-block alert not rendered"
grep -q '^ALERT_NEW_BLOCK_MONERO=1$' "$tmp/.env" || fail "Monero new-block alert not rendered"
for chain in TARI LITECOIN DOGECOIN BITCOINCASH ZCASH; do
  grep -q "^ALERT_NEW_BLOCK_${chain}=1$" "$tmp/.env" || fail "${chain} new-block alert not rendered"
done
# wallets round-trip through the base64 blob (decode and check the name survived)
watch_b64=$(sed -n 's/^WATCH_WALLETS_B64=//p' "$tmp/.env")
printf '%s' "$watch_b64" | openssl base64 -d -A | grep -q '"name":"Cold storage"' ||
  fail "wallets not rendered into WATCH_WALLETS_B64"
[ -d "$tmp/elsewhere" ] || fail "custom data dir not created"

# 11. Rejects env-file-unsafe notification values and non-URL healthchecks
cat >"$tmp/config.json" <<'EOF'
{"bitcoin": {"node_username": "u", "node_password": "p"},
 "notifications": {"telegram_bot_token": "has space"}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "telegram token with a space was accepted"
cat >"$tmp/config.json" <<'EOF'
{"bitcoin": {"node_username": "u", "node_password": "p"},
 "notifications": {"healthchecks_url": "hc-ping.com/uuid"}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "non-URL healthchecks_url was accepted"
cat >"$tmp/config.json" <<'EOF'
{"notifications": {"healthchecks_url": "http://hc-ping.com/uuid"}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "HTTP healthchecks_url was accepted"

# blockfilterindex: honored on archival and fresh pruned nodes, rejected when added to an already pruned node
cat >"$tmp/config.json" <<'EOF'
{"bitcoin": {"node_username": "u", "node_password": "p", "prune_mb": 0, "blockfilterindex": true}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_BLOCKFILTERINDEX=1$' "$tmp/.env" || fail "blockfilterindex=true not rendered as 1"
cat >"$tmp/config.json" <<'EOF'
{"bitcoin": {"node_username": "u", "node_password": "p", "prune_mb": 550, "blockfilterindex": true}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 || fail "blockfilterindex was rejected when moving archival to pruned"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 || fail "a pruned node that already has blockfilterindex was rejected on re-run"
cat >"$tmp/config.json" <<'EOF'
{"bitcoin": {"node_username": "u", "node_password": "p", "prune_mb": 550, "blockfilterindex": false}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null
cat >"$tmp/config.json" <<'EOF'
{"bitcoin": {"node_username": "u", "node_password": "p", "prune_mb": 550, "blockfilterindex": true}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "blockfilterindex was accepted on an already pruned node"

# zcash.address_index: off by default, accepted on fresh installs and re-runs, refused on a database that ran without it
zc="$tmp/zcash"
mkdir -p "$zc"
cp configure.sh "$zc/"
zcash_config() { printf '{"dashboard":{"password":"configuretest12"},"zcash":{"enabled":%s,"address_index":%s,"data_dir":"%s"}}\n' "$1" "$2" "$3" >"$zc/config.json"; }
zcash_config true false ./data/zcash
(cd "$zc" && ./configure.sh) >/dev/null || fail "Zcash default configure failed"
grep -q '^ZCASH_ADDRESS_INDEX=0$' "$zc/.env" || fail "address_index did not default to 0"
grep -q '^ZCASH_TOML=zakurad.toml$' "$zc/.env" || fail "default Zcash config file changed"
zcash_config true true ./data/zcash
(cd "$zc" && ./configure.sh) >/dev/null 2>&1 && fail "address_index was accepted on a database that ran without it"
grep -q '^ZCASH_ADDRESS_INDEX=0$' "$zc/.env" || fail "refused address_index changed the existing .env"
zcash_config true true ./data/zcash-fresh
(cd "$zc" && ./configure.sh) >/dev/null 2>&1 || fail "address_index was refused with a fresh data directory"
grep -q '^ZCASH_ADDRESS_INDEX=1$' "$zc/.env" || fail "address_index=true not rendered as 1"
grep -q '^ZCASH_TOML=zakurad-address-index.toml$' "$zc/.env" || fail "address_index did not select its config file"
(cd "$zc" && ./configure.sh) >/dev/null 2>&1 || fail "re-run with address_index already on was refused"
zcash_config true false ./data/zcash-skip
(cd "$zc" && ./configure.sh) >/dev/null || fail "Zcash default configure on a second dir failed"
touch "$zc/data/zcash-skip/synced-db"
zcash_config false false ./data/zcash-skip
(cd "$zc" && ./configure.sh) >/dev/null || fail "Zcash disable failed"
zcash_config true true ./data/zcash-skip
(cd "$zc" && ./configure.sh) >/dev/null 2>&1 && fail "address_index accepted after a disable step on a used database"
rm -f "$zc/.env"
(cd "$zc" && ./configure.sh) >/dev/null 2>&1 && fail "address_index accepted on a used database without a previous .env"
[ -f "$zc/data/zcash-fresh/.address-index" ] || fail "index-mode run left no marker"
rm -rf "$zc"
mkdir -p "$zc"
cp configure.sh "$zc/"
zcash_config false true ./data/zcash
(cd "$zc" && ./configure.sh) >/dev/null 2>&1 || fail "address_index with Zcash disabled was refused"
rm -rf "$zc"

# Legacy false is harmless, but true is rejected before the existing .env changes.
cat >"$tmp/config.json" <<'EOF'
{"bitcoin": {"sync_over_clearnet": true}}
EOF
cp "$tmp/.env" "$tmp/env.before"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "sync_over_clearnet=true was accepted"
cmp -s "$tmp/.env" "$tmp/env.before" || fail "rejected clearnet setting changed .env"
echo '{"bitcoin": {"sync_over_clearnet": false}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_SYNC_OVER_CLEARNET=' "$tmp/.env" && fail "legacy clearnet variable remains in .env"

# Dashboard publication is limited to loopback and RFC1918 IPv4 addresses.
printf 'DASHBOARD_BIND_IP=192.168.50.40\nDASHBOARD_PASSWORD=fixturepassword12\n' >"$tmp/.env"
echo '{"dashboard":{"bind_ip":"auto"}}' >"$tmp/config.json"
(cd "$tmp" && BITCOIN_STACK_DEFAULT_BIND_IP=192.168.50.41 ./configure.sh) >/dev/null
grep -q '^DASHBOARD_BIND_IP=192.168.50.41$' "$tmp/.env" || fail "auto dashboard bind reused a stale address"
(cd "$tmp" && BITCOIN_STACK_DEFAULT_BIND_IP=8.8.8.8 ./configure.sh) >"$tmp/auto-bind.out" 2>&1
grep -q '^DASHBOARD_BIND_IP=127.0.0.1$' "$tmp/.env" || fail "auto dashboard bind did not fall back to loopback"
grep -q 'No private LAN address detected' "$tmp/auto-bind.out" || fail "loopback fallback was not reported"
echo '{"dashboard":{"bind_ip":"192.168.50.42"}}' >"$tmp/config.json"
(cd "$tmp" && BITCOIN_STACK_DEFAULT_BIND_IP=192.168.50.43 ./configure.sh) >/dev/null
grep -q '^DASHBOARD_BIND_IP=192.168.50.42$' "$tmp/.env" || fail "explicit dashboard bind was replaced by auto detection"

for bind_ip in 127.0.0.2 10.1.2.3 172.16.0.1 172.31.255.254 192.168.50.4; do
  jq -n --arg ip "$bind_ip" '{dashboard:{bind_ip:$ip}}' >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null || fail "private dashboard bind $bind_ip was rejected"
done
for bind_ip in 0.0.0.0 8.8.8.8 172.15.0.1 172.32.0.1 192.169.1.1 ::1 127.0.0.1/24 999.1.1.1 010.0.0.1 192.168.001.1; do
  jq -n --arg ip "$bind_ip" '{dashboard:{bind_ip:$ip}}' >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "unsafe dashboard bind $bind_ip was accepted"
done
printf '{"dashboard":{"bind_ip":"127.0.0.1\\nDASHBOARD_PASSWORD=x"}}\n' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "newline dashboard bind was accepted"

echo '{"bitcoin":{"rpc_expose":true}}' >"$tmp/config.json"
cp "$tmp/.env" "$tmp/env.before"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "RPC exposure without a bind address was accepted"
cmp -s "$tmp/.env" "$tmp/env.before" || fail "invalid RPC exposure changed .env"
echo '{"bitcoin":{"node_password":"short","rpc_expose":true,"rpc_bind_ip":"192.168.1.117"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "short password was accepted for exposed RPC"
echo '{"bitcoin":{"node_password":"short"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null || fail "short internal-only RPC password was rejected"
rm -f "$tmp/.env"
for bind_ip in 10.1.2.3 172.16.0.1 172.31.255.254 192.168.50.4; do
  jq -n --arg ip "$bind_ip" '{bitcoin:{rpc_expose:true,rpc_bind_ip:$ip},dashboard:{password:"fixturepassword12"}}' >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null || fail "private RPC bind $bind_ip was rejected"
done
for bind_ip in '' 0.0.0.0 127.0.0.1 8.8.8.8 172.15.0.1 172.32.0.1 192.169.1.1 ::1 999.1.1.1 010.0.0.1; do
  jq -n --arg ip "$bind_ip" '{bitcoin:{rpc_expose:true,rpc_bind_ip:$ip},dashboard:{password:"fixturepassword12"}}' >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "unsafe RPC bind $bind_ip was accepted"
done
for rpc_port in 0 65536 -1 '"8332x"'; do
  printf '{"bitcoin":{"rpc_expose":true,"rpc_bind_ip":"192.168.1.117","rpc_port":%s},"dashboard":{"password":"fixturepassword12"}}\n' "$rpc_port" >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "unsafe RPC port $rpc_port was accepted"
done
jq -n '{bitcoin:{rpc_expose:true,rpc_bind_ip:"192.168.1.117",rpc_port:80},dashboard:{bind_ip:"192.168.1.117",password:"fixturepassword12"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "RPC/dashboard host port conflict was accepted"

# inbound_onion defaults off; explicit true opts in
echo '{"bitcoin": {"inbound_onion": false}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_INBOUND_ONION=0$' "$tmp/.env" || fail "inbound_onion: false should render 0"
echo '{"bitcoin": {"inbound_onion": true}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_INBOUND_ONION=1$' "$tmp/.env" || fail "inbound_onion: true should render 1"

# mem_limit_mb: default 0 -> unlimited; a set value renders as "<n>m"; a value
# not above dbcache warns; non-numeric is rejected
rm -f "$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_MEM_LIMIT=0$' "$tmp/.env" || fail "mem_limit default should render 0 (unlimited)"
echo '{"bitcoin": {"mem_limit_mb": 5000, "dbcache_mb": 3000}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_MEM_LIMIT=5000m$' "$tmp/.env" || fail "mem_limit_mb: 5000 should render 5000m"
echo '{"bitcoin": {"mem_limit_mb": 2000, "dbcache_mb": 3000}}' >"$tmp/config.json"
out=$(cd "$tmp" && ./configure.sh)
echo "$out" | grep -qi "WARNING: mem_limit_mb" || fail "no warning when mem_limit_mb <= dbcache_mb"
echo '{"bitcoin": {"mem_limit_mb": "lots"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "non-numeric mem_limit_mb was accepted"
rm -f "$tmp/config.json"

# hard caps: defaults render 0 (unlimited); set values render; non-numeric cpus rejected
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_CPUS=0$' "$tmp/.env" || fail "bitcoin cpus default should render 0"
grep -q '^MONERO_CPUS=0$' "$tmp/.env" || fail "monero cpus default should render 0"
grep -q '^MONERO_MEM_LIMIT=0$' "$tmp/.env" || fail "monero mem default should render 0"
echo '{"bitcoin": {"cpus": 0.5}, "monero": {"cpus": 1, "mem_limit_mb": 2048}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_CPUS=0.5$' "$tmp/.env" || fail "bitcoin cpus 0.5 should render"
grep -q '^MONERO_CPUS=1$' "$tmp/.env" || fail "monero cpus 1 should render"
grep -q '^MONERO_MEM_LIMIT=2048m$' "$tmp/.env" || fail "monero mem_limit_mb 2048 should render 2048m"
echo '{"bitcoin": {"cpus": "fast"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "non-numeric cpus was accepted"
rm -f "$tmp/config.json"

# family hard caps and host thermal knobs
echo '{"tari": {"cpus": 0.5}, "host": {"thermal_limit_c": 85}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^TARI_CPUS=0.5$' "$tmp/.env" || fail "tari cpus 0.5 should render"
grep -q '^LITECOIN_CPUS=0$' "$tmp/.env" || fail "litecoin cpus default should render 0"
grep -q '^THERMAL_LIMIT_C=85$' "$tmp/.env" || fail "thermal limit should render"
grep -q '^THERMAL_RESUME_C=75$' "$tmp/.env" || fail "thermal resume should default to limit-10"
echo '{"host": {"thermal_limit_c": 85, "thermal_resume_c": 90}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "thermal resume above limit was accepted"
echo '{"host": {"thermal_limit_c": 300}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "thermal limit 300 was accepted"
echo '{"host": {"thermal_limit_c": 50, "thermal_resume_c": 1}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "thermal resume 1 was accepted"
echo '{"monero": {"mem_limit_mb": 5}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "monero mem_limit_mb 5 was accepted"
rm -f "$tmp/config.json"

# 12. Passwordless and mixed protected ingress dashboards are rejected.
warn="$tmp/warn"
mkdir "$warn"
cp configure.sh "$warn/"
printf 'BITCOIN_RPC_USER=u\nBITCOIN_RPC_PASSWORD=p\nDASHBOARD_PASSWORD=\n' >"$warn/.env"
cat >"$warn/config.json" <<'EOF'
{"dashboard": {"onion": true}}
EOF
if out=$(cd "$warn" && ./configure.sh 2>&1); then
  fail "passwordless onion dashboard was accepted"
fi
echo "$out" | grep -q "dashboard.password must be 12 to 128" || fail "passwordless onion error is not actionable"
printf '%s\n' '{"dashboard":{"password":"longenough12","onion":true,"public_url":"https://node.example.com"}}' >"$warn/config.json"
if out=$(cd "$warn" && ./configure.sh 2>&1); then
  fail "dashboard onion and public URL were accepted together"
fi
echo "$out" | grep -q "mutually exclusive" || fail "mixed protected ingress error is not actionable"

# 13. Zcash renders as a selectable Tor-only node with an authenticated LAN proxy.
multi="$tmp/multi"
mkdir "$multi"
cp configure.sh VERSION "$multi/"
cat >"$multi/config.json" <<'EOF'
{
  "bitcoin": {"enabled": false},
  "dashboard": {"password": "multiconfig12"},
  "zcash": {
    "enabled": true,
    "node_password": "abcdefghijklmnopqrstuvwx",
    "rpc_expose": true,
    "rpc_bind_ip": "192.168.1.44",
    "rpc_port": 28232,
    "resource_profile": "compact"
  }
}
EOF
(cd "$multi" && ./configure.sh) >/dev/null
grep -q '^COMPOSE_PROFILES=zcash$' "$multi/.env" || fail "Zcash profile was not selected"
grep -q '^COMPOSE_FILE=docker-compose.yml:docker-compose.zcash-rpc.yml$' "$multi/.env" || fail "Zcash RPC proxy override was not selected"
grep -q '^ZCASH_RPC_URL=http://172.29.0.36:8232$' "$multi/.env" || fail "Zcash internal RPC URL changed"
grep -q '^ZCASH_STEADY_MEM_LIMIT_MB=4096$' "$multi/.env" || fail "Zcash post-sync memory budget changed"
grep -q '^ZCASH_DATA_DIR=./data/zcash$' "$multi/.env" || fail "Zcash data path default changed"

cat >"$multi/config.json" <<'EOF'
{"bitcoin":{"enabled":false},"tari":{"enabled":true,"grpc_expose":false},"dashboard":{"password":"multiconfig12"}}
EOF
(cd "$multi" && ./configure.sh) >/dev/null
grep -q '^COMPOSE_FILE=docker-compose.yml:docker-compose.tari-wallet.yml$' "$multi/.env" ||
  fail "Tari did not select its private dashboard control mount"

# 14. Version string: release number for a clean tagged checkout, branch-commit
#     for a dev build (drives the dashboard's version display).
if command -v git >/dev/null; then
  gtmp=$(mktemp -d)
  cp configure.sh "$gtmp/"
  printf '9.9.9\n' >"$gtmp/VERSION"
  printf '%s\n' '{"dashboard":{"password":"taggedconfig12"}}' >"$gtmp/config.json"
  (
    cd "$gtmp"
    git init -q && git config user.email t@t && git config user.name t
    git add -A && git commit -qm init
    git tag -a v9.9.9 -m "Release v9.9.9" # annotated, exactly as scripts/release.sh does
    ./configure.sh >/dev/null
    grep -q '^STACK_VERSION=9.9.9$' .env || {
      echo "FAIL: tagged checkout should show release number"
      exit 1
    }
    # detached HEAD at the tag (how a box deploys: git checkout v9.9.9) is still official
    git checkout -q v9.9.9
    ./configure.sh >/dev/null
    grep -q '^STACK_VERSION=9.9.9$' .env || {
      echo "FAIL: detached checkout of the tag should show release number"
      exit 1
    }
    git checkout -qb feature && git commit -q --allow-empty -m more
    ./configure.sh >/dev/null
    grep -qE '^STACK_VERSION=feature-[0-9a-f]{7,}$' .env || {
      echo "FAIL: dev build should show branch-commit"
      exit 1
    }
    # a slashed branch name must NOT produce a slash (invalid Docker image tag,
    # since STACK_VERSION is the compose image tag)
    git checkout -qb feat/thing && git commit -q --allow-empty -m more
    ./configure.sh >/dev/null
    grep -qE '^STACK_VERSION=feat-thing-[0-9a-f]{7,}$' .env || {
      echo "FAIL: slashed branch should render as feat-thing-<hash>, got: $(grep '^STACK_VERSION=' .env)"
      exit 1
    }
  ) || exit 1
  rm -rf "$gtmp"
fi

# The zero-config .env must render a valid compose config (what `./stack up` runs)
if command -v docker >/dev/null; then
  rm -f "$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null
  docker compose --env-file "$tmp/.env" config >/dev/null 2>&1 ||
    fail "zero-config .env does not render a valid compose config"
  echo '{"bitcoin":{"node_password":"longrpcpassword1234567890","rpc_expose":true,"rpc_bind_ip":"192.168.1.117","rpc_port":18332}}' >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null
  rpc_rendered=$(docker compose --env-file "$tmp/.env" config)
  echo "$rpc_rendered" | grep -A5 'target: 8332' | grep -q 'published: "18332"' ||
    fail "rendered COMPOSE_FILE did not select the RPC override"
fi

# Monero is opt-in, pruned, and its LAN RPC/ZMQ relays require private binds.
cat >"$tmp/config.json" <<'EOF'
{"bitcoin":{"enabled":false,"rpc_expose":true,"rpc_bind_ip":"192.168.1.118"},"monero":{"enabled":false,"rpc_expose":true,"rpc_bind_ip":"192.168.1.118","zmq_expose":true,"zmq_bind_ip":"192.168.1.118"},"tari":{"enabled":true}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^COMPOSE_FILE=docker-compose.yml:docker-compose.tari-wallet.yml$' "$tmp/.env" || fail "disabled nodes retained stale LAN relay overrides"
grep -q '^BITCOIN_RPC_EXPOSE=0$' "$tmp/.env" || fail "disabled Bitcoin retained stale RPC exposure"
grep -q '^MONERO_RPC_EXPOSE=0$' "$tmp/.env" || fail "disabled Monero retained stale RPC exposure"
grep -q '^MONERO_ZMQ_EXPOSE=0$' "$tmp/.env" || fail "disabled Monero retained stale ZMQ exposure"
grep -q '^TARI_WALLET_ENABLED=1$' "$tmp/.env" || fail "Tari wallet integration did not follow Tari enablement"

cat >"$tmp/config.json" <<'EOF'
{"bitcoin":{"enabled":false},"monero":{"enabled":true,"node_username":"moneroadmin","node_password":"moneropassword1234567890123456","data_dir":"./monero-chain","rpc_expose":true,"rpc_bind_ip":"192.168.1.118","rpc_port":28081,"zmq_expose":true,"zmq_bind_ip":"192.168.1.118","zmq_port":28083}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^COMPOSE_PROFILES=monero$' "$tmp/.env" || fail "Monero-only selection did not disable Bitcoin"
grep -q '^MONERO_ENABLED=1$' "$tmp/.env" || fail "Monero enablement not rendered"
grep -q '^MONERO_RPC_USER=moneroadmin$' "$tmp/.env" || fail "separate Monero RPC user not rendered"
grep -q '^MONERO_RPC_PASSWORD=moneropassword1234567890123456$' "$tmp/.env" || fail "separate Monero RPC password not rendered"
grep -q '^MONERO_DATA_DIR=./monero-chain$' "$tmp/.env" || fail "Monero data path not rendered"
grep -q '^COMPOSE_FILE=docker-compose.yml:docker-compose.monero-rpc.yml:docker-compose.monero-zmq.yml$' "$tmp/.env" ||
  fail "Monero LAN overrides not selected"
[ -d "$tmp/monero-chain" ] || fail "Monero data directory not created"

for field in rpc zmq; do
  jq -n --arg field "$field" '{monero:{enabled:true,node_password:"longmoneropassword123456789",($field+"_expose"):true}}' >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "Monero $field exposure without a bind address was accepted"
done
echo '{"monero":{"enabled":true,"node_password":"short","rpc_expose":true,"rpc_bind_ip":"192.168.1.118"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "short exposed Monero RPC password was accepted"

cat >"$tmp/config.json" <<'EOF'
{"bitcoin":{"enabled":true,"node_password":"longbitcoinpassword123456789","rpc_expose":true,"rpc_bind_ip":"192.168.1.118","rpc_port":28081},"monero":{"enabled":true,"node_password":"longmoneropassword123456789","rpc_expose":true,"rpc_bind_ip":"192.168.1.118","rpc_port":28081}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "Bitcoin and Monero RPC host port collision was accepted"
cat >"$tmp/config.json" <<'EOF'
{"bitcoin":{"enabled":true,"node_password":"longbitcoinpassword123456789","rpc_expose":true,"rpc_bind_ip":"192.168.1.118","rpc_port":28083},"monero":{"enabled":true,"node_password":"longmoneropassword123456789","zmq_expose":true,"zmq_bind_ip":"192.168.1.118","zmq_port":28083}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "Bitcoin RPC and Monero ZMQ host port collision was accepted"
cat >"$tmp/config.json" <<'EOF'
{"bitcoin":{"enabled":true,"node_password":"longbitcoinpassword123456789","rpc_expose":true,"rpc_bind_ip":"192.168.1.118","rpc_port":28081},"monero":{"enabled":true,"node_password":"longmoneropassword123456789","rpc_expose":true,"rpc_bind_ip":"192.168.1.119","rpc_port":28081}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null || fail "same cross-chain host port on different bind addresses was rejected"

for field in bitcoin monero; do
  for port in 080 0008332 18446744073709551617; do
    jq -n --arg field "$field" --arg port "$port" '{($field):{rpc_port:$port}}' >"$tmp/config.json"
    (cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "$field accepted an ambiguous or overflowing RPC port"
  done
done

echo '{"bitcoin":{"resource_profile":"compact"},"monero":{"resource_profile":"compact"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_RESOURCE_PROFILE=compact$' "$tmp/.env" || fail "Bitcoin compact resource profile not rendered"
grep -q '^MONERO_RESOURCE_PROFILE=compact$' "$tmp/.env" || fail "Monero compact resource profile not rendered"
cat >"$tmp/config.json" <<'EOF'
{"bitcoin":{"resource_profile":"compact","steady_cpus":0.5,"steady_mem_limit_mb":1536},"monero":{"resource_profile":"compact","steady_cpus":1.5,"steady_mem_limit_mb":3072}}
EOF
(cd "$tmp" && ./configure.sh) >/dev/null
grep -q '^BITCOIN_STEADY_CPUS=0.5$' "$tmp/.env" || fail "custom Bitcoin steady CPU limit not rendered"
grep -q '^BITCOIN_STEADY_MEM_LIMIT_MB=1536$' "$tmp/.env" || fail "custom Bitcoin steady memory limit not rendered"
grep -q '^MONERO_STEADY_CPUS=1.5$' "$tmp/.env" || fail "custom Monero steady CPU limit not rendered"
grep -q '^MONERO_STEADY_MEM_LIMIT_MB=3072$' "$tmp/.env" || fail "custom Monero steady memory limit not rendered"
for invalid in \
  '{"bitcoin":{"steady_cpus":0}}' \
  '{"bitcoin":{"steady_cpus":0.001}}' \
  '{"bitcoin":{"steady_cpus":1.234}}' \
  '{"bitcoin":{"steady_cpus":"1"}}' \
  '{"bitcoin":{"steady_cpus":1.2.3}}' \
  '{"monero":{"steady_mem_limit_mb":256}}' \
  '{"monero":{"steady_mem_limit_mb":512.5}}' \
  '{"monero":{"steady_mem_limit_mb":"2048"}}'; do
  printf '%s\n' "$invalid" >"$tmp/config.json"
  (cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "invalid steady resource limit was accepted: $invalid"
done
echo '{"bitcoin":{"resource_profile":"tiny"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "unknown Bitcoin resource profile was accepted"
echo '{"monero":{"resource_profile":"tiny"}}' >"$tmp/config.json"
(cd "$tmp" && ./configure.sh) >/dev/null 2>&1 && fail "unknown Monero resource profile was accepted"

# Package-managed runs must survive a missing BITCOIN_STACK_DATA_DIR, as the
# systemd control helpers once ran without it and crashed under set -u.
pkg_tmp=$(mktemp -d)
printf '%s\n' '{"dashboard":{"password":"configuretest12"}}' >"$pkg_tmp/config.json"
cp configure.sh "$pkg_tmp/"
(cd "$pkg_tmp" && env -u BITCOIN_STACK_DATA_DIR BITCOIN_STACK_PACKAGE_MANAGED=1 ./configure.sh) >/dev/null ||
  fail "package-managed configure failed without BITCOIN_STACK_DATA_DIR"
rm -rf "$pkg_tmp"

# A data dir that cannot be created (e.g. outside a sandboxed helper's
# writable paths) must not abort configure; the dir already exists on the host.
sandbox_tmp=$(mktemp -d)
printf '%s\n' '{"dashboard":{"password":"configuretest12"},"bitcoin":{"data_dir":"/nonexistent-root-probe/bitcoin"}}' >"$sandbox_tmp/config.json"
cp configure.sh "$sandbox_tmp/"
(cd "$sandbox_tmp" && ./configure.sh) >/dev/null ||
  fail "configure aborted on an uncreatable data dir"
rm -rf "$sandbox_tmp"

echo "PASS: test_configure.sh"
