# shellcheck shell=sh disable=SC2154
# Sourced by node-control, node-port-control and node-resource-profile; needs docker_bin, project_name, stack_root, state_root and config_root from the caller, plus ip_bin, ifconfig_bin and host_os for host_addresses and dashboard_id for host_event.

container_id() {
  "$docker_bin" ps -aq \
    --filter "label=com.docker.compose.project=$project_name" \
    --filter "label=com.docker.compose.service=$1" | head -n 1
}

set_project_name() {
  project_name=${BITCOIN_STACK_PROJECT_NAME:-$(env_get COMPOSE_PROJECT_NAME 2>/dev/null)}
  if [ -z "$project_name" ]; then
    case "${BITCOIN_STACK_PACKAGE_MANAGED:-0}" in brew) project_name=bitcoin-starter-stack-brew ;; 1 | apt) project_name=bitcoin-starter-stack-apt ;; *) project_name=bitcoin-starter-stack ;; esac
  fi
}

canonical_config() {
  if [ -f "$1" ]; then jq -S -c . "$1" 2>/dev/null; else printf '{}\n'; fi
}

config_pending() {
  current_approved=$(canonical_config "$config_file") || return 0
  saved_approved=$(canonical_config "$approved_config") || return 0
  [ -f "$approved_config" ] && [ "$current_approved" = "$saved_approved" ] && return 1
  return 0
}

approve_config() {
  BITCOIN_STACK_ROOT="$stack_root" BITCOIN_STACK_STATE_DIR="$state_root" BITCOIN_STACK_CONFIG_DIR="$config_root" \
    BITCOIN_STACK_CONFIG_LOCK_HELD=1 "$stack_root/stack" approve-rendered-config >/dev/null 2>&1
}

remember_seen_id() {
  printf '%s\n' "$2" >>"$1"
  chmod 600 "$1"
  seen_ring_tmp=$(mktemp "$(dirname "$1")/.seen.XXXXXX")
  tail -n 256 "$1" >"$seen_ring_tmp"
  chmod 600 "$seen_ring_tmp"
  mv "$seen_ring_tmp" "$1"
}

redact_last_line() {
  redact_fallback=$1
  shift
  awk -v fallback="$redact_fallback" '
    { gsub(/[[:cntrl:]]\[[0-9;]*[A-Za-z]/, ""); gsub(/[[:cntrl:]]/, ""); if ($0 ~ /[^[:space:]]/) last = $0 }
    END {
      lower = tolower(last)
      if (lower ~ /passw|secret|token|auth|login|cookie|phrase|seed|mnemonic|private|prv|pub[0-9a-z]|api.?key|bearer|onion|:\/\/[^\/ ]*@/) last = ""
      n = split(last, words, /[^A-Za-z0-9+=_-]+/)
      for (i = 1; i <= n; i++) if (length(words[i]) >= 40) last = ""
      print (last == "" ? fallback : substr(last, 1, 240))
    }' "$@" 2>/dev/null || true
}

node_label() {
  case "$1" in (bitcoincash) printf '%s\n' "Bitcoin Cash" ;; (*) printf '%s%s\n' "$(printf '%s' "$1" | cut -c1 | tr '[:lower:]' '[:upper:]')" "$(printf '%s' "$1" | cut -c2-)" ;; esac
}

host_event() {
  printf '%sZ Host: %s\n' "${2:-$(date -u +%Y-%m-%dT%H:%M:%S)}" "$1" |
    "$docker_bin" exec -i -u 1000 "$dashboard_id" sh -c 'cat >> /state/events.log' >/dev/null 2>&1
}

host_addresses() {
  case "$host_os" in
    Darwin) "$ifconfig_bin" -a 2>/dev/null ;;
    *) "$ip_bin" -o -4 addr show 2>/dev/null ;;
  esac
}

host_has_ipv4() {
  addresses=$(host_addresses) || return 0
  printf '%s\n' "$addresses" | grep -Fq -e "inet $1/" -e "inet $1 "
}
