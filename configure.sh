#!/usr/bin/env bash
# Render private runtime settings after init has configured authentication.
set -euo pipefail
stack_root=${BITCOIN_STACK_ROOT:-$(cd "$(dirname "$0")" && pwd)}
asset_root=${BITCOIN_STACK_ASSET_ROOT:-$stack_root}
state_dir=${BITCOIN_STACK_STATE_DIR:-$stack_root}
config_dir=${BITCOIN_STACK_CONFIG_DIR:-$state_dir}
config_file=$config_dir/config.json
env_file=$state_dir/.env
stack_command=${BITCOIN_STACK_COMMAND:-./stack}
package_managed=${BITCOIN_STACK_PACKAGE_MANAGED:-0}
BITCOIN_STACK_DATA_DIR=${BITCOIN_STACK_DATA_DIR:-}
umask 077
mkdir -p "$state_dir" "$config_dir"
if [ "${BITCOIN_STACK_CONFIG_LOCK_HELD:-0}" != 1 ]; then
  exec 8>"$state_dir/.config.lock"
  flock 8
fi
cd "$state_dir"

for tool in jq openssl; do
  command -v "$tool" >/dev/null || {
    echo "$tool is required; install it with your host package manager."
    exit 1
  }
done

config='{}'
[ -f "$config_file" ] && config=$(cat "$config_file")

get() { jq -r "$1 // \"${2:-}\"" <<<"$config"; }
flag() { jq -r "if $1 == true then 1 else 0 end" <<<"$config"; }

private_ipv4() {
  local ip=$1 a b c d
  [[ "$ip" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]] || return 1
  IFS=. read -r a b c d <<<"$ip"
  ((10#$a == 10 || (10#$a == 172 && 10#$b >= 16 && 10#$b <= 31) || (10#$a == 192 && 10#$b == 168)))
}

detect_dashboard_bind_ip() {
  local interface ip
  if [ -n "${BITCOIN_STACK_DEFAULT_BIND_IP:-}" ]; then
    if private_ipv4 "$BITCOIN_STACK_DEFAULT_BIND_IP"; then
      printf '%s\n' "$BITCOIN_STACK_DEFAULT_BIND_IP"
    else
      echo "No private LAN address detected; dashboard will listen on 127.0.0.1." >&2
      printf '%s\n' 127.0.0.1
    fi
    return
  fi
  case "$(uname -s)" in
    Darwin)
      interface=$(route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}')
      case "$interface" in '' | lo* | utun* | bridge* | vmnet* | docker*) interface= ;; esac
      [ -z "$interface" ] || ip=$(ipconfig getifaddr "$interface" 2>/dev/null || true)
      ;;
    Linux)
      interface=$(ip -4 route show default 2>/dev/null | awk '/^default /{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1); exit}}')
      case "$interface" in '' | lo | docker* | veth* | virbr* | br-*) interface= ;; esac
      [ -z "$interface" ] || ip=$(ip -o -4 addr show dev "$interface" scope global 2>/dev/null | awk '{sub(/\/.*/,"",$4); print $4; exit}')
      ;;
  esac
  if private_ipv4 "${ip:-}"; then
    printf '%s\n' "$ip"
  else
    echo "No private LAN address detected; dashboard will listen on 127.0.0.1." >&2
    printf '%s\n' 127.0.0.1
  fi
}

# Fresh installs generate separate passwords; upgrades reuse each existing value.
rpc_user=$(get '.bitcoin.node_username')
rpc_password=$(get '.bitcoin.node_password')
bitcoin_enabled=$(jq -r 'if ((.bitcoin // {}) | has("enabled")) then if .bitcoin.enabled == true then 1 else 0 end else empty end' <<<"$config")
monero_enabled=$(jq -r 'if ((.monero // {}) | has("enabled")) then if .monero.enabled == true then 1 else 0 end else empty end' <<<"$config")
bitcoin_active=$(jq -r 'if ((.bitcoin // {}) | has("active")) then if .bitcoin.active == true then 1 else 0 end else 1 end' <<<"$config")
monero_active=$(jq -r 'if ((.monero // {}) | has("active")) then if .monero.active == true then 1 else 0 end else 1 end' <<<"$config")
tari_enabled=$(flag '.tari.enabled')
tari_active=$(jq -r 'if ((.tari // {}) | has("active")) then if .tari.active == true then 1 else 0 end else 1 end' <<<"$config")
litecoin_enabled=$(flag '.litecoin.enabled')
litecoin_active=$(jq -r 'if ((.litecoin // {}) | has("active")) then if .litecoin.active == true then 1 else 0 end else 1 end' <<<"$config")
dogecoin_enabled=$(flag '.dogecoin.enabled')
dogecoin_active=$(jq -r 'if ((.dogecoin // {}) | has("active")) then if .dogecoin.active == true then 1 else 0 end else 1 end' <<<"$config")
bitcoincash_enabled=$(flag '.bitcoincash.enabled')
bitcoincash_active=$(jq -r 'if ((.bitcoincash // {}) | has("active")) then if .bitcoincash.active == true then 1 else 0 end else 1 end' <<<"$config")
zcash_enabled=$(flag '.zcash.enabled')
zcash_active=$(jq -r 'if ((.zcash // {}) | has("active")) then if .zcash.active == true then 1 else 0 end else 1 end' <<<"$config")
monero_rpc_user=$(get '.monero.node_username')
monero_rpc_password=$(get '.monero.node_password')
litecoin_rpc_user=$(get '.litecoin.node_username' 'admin')
litecoin_rpc_password=$(get '.litecoin.node_password')
dogecoin_rpc_user=$(get '.dogecoin.node_username' 'admin')
dogecoin_rpc_password=$(get '.dogecoin.node_password')
bitcoincash_rpc_user=$(get '.bitcoincash.node_username' 'admin')
bitcoincash_rpc_password=$(get '.bitcoincash.node_password')
zcash_rpc_user=$(get '.zcash.node_username' 'admin')
zcash_rpc_password=$(get '.zcash.node_password')
monero_wallet_rpc_user=$(sed -n 's/^MONERO_WALLET_RPC_USER=//p' "$env_file" 2>/dev/null || true)
monero_wallet_rpc_password=$(sed -n 's/^MONERO_WALLET_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
monero_wallet_password=$(sed -n 's/^MONERO_WALLET_PASSWORD=//p' "$env_file" 2>/dev/null || true)
tari_wallet_rpc_user=$(sed -n 's/^TARI_WALLET_RPC_USER=//p' "$env_file" 2>/dev/null || true)
tari_wallet_rpc_password=$(sed -n 's/^TARI_WALLET_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
tari_wallet_password=$(sed -n 's/^TARI_WALLET_PASSWORD=//p' "$env_file" 2>/dev/null || true)
dashboard_password=$(get '.dashboard.password')
dashboard_password_set=$(jq -r 'if ((.dashboard // {}) | has("password")) then 1 else 0 end' <<<"$config")
launch_at_boot=$(jq -r 'if ((.dashboard // {}) | has("launch_at_boot")) then if .dashboard.launch_at_boot == true then "true" else "false" end else "missing" end' <<<"$config")
had_env=0
[ -f "$env_file" ] && had_env=1
restart_policy=on-failure:5
if [ "$launch_at_boot" = true ]; then
  restart_policy=unless-stopped
elif [ "$launch_at_boot" = false ]; then
  restart_policy=on-failure:5
elif [ "$had_env" = 1 ]; then
  saved_restart_policy=$(sed -n 's/^STACK_RESTART_POLICY=//p' "$env_file" 2>/dev/null || true)
  case "$saved_restart_policy" in
    no | on-failure:5 | unless-stopped) restart_policy=$saved_restart_policy ;;
    *) restart_policy=unless-stopped ;;
  esac
fi
[ -n "$rpc_user" ] || rpc_user=$(sed -n 's/^BITCOIN_RPC_USER=//p' "$env_file" 2>/dev/null || true)
[ -n "$rpc_password" ] || rpc_password=$(sed -n 's/^BITCOIN_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
[ -n "$bitcoin_enabled" ] || bitcoin_enabled=$(sed -n 's/^BITCOIN_ENABLED=//p' "$env_file" 2>/dev/null || true)
[ -n "$monero_enabled" ] || monero_enabled=$(sed -n 's/^MONERO_ENABLED=//p' "$env_file" 2>/dev/null || true)
[ -n "$monero_rpc_user" ] || monero_rpc_user=$(sed -n 's/^MONERO_RPC_USER=//p' "$env_file" 2>/dev/null || true)
[ -n "$monero_rpc_password" ] || monero_rpc_password=$(sed -n 's/^MONERO_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
[ -n "$litecoin_rpc_password" ] || litecoin_rpc_password=$(sed -n 's/^LITECOIN_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
[ -n "$dogecoin_rpc_password" ] || dogecoin_rpc_password=$(sed -n 's/^DOGECOIN_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
[ -n "$bitcoincash_rpc_password" ] || bitcoincash_rpc_password=$(sed -n 's/^BITCOINCASH_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
[ -n "$zcash_rpc_password" ] || zcash_rpc_password=$(sed -n 's/^ZCASH_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
[ "$dashboard_password_set" = "1" ] || dashboard_password=$(sed -n 's/^DASHBOARD_PASSWORD=//p' "$env_file" 2>/dev/null || true)
[ -n "$bitcoin_enabled" ] || bitcoin_enabled=1
[ -n "$monero_enabled" ] || monero_enabled=0
[ -n "$rpc_user" ] || rpc_user="admin"
[ -n "$monero_rpc_user" ] || monero_rpc_user="admin"
[ -n "$monero_wallet_rpc_user" ] || monero_wallet_rpc_user="admin"
[ -n "$tari_wallet_rpc_user" ] || tari_wallet_rpc_user="admin"
[ -n "$rpc_password" ] || rpc_password=$(openssl rand -hex 24)
[ -n "$monero_rpc_password" ] || monero_rpc_password=$(openssl rand -hex 24)
[ -n "$litecoin_rpc_password" ] || litecoin_rpc_password=$(openssl rand -hex 24)
[ -n "$dogecoin_rpc_password" ] || dogecoin_rpc_password=$(openssl rand -hex 24)
[ -n "$bitcoincash_rpc_password" ] || bitcoincash_rpc_password=$(openssl rand -hex 24)
[ -n "$zcash_rpc_password" ] || zcash_rpc_password=$(openssl rand -hex 24)
[ -n "$monero_wallet_rpc_password" ] || monero_wallet_rpc_password=$(openssl rand -hex 24)
[ -n "$monero_wallet_password" ] || monero_wallet_password=$(openssl rand -hex 24)
[ -n "$tari_wallet_rpc_password" ] || tari_wallet_rpc_password=$(openssl rand -hex 24)
[ -n "$tari_wallet_password" ] || tari_wallet_password=$(openssl rand -hex 24)
data_dir=$(get '.bitcoin.data_dir')
if [ -z "$data_dir" ]; then
  if [ -n "${BITCOIN_STACK_DATA_DIR:-}" ]; then
    data_dir=$BITCOIN_STACK_DATA_DIR/bitcoin
  else
    data_dir=./data/bitcoin
  fi
fi
monero_data_dir=$(get '.monero.data_dir')
if [ -z "$monero_data_dir" ]; then
  if [ -n "${BITCOIN_STACK_DATA_DIR:-}" ]; then
    monero_data_dir=$BITCOIN_STACK_DATA_DIR/monero
  else
    monero_data_dir=./data/monero
  fi
fi
node_data_dir() {
  local node=$1 configured
  configured=$(jq -r --arg node "$node" '.[$node].data_dir // empty' <<<"$config")
  if [ -n "$configured" ]; then printf '%s\n' "$configured"
  elif [ -n "${BITCOIN_STACK_DATA_DIR:-}" ]; then printf '%s/%s\n' "$BITCOIN_STACK_DATA_DIR" "$node"
  else printf './data/%s\n' "$node"
  fi
}
tari_data_dir=$(node_data_dir tari)
litecoin_data_dir=$(node_data_dir litecoin)
dogecoin_data_dir=$(node_data_dir dogecoin)
bitcoincash_data_dir=$(node_data_dir bitcoincash)
zcash_data_dir=$(node_data_dir zcash)
dbcache=$(jq -r '.bitcoin.dbcache_mb // empty' <<<"$config")
[ -n "$dbcache" ] || {
  dbcache=$(sed -n 's/^BITCOIN_DBCACHE=//p' "$env_file" 2>/dev/null || true)
  [ -n "$dbcache" ] || [ ! -f "$env_file" ] || dbcache=3000
}
[ -n "$dbcache" ] || dbcache=128
prune=$(jq -r '.bitcoin.prune_mb // empty' <<<"$config")
[ -n "$prune" ] || {
  prune=$(sed -n 's/^BITCOIN_PRUNE=//p' "$env_file" 2>/dev/null || true)
  [ -n "$prune" ] || [ ! -f "$env_file" ] || prune=0
}
[ -n "$prune" ] || prune=10000
mem_limit_mb=$(jq -r '.bitcoin.mem_limit_mb // 0' <<<"$config")
inbound_onion=$(jq -r 'if ((.bitcoin // {}) | has("inbound_onion")) then if .bitcoin.inbound_onion == true then 1 else 0 end else empty end' <<<"$config")
[ -n "$inbound_onion" ] || inbound_onion=$(sed -n 's/^BITCOIN_INBOUND_ONION=//p' "$env_file" 2>/dev/null || true)
[ -n "$inbound_onion" ] || inbound_onion=0
blockfilterindex=$(flag '.bitcoin.blockfilterindex')
rpc_expose=$(flag '.bitcoin.rpc_expose')
rpc_bind_ip=$(get '.bitcoin.rpc_bind_ip')
rpc_port=$(jq -r '.bitcoin.rpc_port // 8332' <<<"$config")
monero_rpc_expose=$(flag '.monero.rpc_expose')
monero_rpc_bind_ip=$(get '.monero.rpc_bind_ip')
monero_rpc_port=$(jq -r '.monero.rpc_port // 18081' <<<"$config")
monero_zmq_expose=$(flag '.monero.zmq_expose')
monero_zmq_bind_ip=$(get '.monero.zmq_bind_ip')
monero_zmq_port=$(jq -r '.monero.zmq_port // 18083' <<<"$config")
node_prune() { jq -r --arg node "$1" '.[$node].prune_mb // 10000' <<<"$config"; }
litecoin_prune=$(node_prune litecoin)
dogecoin_prune=$(node_prune dogecoin)
bitcoincash_prune=$(node_prune bitcoincash)
tari_pruning_horizon=$(jq -r '.tari.pruning_horizon // 10000' <<<"$config")
node_expose() { jq -r --arg node "$1" 'if .[$node].rpc_expose == true then 1 else 0 end' <<<"$config"; }
tari_grpc_expose=$(jq -r 'if .tari.grpc_expose == true then 1 else 0 end' <<<"$config")
tari_grpc_bind_ip=$(get '.tari.grpc_bind_ip')
tari_grpc_port=$(jq -r '.tari.grpc_port // 18142' <<<"$config")
litecoin_rpc_expose=$(node_expose litecoin)
litecoin_rpc_bind_ip=$(get '.litecoin.rpc_bind_ip')
litecoin_rpc_port=$(jq -r '.litecoin.rpc_port // 9332' <<<"$config")
dogecoin_rpc_expose=$(node_expose dogecoin)
dogecoin_rpc_bind_ip=$(get '.dogecoin.rpc_bind_ip')
dogecoin_rpc_port=$(jq -r '.dogecoin.rpc_port // 22555' <<<"$config")
bitcoincash_rpc_expose=$(node_expose bitcoincash)
bitcoincash_rpc_bind_ip=$(get '.bitcoincash.rpc_bind_ip')
bitcoincash_rpc_port=$(jq -r '.bitcoincash.rpc_port // 8332' <<<"$config")
zcash_rpc_expose=$(node_expose zcash)
zcash_rpc_bind_ip=$(get '.zcash.rpc_bind_ip')
zcash_rpc_port=$(jq -r '.zcash.rpc_port // 8232' <<<"$config")
[ "$bitcoin_enabled" = "1" ] || rpc_expose=0
[ "$monero_enabled" = "1" ] || { monero_rpc_expose=0; monero_zmq_expose=0; }
[ "$tari_enabled" = "1" ] || tari_grpc_expose=0
[ "$litecoin_enabled" = "1" ] || litecoin_rpc_expose=0
[ "$dogecoin_enabled" = "1" ] || dogecoin_rpc_expose=0
[ "$bitcoincash_enabled" = "1" ] || bitcoincash_rpc_expose=0
[ "$zcash_enabled" = "1" ] || zcash_rpc_expose=0
bitcoin_resource_profile=$(get '.bitcoin.resource_profile')
monero_resource_profile=$(get '.monero.resource_profile')
[ -n "$bitcoin_resource_profile" ] || bitcoin_resource_profile=$(sed -n 's/^BITCOIN_RESOURCE_PROFILE=//p' "$env_file" 2>/dev/null || true)
[ -n "$monero_resource_profile" ] || monero_resource_profile=$(sed -n 's/^MONERO_RESOURCE_PROFILE=//p' "$env_file" 2>/dev/null || true)
[ -n "$bitcoin_resource_profile" ] || bitcoin_resource_profile=off
[ -n "$monero_resource_profile" ] || monero_resource_profile=off
for node in tari litecoin dogecoin bitcoincash zcash; do
  value=$(jq -r --arg node "$node" '.[$node].resource_profile // "off"' <<<"$config")
  case "$node" in
    tari) tari_resource_profile=$value ;;
    litecoin) litecoin_resource_profile=$value ;;
    dogecoin) dogecoin_resource_profile=$value ;;
    bitcoincash) bitcoincash_resource_profile=$value ;;
    zcash) zcash_resource_profile=$value ;;
  esac
done
bitcoin_steady_cpus=$(jq -r '.bitcoin.steady_cpus // empty' <<<"$config")
bitcoin_steady_mem_limit_mb=$(jq -r '.bitcoin.steady_mem_limit_mb // empty' <<<"$config")
monero_steady_cpus=$(jq -r '.monero.steady_cpus // empty' <<<"$config")
monero_steady_mem_limit_mb=$(jq -r '.monero.steady_mem_limit_mb // empty' <<<"$config")
[ -n "$bitcoin_steady_cpus" ] || bitcoin_steady_cpus=$(sed -n 's/^BITCOIN_STEADY_CPUS=//p' "$env_file" 2>/dev/null || true)
[ -n "$bitcoin_steady_mem_limit_mb" ] || bitcoin_steady_mem_limit_mb=$(sed -n 's/^BITCOIN_STEADY_MEM_LIMIT_MB=//p' "$env_file" 2>/dev/null || true)
[ -n "$monero_steady_cpus" ] || monero_steady_cpus=$(sed -n 's/^MONERO_STEADY_CPUS=//p' "$env_file" 2>/dev/null || true)
[ -n "$monero_steady_mem_limit_mb" ] || monero_steady_mem_limit_mb=$(sed -n 's/^MONERO_STEADY_MEM_LIMIT_MB=//p' "$env_file" 2>/dev/null || true)
[ -n "$bitcoin_steady_cpus" ] || bitcoin_steady_cpus=1
[ -n "$bitcoin_steady_mem_limit_mb" ] || bitcoin_steady_mem_limit_mb=2048
[ -n "$monero_steady_cpus" ] || monero_steady_cpus=1
[ -n "$monero_steady_mem_limit_mb" ] || monero_steady_mem_limit_mb=2048
for node in tari litecoin dogecoin bitcoincash zcash; do
  case "$node" in
    litecoin | dogecoin | bitcoincash) default_steady_mem=2048 ;;
    *) default_steady_mem=4096 ;;
  esac
  cpus=$(jq -r --arg node "$node" '.[$node].steady_cpus // 1' <<<"$config")
  memory=$(jq -r --arg node "$node" --argjson default "$default_steady_mem" '.[$node].steady_mem_limit_mb // $default' <<<"$config")
  case "$node" in
    tari) tari_steady_cpus=$cpus; tari_steady_mem_limit_mb=$memory ;;
    litecoin) litecoin_steady_cpus=$cpus; litecoin_steady_mem_limit_mb=$memory ;;
    dogecoin) dogecoin_steady_cpus=$cpus; dogecoin_steady_mem_limit_mb=$memory ;;
    bitcoincash) bitcoincash_steady_cpus=$cpus; bitcoincash_steady_mem_limit_mb=$memory ;;
    zcash) zcash_steady_cpus=$cpus; zcash_steady_mem_limit_mb=$memory ;;
  esac
done
dashboard_onion=$(jq -r 'if ((.dashboard // {}) | has("onion")) then if .dashboard.onion == true then 1 else 0 end else empty end' <<<"$config")
[ -n "$dashboard_onion" ] || dashboard_onion=$(sed -n 's/^DASHBOARD_ONION=//p' "$env_file" 2>/dev/null || true)
[ -n "$dashboard_onion" ] || dashboard_onion=0
if jq -e '(.dashboard // {}) | has("public_url") and (.public_url == null or (.public_url | type != "string"))' >/dev/null <<<"$config"; then
  echo "dashboard.public_url must be a string when set."
  exit 1
fi
if jq -e '(.dashboard // {}) | has("public_url")' >/dev/null <<<"$config"; then
  dashboard_public_url=$(jq -r '.dashboard.public_url' <<<"$config")
else
  dashboard_public_url=$(sed -n 's/^DASHBOARD_PUBLIC_URL=//p' "$env_file" 2>/dev/null || true)
fi
dashboard_bind_setting=$(get '.dashboard.bind_ip')
if [ "$dashboard_bind_setting" = auto ]; then
  dashboard_bind_ip=$(detect_dashboard_bind_ip)
elif [ -n "$dashboard_bind_setting" ]; then
  dashboard_bind_ip=$dashboard_bind_setting
else
  dashboard_bind_ip=$(sed -n 's/^DASHBOARD_BIND_IP=//p' "$env_file" 2>/dev/null || true)
  [ -n "$dashboard_bind_ip" ] || dashboard_bind_ip=$(detect_dashboard_bind_ip)
fi
dashboard_default_port=80
[ "$package_managed" != brew ] || dashboard_default_port=8080
dashboard_port=$(jq -r '.dashboard.port // empty' <<<"$config")
[ -n "$dashboard_port" ] || dashboard_port=$(sed -n 's/^DASHBOARD_PORT=//p' "$env_file" 2>/dev/null || true)
[ -n "$dashboard_port" ] || dashboard_port=$dashboard_default_port
telegram_bot_token=$(get '.notifications.telegram_bot_token')
telegram_chat_id=$(get '.notifications.telegram_chat_id')
healthchecks_url=$(get '.notifications.healthchecks_url')
alert_new_block=$(flag '.notifications.alert_new_block')
alert_new_block_monero=$(flag '.notifications.alert_new_block_monero')
alert_new_block_tari=$(flag '.notifications.alert_new_block_tari')
alert_new_block_litecoin=$(flag '.notifications.alert_new_block_litecoin')
alert_new_block_dogecoin=$(flag '.notifications.alert_new_block_dogecoin')
alert_new_block_bitcoincash=$(flag '.notifications.alert_new_block_bitcoincash')
alert_new_block_zcash=$(flag '.notifications.alert_new_block_zcash')
# Watch-only public keys travel as a base64 JSON blob so xpubs/descriptors
# pass through .env untouched (no quoting or $-expansion footguns).
default_wallets='[{"name":"Binance (public)","key":"34xp4vRoCGJym3xR7yCVPFHoCNxv4Twseo","birthday":""}]'
if [ "$package_managed" != "0" ] && [ "$prune" != "0" ]; then
  default_wallets='[]'
fi
watch_wallets_b64=$(jq -cej --argjson defaults "$default_wallets" '
  def public_key:
    type == "string" and
    (gsub("^\\s+|\\s+$"; "") |
      (test("^(?:(?:bc1|tb1|bcrt1)[0-9a-z]{20,}|[13][1-9A-HJ-NP-Za-km-z]{25,34}|[xtyuzv]pub[1-9A-HJ-NP-Za-km-z]{107})$") or
       (test("^(?:addr|pk|pkh|wpkh|sh|wsh|tr|rawtr|combo|raw|multi|sortedmulti)\\(") and
        (test("\\s") | not) and
        (test("(^|[^A-Za-z0-9])[xtyuzvXYZUV]prv[A-Za-z0-9]+|(^|[^1-9A-HJ-NP-Za-km-z])[5KL9c][1-9A-HJ-NP-Za-km-z]{50,51}($|[^1-9A-HJ-NP-Za-km-z])") | not))));
  (if has("wallets") then .wallets else $defaults end) |
  if type != "array" then error("wallets must be an array")
  elif all(.[]; type == "object" and (.key | public_key)) then .
  else error("wallets accept only public addresses, extended public keys, or public descriptors; remove private or invalid material from config.json") end
' <<<"$config" | base64 | tr -d '\n')

check_alnum() {
  case "$2" in
  *[!a-zA-Z0-9]*)
    echo "$1 must contain only letters and numbers."
    exit 1
    ;;
  esac
}
check_alnum "node_username" "$rpc_user"
check_alnum "node_password" "$rpc_password"
check_alnum "monero node_username" "$monero_rpc_user"
check_alnum "monero node_password" "$monero_rpc_password"
check_alnum "litecoin node_username" "$litecoin_rpc_user"
check_alnum "litecoin node_password" "$litecoin_rpc_password"
check_alnum "dogecoin node_username" "$dogecoin_rpc_user"
check_alnum "dogecoin node_password" "$dogecoin_rpc_password"
check_alnum "bitcoincash node_username" "$bitcoincash_rpc_user"
check_alnum "bitcoincash node_password" "$bitcoincash_rpc_password"
check_alnum "zcash node_username" "$zcash_rpc_user"
check_alnum "zcash node_password" "$zcash_rpc_password"
check_alnum "monero wallet RPC username" "$monero_wallet_rpc_user"
check_alnum "monero wallet RPC password" "$monero_wallet_rpc_password"
check_alnum "monero wallet password" "$monero_wallet_password"
check_alnum "tari wallet RPC username" "$tari_wallet_rpc_user"
check_alnum "tari wallet RPC password" "$tari_wallet_rpc_password"
check_alnum "tari wallet password" "$tari_wallet_password"
check_alnum "dashboard password" "$dashboard_password"
if [ -z "$dashboard_password" ] || ((${#dashboard_password} < 12 || ${#dashboard_password} > 128)); then
  echo "dashboard.password must be 12 to 128 alphanumeric characters; set it in config.json before applying."
  exit 1
fi

if [ -n "$dashboard_public_url" ]; then
  if [[ ! "$dashboard_public_url" =~ ^https://[^/\?#@]+$ ]]; then
    echo "dashboard.public_url must be a canonical HTTPS origin without credentials, path, query, or fragment."
    exit 1
  fi
  dashboard_authority=${dashboard_public_url#https://}
  dashboard_host=$dashboard_authority
  dashboard_url_port=""
  if [[ "$dashboard_authority" == *:* ]]; then
    dashboard_host=${dashboard_authority%:*}
    dashboard_url_port=${dashboard_authority##*:}
    if [[ -z "$dashboard_host" || -z "$dashboard_url_port" || ${#dashboard_url_port} -gt 5 || ! "$dashboard_url_port" =~ ^[0-9]+$ ]] ||
      ((10#$dashboard_url_port < 1 || 10#$dashboard_url_port > 65535)); then
      echo "dashboard.public_url port must be a whole number from 1 to 65535."
      exit 1
    fi
  fi
  if [[ "$dashboard_host" == *:* || ${#dashboard_host} -gt 253 || "$dashboard_host" == .* || "$dashboard_host" == *. ]]; then
    echo "dashboard.public_url must use a valid DNS hostname."
    exit 1
  fi
  IFS=. read -r -a dashboard_labels <<<"$dashboard_host"
  for dashboard_label in "${dashboard_labels[@]}"; do
    if [[ -z "$dashboard_label" || ${#dashboard_label} -gt 63 || ! "$dashboard_label" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$ ]]; then
      echo "dashboard.public_url must use a valid DNS hostname."
      exit 1
    fi
  done
fi
if [ "$dashboard_onion" = "1" ] && [ -n "$dashboard_public_url" ]; then
  echo "dashboard.public_url and dashboard.onion are mutually exclusive; choose one protected ingress."
  exit 1
fi

if [ "$rpc_expose" = "1" ] && ((${#rpc_password} < 24)); then
  echo "bitcoin.node_password must be at least 24 characters when bitcoin.rpc_expose is true."
  exit 1
fi
if [ "$monero_rpc_expose" = "1" ] && ((${#monero_rpc_password} < 24)); then
  echo "monero.node_password must be at least 24 characters when monero.rpc_expose is true."
  exit 1
fi
for exposed_password in "$litecoin_rpc_expose:$litecoin_rpc_password:litecoin" "$dogecoin_rpc_expose:$dogecoin_rpc_password:dogecoin" "$bitcoincash_rpc_expose:$bitcoincash_rpc_password:bitcoincash" "$zcash_rpc_expose:$zcash_rpc_password:zcash"; do
  IFS=: read -r exposed password node <<<"$exposed_password"
  if [ "$exposed" = 1 ] && ((${#password} < 24)); then
    echo "$node.node_password must be at least 24 characters when $node.rpc_expose is true."
    exit 1
  fi
done

check_bind_ip() {
  local field=$1 ip=$2 allow_loopback=$3 a b c d octet expected="a private IPv4 address"
  [ "$allow_loopback" = "1" ] && expected="a loopback or private IPv4 address"
  if [[ ! "$ip" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
    echo "$field must be $expected."
    exit 1
  fi
  IFS=. read -r a b c d <<<"$ip"
  for octet in "$a" "$b" "$c" "$d"; do
    if { ((${#octet} > 1)) && [[ "$octet" == 0* ]]; } || ((10#$octet > 255)); then
      echo "$field must be $expected."
      exit 1
    fi
  done
  if ! (((allow_loopback == 1 && 10#$a == 127) || 10#$a == 10 || (10#$a == 172 && 10#$b >= 16 && 10#$b <= 31) || (10#$a == 192 && 10#$b == 168))); then
    echo "$field must be $expected."
    exit 1
  fi
}
if [ "$dashboard_bind_ip" = ::1 ]; then
  [ -n "$dashboard_public_url" ] || { echo "dashboard.bind_ip must be a loopback or private IPv4 address."; exit 1; }
else
  check_bind_ip dashboard.bind_ip "$dashboard_bind_ip" 1
fi
if [ -n "$dashboard_public_url" ] && [ "$dashboard_bind_ip" != 127.0.0.1 ] && [ "$dashboard_bind_ip" != ::1 ]; then
  echo "dashboard.bind_ip must be 127.0.0.1 or ::1 when dashboard.public_url is set."
  exit 1
fi
case "$dashboard_port" in '' | 0* | *[!0-9]*) echo "dashboard.port must be a whole number from 1 to 65535."; exit 1 ;; esac
if ((${#dashboard_port} > 5 || 10#$dashboard_port > 65535)); then
  echo "dashboard.port must be a whole number from 1 to 65535."
  exit 1
fi

if [ -n "$rpc_bind_ip" ]; then
  check_bind_ip bitcoin.rpc_bind_ip "$rpc_bind_ip" 0
elif [ "$rpc_expose" = "1" ]; then
  echo "bitcoin.rpc_bind_ip is required when bitcoin.rpc_expose is true."
  exit 1
fi
case "$rpc_port" in '' | 0* | *[!0-9]*)
  echo "bitcoin.rpc_port must be a whole number from 1 to 65535."
  exit 1
  ;;
esac
if ((${#rpc_port} > 5 || 10#$rpc_port > 65535)); then
  echo "bitcoin.rpc_port must be a whole number from 1 to 65535."
  exit 1
fi
if [ "$rpc_expose" = "1" ] && [ "$rpc_bind_ip" = "$dashboard_bind_ip" ] && [ "$rpc_port" = "$dashboard_port" ]; then
  echo "bitcoin.rpc_port conflicts with the dashboard on $rpc_bind_ip:$dashboard_port."
  exit 1
fi

validate_monero_port() {
  case "$2" in '' | 0* | *[!0-9]*)
    echo "$1 must be a whole number from 1 to 65535."
    exit 1
    ;;
  esac
  if ((${#2} > 5 || 10#$2 > 65535)); then
    echo "$1 must be a whole number from 1 to 65535."
    exit 1
  fi
}
validate_monero_port monero.rpc_port "$monero_rpc_port"
validate_monero_port monero.zmq_port "$monero_zmq_port"
validate_monero_port tari.grpc_port "$tari_grpc_port"
validate_monero_port litecoin.rpc_port "$litecoin_rpc_port"
validate_monero_port dogecoin.rpc_port "$dogecoin_rpc_port"
validate_monero_port bitcoincash.rpc_port "$bitcoincash_rpc_port"
validate_monero_port zcash.rpc_port "$zcash_rpc_port"
case "$bitcoin_resource_profile" in compact | off) ;; *) echo "bitcoin.resource_profile must be compact or off."; exit 1 ;; esac
case "$monero_resource_profile" in compact | off) ;; *) echo "monero.resource_profile must be compact or off."; exit 1 ;; esac
for profile in "tari:$tari_resource_profile" "litecoin:$litecoin_resource_profile" "dogecoin:$dogecoin_resource_profile" "bitcoincash:$bitcoincash_resource_profile" "zcash:$zcash_resource_profile"; do
  IFS=: read -r node value <<<"$profile"
  case "$value" in compact | off) ;; *) echo "$node.resource_profile must be compact or off."; exit 1 ;; esac
done
validate_steady_limits() {
  local node=$1 cpus=$2 memory=$3
  if ! awk -v value="$cpus" 'BEGIN { exit !(value ~ /^[0-9]+([.][0-9][0-9]?)?$/ && value >= 0.01 && value <= 256) }'; then
    echo "$node.steady_cpus must be a number from 0.01 to 256 with at most two decimal places."
    exit 1
  fi
  case "$memory" in '' | *[!0-9]*)
    echo "$node.steady_mem_limit_mb must be a whole number from 257 to 1048576."
    exit 1
    ;;
  esac
  if [ "$memory" -le 256 ] || [ "$memory" -gt 1048576 ]; then
    echo "$node.steady_mem_limit_mb must be a whole number from 257 to 1048576."
    exit 1
  fi
}
for node in bitcoin monero tari litecoin dogecoin bitcoincash zcash; do
  if ! jq -e --arg node "$node" '
    (.[$node] // {}) as $settings |
    (($settings | has("steady_cpus") | not) or ($settings.steady_cpus | type == "number")) and
    (($settings | has("steady_mem_limit_mb") | not) or
      ($settings.steady_mem_limit_mb | type == "number" and floor == .))
  ' >/dev/null <<<"$config"; then
    echo "$node steady resource limits must be JSON numbers."
    exit 1
  fi
done
validate_steady_limits bitcoin "$bitcoin_steady_cpus" "$bitcoin_steady_mem_limit_mb"
validate_steady_limits monero "$monero_steady_cpus" "$monero_steady_mem_limit_mb"
validate_steady_limits tari "$tari_steady_cpus" "$tari_steady_mem_limit_mb"
validate_steady_limits litecoin "$litecoin_steady_cpus" "$litecoin_steady_mem_limit_mb"
validate_steady_limits dogecoin "$dogecoin_steady_cpus" "$dogecoin_steady_mem_limit_mb"
validate_steady_limits bitcoincash "$bitcoincash_steady_cpus" "$bitcoincash_steady_mem_limit_mb"
validate_steady_limits zcash "$zcash_steady_cpus" "$zcash_steady_mem_limit_mb"
for port_config in \
  "tari:$tari_grpc_expose:$tari_grpc_bind_ip:$tari_grpc_port:grpc" \
  "litecoin:$litecoin_rpc_expose:$litecoin_rpc_bind_ip:$litecoin_rpc_port:rpc" \
  "dogecoin:$dogecoin_rpc_expose:$dogecoin_rpc_bind_ip:$dogecoin_rpc_port:rpc" \
  "bitcoincash:$bitcoincash_rpc_expose:$bitcoincash_rpc_bind_ip:$bitcoincash_rpc_port:rpc" \
  "zcash:$zcash_rpc_expose:$zcash_rpc_bind_ip:$zcash_rpc_port:rpc"; do
  IFS=: read -r node exposed bind_ip port kind <<<"$port_config"
  if [ "$exposed" = 1 ]; then
    [ -n "$bind_ip" ] || { echo "$node.${kind}_bind_ip is required when $node.${kind}_expose is true."; exit 1; }
    check_bind_ip "$node.${kind}_bind_ip" "$bind_ip" 0
    if [ "$bind_ip" = "$dashboard_bind_ip" ] && [ "$port" = "$dashboard_port" ]; then
      echo "$node.${kind}_port conflicts with the dashboard on $bind_ip:$dashboard_port."
      exit 1
    fi
  fi
done
exposed_listener_keys=()
exposed_listener_names=()
register_listener() {
  local name=$1 enabled=$2 bind_ip=$3 port=$4 key index
  [ "$enabled" = 1 ] || return 0
  key=$bind_ip:$port
  for ((index=0; index<${#exposed_listener_keys[@]}; index++)); do
    if [ "${exposed_listener_keys[$index]}" = "$key" ]; then
      echo "$name conflicts with ${exposed_listener_names[$index]} on $key."
      exit 1
    fi
  done
  exposed_listener_keys+=("$key")
  exposed_listener_names+=("$name")
}
register_listener dashboard 1 "$dashboard_bind_ip" "$dashboard_port"
register_listener bitcoin.rpc_port "$rpc_expose" "$rpc_bind_ip" "$rpc_port"
register_listener monero.rpc_port "$monero_rpc_expose" "$monero_rpc_bind_ip" "$monero_rpc_port"
register_listener monero.zmq_port "$monero_zmq_expose" "$monero_zmq_bind_ip" "$monero_zmq_port"
register_listener tari.grpc_port "$tari_grpc_expose" "$tari_grpc_bind_ip" "$tari_grpc_port"
register_listener litecoin.rpc_port "$litecoin_rpc_expose" "$litecoin_rpc_bind_ip" "$litecoin_rpc_port"
register_listener dogecoin.rpc_port "$dogecoin_rpc_expose" "$dogecoin_rpc_bind_ip" "$dogecoin_rpc_port"
register_listener bitcoincash.rpc_port "$bitcoincash_rpc_expose" "$bitcoincash_rpc_bind_ip" "$bitcoincash_rpc_port"
register_listener zcash.rpc_port "$zcash_rpc_expose" "$zcash_rpc_bind_ip" "$zcash_rpc_port"
enabled_total=$((bitcoin_enabled + monero_enabled + tari_enabled + litecoin_enabled + dogecoin_enabled + bitcoincash_enabled + zcash_enabled))
[ "$enabled_total" -gt 0 ] || { echo "At least one node must be enabled."; exit 1; }
case "$tari_pruning_horizon" in '' | 0 | *[!0-9]*) echo "tari.pruning_horizon must be a positive whole number."; exit 1 ;; esac
for prune_config in "litecoin:$litecoin_prune:550" "dogecoin:$dogecoin_prune:2200" "bitcoincash:$bitcoincash_prune:550"; do
  IFS=: read -r node value minimum <<<"$prune_config"
  case "$value" in '' | *[!0-9]*) echo "$node.prune_mb must be a whole number of at least $minimum."; exit 1 ;; esac
  [ "$value" -ge "$minimum" ] || { echo "$node.prune_mb must be at least $minimum."; exit 1; }
done
if [ "$monero_rpc_expose" = "1" ]; then
  [ -n "$monero_rpc_bind_ip" ] || { echo "monero.rpc_bind_ip is required when monero.rpc_expose is true."; exit 1; }
  check_bind_ip monero.rpc_bind_ip "$monero_rpc_bind_ip" 0
fi
if [ "$monero_zmq_expose" = "1" ]; then
  [ -n "$monero_zmq_bind_ip" ] || { echo "monero.zmq_bind_ip is required when monero.zmq_expose is true."; exit 1; }
  check_bind_ip monero.zmq_bind_ip "$monero_zmq_bind_ip" 0
fi
if [ "$monero_rpc_expose" = "1" ] && [ "$monero_rpc_bind_ip" = "$dashboard_bind_ip" ] && [ "$monero_rpc_port" = "$dashboard_port" ]; then
  echo "monero.rpc_port conflicts with the dashboard on $monero_rpc_bind_ip:$dashboard_port."
  exit 1
fi
if [ "$monero_zmq_expose" = "1" ] && [ "$monero_zmq_bind_ip" = "$dashboard_bind_ip" ] && [ "$monero_zmq_port" = "$dashboard_port" ]; then
  echo "monero.zmq_port conflicts with the dashboard on $monero_zmq_bind_ip:$dashboard_port."
  exit 1
fi
if [ "$monero_rpc_expose" = "1" ] && [ "$monero_zmq_expose" = "1" ] && [ "$monero_rpc_bind_ip:$monero_rpc_port" = "$monero_zmq_bind_ip:$monero_zmq_port" ]; then
  echo "monero.rpc_port and monero.zmq_port conflict on $monero_rpc_bind_ip:$monero_rpc_port."
  exit 1
fi
if [ "$rpc_expose" = "1" ] && [ "$monero_rpc_expose" = "1" ] && [ "$rpc_bind_ip:$rpc_port" = "$monero_rpc_bind_ip:$monero_rpc_port" ]; then
  echo "bitcoin.rpc_port and monero.rpc_port conflict on $rpc_bind_ip:$rpc_port."
  exit 1
fi
if [ "$rpc_expose" = "1" ] && [ "$monero_zmq_expose" = "1" ] && [ "$rpc_bind_ip:$rpc_port" = "$monero_zmq_bind_ip:$monero_zmq_port" ]; then
  echo "bitcoin.rpc_port and monero.zmq_port conflict on $rpc_bind_ip:$rpc_port."
  exit 1
fi

compose_file=docker-compose.yml
[ "$tari_enabled" = "1" ] && compose_file=$compose_file:docker-compose.tari-wallet.yml
[ "$rpc_expose" = "1" ] && compose_file=$compose_file:docker-compose.rpc.yml
[ "$monero_rpc_expose" = "1" ] && compose_file=$compose_file:docker-compose.monero-rpc.yml
[ "$monero_zmq_expose" = "1" ] && compose_file=$compose_file:docker-compose.monero-zmq.yml
[ "$tari_grpc_expose" = "1" ] && compose_file=$compose_file:docker-compose.tari-grpc.yml
[ "$litecoin_rpc_expose" = "1" ] && compose_file=$compose_file:docker-compose.litecoin-rpc.yml
[ "$dogecoin_rpc_expose" = "1" ] && compose_file=$compose_file:docker-compose.dogecoin-rpc.yml
[ "$bitcoincash_rpc_expose" = "1" ] && compose_file=$compose_file:docker-compose.bitcoincash-rpc.yml
[ "$zcash_rpc_expose" = "1" ] && compose_file=$compose_file:docker-compose.zcash-rpc.yml
compose_profiles=
append_profile() { [ -z "$compose_profiles" ] || compose_profiles=$compose_profiles,; compose_profiles=${compose_profiles}$1; }
[ "$bitcoin_enabled:$bitcoin_active" = "1:1" ] && append_profile bitcoin
[ "$monero_enabled:$monero_active" = "1:1" ] && append_profile monero
[ "$tari_enabled:$tari_active" = "1:1" ] && append_profile tari
[ "$litecoin_enabled:$litecoin_active" = "1:1" ] && append_profile litecoin
[ "$dogecoin_enabled:$dogecoin_active" = "1:1" ] && append_profile dogecoin
[ "$bitcoincash_enabled:$bitcoincash_active" = "1:1" ] && append_profile bitcoincash
[ "$zcash_enabled:$zcash_active" = "1:1" ] && append_profile zcash

if jq -e '.bitcoin.sync_over_clearnet == true' >/dev/null <<<"$config"; then
  echo "bitcoin.sync_over_clearnet is no longer supported; all public internet traffic must use Tor."
  exit 1
fi

# Notification values pass through .env — reject anything env-file-unsafe
check_envsafe() {
  case "$2" in
  *[\ \	\'\"\$]*)
    echo "$1 must not contain spaces, quotes, or \$."
    exit 1
    ;;
  esac
}
check_envsafe "telegram_bot_token" "$telegram_bot_token"
check_envsafe "telegram_chat_id" "$telegram_chat_id"
check_envsafe "healthchecks_url" "$healthchecks_url"
check_envsafe "dashboard_public_url" "$dashboard_public_url"

if [ -n "$healthchecks_url" ]; then
  case "$healthchecks_url" in
  https://*) ;;
  *)
    echo "healthchecks_url must be a full HTTPS URL (paste it from healthchecks.io)."
    exit 1
    ;;
  esac
fi


# bitcoind rejects prune targets between 1 and 549 MB
case "$prune" in *[!0-9]*) prune=-1 ;; esac
if [ "$prune" -lt 0 ] || { [ "$prune" -ne 0 ] && [ "$prune" -lt 550 ]; }; then
  echo "prune_mb must be 0 (full node) or at least 550."
  exit 1
fi

# Optional bitcoind container memory cap. 0 (default) = unlimited (Docker's
# default, i.e. today's behavior). A cap turns a bitcoind memory blowup into a
# contained restart instead of a whole-host OOM. Rendered as e.g. "5000m".
case "$mem_limit_mb" in *[!0-9]*)
  echo "mem_limit_mb must be a whole number of MB (0 = unlimited)."
  exit 1
  ;;
esac
if [ "$mem_limit_mb" = "0" ]; then
  bitcoin_mem_limit=0
else
  bitcoin_mem_limit="${mem_limit_mb}m"
  if [ "$mem_limit_mb" -le "$dbcache" ]; then
    echo "WARNING: mem_limit_mb ($mem_limit_mb) is not above dbcache_mb ($dbcache) — bitcoind may be OOM-killed and restart-loop. Leave ~1.5-2 GB headroom above dbcache."
  fi
fi

# blockfilterindex needs the full chain — bitcoind refuses to start with pruning
if [ "$blockfilterindex" = "1" ] && [ "$prune" != "0" ]; then
  echo "blockfilterindex requires a full node (prune_mb: 0) — it can't run on a pruned node."
  exit 1
fi

if [ "${BITCOIN_STACK_VALIDATE_ONLY:-0}" = 1 ]; then
  exit 0
fi

# rpcauth = salted HMAC of the password (same scheme as Bitcoin Core's
# rpcauth.py), so the bitcoin container never holds the plaintext password.
# Salt and hash stay as separate hex values; the container assembles the
# user:salt$hash string itself (a literal $ in .env is a compose footgun).
# Reuse the existing salt (like the password above) so an unchanged password
# re-renders byte-identical — a fresh salt every run would rotate the hash and
# needlessly recreate the bitcoin container on every apply/upgrade.
rpcauth_salt=$(sed -n 's/^BITCOIN_RPCAUTH_SALT=//p' "$env_file" 2>/dev/null || true)
[ -n "$rpcauth_salt" ] || rpcauth_salt=$(openssl rand -hex 16)
rpcauth_hash=$(printf '%s' "$rpc_password" | openssl dgst -sha256 -hmac "$rpcauth_salt" -r | cut -d' ' -f1)

# Displayed version: the release number when this checkout IS the tagged release
# (or an unpacked release tarball, which has no git), otherwise branch-commit so
# dev builds are identifiable rather than masquerading as the release.
version=$(cat "$stack_root/VERSION" 2>/dev/null || echo dev)
git_root=$(git -C "$stack_root" rev-parse --show-toplevel 2>/dev/null || true)
if [ "$git_root" = "$(cd "$stack_root" && pwd -P)" ]; then
  head=$(git -C "$stack_root" rev-parse HEAD 2>/dev/null || echo)
  # ^{commit} dereferences the (annotated) release tag to its commit; without it
  # the tag-object sha never equals HEAD and every deploy looks like a dev build.
  tag=$(git -C "$stack_root" rev-parse "v$version^{commit}" 2>/dev/null || echo)
  # diff-index checks tracked files only, so runtime files (.env, data/) don't
  # demote a clean release checkout to a dev build.
  if [ -n "$head" ] && [ "$head" = "$tag" ] && git -C "$stack_root" diff-index --quiet HEAD -- 2>/dev/null; then
    stack_version="$version" # clean checkout of the release tag
  else
    # '/' is legal in a git branch name but not in a Docker image tag, and
    # STACK_VERSION is used as the compose image tag — sanitize so a branch like
    # feat/x doesn't render an invalid ghcr reference and break `docker compose`.
    branch=$(git -C "$stack_root" rev-parse --abbrev-ref HEAD 2>/dev/null | tr '/' '-')
    short=$(git -C "$stack_root" rev-parse --short HEAD 2>/dev/null || echo unknown)
    if [ -n "$branch" ] && [ "$branch" != "HEAD" ]; then
      stack_version="$branch-$short" # a named branch
    else
      stack_version="$short" # detached HEAD, not at the release tag
    fi
  fi
else
  stack_version="$version" # release tarball (no .git)
fi

env_tmp=$(mktemp "$state_dir/.env.tmp.XXXXXX")
trap 'rm -f "$env_tmp"' EXIT
compose_project_name=${BITCOIN_STACK_PROJECT_NAME:-}
if [ -z "$compose_project_name" ] && [ -f "$env_file" ]; then
  compose_project_name=$(sed -n 's/^COMPOSE_PROJECT_NAME=//p' "$env_file" 2>/dev/null | tail -n 1)
fi
[ -n "$compose_project_name" ] || { [ -f "$env_file" ] && compose_project_name=bitcoin-starter-stack || compose_project_name='node-manager-source'; }
cat >"$env_tmp" <<EOF
COMPOSE_PROJECT_NAME=$compose_project_name
COMPOSE_FILE=$compose_file
COMPOSE_PROFILES=$compose_profiles
BITCOIN_ENABLED=$bitcoin_enabled
BITCOIN_ACTIVE=$bitcoin_active
BITCOIN_RPC_USER=$rpc_user
BITCOIN_RPC_PASSWORD=$rpc_password
BITCOIN_RPCAUTH_SALT=$rpcauth_salt
BITCOIN_RPCAUTH_HASH=$rpcauth_hash
BITCOIN_RPC_EXPOSE=$rpc_expose
BITCOIN_RPC_BIND_IP=$rpc_bind_ip
BITCOIN_RPC_PORT=$rpc_port
BITCOIN_DATA_DIR=$data_dir
BITCOIN_DBCACHE=$dbcache
BITCOIN_PRUNE=$prune
BITCOIN_INBOUND_ONION=$inbound_onion
BITCOIN_BLOCKFILTERINDEX=$blockfilterindex
BITCOIN_MEM_LIMIT=$bitcoin_mem_limit
BITCOIN_RESOURCE_PROFILE=$bitcoin_resource_profile
BITCOIN_STEADY_CPUS=$bitcoin_steady_cpus
BITCOIN_STEADY_MEM_LIMIT_MB=$bitcoin_steady_mem_limit_mb
MONERO_ENABLED=$monero_enabled
MONERO_ACTIVE=$monero_active
MONERO_RPC_URL=http://172.29.0.30:18085
MONERO_RPC_USER=$monero_rpc_user
MONERO_RPC_PASSWORD=$monero_rpc_password
MONERO_WALLET_RPC_URL=http://172.29.0.31:18082
MONERO_WALLET_RPC_USER=$monero_wallet_rpc_user
MONERO_WALLET_RPC_PASSWORD=$monero_wallet_rpc_password
MONERO_WALLET_PASSWORD=$monero_wallet_password
MONERO_DATA_DIR=$monero_data_dir
MONERO_RPC_EXPOSE=$monero_rpc_expose
MONERO_RPC_BIND_IP=$monero_rpc_bind_ip
MONERO_RPC_PORT=$monero_rpc_port
MONERO_ZMQ_EXPOSE=$monero_zmq_expose
MONERO_ZMQ_BIND_IP=$monero_zmq_bind_ip
MONERO_ZMQ_PORT=$monero_zmq_port
MONERO_RESOURCE_PROFILE=$monero_resource_profile
MONERO_STEADY_CPUS=$monero_steady_cpus
MONERO_STEADY_MEM_LIMIT_MB=$monero_steady_mem_limit_mb
TARI_ENABLED=$tari_enabled
TARI_ACTIVE=$tari_active
TARI_WALLET_ENABLED=$tari_enabled
TARI_GRPC_URL=http://172.29.0.32:18142
TARI_WALLET_GRPC_URL=http://172.29.0.37:18143
TARI_WALLET_RPC_USER=$tari_wallet_rpc_user
TARI_WALLET_RPC_PASSWORD=$tari_wallet_rpc_password
TARI_WALLET_PASSWORD=$tari_wallet_password
TARI_DATA_DIR=$tari_data_dir
TARI_PRUNING_HORIZON=$tari_pruning_horizon
TARI_GRPC_EXPOSE=$tari_grpc_expose
TARI_GRPC_BIND_IP=$tari_grpc_bind_ip
TARI_GRPC_PORT=$tari_grpc_port
TARI_RESOURCE_PROFILE=$tari_resource_profile
TARI_STEADY_CPUS=$tari_steady_cpus
TARI_STEADY_MEM_LIMIT_MB=$tari_steady_mem_limit_mb
LITECOIN_ENABLED=$litecoin_enabled
LITECOIN_ACTIVE=$litecoin_active
LITECOIN_RPC_URL=http://172.29.0.33:9332
LITECOIN_RPC_USER=$litecoin_rpc_user
LITECOIN_RPC_PASSWORD=$litecoin_rpc_password
LITECOIN_DATA_DIR=$litecoin_data_dir
LITECOIN_PRUNE=$litecoin_prune
LITECOIN_RPC_EXPOSE=$litecoin_rpc_expose
LITECOIN_RPC_BIND_IP=$litecoin_rpc_bind_ip
LITECOIN_RPC_PORT=$litecoin_rpc_port
LITECOIN_RESOURCE_PROFILE=$litecoin_resource_profile
LITECOIN_STEADY_CPUS=$litecoin_steady_cpus
LITECOIN_STEADY_MEM_LIMIT_MB=$litecoin_steady_mem_limit_mb
DOGECOIN_ENABLED=$dogecoin_enabled
DOGECOIN_ACTIVE=$dogecoin_active
DOGECOIN_RPC_URL=http://172.29.0.34:22555
DOGECOIN_RPC_USER=$dogecoin_rpc_user
DOGECOIN_RPC_PASSWORD=$dogecoin_rpc_password
DOGECOIN_DATA_DIR=$dogecoin_data_dir
DOGECOIN_PRUNE=$dogecoin_prune
DOGECOIN_RPC_EXPOSE=$dogecoin_rpc_expose
DOGECOIN_RPC_BIND_IP=$dogecoin_rpc_bind_ip
DOGECOIN_RPC_PORT=$dogecoin_rpc_port
DOGECOIN_RESOURCE_PROFILE=$dogecoin_resource_profile
DOGECOIN_STEADY_CPUS=$dogecoin_steady_cpus
DOGECOIN_STEADY_MEM_LIMIT_MB=$dogecoin_steady_mem_limit_mb
BITCOINCASH_ENABLED=$bitcoincash_enabled
BITCOINCASH_ACTIVE=$bitcoincash_active
BITCOINCASH_RPC_URL=http://172.29.0.35:8332
BITCOINCASH_RPC_USER=$bitcoincash_rpc_user
BITCOINCASH_RPC_PASSWORD=$bitcoincash_rpc_password
BITCOINCASH_DATA_DIR=$bitcoincash_data_dir
BITCOINCASH_PRUNE=$bitcoincash_prune
BITCOINCASH_RPC_EXPOSE=$bitcoincash_rpc_expose
BITCOINCASH_RPC_BIND_IP=$bitcoincash_rpc_bind_ip
BITCOINCASH_RPC_PORT=$bitcoincash_rpc_port
BITCOINCASH_RESOURCE_PROFILE=$bitcoincash_resource_profile
BITCOINCASH_STEADY_CPUS=$bitcoincash_steady_cpus
BITCOINCASH_STEADY_MEM_LIMIT_MB=$bitcoincash_steady_mem_limit_mb
ZCASH_ENABLED=$zcash_enabled
ZCASH_ACTIVE=$zcash_active
ZCASH_RPC_URL=http://172.29.0.36:8232
ZCASH_RPC_USER=$zcash_rpc_user
ZCASH_RPC_PASSWORD=$zcash_rpc_password
ZCASH_DATA_DIR=$zcash_data_dir
ZCASH_RPC_EXPOSE=$zcash_rpc_expose
ZCASH_RPC_BIND_IP=$zcash_rpc_bind_ip
ZCASH_RPC_PORT=$zcash_rpc_port
ZCASH_RESOURCE_PROFILE=$zcash_resource_profile
ZCASH_STEADY_CPUS=$zcash_steady_cpus
ZCASH_STEADY_MEM_LIMIT_MB=$zcash_steady_mem_limit_mb
DASHBOARD_PASSWORD=$dashboard_password
DASHBOARD_ONION=$dashboard_onion
DASHBOARD_PUBLIC_URL=$dashboard_public_url
DASHBOARD_BIND_IP=$dashboard_bind_ip
DASHBOARD_PORT=$dashboard_port
TELEGRAM_BOT_TOKEN=$telegram_bot_token
TELEGRAM_CHAT_ID=$telegram_chat_id
HEALTHCHECKS_URL=$healthchecks_url
ALERT_NEW_BLOCK=$alert_new_block
ALERT_NEW_BLOCK_MONERO=$alert_new_block_monero
ALERT_NEW_BLOCK_TARI=$alert_new_block_tari
ALERT_NEW_BLOCK_LITECOIN=$alert_new_block_litecoin
ALERT_NEW_BLOCK_DOGECOIN=$alert_new_block_dogecoin
ALERT_NEW_BLOCK_BITCOINCASH=$alert_new_block_bitcoincash
ALERT_NEW_BLOCK_ZCASH=$alert_new_block_zcash
NODE_NAME=$(hostname)
STACK_VERSION=$stack_version
STACK_RESTART_POLICY=$restart_policy
STACK_ROOT=$stack_root
STACK_ASSET_ROOT=$asset_root
WATCH_WALLETS_B64=$watch_wallets_b64
EOF
chmod 600 "$env_tmp"
mv -f "$env_tmp" "$env_file"

# Create the data dir up front, or docker creates it root-owned
# and bitcoind (uid 1000) can't write to it. Sandboxed callers (the systemd
# control units) cannot reach custom dirs outside their ReadWritePaths; those
# already exist on the host, so creation failures are tolerated.
mkdir -p "$data_dir" "$monero_data_dir" "$tari_data_dir" "$litecoin_data_dir" "$dogecoin_data_dir" "$bitcoincash_data_dir" "$zcash_data_dir" 2>/dev/null || true
if [ "$package_managed" = "1" ] && [ -n "$BITCOIN_STACK_DATA_DIR" ] && [ "$data_dir" = "${BITCOIN_STACK_DATA_DIR%/}/bitcoin" ]; then
  chown 1000:1000 "$data_dir"
  chmod 700 "$data_dir"
fi
if [ "$package_managed" = "1" ] && [ -n "$BITCOIN_STACK_DATA_DIR" ]; then
  for node_dir in "$tari_data_dir" "$litecoin_data_dir" "$dogecoin_data_dir" "$bitcoincash_data_dir"; do
    case "$node_dir" in "${BITCOIN_STACK_DATA_DIR%/}"/*) chown 1000:1000 "$node_dir"; chmod 700 "$node_dir" ;; esac
  done
  case "$zcash_data_dir" in
    "${BITCOIN_STACK_DATA_DIR%/}"/*)
      chown 10001:1000 "$zcash_data_dir"
      chmod 750 "$zcash_data_dir"
      ;;
  esac
fi
if [ "$package_managed" = "1" ] && [ -n "$BITCOIN_STACK_DATA_DIR" ] && [ "$monero_data_dir" = "${BITCOIN_STACK_DATA_DIR%/}/monero" ]; then
  chown 1000:1000 "$monero_data_dir"
  chmod 700 "$monero_data_dir"
fi

echo "Done. Start local images with: $stack_command up (download missing images: $stack_command pull)."
