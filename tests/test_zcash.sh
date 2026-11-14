#!/bin/sh
set -eu
root="$(cd -- "$(dirname "$0")/.." && pwd)"
config="$root/build/zcash/zakurad.toml"
healthcheck="$root/build/zcash/healthcheck.sh"
fake_path="$root/tests/fixtures/zcash"
compose="$root/docker-compose.yml"

grep -q '^\[state.storage_mode.pruned\]$' "$config"
grep -q '^tx_retention = 10000$' "$config"
grep -q '^cache_dir = false$' "$config"
grep -q '^p2p_stack = "legacy"$' "$config"
grep -q '^listen_addr = "127.0.0.1:8233"$' "$config"
grep -q '^enable_cookie_auth = true$' "$config"

for peer in 18233 18234 18235 18236; do
  grep -q "172.29.0.25:$peer" "$config"
done

if grep -Eq 'dnsseed|seeder\.' "$config"; then
  echo "Zcash daemon config must not contain public DNS seeds" >&2
  exit 1
fi

grep -q 'getblockchaininfo' "$healthcheck"
[ -x "$healthcheck" ]
[ -r "$config" ]

zcash_service="$(awk '
  /^  zcash:$/ { inside = 1; print; next }
  inside && /^  [[:alnum:]_-]+:$/ { exit }
  inside { print }
' "$compose")"
printf '%s\n' "$zcash_service" | grep -q '172.29.0.36'
printf '%s\n' "$zcash_service" | grep -q 'DAC_READ_SEARCH'
if printf '%s\n' "$zcash_service" | grep -Eq '^    (ports|user):'; then
  echo "Zcash must not publish its native RPC or bypass the image privilege drop" >&2
  exit 1
fi
if printf '%s\n' "$zcash_service" | grep -Eq '^      (UID|GID):'; then
  echo "Zcash must use the account shipped by its image" >&2
  exit 1
fi

index_config="$root/build/zcash/zakurad-address-index.toml"
grep -q '^checkpoint_sync = false$' "$index_config"
grep -q '^\[state.storage_mode.pruned\]$' "$index_config"
[ "$(grep -v '^checkpoint_sync' "$config")" = "$(grep -v '^checkpoint_sync' "$index_config")" ] || {
  echo "Address-index config must differ from zakurad.toml only in checkpoint_sync" >&2
  exit 1
}

cookie_dir="$(mktemp -d)"
trap 'rm -rf "$cookie_dir"' EXIT
cookie='__cookie__:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='
printf '%s' "$cookie" >"$cookie_dir/.cookie"
chmod 600 "$cookie_dir/.cookie"

ZCASH_COOKIE_FILE="$cookie_dir/.cookie" ZCASH_COOKIE_OWNER_UID="$(id -u)" \
  ZCASH_DASHBOARD_GID="$(id -g)" FAKE_CURL_EXPECT_USER="$cookie" \
  FAKE_CURL_RESPONSE='{"jsonrpc":"2.0","result":{"blocks":1},"id":"health"}' \
  PATH="$fake_path:/usr/bin:/bin" "$healthcheck"
cookie_mode="$(stat -c '%a' "$cookie_dir/.cookie" 2>/dev/null || stat -f '%Lp' "$cookie_dir/.cookie")"
[ "$cookie_mode" = 640 ]
directory_mode="$(stat -c '%a' "$cookie_dir" 2>/dev/null || stat -f '%Lp' "$cookie_dir")"
[ "$directory_mode" = 750 ]

if ZCASH_COOKIE_FILE="$cookie_dir/missing" ZCASH_COOKIE_OWNER_UID="$(id -u)" \
  ZCASH_DASHBOARD_GID="$(id -g)" PATH="$fake_path:/usr/bin:/bin" "$healthcheck"; then
  echo "Zcash healthcheck accepted a missing RPC cookie" >&2
  exit 1
fi

printf '%s' '__cookie__:short' >"$cookie_dir/.cookie"
if ZCASH_COOKIE_FILE="$cookie_dir/.cookie" ZCASH_COOKIE_OWNER_UID="$(id -u)" \
  ZCASH_DASHBOARD_GID="$(id -g)" PATH="$fake_path:/usr/bin:/bin" "$healthcheck"; then
  echo "Zcash healthcheck accepted a malformed RPC cookie" >&2
  exit 1
fi
printf '%s' "$cookie" >"$cookie_dir/.cookie"

if ZCASH_COOKIE_FILE="$cookie_dir/.cookie" ZCASH_COOKIE_OWNER_UID="$(id -u)" \
  ZCASH_DASHBOARD_GID="$(id -g)" FAKE_CURL_EXPECT_USER="$cookie" \
  FAKE_CURL_RESPONSE='{"jsonrpc":"2.0","result":null,"error":{"code":-1}}' \
  PATH="$fake_path:/usr/bin:/bin" "$healthcheck"; then
  echo "Zcash healthcheck accepted an RPC error" >&2
  exit 1
fi

if ZCASH_COOKIE_FILE="$cookie_dir/.cookie" ZCASH_COOKIE_OWNER_UID="$(id -u)" \
  ZCASH_DASHBOARD_GID="$(id -g)" FAKE_CURL_EXPECT_USER="$cookie" \
  FAKE_CURL_EXIT=22 PATH="$fake_path:/usr/bin:/bin" "$healthcheck"; then
  echo "Zcash healthcheck ignored a transport error" >&2
  exit 1
fi
