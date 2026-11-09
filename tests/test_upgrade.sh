#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fail() { echo "FAIL: $1"; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
export PROVIDER_LOG="$tmp/providers.log"
for provider in git docker curl wget brew apt sudo; do
  cat >"$tmp/bin/$provider" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$0 $*" >>"$PROVIDER_LOG"
exit 97
SH
  chmod +x "$tmp/bin/$provider"
done

out=$(PATH="$tmp/bin:$PATH" ./stack upgrade)
printf '%s\n' "$out" | grep -q 'does not update itself' || fail "manual-update policy is missing"
printf '%s\n' "$out" | grep -q 'sudo apt install --only-upgrade node-manager' || fail "APT instructions are missing"
printf '%s\n' "$out" | grep -q 'brew upgrade node-manager' || fail "Homebrew instructions are missing"
printf '%s\n' "$out" | grep -q 'node-manager/releases/latest' || fail "GitHub instructions are missing"
printf '%s\n' "$out" | grep -q './stack pull' || fail "runtime image instructions are missing"
printf '%s\n' "$out" | grep -q './stack apply' || fail "runtime apply instructions are missing"
[ ! -e "$PROVIDER_LOG" ] || fail "upgrade invoked an external update provider"

upgrade_agent_body=$(sed -n '/^upgrade_agent()/,/^}/p' stack)
printf '%s\n' "$upgrade_agent_body" | grep -q 'exec .* host-service' || fail "legacy upgrade-agent does not replace itself with host-service"
printf '%s\n' "$upgrade_agent_body" | grep -Eq 'do_upgrade|upgrade\.request' && fail "legacy upgrade-agent retains an update path"

echo "PASS: test_upgrade.sh (manual guidance only; no self-update provider invoked)"
