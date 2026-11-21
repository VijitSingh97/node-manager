#!/usr/bin/env bash
# shellcheck disable=SC2154 # settings are assigned by eval of the jq output
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

[ "${1:-}" != --detect-bind-ip ] || { detect_dashboard_bind_ip; exit 0; }
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

settings=$(jq -r '
  def s: if type == "string" then sub("\\n+\\z"; "") else tojson end;
  def o($name; value): "\($name)=\(value | s | @sh)";
  def fl(v): if v == true then 1 else 0 end;
  def act($n): if ((.[$n] // {}) | has("active")) then fl(.[$n].active) else 1 end;
  def en($n): if ((.[$n] // {}) | has("enabled")) then fl(.[$n].enabled) else "" end;
  (.network | type) as $net_type | (if $net_type == "object" then .network else {} end) as $net |
  o("rpc_user"; (.bitcoin.node_username // "")),
  o("rpc_password"; (.bitcoin.node_password // "")),
  o("bitcoin_enabled"; en("bitcoin")),
  o("monero_enabled"; en("monero")),
  o("bitcoin_active"; act("bitcoin")),
  o("monero_active"; act("monero")),
  o("tari_enabled"; fl(.tari.enabled)),
  o("tari_active"; act("tari")),
  o("litecoin_enabled"; fl(.litecoin.enabled)),
  o("litecoin_active"; act("litecoin")),
  o("dogecoin_enabled"; fl(.dogecoin.enabled)),
  o("dogecoin_active"; act("dogecoin")),
  o("dogecoin_watch_wallet"; fl(.dogecoin.watch_wallet)),
  o("bitcoincash_enabled"; fl(.bitcoincash.enabled)),
  o("bitcoincash_active"; act("bitcoincash")),
  o("zcash_enabled"; fl(.zcash.enabled)),
  o("zcash_address_index"; fl(.zcash.address_index)),
  o("zcash_active"; act("zcash")),
  o("monero_rpc_user"; (.monero.node_username // "")),
  o("monero_rpc_password"; (.monero.node_password // "")),
  o("litecoin_rpc_user"; (.litecoin.node_username // "admin")),
  o("litecoin_rpc_password"; (.litecoin.node_password // "")),
  o("dogecoin_rpc_user"; (.dogecoin.node_username // "admin")),
  o("dogecoin_rpc_password"; (.dogecoin.node_password // "")),
  o("bitcoincash_rpc_user"; (.bitcoincash.node_username // "admin")),
  o("bitcoincash_rpc_password"; (.bitcoincash.node_password // "")),
  o("zcash_rpc_user"; (.zcash.node_username // "admin")),
  o("zcash_rpc_password"; (.zcash.node_password // "")),
  o("dashboard_password"; (.dashboard.password // "")),
  o("dashboard_password_set"; if ((.dashboard // {}) | has("password")) then 1 else 0 end),
  o("launch_at_boot"; if ((.dashboard // {}) | has("launch_at_boot")) then if .dashboard.launch_at_boot == true then "true" else "false" end else "missing" end),
  o("data_dir"; (.bitcoin.data_dir // "")),
  o("monero_data_dir"; (.monero.data_dir // "")),
  o("dbcache"; .bitcoin.dbcache_mb // ""),
  o("prune"; .bitcoin.prune_mb // ""),
  o("mem_limit_mb"; .bitcoin.mem_limit_mb // 0),
  o("bitcoin_cpus"; .bitcoin.cpus // 0),
  o("monero_cpus"; .monero.cpus // 0),
  o("monero_mem_limit_mb"; .monero.mem_limit_mb // 0),
  o("thermal_limit_c"; .host.thermal_limit_c // 0),
  o("thermal_resume_c"; .host.thermal_resume_c // 0),
  o("tor_clearnet"; if $net.clearnet_over_tor == false then 0 else 1 end),
  o("tor_inbound"; if $net.inbound_connections == false then 0 else 1 end),
  o("blockfilterindex"; fl(.bitcoin.blockfilterindex)),
  o("utxo_index"; fl(.bitcoin.utxo_index)),
  o("litecoin_utxo_index"; fl(.litecoin.utxo_index)),
  o("bitcoincash_utxo_index"; fl(.bitcoincash.utxo_index)),
  o("dogecoin_utxo_index"; fl(.dogecoin.utxo_index)),
  o("rpc_expose"; fl(.bitcoin.rpc_expose)),
  o("rpc_bind_ip"; (.bitcoin.rpc_bind_ip // "")),
  o("rpc_port"; .bitcoin.rpc_port // 8332),
  o("monero_rpc_expose"; fl(.monero.rpc_expose)),
  o("monero_rpc_bind_ip"; (.monero.rpc_bind_ip // "")),
  o("monero_rpc_port"; .monero.rpc_port // 18081),
  o("monero_zmq_expose"; fl(.monero.zmq_expose)),
  o("monero_zmq_bind_ip"; (.monero.zmq_bind_ip // "")),
  o("monero_zmq_port"; .monero.zmq_port // 18083),
  o("litecoin_prune"; (.litecoin.prune_mb // 10000)),
  o("dogecoin_prune"; (.dogecoin.prune_mb // 10000)),
  o("bitcoincash_prune"; (.bitcoincash.prune_mb // 10000)),
  o("tari_pruning_horizon"; .tari.pruning_horizon // 10000),
  o("tari_grpc_expose"; if .tari.grpc_expose == true then 1 else 0 end),
  o("tari_grpc_bind_ip"; (.tari.grpc_bind_ip // "")),
  o("tari_grpc_port"; .tari.grpc_port // 18142),
  o("tari_grpc_extra_methods"; .tari.grpc_extra_methods // [] | if type == "array" and all(.[]; type == "string" and test("^[a-z][a-z_]*$")) then join(",") else "!" end),
  o("litecoin_rpc_expose"; fl(.litecoin.rpc_expose)),
  o("litecoin_rpc_bind_ip"; (.litecoin.rpc_bind_ip // "")),
  o("litecoin_rpc_port"; .litecoin.rpc_port // 9332),
  o("dogecoin_rpc_expose"; fl(.dogecoin.rpc_expose)),
  o("dogecoin_rpc_bind_ip"; (.dogecoin.rpc_bind_ip // "")),
  o("dogecoin_rpc_port"; .dogecoin.rpc_port // 22555),
  o("bitcoincash_rpc_expose"; fl(.bitcoincash.rpc_expose)),
  o("bitcoincash_rpc_bind_ip"; (.bitcoincash.rpc_bind_ip // "")),
  o("bitcoincash_rpc_port"; .bitcoincash.rpc_port // 8332),
  o("zcash_rpc_expose"; fl(.zcash.rpc_expose)),
  o("zcash_rpc_bind_ip"; (.zcash.rpc_bind_ip // "")),
  o("zcash_rpc_port"; .zcash.rpc_port // 8232),
  o("bitcoin_resource_profile"; (.bitcoin.resource_profile // "")),
  o("monero_resource_profile"; (.monero.resource_profile // "")),
  o("bitcoin_steady_cpus"; .bitcoin.steady_cpus // ""),
  o("bitcoin_steady_mem_limit_mb"; .bitcoin.steady_mem_limit_mb // ""),
  o("monero_steady_cpus"; .monero.steady_cpus // ""),
  o("monero_steady_mem_limit_mb"; .monero.steady_mem_limit_mb // ""),
  o("dashboard_onion"; if ((.dashboard // {}) | has("onion")) then if .dashboard.onion == true then 1 else 0 end else "" end),
  o("dashboard_bind_setting"; (.dashboard.bind_ip // "")),
  o("dashboard_port"; .dashboard.port // ""),
  o("telegram_bot_token"; (.notifications.telegram_bot_token // "")),
  o("telegram_chat_id"; (.notifications.telegram_chat_id // "")),
  o("healthchecks_url"; (.notifications.healthchecks_url // "")),
  o("alert_new_block"; fl(.notifications.alert_new_block)),
  o("alert_new_block_monero"; fl(.notifications.alert_new_block_monero)),
  o("alert_new_block_tari"; fl(.notifications.alert_new_block_tari)),
  o("alert_new_block_litecoin"; fl(.notifications.alert_new_block_litecoin)),
  o("alert_new_block_dogecoin"; fl(.notifications.alert_new_block_dogecoin)),
  o("alert_new_block_bitcoincash"; fl(.notifications.alert_new_block_bitcoincash)),
  o("alert_new_block_zcash"; fl(.notifications.alert_new_block_zcash)),
  (("tari","litecoin","dogecoin","bitcoincash","zcash") as $n | o("cfg_\($n)_data_dir"; .[$n].data_dir // "")),
  (("tari","litecoin","dogecoin","bitcoincash","zcash") as $n | o("\($n)_resource_profile"; .[$n].resource_profile // "off")),
  (("tari","litecoin","dogecoin","bitcoincash","zcash") as $n | o("\($n)_steady_cpus"; .[$n].steady_cpus // 1)),
  (("tari","litecoin","dogecoin","bitcoincash","zcash") as $n | o("\($n)_steady_mem_limit_mb"; .[$n].steady_mem_limit_mb // (if $n == "tari" or $n == "zcash" then 4096 else 2048 end))),
  (("tari","litecoin","dogecoin","bitcoincash","zcash") as $n | o("\($n)_cpus"; .[$n].cpus // 0)),
  (("bitcoin","monero","tari","litecoin","dogecoin","bitcoincash","zcash") as $n | o("steady_limits_ok_\($n)"; fl((.[$n] // {}) as $settings | (($settings | has("steady_cpus") | not) or ($settings.steady_cpus | type == "number")) and (($settings | has("steady_mem_limit_mb") | not) or ($settings.steady_mem_limit_mb | type == "number" and floor == .))))),
  (("bitcoin","litecoin","bitcoincash","dogecoin") as $n | o("\($n)_utxo_index_invalid"; fl((.[$n] // {}) | has("utxo_index") and (.utxo_index | type) != "boolean"))),
  o("network_ok"; fl(if $net_type == "null" or $net_type == "object" then all(.network.clearnet_over_tor, .network.inbound_connections; . == null or type == "boolean") else false end)),
  o("sync_over_clearnet"; fl(.bitcoin.sync_over_clearnet)),
  o("dashboard_public_url_invalid"; fl((.dashboard // {}) | has("public_url") and (.public_url == null or (.public_url | type != "string")))),
  o("dashboard_public_url_set"; fl((.dashboard // {}) | has("public_url"))),
  o("dashboard_public_url_config"; .dashboard.public_url // "")
' <<<"$config")
eval "$settings"

# Fresh installs generate separate passwords; upgrades reuse each existing value.
zcash_toml=zakurad.toml
if [ "$zcash_address_index" = 1 ]; then zcash_toml=zakurad-address-index.toml; fi
monero_wallet_rpc_user=$(sed -n 's/^MONERO_WALLET_RPC_USER=//p' "$env_file" 2>/dev/null || true)
monero_wallet_rpc_password=$(sed -n 's/^MONERO_WALLET_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
monero_wallet_password=$(sed -n 's/^MONERO_WALLET_PASSWORD=//p' "$env_file" 2>/dev/null || true)
tari_wallet_rpc_user=$(sed -n 's/^TARI_WALLET_RPC_USER=//p' "$env_file" 2>/dev/null || true)
tari_wallet_rpc_password=$(sed -n 's/^TARI_WALLET_RPC_PASSWORD=//p' "$env_file" 2>/dev/null || true)
tari_wallet_password=$(sed -n 's/^TARI_WALLET_PASSWORD=//p' "$env_file" 2>/dev/null || true)
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
if [ -z "$data_dir" ]; then
  if [ -n "${BITCOIN_STACK_DATA_DIR:-}" ]; then
    data_dir=$BITCOIN_STACK_DATA_DIR/bitcoin
  else
    data_dir=./data/bitcoin
  fi
fi
if [ -z "$monero_data_dir" ]; then
  if [ -n "${BITCOIN_STACK_DATA_DIR:-}" ]; then
    monero_data_dir=$BITCOIN_STACK_DATA_DIR/monero
  else
    monero_data_dir=./data/monero
  fi
fi
node_data_dir() {
  local node=$1 configured
  configured=cfg_${node}_data_dir
  configured=${!configured}
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
[ -n "$dbcache" ] || {
  dbcache=$(sed -n 's/^BITCOIN_DBCACHE=//p' "$env_file" 2>/dev/null || true)
  [ -n "$dbcache" ] || [ ! -f "$env_file" ] || dbcache=3000
}
[ -n "$dbcache" ] || dbcache=128
[ -n "$prune" ] || {
  prune=$(sed -n 's/^BITCOIN_PRUNE=//p' "$env_file" 2>/dev/null || true)
  [ -n "$prune" ] || [ ! -f "$env_file" ] || prune=0
}
[ -n "$prune" ] || prune=10000
[ "$network_ok" = 1 ] || {
  echo "network.clearnet_over_tor and network.inbound_connections must be true or false."
  exit 1
}
[ "$bitcoin_enabled" = "1" ] || rpc_expose=0
[ "$monero_enabled" = "1" ] || { monero_rpc_expose=0; monero_zmq_expose=0; }
[ "$tari_enabled" = "1" ] || tari_grpc_expose=0
[ "$litecoin_enabled" = "1" ] || litecoin_rpc_expose=0
[ "$dogecoin_enabled" = "1" ] || dogecoin_rpc_expose=0
[ "$bitcoincash_enabled" = "1" ] || bitcoincash_rpc_expose=0
[ "$zcash_enabled" = "1" ] || zcash_rpc_expose=0
paused_listeners=$(awk '{print $1}' "$state_dir/.paused-listeners" 2>/dev/null || true)
for listener in rpc monero_rpc monero_zmq tari_grpc litecoin_rpc dogecoin_rpc bitcoincash_rpc zcash_rpc; do
  bind_ip_var=${listener}_bind_ip
  [ -z "${!bind_ip_var}" ] || ! grep -Fxq -- "${!bind_ip_var}" <<<"$paused_listeners" || printf -v "${listener}_expose" 0
done
[ -n "$bitcoin_resource_profile" ] || bitcoin_resource_profile=$(sed -n 's/^BITCOIN_RESOURCE_PROFILE=//p' "$env_file" 2>/dev/null || true)
[ -n "$monero_resource_profile" ] || monero_resource_profile=$(sed -n 's/^MONERO_RESOURCE_PROFILE=//p' "$env_file" 2>/dev/null || true)
[ -n "$bitcoin_resource_profile" ] || bitcoin_resource_profile=off
[ -n "$monero_resource_profile" ] || monero_resource_profile=off
[ -n "$bitcoin_steady_cpus" ] || bitcoin_steady_cpus=$(sed -n 's/^BITCOIN_STEADY_CPUS=//p' "$env_file" 2>/dev/null || true)
[ -n "$bitcoin_steady_mem_limit_mb" ] || bitcoin_steady_mem_limit_mb=$(sed -n 's/^BITCOIN_STEADY_MEM_LIMIT_MB=//p' "$env_file" 2>/dev/null || true)
[ -n "$monero_steady_cpus" ] || monero_steady_cpus=$(sed -n 's/^MONERO_STEADY_CPUS=//p' "$env_file" 2>/dev/null || true)
[ -n "$monero_steady_mem_limit_mb" ] || monero_steady_mem_limit_mb=$(sed -n 's/^MONERO_STEADY_MEM_LIMIT_MB=//p' "$env_file" 2>/dev/null || true)
[ -n "$bitcoin_steady_cpus" ] || bitcoin_steady_cpus=1
[ -n "$bitcoin_steady_mem_limit_mb" ] || bitcoin_steady_mem_limit_mb=2048
[ -n "$monero_steady_cpus" ] || monero_steady_cpus=1
[ -n "$monero_steady_mem_limit_mb" ] || monero_steady_mem_limit_mb=2048
[ -n "$dashboard_onion" ] || dashboard_onion=$(sed -n 's/^DASHBOARD_ONION=//p' "$env_file" 2>/dev/null || true)
[ -n "$dashboard_onion" ] || dashboard_onion=0
if [ "$dashboard_public_url_invalid" = 1 ]; then
  echo "dashboard.public_url must be a string when set."
  exit 1
fi
if [ "$dashboard_public_url_set" = 1 ]; then
  dashboard_public_url=$dashboard_public_url_config
else
  dashboard_public_url=$(sed -n 's/^DASHBOARD_PUBLIC_URL=//p' "$env_file" 2>/dev/null || true)
fi
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
[ -n "$dashboard_port" ] || dashboard_port=$(sed -n 's/^DASHBOARD_PORT=//p' "$env_file" 2>/dev/null || true)
[ -n "$dashboard_port" ] || dashboard_port=$dashboard_default_port
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
  steady_ok=steady_limits_ok_$node
  if [ "${!steady_ok}" != 1 ]; then
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
[ "$tari_grpc_extra_methods" != "!" ] || { echo 'tari.grpc_extra_methods must be a list of Tari gRPC method names in snake_case, such as ["list_headers"].'; exit 1; }
for prune_config in "litecoin:$litecoin_prune:550" "dogecoin:$dogecoin_prune:2200" "bitcoincash:$bitcoincash_prune:550"; do
  IFS=: read -r node value minimum <<<"$prune_config"
  case "$value" in '' | *[!0-9]*) echo "$node.prune_mb must be a whole number of at least $minimum."; exit 1 ;; esac
  [ "$node:$value" = "dogecoin:0" ] || [ "$node:$value" = "bitcoincash:0" ] && continue
  [ "$value" -ge "$minimum" ] || { echo "$node.prune_mb must be at least $minimum."; exit 1; }
done
if [ "$dogecoin_watch_wallet" = "1" ] && [ "$dogecoin_prune" != "0" ]; then
  echo "dogecoin.watch_wallet needs an archival node: set dogecoin.prune_mb to 0 (Dogecoin cannot rescan a pruned chain)."
  exit 1
fi
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

if [ "$bitcoin_utxo_index_invalid" = 1 ]; then
  echo "bitcoin.utxo_index must be true or false."
  exit 1
fi
if [ "$utxo_index" = "1" ] && [ "$bitcoin_enabled" != "1" ]; then
  echo "bitcoin.utxo_index requires bitcoin.enabled."
  exit 1
fi
if [ "$litecoin_utxo_index_invalid" = 1 ]; then
  echo "litecoin.utxo_index must be true or false."
  exit 1
fi
if [ "$litecoin_utxo_index" = "1" ] && [ "$litecoin_enabled" != "1" ]; then
  echo "litecoin.utxo_index requires litecoin.enabled."
  exit 1
fi
if [ "$bitcoincash_utxo_index_invalid" = 1 ]; then
  echo "bitcoincash.utxo_index must be true or false."
  exit 1
fi
if [ "$bitcoincash_utxo_index" = "1" ] && [ "$bitcoincash_enabled" != "1" ]; then
  echo "bitcoincash.utxo_index requires bitcoincash.enabled."
  exit 1
fi
if [ "$dogecoin_utxo_index_invalid" = 1 ]; then
  echo "dogecoin.utxo_index must be true or false."
  exit 1
fi
if [ "$dogecoin_utxo_index" = "1" ] && [ "$dogecoin_enabled" != "1" ]; then
  echo "dogecoin.utxo_index requires dogecoin.enabled."
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
if [ "$utxo_index" = "1" ]; then
  compose_file=$compose_file:docker-compose.utxo-index.yml
  echo "bitcoin.utxo_index is on: ./stack apply recreates the dashboard with a utxo_index volume, a 1g memory limit and 0.5 CPU. bitcoind is not restarted. The first seed takes about 20 minutes and the index uses about 7.5 GiB, up to about 17 GiB while it seeds."
fi
if [ "$litecoin_utxo_index" = "1" ]; then
  compose_file=$compose_file:docker-compose.litecoin-utxo-index.yml
  echo "litecoin.utxo_index is on: ./stack apply recreates the dashboard with a utxo_index_litecoin volume, a 1g memory limit and 0.5 CPU. litecoind is not restarted. Its seed time and size on mainnet are not measured yet."
fi
if [ "$dogecoin_utxo_index" = "1" ]; then
  compose_file=$compose_file:docker-compose.dogecoin-utxo-index.yml
  echo "dogecoin.utxo_index is on: ./stack apply recreates the dashboard with a utxo_index_dogecoin volume, a 1g memory limit and 0.5 CPU. To seed it, the Dogecoin node stops while its chainstate is copied, which works on a pruned node too. Its seed time and size on mainnet are not measured yet."
fi
if [ "$bitcoincash_utxo_index" = "1" ]; then
  compose_file=$compose_file:docker-compose.bitcoincash-utxo-index.yml
  echo "bitcoincash.utxo_index is on: ./stack apply recreates the dashboard with a utxo_index_bitcoincash volume, a 1g memory limit and 0.5 CPU. To seed it, the Bitcoin Cash node stops while its chainstate is copied, which works on a pruned node too; without the host helper, an archival node replays every block from genesis instead. Its seed time and size on mainnet are not measured yet."
fi
compose_profiles=
append_profile() { [ -z "$compose_profiles" ] || compose_profiles=$compose_profiles,; compose_profiles=${compose_profiles}$1; }
[ "$bitcoin_enabled:$bitcoin_active" = "1:1" ] && append_profile bitcoin
[ "$monero_enabled:$monero_active" = "1:1" ] && append_profile monero
[ "$tari_enabled:$tari_active" = "1:1" ] && append_profile tari
[ "$litecoin_enabled:$litecoin_active" = "1:1" ] && append_profile litecoin
[ "$dogecoin_enabled:$dogecoin_active" = "1:1" ] && append_profile dogecoin
[ "$bitcoincash_enabled:$bitcoincash_active" = "1:1" ] && append_profile bitcoincash
[ "$zcash_enabled:$zcash_active" = "1:1" ] && append_profile zcash
tor_onion_nodes=
if [ "$tor_inbound" = "1" ]; then
  for node in bitcoin litecoin bitcoincash; do
    case ",$compose_profiles," in *",$node,"*) tor_onion_nodes=${tor_onion_nodes:+$tor_onion_nodes,}$node ;; esac
  done
fi

if [ "$sync_over_clearnet" = 1 ]; then
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

case "$monero_mem_limit_mb" in *[!0-9]*)
  echo "monero.mem_limit_mb must be a whole number of MB (0 = unlimited)."
  exit 1
  ;;
esac
if [ "$monero_mem_limit_mb" = "0" ]; then
  monero_mem_limit=0
else
  if [ "$monero_mem_limit_mb" -lt 64 ]; then
    echo "monero.mem_limit_mb must be 0 or at least 64."
    exit 1
  fi
  monero_mem_limit="${monero_mem_limit_mb}m"
fi
for cpus_pair in "bitcoin:$bitcoin_cpus" "monero:$monero_cpus" "tari:$tari_cpus" "litecoin:$litecoin_cpus" "dogecoin:$dogecoin_cpus" "bitcoincash:$bitcoincash_cpus" "zcash:$zcash_cpus"; do
  if ! awk -v value="${cpus_pair#*:}" 'BEGIN { exit !(value ~ /^[0-9]+([.][0-9][0-9]?)?$/ && value <= 256) }'; then
    echo "${cpus_pair%%:*}.cpus must be a number of CPUs (0 = unlimited)."
    exit 1
  fi
done
case "$thermal_limit_c" in *[!0-9]*)
  echo "host.thermal_limit_c must be a whole number of degrees Celsius (0 = off)."
  exit 1
  ;;
esac
case "$thermal_resume_c" in *[!0-9]*)
  echo "host.thermal_resume_c must be a whole number of degrees Celsius (0 = limit minus 10)."
  exit 1
  ;;
esac
if [ "$thermal_limit_c" != 0 ]; then
  if [ "$thermal_limit_c" -lt 50 ] || [ "$thermal_limit_c" -gt 110 ]; then
    echo "host.thermal_limit_c must be between 50 and 110."
    exit 1
  fi
  [ "$thermal_resume_c" != 0 ] || thermal_resume_c=$((thermal_limit_c - 10))
  if [ "$thermal_resume_c" -ge "$thermal_limit_c" ] || [ "$thermal_resume_c" -lt 20 ]; then
    echo "host.thermal_resume_c must be between 20 and host.thermal_limit_c."
    exit 1
  fi
fi

# Core refuses to add a block filter index once blocks are pruned (init.cpp, v32.1).
prev_prune=$(sed -n 's/^BITCOIN_PRUNE=//p' "$env_file" 2>/dev/null || true)
prev_filter=$(sed -n 's/^BITCOIN_BLOCKFILTERINDEX=//p' "$env_file" 2>/dev/null || true)
if [ "$blockfilterindex" = "1" ] && [ "$prune" != "0" ] && [ -n "$prev_prune" ] && [ "$prev_prune" != "0" ] && [ "$prev_filter" != "1" ]; then
  echo "The previous configuration shows a pruned node without blockfilterindex, and Bitcoin Core cannot add"
  echo "a block filter index to an already pruned node. Enable it before the first prune, or re-download the"
  echo "chain into a new data directory with blockfilterindex set from the start."
  exit 1
fi

# Zakura only builds the transparent address index on a database that never used checkpoint sync.
prev_zcash_enabled=$(sed -n 's/^ZCASH_ENABLED=//p' "$env_file" 2>/dev/null || true)
prev_zcash_index=$(sed -n 's/^ZCASH_ADDRESS_INDEX=//p' "$env_file" 2>/dev/null || true)
prev_zcash_dir=$(sed -n 's/^ZCASH_DATA_DIR=//p' "$env_file" 2>/dev/null || true)
zcash_dir_used=0
if [ ! -e "$zcash_data_dir/.address-index" ] && [ -n "$(ls -A "$zcash_data_dir" 2>/dev/null)" ]; then zcash_dir_used=1; fi
if [ "$zcash_address_index" = "1" ] && { [ "$zcash_dir_used" = 1 ] || { [ "$prev_zcash_enabled" = "1" ] && [ "$prev_zcash_index" != "1" ] && [ "$prev_zcash_dir" = "$zcash_data_dir" ]; }; }; then
  echo "The Zcash data directory already holds a database built without zcash.address_index, and Zakura cannot add the"
  echo "transparent address index to that database. Point zcash.data_dir at a fresh directory to resync with it."
  exit 1
fi

prev_bitcoincash_enabled=$(sed -n 's/^BITCOINCASH_ENABLED=//p' "$env_file" 2>/dev/null || true)
prev_bitcoincash_prune=$(sed -n 's/^BITCOINCASH_PRUNE=//p' "$env_file" 2>/dev/null || true)
prev_bitcoincash_dir=$(sed -n 's/^BITCOINCASH_DATA_DIR=//p' "$env_file" 2>/dev/null || true)
bitcoincash_dir_pruned=0
if [ -e "$bitcoincash_data_dir/blocks/index" ] && [ ! -e "$bitcoincash_data_dir/blocks/blk00000.dat" ]; then bitcoincash_dir_pruned=1; fi
if [ "$bitcoincash_prune" = "0" ] && { [ "$bitcoincash_dir_pruned" = 1 ] || { [ "$prev_bitcoincash_enabled" = "1" ] && [ -n "$prev_bitcoincash_prune" ] && [ "$prev_bitcoincash_prune" != "0" ] && [ "$prev_bitcoincash_dir" = "$bitcoincash_data_dir" ]; }; }; then
  echo "The Bitcoin Cash data directory holds a pruned chain, and a pruned node cannot become archival. Point"
  echo "bitcoincash.data_dir at a fresh directory to download the full chain with bitcoincash.prune_mb set to 0."
  exit 1
fi

prev_dogecoin_enabled=$(sed -n 's/^DOGECOIN_ENABLED=//p' "$env_file" 2>/dev/null || true)
prev_dogecoin_prune=$(sed -n 's/^DOGECOIN_PRUNE=//p' "$env_file" 2>/dev/null || true)
prev_dogecoin_dir=$(sed -n 's/^DOGECOIN_DATA_DIR=//p' "$env_file" 2>/dev/null || true)
dogecoin_dir_pruned=0
if [ -e "$dogecoin_data_dir/blocks/index" ] && [ ! -e "$dogecoin_data_dir/blocks/blk00000.dat" ]; then dogecoin_dir_pruned=1; fi
if [ "$dogecoin_prune" = "0" ] && { [ "$dogecoin_dir_pruned" = 1 ] || { [ "$prev_dogecoin_enabled" = "1" ] && [ -n "$prev_dogecoin_prune" ] && [ "$prev_dogecoin_prune" != "0" ] && [ "$prev_dogecoin_dir" = "$dogecoin_data_dir" ]; }; }; then
  echo "The Dogecoin data directory holds a pruned chain, and a pruned node cannot become archival or rescan for a"
  echo "watch-only wallet. Point dogecoin.data_dir at a fresh directory to download the full chain with dogecoin.prune_mb set to 0."
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
TOR_CLEARNET=$tor_clearnet
TOR_INBOUND=$tor_inbound
TOR_ONION_NODES=$tor_onion_nodes
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
BITCOIN_BLOCKFILTERINDEX=$blockfilterindex
BITCOIN_MEM_LIMIT=$bitcoin_mem_limit
BITCOIN_CPUS=$bitcoin_cpus
THERMAL_LIMIT_C=$thermal_limit_c
THERMAL_RESUME_C=$thermal_resume_c
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
MONERO_CPUS=$monero_cpus
MONERO_MEM_LIMIT=$monero_mem_limit
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
TARI_GRPC_EXTRA_METHODS=$tari_grpc_extra_methods
TARI_RESOURCE_PROFILE=$tari_resource_profile
TARI_STEADY_CPUS=$tari_steady_cpus
TARI_STEADY_MEM_LIMIT_MB=$tari_steady_mem_limit_mb
TARI_CPUS=$tari_cpus
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
LITECOIN_CPUS=$litecoin_cpus
DOGECOIN_ENABLED=$dogecoin_enabled
DOGECOIN_ACTIVE=$dogecoin_active
DOGECOIN_RPC_URL=http://172.29.0.34:22555
DOGECOIN_RPC_USER=$dogecoin_rpc_user
DOGECOIN_RPC_PASSWORD=$dogecoin_rpc_password
DOGECOIN_DATA_DIR=$dogecoin_data_dir
DOGECOIN_PRUNE=$dogecoin_prune
DOGECOIN_WATCH_WALLET=$dogecoin_watch_wallet
DOGECOIN_RPC_EXPOSE=$dogecoin_rpc_expose
DOGECOIN_RPC_BIND_IP=$dogecoin_rpc_bind_ip
DOGECOIN_RPC_PORT=$dogecoin_rpc_port
DOGECOIN_RESOURCE_PROFILE=$dogecoin_resource_profile
DOGECOIN_STEADY_CPUS=$dogecoin_steady_cpus
DOGECOIN_STEADY_MEM_LIMIT_MB=$dogecoin_steady_mem_limit_mb
DOGECOIN_CPUS=$dogecoin_cpus
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
BITCOINCASH_CPUS=$bitcoincash_cpus
ZCASH_ENABLED=$zcash_enabled
ZCASH_ACTIVE=$zcash_active
ZCASH_RPC_URL=http://172.29.0.36:8232
ZCASH_RPC_USER=$zcash_rpc_user
ZCASH_RPC_PASSWORD=$zcash_rpc_password
ZCASH_DATA_DIR=$zcash_data_dir
ZCASH_ADDRESS_INDEX=$zcash_address_index
ZCASH_TOML=$zcash_toml
ZCASH_RPC_EXPOSE=$zcash_rpc_expose
ZCASH_RPC_BIND_IP=$zcash_rpc_bind_ip
ZCASH_RPC_PORT=$zcash_rpc_port
ZCASH_RESOURCE_PROFILE=$zcash_resource_profile
ZCASH_STEADY_CPUS=$zcash_steady_cpus
ZCASH_STEADY_MEM_LIMIT_MB=$zcash_steady_mem_limit_mb
ZCASH_CPUS=$zcash_cpus
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
if [ "$zcash_address_index" = "1" ]; then : >"$zcash_data_dir/.address-index" 2>/dev/null || true; fi
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
