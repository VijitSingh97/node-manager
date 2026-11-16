#!/usr/bin/env bash
# ./stack CLI: usage paths and the docker-free backup/restore round-trip.
set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
  echo "FAIL: $1"
  exit 1
}

[ -x scripts/node-control ] || fail "source node-control helper is not executable"
grep -Fq 'for helper in node-control node-port-control' stack ||
  fail "host-service does not run node controls"
for private_runtime_path in credential-control/transaction.after port-control/request.json node-control/transaction.after; do
  git check-ignore -q "$private_runtime_path" || fail "private runtime path is not ignored: $private_runtime_path"
done

repo_root=$PWD
tmp=$(mktemp -d)
legacy_pid=
unrelated_pid=
cleanup() {
  [ -z "$legacy_pid" ] || kill "$legacy_pid" 2>/dev/null || true
  [ -z "$unrelated_pid" ] || kill "$unrelated_pid" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT
cp stack configure.sh config.example.json "$tmp/"
printf '1.2.3\n' >"$tmp/VERSION"
cd "$tmp"

# help works, unknown command fails loudly
./stack help | grep -q "Usage:" || fail "help does not print usage"
./stack definitely-not-a-command >/dev/null 2>&1 && fail "unknown command exited 0"

# backup captures config + .env; restore round-trips them
cat >config.json <<'EOF'
{"bitcoin": {"node_username": "cliuser", "node_password": "clipass1"}, "dashboard": {"password": "cliconfig123"}}
EOF
./configure.sh >/dev/null
./stack backup | grep -q "Wrote .*backups/" || fail "backup did not report an archive"
archive=$(ls backups/stack-backup-*.tar.gz)
tar tzf "$archive" | grep -q "config.json" || fail "backup missing config.json"
tar tzf "$archive" | grep -q ".env" || fail "backup missing .env"

echo '{"bitcoin": {"node_username": "changed", "node_password": "changed1"}}' >config.json
rm .env
./stack restore -y "$archive" >/dev/null
grep -q "cliuser" config.json || fail "restore did not bring back config.json"
grep -q "^BITCOIN_RPC_USER=cliuser$" .env || fail "restore did not bring back .env"
[ -f .approved-config.json ] || fail "explicit restore did not approve the restored config"
cmp -s .env .approved.env || fail "explicit restore did not approve the restored environment"
# shellcheck disable=SC2012 # fixed filename, ls is the portable way to read the mode string
perms=$(ls -l .env | cut -c1-10)
[ "$perms" = "-rw-------" ] || fail "restored .env is not mode 600"
# shellcheck disable=SC2012 # fixed filename, ls is the portable way to read the mode string
perms=$(ls -l config.json | cut -c1-10)
[ "$perms" = "-rw-------" ] || fail "restored config.json is not mode 600"

# restore without -y aborts on anything but yes
echo n | ./stack restore "$archive" >/dev/null 2>&1 && fail "restore proceeded without confirmation"

# backup/restore round-trips each node's onion keys through the tor volume
onion_stub=$tmp/onion-stubs
fake_tor=$tmp/fake-tor
mkdir -p "$onion_stub" "$fake_tor/bitcoin_onion" "$fake_tor/litecoin_onion"
for node in bitcoin litecoin; do
  printf '%s-secret' "$node" >"$fake_tor/${node}_onion/hs_ed25519_secret_key"
done
cat >"$onion_stub/docker" <<'EOS'
#!/usr/bin/env bash
echo "docker $*" >>"$FAKE_ONION_LOG"
args=("$@")
for i in "${!args[@]}"; do
  [ "${args[$i]}" = cp ] || continue
  src=${args[$((i + 1))]} dst=${args[$((i + 2))]}
  case $src in
    tor:/var/lib/tor/*) [ -d "$FAKE_TOR/${src#tor:/var/lib/tor/}" ] && cp -R "$FAKE_TOR/${src#tor:/var/lib/tor/}" "$dst"; exit ;;
    *:*) exit 1 ;;
  esac
  [ "$dst" = tor:/var/lib/tor/ ] && cp -R "$src" "$FAKE_TOR/"
  exit
done
exit 0
EOS
chmod +x "$onion_stub/docker"
export FAKE_TOR=$fake_tor FAKE_ONION_LOG=$tmp/onion-calls.log
onion_archive=$(PATH="$onion_stub:$PATH" ./stack backup | sed -n 's/^Wrote \([^ ]*\) .*/\1/p')
[ -f "$onion_archive" ] || fail "onion backup did not report its archive"
for node in bitcoin litecoin; do
  tar tzf "$onion_archive" | grep -q "${node}_onion/hs_ed25519_secret_key" || fail "backup missing the $node onion key"
done
tar tzf "$onion_archive" | grep -q bitcoincash_onion && fail "backup invented an onion directory Tor did not have"
mv "$fake_tor" "$tmp/fake-tor-lost"
mkdir "$fake_tor"
onion_restore=$(PATH="$onion_stub:$PATH" ./stack restore -y "$onion_archive")
printf '%s\n' "$onion_restore" | grep -qx 'Restored onion keys: bitcoin_onion litecoin_onion (tor restarted)' ||
  fail "restore did not report the node onion keys"
for node in bitcoin litecoin; do
  [ "$(cat "$fake_tor/${node}_onion/hs_ed25519_secret_key")" = "$node-secret" ] || fail "restore lost the $node onion key"
  grep -q "exec -u root tor chown -R tor:tor /var/lib/tor/${node}_onion$" "$FAKE_ONION_LOG" || fail "restore did not hand the $node onion key to tor"
  grep -q " restart $node$" "$FAKE_ONION_LOG" || fail "restore did not restart $node to advertise its onion"
done
[ "$(grep -c ' restart tor$' "$FAKE_ONION_LOG")" = 1 ] || fail "restore did not restart tor exactly once"

package_root=$tmp/package-root
package_state=$tmp/package-state
package_config=$tmp/package-config
package_cwd=$tmp/package-cwd
package_bin=$tmp/package-bin
mkdir -p "$package_root" "$package_state" "$package_config" "$package_cwd" "$package_bin"
cp stack "$package_root/"
printf '{"bitcoin": {"enabled": false}}\n' >"$package_config/config.json"
printf 'BITCOIN_ENABLED=0\n' >"$package_state/.env"
cat >"$package_bin/docker" <<'EOS'
#!/usr/bin/env bash
[ "${1:-}" != inspect ]
EOS
chmod +x "$package_bin/docker"
touch "$package_state/backups"
(cd "$package_cwd" &&
  PATH="$package_bin:$PATH" BITCOIN_STACK_ROOT="$package_root" \
  BITCOIN_STACK_STATE_DIR="$package_state" BITCOIN_STACK_CONFIG_DIR="$package_config" \
  BITCOIN_STACK_PACKAGE_MANAGED=apt "$package_root/stack" backup >/dev/null 2>&1) &&
  fail "package backup ignored an unusable managed backup path"
[ ! -e "$package_cwd/backups" ] || fail "failed package backup fell back to the caller directory"
rm "$package_state/backups"
package_backup_out=$(cd "$package_cwd" &&
  PATH="$package_bin:$PATH" BITCOIN_STACK_ROOT="$package_root" \
  BITCOIN_STACK_STATE_DIR="$package_state" BITCOIN_STACK_CONFIG_DIR="$package_config" \
  BITCOIN_STACK_PACKAGE_MANAGED=apt "$package_root/stack" backup)
printf '%s\n' "$package_backup_out" | grep -q "Wrote $package_state/backups/stack-backup-" ||
  fail "package backup did not report its managed path"
package_archive=$(find "$package_state/backups" -type f -name 'stack-backup-*.tar.gz' -print)
[ -n "$package_archive" ] || fail "package backup was not written under managed state"
[ ! -e "$package_cwd/backups" ] || fail "package backup was written under the caller directory"
tar tzf "$package_archive" | grep -q 'config.json' || fail "package backup missing managed config"
# shellcheck disable=SC2012 # fixed filename, ls is portable across package targets
[ "$(ls -l "$package_archive" | cut -c1-10)" = "-rw-------" ] ||
  fail "package backup archive is not mode 600"


# ensure_agent: up/apply must start the host-control agent + install the @reboot
# cron entry when a dashboard password is set, and defer to systemd.
# Everything host-touching (crontab, docker, pgrep/pkill, setsid, systemctl) is
# stubbed onto PATH so the real machine is never modified.
stub=$tmp/stubs
mkdir -p "$stub"
export FAKE_CRON="$tmp/fake-crontab" FAKE_LOG="$tmp/stub-calls.log" FAKE_IMAGES_READY="$tmp/images-ready" REAL_JQ_BIN
REAL_JQ_BIN=$(command -v jq)
cat >"$stub/crontab" <<'EOS'
#!/usr/bin/env bash
if [ "${1:-}" = "-l" ]; then [ -f "$FAKE_CRON" ] && cat "$FAKE_CRON" || exit 1; else cat >"$FAKE_CRON"; fi
EOS
cat >"$stub/docker" <<'EOS'
#!/usr/bin/env bash
echo "docker $*" >>"$FAKE_LOG"
case " $* " in
  *' info '*) [ "${FAKE_DOCKER_INFO_FAIL:-0}" != 1 ]; exit ;;
  *' exec dashboard cat /state/dashboard_auth.json '*) [ -f "${FAKE_DASH_AUTH_FILE:-}" ] && cat "$FAKE_DASH_AUTH_FILE"; exit ;;
  *' compose '*' config --images '*) printf 'example.invalid/stack:test\n' ;;
  *' image inspect '*) [ "${FAKE_IMAGES_MISSING:-0}" != 1 ] || [ -f "$FAKE_IMAGES_READY" ]; exit ;;
  *' compose '*' pull '*) [ "${FAKE_PULL_FAIL:-0}" != 1 ] || exit 1; touch "$FAKE_IMAGES_READY"; exit ;;
  *' compose '*' up '*) [ "${FAKE_UP_FAIL:-0}" != 1 ]; exit ;;
  *' ps -aq '*'com.docker.compose.service=dashboard'*) printf 'dashboard-id\n'; exit ;;
  *'com.docker.compose.project.config_files'*) printf '%s/docker-compose.yml\n' "$PWD" ;;
  *'com.docker.compose.project.working_dir'*) printf '%s\n' "$PWD" ;;
  *' inspect --format {{.State.Running}} dashboard-id '*) printf 'true\n' ;;
esac
exit 0
EOS
cat >"$stub/pgrep" <<'EOS'
#!/usr/bin/env bash
exit 1
EOS
cat >"$stub/pkill" <<'EOS'
#!/usr/bin/env bash
echo "pkill $*" >>"$FAKE_LOG"; exit 0
EOS
cat >"$stub/setsid" <<'EOS'
#!/usr/bin/env bash
echo "setsid $*" >>"$FAKE_LOG"; exit 0
EOS
cat >"$stub/nohup" <<'EOS'
#!/usr/bin/env bash
echo "nohup $*" >>"$FAKE_LOG"
exit 0
EOS
cat >"$stub/brew" <<'EOS'
#!/usr/bin/env bash
echo "brew $*" >>"$FAKE_LOG"
[ "${FAKE_BREW_FAIL:-0}" != 1 ]
EOS
cat >"$stub/systemctl" <<'EOS'
#!/usr/bin/env bash
exit 1
EOS
cat >"$stub/uname" <<'EOS'
#!/usr/bin/env bash
printf '%s\n' "${FAKE_UNAME:-Linux}"
EOS
cat >"$stub/flock" <<'EOS'
#!/usr/bin/env bash
echo "flock $*" >>"$FAKE_LOG"
[ "${FAKE_FLOCK_BUSY:-0} ${1:-} ${2:-}" != "1 -n 6" ]
EOS
cat >"$stub/jq" <<'EOS'
#!/usr/bin/env bash
if [ "${FAKE_APPROVE_FAIL:-0}" = 1 ] && [ "${1:-}" = -S ] && [ "${2:-}" = . ] && [ "$#" = 3 ]; then
  exit 73
fi
exec "$REAL_JQ_BIN" "$@"
EOS
chmod +x "$stub"/*

wizard="$tmp/wizard"
mkdir "$wizard"
cp stack configure.sh "$wizard/"
printf '1.2.3\n' >"$wizard/VERSION"
rm -f "$FAKE_LOG"
rm -f "$FAKE_IMAGES_READY"
(cd "$wizard" &&
  BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.116 FAKE_IMAGES_MISSING=1 HOME="$tmp" PATH="$stub:$PATH" ./stack init >"$tmp/wizard-init.log" 2>&1) || {
  cat "$tmp/wizard-init.log"
  fail "init failed with mocked Linux host controls"
}
grep -q 'docker compose .* pull --policy missing' "$FAKE_LOG" || fail "init did not preserve local images while pulling missing ones"
grep -q 'docker compose .* up -d --pull never --no-build' "$FAKE_LOG" || fail "init start allows implicit downloads or builds"
[ "$(jq -r '.bitcoin.enabled' "$wizard/config.json")" = true ] || fail "init did not default to Bitcoin"
dashboard_password=$(jq -r '.dashboard.password' "$wizard/config.json")
[[ "$dashboard_password" =~ ^[a-f0-9]{32}$ ]] || fail "init did not generate a 32-character dashboard password"
[ "$(stat -c '%a' "$wizard/config.json" 2>/dev/null || stat -f '%Lp' "$wizard/config.json")" = 600 ] || fail "init config is not mode 600"
[ "$(jq -r '.dashboard.bind_ip' "$wizard/config.json")" = auto ] || fail "init did not keep the dashboard bind dynamic"
[ "$(jq -r '.dashboard.launch_at_boot' "$wizard/config.json")" = false ] || fail "init enabled launch at boot"
grep -q '^DASHBOARD_BIND_IP=192.168.1.116$' "$wizard/.env" || fail "init did not use the detected LAN address"
grep -q '^BITCOIN_PRUNE=10000$' "$wizard/.env" || fail "init did not default to a pruned node"
grep -q '^COMPOSE_PROFILES=bitcoin$' "$wizard/.env" || fail "init enabled extra nodes"
grep -q '^BITCOIN_RPC_EXPOSE=0$' "$wizard/.env" || fail "init exposed Bitcoin RPC"
[ -f "$wizard/.approved-config.json" ] || fail "init did not approve its config"
grep -q "Dashboard — username: admin  password: $dashboard_password" "$tmp/wizard-init.log" || fail "init did not print the generated dashboard credential"
grep -q 'Bitcoin RPC — username: admin  password:' "$tmp/wizard-init.log" || fail "init did not print the selected node credentials"
wizard_config_hash=$(shasum -a 256 "$wizard/config.json" | cut -d' ' -f1)
(cd "$wizard" && HOME="$tmp" PATH="$stub:$PATH" ./stack init >"$tmp/wizard-repeat.log")
[ "$(shasum -a 256 "$wizard/config.json" | cut -d' ' -f1)" = "$wizard_config_hash" ] || fail "repeated init rotated the generated dashboard password"
grep -Fq "$dashboard_password" "$tmp/wizard-repeat.log" && fail "repeated init replayed a potentially superseded dashboard password"

wizard_loaded="$tmp/wizard-loaded"
mkdir "$wizard_loaded"
cp stack configure.sh "$wizard_loaded/"
printf '1.2.3\n' >"$wizard_loaded/VERSION"
rm -f "$FAKE_LOG"
cat >"$wizard_loaded/config.json" <<'EOF'
{"bitcoin":{"enabled":false},"monero":{"enabled":true,"data_dir":"./custom-monero","node_password":"moneropassword1234567890123456"},"dashboard":{"password":"customdashboardpassword","bind_ip":"192.168.1.118"}}
EOF
(cd "$wizard_loaded" && HOME="$tmp" PATH="$stub:$PATH" ./stack init >"$tmp/wizard-loaded.log")
grep -q 'docker compose .* pull' "$FAKE_LOG" && fail "init pulled images that were already local"
grep -q 'docker compose .* up -d --pull never --no-build' "$FAKE_LOG" || fail "init did not start local images"
[ "$(jq -r '.monero.data_dir' "$wizard_loaded/config.json")" = ./custom-monero ] || fail "init replaced a preseeded data path"
[ "$(jq -r '.monero.node_password' "$wizard_loaded/config.json")" = moneropassword1234567890123456 ] || fail "init replaced a preseeded credential"
grep -q '^COMPOSE_PROFILES=monero$' "$wizard_loaded/.env" || fail "init replaced a preseeded node selection"
grep -q '^DASHBOARD_PASSWORD=customdashboardpassword$' "$wizard_loaded/.env" || fail "init replaced a preseeded dashboard password"
grep -q 'Dashboard password — use the saved login' "$tmp/wizard-loaded.log" || fail "init did not avoid replaying the preset dashboard credential"
grep -q 'Monero RPC — username: admin  password: moneropassword1234567890123456' "$tmp/wizard-loaded.log" || fail "init did not print the selected fixture node credential"
loaded_config=$(shasum -a 256 "$wizard_loaded/config.json" | cut -d' ' -f1)
(cd "$wizard_loaded" && HOME="$tmp" PATH="$stub:$PATH" ./stack init >/dev/null)
[ "$(shasum -a 256 "$wizard_loaded/config.json" | cut -d' ' -f1)" = "$loaded_config" ] || fail "repeated init changed a preseeded config"

wizard_failed="$tmp/wizard-failed"
mkdir "$wizard_failed"
cp stack configure.sh "$wizard_failed/"
printf '1.2.3\n' >"$wizard_failed/VERSION"
rm -f "$FAKE_LOG"
rm -f "$FAKE_IMAGES_READY"
(cd "$wizard_failed" &&
  BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.116 FAKE_IMAGES_MISSING=1 FAKE_PULL_FAIL=1 HOME="$tmp" PATH="$stub:$PATH" ./stack init >/dev/null 2>&1) &&
  fail "init ignored a missing-image pull failure"
grep -q 'docker compose .* pull --policy missing' "$FAKE_LOG" || fail "init failure did not attempt a missing-only pull"
grep -q 'docker compose .* up -d' "$FAKE_LOG" && fail "init started after a failed pull"

wizard_no_docker="$tmp/wizard-no-docker"
mkdir "$wizard_no_docker"
cp stack configure.sh "$wizard_no_docker/"
printf '1.2.3\n' >"$wizard_no_docker/VERSION"
(cd "$wizard_no_docker" &&
  BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.116 FAKE_DOCKER_INFO_FAIL=1 HOME="$tmp" PATH="$stub:$PATH" ./stack init >"$tmp/no-docker.log" 2>&1) &&
  fail "init succeeded while Docker was unavailable"
[ -f "$wizard_no_docker/config.json" ] || fail "init discarded its config when Docker was unavailable"
[ "$(jq -r '.dashboard.password | length' "$wizard_no_docker/config.json")" = 32 ] || fail "no-Docker init did not persist its generated dashboard password"
no_docker_password=$(jq -r '.dashboard.password' "$wizard_no_docker/config.json")
grep -Fq "Dashboard — username: admin  password: $no_docker_password" "$tmp/no-docker.log" || fail "failed first init concealed the generated dashboard login"
[ ! -f "$wizard_no_docker/.approved-config.json" ] || fail "init approved a config that was not started"
grep -q 'Configuration was saved, but Docker is not ready' "$tmp/no-docker.log" || fail "init did not explain how to recover when Docker was unavailable"

pending="$tmp/pending-config"
mkdir "$pending"
cp stack configure.sh "$pending/"
printf '1.2.3\n' >"$pending/VERSION"
(cd "$pending" && BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.116 HOME="$tmp" PATH="$stub:$PATH" ./stack init >/dev/null)
(cd "$pending" && BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.117 HOME="$tmp" PATH="$stub:$PATH" ./stack up >/dev/null)
grep -q '^DASHBOARD_BIND_IP=192.168.1.117$' "$pending/.env" || fail "start did not refresh an automatic LAN bind"
jq '.dashboard.port=8088' "$pending/config.json" >"$pending/config.next"
mv "$pending/config.next" "$pending/config.json"
(cd "$pending" && BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.117 ./configure.sh >/dev/null)
grep -q '^DASHBOARD_PORT=8088$' "$pending/.env" || fail "pending config test did not render its changed environment"
out=$(cd "$pending" && HOME="$tmp" PATH="$stub:$PATH" ./stack up)
printf '%s' "$out" | grep -q 'Config change detected' || fail "noninteractive start did not report a pending config"
grep -q '^DASHBOARD_PORT=80$' "$pending/.env" || fail "noninteractive start silently applied a pending config"
[ "$(jq -r '.dashboard.port // 80' "$pending/.approved-config.json")" = 80 ] || fail "noninteractive start approved a pending config"
out=$(cd "$pending" && printf 'n\n' | BITCOIN_STACK_INTERACTIVE=1 HOME="$tmp" PATH="$stub:$PATH" ./stack restart)
printf '%s' "$out" | grep -q 'Run new config?' || fail "manual restart did not ask before applying a pending config"
grep -q '^DASHBOARD_PORT=80$' "$pending/.env" || fail "declined config was applied"
(cd "$pending" && printf 'y\n' | BITCOIN_STACK_INTERACTIVE=1 HOME="$tmp" PATH="$stub:$PATH" ./stack restart >/dev/null)
grep -q '^DASHBOARD_PORT=8088$' "$pending/.env" || fail "accepted config was not applied"
[ "$(jq -r '.dashboard.port' "$pending/.approved-config.json")" = 8088 ] || fail "accepted config was not approved"
jq '.dashboard.port=8090' "$pending/config.json" >"$pending/config.next"
mv "$pending/config.next" "$pending/config.json"
(cd "$pending" && FAKE_UP_FAIL=1 HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null 2>&1) && fail "failed apply returned success"
grep -q '^DASHBOARD_PORT=8088$' "$pending/.env" || fail "failed apply did not restore the previous runtime environment"
[ "$(jq -r '.dashboard.port' "$pending/.approved-config.json")" = 8088 ] || fail "failed apply replaced the approved config"
cp "$pending/.approved.env" "$pending/approved-env.before"
: >"$FAKE_LOG"
(cd "$pending" && FAKE_APPROVE_FAIL=1 HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null 2>&1) && fail "apply succeeded after approval failed"
grep -q '^DASHBOARD_PORT=8088$' "$pending/.env" || fail "approval failure did not restore the previous runtime environment"
[ "$(jq -r '.dashboard.port' "$pending/.approved-config.json")" = 8088 ] || fail "approval failure replaced the approved config"
cmp -s "$pending/.approved.env" "$pending/approved-env.before" || fail "approval failure replaced the approved environment"
[ "$(grep -c 'docker compose .* up -d --pull never --no-build' "$FAKE_LOG")" -ge 2 ] || fail "approval failure did not reconcile the previous runtime"
cp "$pending/config.json" "$pending/config.before-invalid"
printf '{\n' >"$pending/config.json"
: >"$FAKE_LOG"
(cd "$pending" && HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null 2>&1) && fail "invalid config apply returned success"
grep -q '^docker ' "$FAKE_LOG" && fail "invalid config apply touched the runtime"
grep -q '^DASHBOARD_PORT=8088$' "$pending/.env" || fail "invalid config apply did not restore the previous environment"
[ "$(jq -r '.dashboard.port' "$pending/.approved-config.json")" = 8088 ] || fail "invalid config apply replaced the approved config"
cmp -s "$pending/.approved.env" "$pending/approved-env.before" || fail "invalid config apply replaced the approved environment"
mv "$pending/config.before-invalid" "$pending/config.json"

fresh_invalid="$tmp/fresh-invalid-config"
mkdir "$fresh_invalid"
cp stack configure.sh "$fresh_invalid/"
printf '1.2.3\n' >"$fresh_invalid/VERSION"
printf '{\n' >"$fresh_invalid/config.json"
: >"$FAKE_LOG"
(cd "$fresh_invalid" && HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null 2>&1) && fail "fresh invalid config apply returned success"
grep -q '^docker ' "$FAKE_LOG" && fail "fresh invalid config apply touched the runtime"
[ ! -f "$fresh_invalid/.env" ] || fail "fresh invalid config apply retained an environment"
[ ! -f "$fresh_invalid/.approved-config.json" ] || fail "fresh invalid config apply created an approved config"
[ ! -f "$fresh_invalid/.approved.env" ] || fail "fresh invalid config apply created an approved environment"

wizard_approval_failed="$tmp/wizard-approval-failed"
mkdir "$wizard_approval_failed"
cp stack configure.sh "$wizard_approval_failed/"
printf '1.2.3\n' >"$wizard_approval_failed/VERSION"
: >"$FAKE_LOG"
(cd "$wizard_approval_failed" && BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.116 FAKE_APPROVE_FAIL=1 HOME="$tmp" PATH="$stub:$PATH" ./stack init >/dev/null 2>&1) &&
  fail "init succeeded after approval failed"
grep -q 'docker compose .* down' "$FAKE_LOG" || fail "fresh init approval failure did not stop the unapproved runtime"
[ ! -f "$wizard_approval_failed/.env" ] || fail "fresh init approval failure retained an unapproved environment"
[ ! -f "$wizard_approval_failed/.approved-config.json" ] || fail "fresh init approval failure retained an approved config"
[ ! -f "$wizard_approval_failed/.approved.env" ] || fail "fresh init approval failure retained an approved environment"

migration="$tmp/migration-config"
mkdir "$migration"
cp stack configure.sh "$migration/"
printf '1.2.3\n' >"$migration/VERSION"
echo '{"dashboard":{"port":80,"password":"migration123"}}' >"$migration/config.json"
(cd "$migration" && BITCOIN_STACK_DEFAULT_BIND_IP=192.168.1.116 ./configure.sh >/dev/null)
echo '{"dashboard":{"port":8088}}' >"$migration/config.json"
out=$(cd "$migration" && HOME="$tmp" PATH="$stub:$PATH" ./stack up)
printf '%s' "$out" | grep -q 'Config change detected' || fail "legacy runtime adoption did not report the unapproved config"
grep -q '^DASHBOARD_PORT=80$' "$migration/.env" || fail "legacy runtime adoption silently applied config"
jq -e 'type == "null"' "$migration/.approved-config.json" >/dev/null || fail "legacy runtime adoption claimed an unknown config was approved"

approved_empty="$tmp/approved-empty-config"
mkdir "$approved_empty"
cp stack configure.sh "$approved_empty/"
printf '1.2.3\n' >"$approved_empty/VERSION"
printf '{}\n' >"$approved_empty/.approved-config.json"
printf 'DASHBOARD_PORT=8099\nSTACK_RESTART_POLICY=on-failure:5\nCOMPOSE_PROFILES=monero\nBITCOIN_ENABLED=0\nMONERO_ENABLED=1\nMONERO_RPC_USER=fixture-admin\nMONERO_RPC_PASSWORD=fixture-password\n' >"$approved_empty/.approved.env"
printf 'DASHBOARD_PORT=9000\n' >"$approved_empty/.env"
(cd "$approved_empty" && HOME="$tmp" PATH="$stub:$PATH" ./stack up >/dev/null)
grep -q '^DASHBOARD_PORT=8099$' "$approved_empty/.env" || fail "missing empty config did not restore the approved runtime"
grep -q '^COMPOSE_PROFILES=monero$' "$approved_empty/.env" || fail "missing empty config replaced the approved node selection"
grep -q '^MONERO_RPC_PASSWORD=fixture-password$' "$approved_empty/.env" || fail "missing empty config replaced the approved credential"

echo '{"dashboard": {"password": "hunterpassword12", "control": true}}' >config.json

# A legacy dashboard.control key is ignored, and only this root's loaded old agent is retired.
sleep 60 &
legacy_pid=$!
sleep 60 &
unrelated_pid=$!
proc_fixture=$tmp/proc-fixture
unrelated_root=$tmp/unrelated-stack
mkdir -p "$proc_fixture/$legacy_pid" "$proc_fixture/$unrelated_pid" "$unrelated_root"
ln -s "$tmp" "$proc_fixture/$legacy_pid/cwd"
ln -s "$unrelated_root" "$proc_fixture/$unrelated_pid/cwd"
printf '/bin/bash\0./stack\0upgrade-agent\0' >"$proc_fixture/$legacy_pid/cmdline"
printf '/bin/bash\0./stack\0upgrade-agent\0' >"$proc_fixture/$unrelated_pid/cmdline"
: >"$FAKE_LOG"
out=$(BITCOIN_STACK_PROC_ROOT="$proc_fixture" HOME="$tmp" PATH="$stub:$PATH" ./stack apply)
kill -0 "$legacy_pid" 2>/dev/null && fail "legacy upgrade-agent for this root survived migration"
wait "$legacy_pid" 2>/dev/null || true
rm -rf "${proc_fixture:?}/${legacy_pid:?}"
legacy_pid=
kill -0 "$unrelated_pid" 2>/dev/null || fail "legacy migration stopped another installation"
kill "$unrelated_pid"
wait "$unrelated_pid" 2>/dev/null || true
unrelated_pid=
awk '/^flock 8$/ { lock=NR } /docker compose .* up -d --pull never --no-build/ { up=NR } /^flock -u 8$/ { unlock=NR } END { exit !(lock && lock < up && up < unlock) }' "$FAKE_LOG" ||
  fail "apply did not hold the config lock through Compose"
grep -q 'docker compose .* exec -T -u 1000 dashboard sh -c .*node-runtime.json' "$FAKE_LOG" ||
  fail "apply did not synchronize dashboard node runtime state"
grep -q "stack host-service" "$FAKE_CRON" || fail "ensure_agent: no @reboot cron entry installed"
grep -q "^@reboot" "$FAKE_CRON" || fail "ensure_agent: cron entry is not @reboot"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  grep -q 'nohup env .*stack host-service' "$FAKE_LOG" 2>/dev/null && break
  sleep 0.3
done
grep -q 'nohup env .*stack host-service' "$FAKE_LOG" || fail "ensure_agent: host service not started"
printf '%s' "$out" | grep -q "host-service: started" || fail "ensure_agent: start not reported"

sleep 60 &
legacy_pid=$!
mkdir -p "$proc_fixture/$legacy_pid"
ln -s "$tmp" "$proc_fixture/$legacy_pid/cwd"
printf '/bin/bash\0./stack\0upgrade-agent\0' >"$proc_fixture/$legacy_pid/cmdline"
FAKE_FLOCK_BUSY=1 BITCOIN_STACK_PROC_ROOT="$proc_fixture" HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null
kill -0 "$legacy_pid" 2>/dev/null && fail "legacy upgrade-agent survived while host-service lock was held"
wait "$legacy_pid" 2>/dev/null || true
rm -rf "${proc_fixture:?}/${legacy_pid:?}"
legacy_pid=

# idempotent: a second apply must not duplicate the cron entry
HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null
[ "$(grep -Fc "cd $tmp && ./stack host-service" "$FAKE_CRON")" = "1" ] || fail "ensure_agent: cron entry duplicated"
grep -q 'docker compose .* up -d --pull never --no-build' "$FAKE_LOG" || fail "apply allows implicit image downloads or builds"
: >"$FAKE_LOG"
HOME="$tmp" PATH="$stub:$PATH" ./stack apply-port-settings >/dev/null
grep -q 'docker compose .* up -d --pull never --no-build --no-deps tor dashboard' "$FAKE_LOG" ||
  fail "port settings apply was not limited to Tor and dashboard"
: >"$FAKE_LOG"
HOME="$tmp" PATH="$stub:$PATH" ./stack up >/dev/null
awk '/^flock 8$/ { lock=NR } /docker compose .* up -d --pull never --no-build/ { up=NR } /^flock -u 8$/ { unlock=NR } END { exit !(lock && lock < up && up < unlock) }' "$FAKE_LOG" ||
  fail "up did not hold the config lock through Compose"
: >"$FAKE_LOG"
HOME="$tmp" PATH="$stub:$PATH" ./stack up tari-wallet >/dev/null
grep -q 'docker compose .* up -d --pull never --no-build tari-wallet' "$FAKE_LOG" || fail "up rejected the Tari wallet service"
HOME="$tmp" PATH="$stub:$PATH" ./stack up --build >/dev/null 2>&1 && fail "up allows an implicit build"

# Explicit pulls use the host Docker configuration and report pull failures.
rm -f "$FAKE_LOG"
HOME="$tmp" PATH="$stub:$PATH" ./stack pull >/dev/null || fail "host Docker pull failed"
grep -q 'docker compose .* pull' "$FAKE_LOG" || fail "pull did not reach Compose"
grep -q 'docker info' "$FAKE_LOG" && fail "pull inspected or changed host Docker proxy settings"
rm -f "$FAKE_LOG"
HOME="$tmp" PATH="$stub:$PATH" ./stack pull --policy missing >/dev/null || fail "pull policy was rejected"
grep -q 'docker compose .* pull --policy missing' "$FAKE_LOG" || fail "pull arguments did not reach Compose"
FAKE_PULL_FAIL=1 HOME="$tmp" PATH="$stub:$PATH" ./stack pull >/dev/null 2>&1 && fail "pull failure was ignored"

rm -f "$FAKE_LOG"
upgrade_out=$(PATH="$stub:$PATH" ./stack upgrade)
printf '%s' "$upgrade_out" | grep -q 'sudo apt install --only-upgrade node-manager' || fail "upgrade omitted APT instructions"
printf '%s' "$upgrade_out" | grep -q 'brew upgrade node-manager' || fail "upgrade omitted Homebrew instructions"
printf '%s' "$upgrade_out" | grep -q 'node-manager/releases/latest' || fail "upgrade omitted GitHub releases"
[ ! -f "$FAKE_LOG" ] || fail "upgrade touched Docker or another host service"

# Host controls remain available when the ignored legacy key is false.
echo '{"dashboard": {"password": "hunterpassword12", "control": false}}' >config.json
HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null
grep -q "stack host-service" "$FAKE_CRON" || fail "ensure_agent: dashboard lost LAN port controls"
grep -q "pkill" "$FAKE_LOG" && fail "ensure_agent: control-off dashboard stopped the host-control agent"

# Authenticated LAN dashboards retain host controls through capability authentication.
echo '{"dashboard": {"password": "dashboardtest12", "control": false}}' >config.json
: >"$FAKE_LOG"
HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null
grep -q "stack host-service" "$FAKE_CRON" || fail "ensure_agent: authenticated dashboard lost host controls"
grep -q "pkill" "$FAKE_LOG" && fail "ensure_agent: authenticated dashboard stopped host controls"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  grep -q '^setsid ' "$FAKE_LOG" 2>/dev/null && break
  sleep 0.1
done

# Source installs on macOS must not install a Linux cron/setsid agent.
rm -f "$FAKE_CRON" "$FAKE_LOG"
echo '{"dashboard": {"password": "hunterpassword12", "control": true}}' >config.json
out=$(FAKE_UNAME=Darwin HOME="$tmp" PATH="$stub:$PATH" ./stack apply)
[ ! -f "$FAKE_CRON" ] || fail "ensure_agent: macOS source install wrote a cron entry"
grep -q '^setsid ' "$FAKE_LOG" 2>/dev/null && fail "ensure_agent: macOS source install launched setsid"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  grep -q 'nohup env .*stack host-service' "$FAKE_LOG" 2>/dev/null && break
  sleep 0.1
done
grep -q 'nohup env .*stack host-service' "$FAKE_LOG" || fail "ensure_agent: macOS source install did not start host-service"
printf '%s' "$out" | grep -q 'host-service: started for this login session' || fail "ensure_agent: macOS source host-service start was not reported"

# Homebrew installs start the persistent supervisor without invoking a real service manager.
: >"$FAKE_LOG"
echo '{"dashboard": {"password": "hunterpassword12", "control": false}}' >config.json
BITCOIN_STACK_PACKAGE_MANAGED=brew FAKE_UNAME=Darwin HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null
grep -q '^brew services start node-manager$' "$FAKE_LOG" || fail "ensure_agent: Homebrew supervisor was not started"

# systemd unit enabled -> stack stays out of the way entirely
cat >"$stub/systemctl" <<'EOS'
#!/usr/bin/env bash
exit 0
EOS
chmod +x "$stub/systemctl"
rm -f "$FAKE_LOG"
echo '{"dashboard": {"password": "hunterpassword12", "control": true}}' >config.json
HOME="$tmp" PATH="$stub:$PATH" ./stack apply >/dev/null
sleep 0.5
grep -qE '^(pkill|setsid) ' "$FAKE_LOG" && fail "ensure_agent: touched the host despite an enabled systemd unit"
grep -q "stack host-service" "$FAKE_CRON" 2>/dev/null && fail "ensure_agent: installed cron despite systemd"
rm -f config.json .env "$FAKE_CRON" "$FAKE_LOG"

# utxo-seed: stubbed docker drives the request, ack, follow, refusal and unpark paths.
utxo_dir=$tmp/utxo-seed
utxo_stub=$tmp/utxo-stubs
export FAKE_UTXO=$tmp/utxo-fake
mkdir -p "$utxo_dir" "$utxo_stub" "$FAKE_UTXO"
cp stack configure.sh "$utxo_dir/"
printf '1.2.3\n' >"$utxo_dir/VERSION"
echo '{"bitcoin": {"enabled": true, "utxo_index": true}, "dashboard": {"password": "utxoseedtest12"}}' >"$utxo_dir/config.json"
(cd "$utxo_dir" && HOME="$tmp" PATH="$stub:$PATH" ./stack init >/dev/null 2>&1) || fail "utxo test setup init failed"
grep -q '^COMPOSE_FILE=.*docker-compose.utxo-index.yml' "$utxo_dir/.env" || fail "utxo_index did not render the overlay"
cat >"$utxo_stub/docker" <<'EOS'
#!/usr/bin/env bash
echo "docker $*" >>"$FAKE_LOG"
args=" $* "
rpcinfo='{"active_commands":[{"method":"getrpcinfo"}]}'
case "$args" in
  *' ps -aq '*'service=bitcoin'* | *' ps -aq '*'service=litecoin'*) [ "${FAKE_BITCOIN_DOWN:-0}" = 1 ] || echo node-id ;;
  *' ps -aq '*'service=dashboard'*) echo dashboard-id ;;
  *' inspect --format {{.State.Running}} '*) echo true ;;
  *' exec -T bitcoin bitcoin-cli -datadir=/data getrpcinfo '* | *' exec -T litecoin litecoin-cli -datadir=/data getrpcinfo '*)
    [ "${FAKE_RPC_FAIL:-0}" != 1 ] || exit 1
    rn=$(($(cat "$FAKE_UTXO/rpc_n" 2>/dev/null || echo 0) + 1))
    echo "$rn" >"$FAKE_UTXO/rpc_n"
    if [ "$rn" -gt 1 ] && [ -n "${FAKE_RPCINFO_LATE:-}" ]; then printf '%s\n' "$FAKE_RPCINFO_LATE"; else printf '%s\n' "${FAKE_RPCINFO:-$rpcinfo}"; fi
    ;;
  *' volume ls '*)
    [ "${FAKE_DOCKER_FAIL:-0}" != 1 ] || exit 1
    [ "${FAKE_VOL_MISSING:-0}" = 1 ] || echo "${args##*name=}" | tr -d ' '
    ;;
  *' ps -aq --filter volume='*) [ "${FAKE_VOL_MOUNTED:-0}" != 1 ] || echo dashboard-id ;;
  *'.utxo-seed.request.tmp'* | *'.litecoin-utxo-seed.request.tmp'*) cat >"$FAKE_UTXO/request.json" ;;
  *'/state/utxo-seed.cancel'* | *'/state/litecoin-utxo-seed.cancel'*) : >"$FAKE_UTXO/cancelled" ;;
  *'.utxo-bench.request.tmp'*) cat >"$FAKE_UTXO/bench-request.json" ;;
  *' exec -T -u 1000 dashboard cat /state/utxo-bench.result '*)
    [ -f "$FAKE_UTXO/bench-request.json" ] || exit 1
    case "${FAKE_BENCH_SCENARIO:-ok}" in
      none) echo '{"id":"someone-else","wallets":[]}' ;;
      refused) jq -n --slurpfile req "$FAKE_UTXO/bench-request.json" '{id: $req[0].id, repeat: $req[0].repeat, state: "catching_up", refusals: ["the index is catching_up, not ready"]}' ;;
      ok)
        jq -n --slurpfile req "$FAKE_UTXO/bench-request.json" '
          {lookup: {first_s: 0.4, p50_s: 0.3, p99_s: 0.5, max_s: 0.6}} as $t |
          {id: $req[0].id, repeat: $req[0].repeat, state: "ready", node_tip_height: 900, index_tip_height: 900,
           wallets: [{name: "alpha", derive_s: 0.5, scripts: 2000, fallback: false, lookup: $t.lookup, coins: 7, sats: 1234},
                     {name: "beta", derive_s: 0.01, scripts: 0, fallback: true, coins: 0, sats: 0}],
           batches: [{wallets: 1, scripts: 2000, expected_scripts: 2000, fallback_wallets: 0, coins: 7, lookup: $t.lookup}, {wallets: 2, scripts: 2000, expected_scripts: 2000, fallback_wallets: 1, coins: 7, lookup: $t.lookup}],
           cached_read: {first_s: 0.001, p50_s: 0.002, p99_s: 0.004, max_s: 0.005, roster_wallets: 3}} +
          (if $req[0].scan then {scan: {height: 900, batch_s: 41.5, single_s: 20.25, wallets: [
             {name: "alpha", coins: 7, sats: 1234, index_coins: 7, index_sats: 1234, match: true},
             {name: "beta", coins: 3, sats: 5, index_coins: 0, index_sats: 0}]}} else {} end)'
        ;;
    esac
    ;;
  *' exec -T -u 1000 dashboard cat /state/utxo-seed.json '* | *' exec -T -u 1000 dashboard cat /state/litecoin-utxo-seed.json '*)
    if [ "${FAKE_DASH_SEEDING:-0}" = 1 ]; then
      if [ -f "$FAKE_UTXO/cancelled" ] && [ "${FAKE_DASH_STUCK:-0}" != 1 ]; then echo '{"seed_state":"cancelled"}'; else echo '{"seed_state":"seeding"}'; fi
      exit 0
    fi
    [ -f "$FAKE_UTXO/request.json" ] || exit 1
    n=$(($(cat "$FAKE_UTXO/n" 2>/dev/null || echo 0) + 1))
    echo "$n" >"$FAKE_UTXO/n"
    id=$(jq -r .id "$FAKE_UTXO/request.json")
    case "${FAKE_SEED_SCENARIO:-ok}" in
      ok)
        case "$n" in
          1) printf '{"request_id":"%s","seed_state":"seeding","phase":"waiting","progress":0}\n' "$id" ;;
          2) printf '{"request_id":"%s","seed_state":"seeding","phase":"importing","progress":0.4}\n' "$id" ;;
          *) printf '{"request_id":"%s","seed_state":"ok"}\n' "$id" ;;
        esac
        ;;
      refused) printf '{"request_id":"%s","seed_state":"refused","refusals":[{"guard":"thermal","detail":"hottest zone 80 C is above 75 C"},{"guard":"free_space","detail":"3.0 GB free, 27 GB needed"}]}\n' "$id" ;;
      failed)
        if [ "$n" = 1 ]; then
          printf '{"request_id":"%s","seed_state":"seeding"}\n' "$id"
        else
          printf '{"request_id":"%s","seed_state":"failed","last_seed":{"outcome":"failed","reason":"less than 2 GB free during the seed"}}\n' "$id"
        fi
        ;;
      cancelled) printf '{"request_id":"%s","seed_state":"cancelled"}\n' "$id" ;;
      lost)
        if [ "$n" = 1 ]; then printf '{"request_id":"%s","seed_state":"seeding"}\n' "$id"; else printf '{"request_id":"other","seed_state":"seeding"}\n'; fi
        ;;
      noack) printf '{"request_id":"someone-else","seed_state":"ok"}\n' ;;
    esac
    ;;
esac
exit 0
EOS
chmod +x "$utxo_stub/docker"
useed() { (cd "$utxo_dir" && HOME="$tmp" BITCOIN_STACK_UTXO_SEED_POLL_SECONDS=1 BITCOIN_STACK_UTXO_SEED_ACK_SECONDS=2 BITCOIN_STACK_UTXO_SEED_CANCEL_SECONDS=2 PATH="$utxo_stub:$stub:$PATH" ./stack "$@"); }
reset_useed() {
  rm -f "$FAKE_UTXO/request.json" "$FAKE_UTXO/n" "$FAKE_UTXO/rpc_n" "$FAKE_UTXO/cancelled" "$FAKE_LOG"
  unset FAKE_RPCINFO_LATE FAKE_RPCINFO FAKE_RPC_FAIL FAKE_DASH_SEEDING FAKE_DASH_STUCK FAKE_SEED_SCENARIO FAKE_BITCOIN_DOWN FAKE_VOL_MISSING FAKE_VOL_MOUNTED
}
pipe_removals() { grep -c ' rm -f /data/utxo-seed.dat /data/utxo-seed.dat.incomplete' "$FAKE_LOG" || true; }

reset_useed
useed utxo-seed -y </dev/null >"$tmp/useed.out" 2>&1 || {
  cat "$tmp/useed.out"
  fail "utxo-seed failed on the happy path"
}
grep -q 'about 20 minutes' "$tmp/useed.out" || fail "utxo-seed did not print the duration warning"
grep -q 'stops answering for about 5 minutes' "$tmp/useed.out" || fail "utxo-seed did not print the RPC warning"
grep -q '7.5 GiB.*17 GiB.*27 GB' "$tmp/useed.out" || fail "utxo-seed did not print the disk warning"
grep -q 'alerts are held for up to 15 minutes' "$tmp/useed.out" || fail "utxo-seed did not print the alert warning"
grep -q 'importing 40%' "$tmp/useed.out" || fail "utxo-seed did not follow the progress"
grep -q 'Seed finished' "$tmp/useed.out" || fail "utxo-seed did not report success"
awk '/getrpcinfo/ && !r { r=NR } / rm -f \/data\/utxo-seed.dat/ && !f { f=NR } /mkfifo -m 600 \/data\/utxo-seed.dat.incomplete/ { m=NR } /utxo-seed.request.tmp/ { q=NR } END { exit !(r && f && m && q && r < f && f < m && m < q) }' "$FAKE_LOG" ||
  fail "utxo-seed did not check RPC, clear stale files, make the FIFO and write the request in order"
[ "$(pipe_removals)" = 2 ] || fail "utxo-seed did not remove the FIFO names before and after"
jq -e '(.id | test("^[0-9a-f]{32}$")) and (.requested_at | type == "number")' "$FAKE_UTXO/request.json" >/dev/null || fail "utxo-seed request is not {id, requested_at}"
grep -q ' -u 1000 dashboard sh -c umask 077; cat > /state/.utxo-seed.request.tmp' "$FAKE_LOG" || fail "utxo-seed did not write the request privately as the dashboard user"

reset_useed
useed utxo-seed </dev/null >/dev/null 2>&1 && fail "utxo-seed started without a terminal or -y"
grep -q mkfifo "$FAKE_LOG" && fail "declined utxo-seed created a FIFO"
reset_useed
printf 'n\n' | BITCOIN_STACK_INTERACTIVE=1 useed utxo-seed >/dev/null 2>&1 && fail "utxo-seed ignored a no answer"
grep -q mkfifo "$FAKE_LOG" && fail "utxo-seed created a FIFO after a no answer"
reset_useed
printf 'y\n' | BITCOIN_STACK_INTERACTIVE=1 useed utxo-seed >/dev/null 2>&1 || fail "utxo-seed did not start after y"
grep -q mkfifo "$FAKE_LOG" || fail "utxo-seed did not create the FIFO after y"

reset_useed
export FAKE_RPCINFO='{"active_commands":[{"method":"getrpcinfo"},{"method":"dumptxoutset"}]}'
useed utxo-seed -y >/dev/null 2>&1 && fail "utxo-seed ran while a dump was active"
grep -q mkfifo "$FAKE_LOG" && fail "utxo-seed made a FIFO while a dump was active"
export FAKE_RPCINFO='{"active_commands":[{"method":"scantxoutset"}]}'
useed utxo-seed -y >/dev/null 2>&1 && fail "utxo-seed ran while a scan was active"
reset_useed
export FAKE_RPC_FAIL=1
useed utxo-seed -y >/dev/null 2>&1 && fail "utxo-seed ran with RPC down"
grep -q mkfifo "$FAKE_LOG" && fail "utxo-seed made a FIFO with RPC down"

reset_useed
export FAKE_SEED_SCENARIO=refused
useed utxo-seed -y >"$tmp/useed.out" 2>&1 && fail "a refused seed exited 0"
grep -q 'refused: thermal: hottest zone 80 C is above 75 C' "$tmp/useed.out" || fail "utxo-seed did not print the thermal refusal"
grep -q 'refused: free_space: 3.0 GB free, 27 GB needed' "$tmp/useed.out" || fail "utxo-seed did not print every refusal"
[ "$(pipe_removals)" = 2 ] || fail "refused seed left the FIFO names"
reset_useed
export FAKE_SEED_SCENARIO=failed
useed utxo-seed -y >"$tmp/useed.out" 2>&1 && fail "a failed seed exited 0"
grep -q 'less than 2 GB free during the seed' "$tmp/useed.out" || fail "utxo-seed did not report the failure reason"

reset_useed
export FAKE_SEED_SCENARIO=noack
useed utxo-seed -y >"$tmp/useed.out" 2>&1 && fail "an unacknowledged request exited 0"
grep -q 'did not acknowledge' "$tmp/useed.out" || fail "utxo-seed did not explain the ack timeout"
grep -q ' dashboard rm -f /state/utxo-seed.request' "$FAKE_LOG" || fail "ack timeout left the request behind"
[ "$(pipe_removals)" = 2 ] || fail "ack timeout left the FIFO names"

reset_useed
export FAKE_SEED_SCENARIO=cancelled
useed utxo-seed -y >"$tmp/useed.out" 2>&1 && fail "a cancelled seed exited 0"
grep -q 'Seed cancelled' "$tmp/useed.out" || fail "utxo-seed did not report the cancel"
[ "$(pipe_removals)" = 2 ] || fail "cancelled seed left the FIFO names"

reset_useed
export FAKE_SEED_SCENARIO=lost
BITCOIN_STACK_UTXO_SEED_LOST_SECONDS=2 useed utxo-seed -y >"$tmp/useed.out" 2>&1 && fail "a vanished dashboard status exited 0"
grep -q 'Lost track of the seed' "$tmp/useed.out" || fail "utxo-seed spun on a vanished dashboard status"

reset_useed
export FAKE_DASH_SEEDING=1
useed utxo-seed -y >"$tmp/useed.out" 2>&1 && fail "utxo-seed ran while the dashboard was seeding"
grep -q 'already seeding' "$tmp/useed.out" || fail "utxo-seed did not explain the busy dashboard"
grep -q 'mkfifo\| rm -f /data/utxo-seed' "$FAKE_LOG" && fail "utxo-seed touched the FIFO while the dashboard was seeding"

reset_useed
export FAKE_SEED_SCENARIO=noack FAKE_RPCINFO_LATE='{"active_commands":[{"method":"getrpcinfo"},{"method":"dumptxoutset"}]}'
useed utxo-seed -y >/dev/null 2>&1 && fail "an unacknowledged request exited 0 with a parked dump"
grep -q 'getrpcinfo' "$FAKE_LOG" || fail "ack timeout did not check for a parked dump"
awk '/timeout 5 cat \/data\/utxo-seed.dat.incomplete/ { u=NR } / rm -f \/data\/utxo-seed.dat / { r=NR } END { exit !(u && r && u < r) }' "$FAKE_LOG" ||
  fail "ack timeout removed the FIFO without unparking a parked dump"

reset_useed
export FAKE_SEED_SCENARIO=noack
(cd "$utxo_dir" && exec env HOME="$tmp" BITCOIN_STACK_UTXO_SEED_POLL_SECONDS=1 BITCOIN_STACK_UTXO_SEED_ACK_SECONDS=30 PATH="$utxo_stub:$stub:$PATH" ./stack utxo-seed -y) >/dev/null 2>&1 &
useed_pid=$!
for _ in $(seq 1 50); do grep -q 'utxo-seed.request.tmp' "$FAKE_LOG" 2>/dev/null && break; sleep 0.1; done
kill -TERM "$useed_pid"
wait "$useed_pid" 2>/dev/null || true
grep -q ' dashboard rm -f /state/utxo-seed.request' "$FAKE_LOG" || fail "a signal during the ack wait left the request behind"
[ "$(pipe_removals)" = 2 ] || fail "a signal during the ack wait left the FIFO names"

reset_useed
useed utxo-seed cancel >/dev/null || fail "utxo-seed cancel failed"
grep -q ' -u 1000 dashboard sh -c umask 077; : > /state/utxo-seed.cancel' "$FAKE_LOG" || fail "cancel did not write the cancel file"
useed utxo-seed bogus >/dev/null 2>&1 && fail "utxo-seed accepted an unknown argument"
reset_useed
FAKE_BITCOIN_DOWN=1 useed utxo-seed -y >/dev/null 2>&1 && fail "utxo-seed ran without the bitcoin container"

reset_useed
cp "$utxo_dir/.env" "$utxo_dir/.env.keep"
grep -v '^COMPOSE_FILE=' "$utxo_dir/.env.keep" >"$utxo_dir/.env"
useed utxo-seed -y >"$tmp/useed.out" 2>&1 && fail "utxo-seed ran without the overlay"
grep -q 'not enabled' "$tmp/useed.out" || fail "utxo-seed did not explain the missing overlay"
useed utxo-seed cancel >/dev/null 2>&1 && fail "cancel ran without the overlay"
export FAKE_RPCINFO='{"active_commands":[{"method":"dumptxoutset"}]}'
: >"$FAKE_LOG"
useed stop >/dev/null
grep -q 'timeout 5 cat' "$FAKE_LOG" && fail "stop unparked a dump without the overlay"
mv "$utxo_dir/.env.keep" "$utxo_dir/.env"

# utxo-index remove: refuses while the option is on or a container mounts the volume.
vol=$(sed -n 's/^COMPOSE_PROJECT_NAME=//p' "$utxo_dir/.env" | tail -n 1)_utxo_index
reset_useed
useed utxo-index remove -y >"$tmp/useed.out" 2>&1 && fail "utxo-index remove ran with the option on"
grep -q 'still on' "$tmp/useed.out" || fail "utxo-index remove did not explain the option is on"
grep -q 'volume rm' "$FAKE_LOG" && fail "utxo-index remove removed the volume with the option on"
cp "$utxo_dir/.env" "$utxo_dir/.env.keep"
grep -v '^COMPOSE_FILE=' "$utxo_dir/.env.keep" >"$utxo_dir/.env"
reset_useed
FAKE_VOL_MOUNTED=1 useed utxo-index remove -y >"$tmp/useed.out" 2>&1 && fail "utxo-index remove ran while a container mounts the volume"
grep -q 'still mounts' "$tmp/useed.out" || fail "utxo-index remove did not explain the mount"
grep -q 'volume rm' "$FAKE_LOG" && fail "utxo-index remove removed a mounted volume"
reset_useed
FAKE_VOL_MISSING=1 useed utxo-index remove -y >"$tmp/useed.out" 2>&1 || fail "utxo-index remove failed with no volume"
grep -q 'Nothing to remove' "$tmp/useed.out" || fail "utxo-index remove did not report nothing to remove"
grep -q 'volume rm' "$FAKE_LOG" && fail "utxo-index remove removed a missing volume"
reset_useed
FAKE_DOCKER_FAIL=1 useed utxo-index remove -y >"$tmp/useed.out" 2>&1 && fail "utxo-index remove succeeded with docker failing"
grep -q 'Nothing to remove' "$tmp/useed.out" && fail "utxo-index remove reported a docker failure as a missing volume"
reset_useed
printf 'n\n' | BITCOIN_STACK_INTERACTIVE=1 useed utxo-index remove >/dev/null 2>&1 && fail "utxo-index remove ignored a no answer"
useed utxo-index remove </dev/null >/dev/null 2>&1 && fail "utxo-index remove ran without a terminal or -y"
grep -q 'volume rm' "$FAKE_LOG" && fail "declined utxo-index remove removed the volume"
useed utxo-index bogus >/dev/null 2>&1 && fail "utxo-index accepted an unknown subcommand"
reset_useed
useed utxo-index remove -y >"$tmp/useed.out" 2>&1 || fail "utxo-index remove -y failed"
grep -qx "docker volume rm $vol" "$FAKE_LOG" || fail "utxo-index remove did not remove $vol"
grep -q 'needs a new' "$tmp/useed.out" || fail "utxo-index remove did not mention re-seeding"
reset_useed
printf 'y\n' | BITCOIN_STACK_INTERACTIVE=1 useed utxo-index remove >/dev/null 2>&1 || fail "utxo-index remove failed after y"
grep -qx "docker volume rm $vol" "$FAKE_LOG" || fail "utxo-index remove did not remove after y"
mv "$utxo_dir/.env.keep" "$utxo_dir/.env"

# utxo-index bench: option off or a bad wallet file is refused before anything is written.
reset_ubench() {
  reset_useed
  rm -f "$FAKE_UTXO/bench-request.json"
  unset FAKE_BENCH_SCENARIO
}
ubench() { (cd "$utxo_dir" && HOME="$tmp" BITCOIN_STACK_UTXO_BENCH_POLL_SECONDS=1 BITCOIN_STACK_UTXO_BENCH_TIMEOUT_SECONDS="${BENCH_TIMEOUT:-5}" PATH="$utxo_stub:$stub:$PATH" ./stack "$@"); }
bench_wallets=$tmp/bench-wallets.json
echo '[{"name": "alpha", "key": "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"}, {"name": "beta", "key": "wpkh(xpubplaceholder/<0;1>/*)"}]' >"$bench_wallets"
bench_written() { grep -q '.utxo-bench.request.tmp' "$FAKE_LOG" 2>/dev/null; }
rm -f "$utxo_dir"/utxo-bench-*.json

reset_ubench
cp "$utxo_dir/.env" "$utxo_dir/.env.keep"
grep -v '^COMPOSE_FILE=' "$utxo_dir/.env.keep" >"$utxo_dir/.env"
ubench utxo-index bench "$bench_wallets" >"$tmp/ubench.out" 2>&1 && fail "utxo-index bench ran without the overlay"
grep -q 'not enabled' "$tmp/ubench.out" || fail "utxo-index bench did not explain the missing overlay"
bench_written && fail "utxo-index bench wrote a request without the overlay"
mv "$utxo_dir/.env.keep" "$utxo_dir/.env"

echo '{"name": "alpha", "key": "x"}' >"$tmp/bad1.json"
echo '[]' >"$tmp/bad2.json"
echo '[{"name": "alpha"}]' >"$tmp/bad3.json"
echo '[{"name": "alpha", "key": 5}]' >"$tmp/bad4.json"
echo '[{"name": "", "key": "x"}]' >"$tmp/bad5.json"
echo 'not json' >"$tmp/bad6.json"
jq -nc '[range(51) | {name: "w\(.)", key: "k\(.)"}]' >"$tmp/bad7.json"
for bad in "$tmp"/bad[1-7].json; do
  reset_ubench
  ubench utxo-index bench "$bad" >"$tmp/ubench.out" 2>&1 && fail "utxo-index bench accepted $bad"
  grep -q 'must be a JSON array of 1 to 50' "$tmp/ubench.out" || fail "utxo-index bench did not explain $bad"
  bench_written && fail "utxo-index bench wrote a request for $bad"
done
for args in "--repeat 0" "--repeat 201" "--repeat x" "--repeat" "--bogus" "extra.json"; do
  reset_ubench
  # shellcheck disable=SC2086
  ubench utxo-index bench "$bench_wallets" $args >/dev/null 2>&1 && fail "utxo-index bench accepted arguments '$args'"
  bench_written && fail "utxo-index bench wrote a request for arguments '$args'"
done
reset_ubench
ubench utxo-index bench "$tmp/missing.json" >"$tmp/ubench.out" 2>&1 && fail "utxo-index bench accepted a missing wallet file"
grep -q "Cannot read $tmp/missing.json" "$tmp/ubench.out" || fail "utxo-index bench did not name the unreadable wallet file"
bench_written && fail "utxo-index bench wrote a request for a missing wallet file"
reset_ubench
ubench utxo-index bench >/dev/null 2>&1 && fail "utxo-index bench ran without a wallet file"
ubench utxo-index bogus >/dev/null 2>&1 && fail "utxo-index accepted an unknown subcommand after bench was added"

reset_ubench
ubench utxo-index bench "$bench_wallets" >"$tmp/ubench.out" 2>&1 || fail "utxo-index bench failed on the happy path"
grep -q ' -u 1000 dashboard sh -c umask 077; cat > /state/.utxo-bench.request.tmp' "$FAKE_LOG" || fail "utxo-index bench did not write the request privately as the dashboard user"
jq -e '(.id | test("^[0-9a-f]{32}$")) and (.requested_at | . > 1e9 and . <= now + 5) and .repeat == 20 and .scan == false and (.wallets | length == 2) and .wallets[0].name == "alpha" and .wallets[1].key == "wpkh(xpubplaceholder/<0;1>/*)"' "$FAKE_UTXO/bench-request.json" >/dev/null || fail "utxo-index bench request is not {id, wallets, repeat, scan}"
grep -q 'scantxoutset' "$tmp/ubench.out" && fail "utxo-index bench warned about a scan without --scan"
grep -q 'alpha  *2000  *7  *1234' "$tmp/ubench.out" || fail "utxo-index bench did not print the per-wallet table"
grep -q 'beta .*fallback' "$tmp/ubench.out" || fail "utxo-index bench did not mark the fallback wallet"
grep -q 'Batched lookups' "$tmp/ubench.out" || fail "utxo-index bench did not print the batches"
grep -q 'Cached read (seconds): p50 0.002, p99 0.004, max 0.005, roster wallets 3' "$tmp/ubench.out" || fail "utxo-index bench did not print the cached read and roster size"
grep -q 'warning: 1 wallet(s) fell back' "$tmp/ubench.out" || fail "utxo-index bench did not warn about the fallback wallet"
grep -q '^2  *2000  *2000  *1  *7' "$tmp/ubench.out" || fail "utxo-index bench did not print expected scripts and fallback wallets per batch"
grep -q 'Scan at height' "$tmp/ubench.out" && fail "utxo-index bench printed a scan section without --scan"

reset_ubench
mkdir -p "$tmp/bench-cwd"
cp "$bench_wallets" "$tmp/bench-cwd/relative-wallets.json"
(cd "$tmp/bench-cwd" && HOME="$tmp" BITCOIN_STACK_UTXO_BENCH_POLL_SECONDS=1 BITCOIN_STACK_UTXO_BENCH_TIMEOUT_SECONDS="${BENCH_TIMEOUT:-5}" PATH="$utxo_stub:$stub:$PATH" "$utxo_dir/stack" utxo-index bench relative-wallets.json) >"$tmp/ubench.out" 2>&1 || fail "utxo-index bench did not read a wallet file relative to the caller's directory"
jq -e '.wallets | length == 2' "$FAKE_UTXO/bench-request.json" >/dev/null || fail "utxo-index bench sent the wrong wallets for a relative path"
saved=$(sed -n 's/^Saved \(.*\) (mode 600)\.$/\1/p' "$tmp/ubench.out")
[ -f "$saved" ] || fail "utxo-index bench did not print the saved result path"
[ -n "$(find "$saved" -perm 600)" ] || fail "utxo-index bench result is not mode 600"
jq -e '.state == "ready"' "$saved" >/dev/null || fail "utxo-index bench saved result is not the dashboard result"

reset_ubench
ubench utxo-index bench "$bench_wallets" --scan >"$tmp/ubench.out" 2>&1 </dev/null && fail "utxo-index bench --scan ran without a terminal or -y"
grep -q 'Run this on a terminal, or pass -y' "$tmp/ubench.out" || fail "utxo-index bench --scan did not ask for -y"
bench_written && fail "utxo-index bench --scan wrote a request without confirmation"
echo n | BITCOIN_STACK_INTERACTIVE=1 ubench utxo-index bench "$bench_wallets" --scan >"$tmp/ubench.out" 2>&1 && fail "utxo-index bench --scan ran after the answer n"
grep -q 'Not started' "$tmp/ubench.out" || fail "utxo-index bench --scan did not report the declined scan"
bench_written && fail "utxo-index bench --scan wrote a request after the answer n"
reset_ubench
echo y | BITCOIN_STACK_INTERACTIVE=1 ubench utxo-index bench "$bench_wallets" --scan >"$tmp/ubench.out" 2>&1 || fail "utxo-index bench --scan failed after the answer y"
jq -e '.scan == true' "$FAKE_UTXO/bench-request.json" >/dev/null || fail "utxo-index bench --scan did not write the confirmed request"

reset_ubench
ubench utxo-index bench "$bench_wallets" --repeat 7 --scan -y >"$tmp/ubench.out" 2>&1 || fail "utxo-index bench --scan -y failed"
jq -e '.repeat == 7 and .scan == true' "$FAKE_UTXO/bench-request.json" >/dev/null || fail "utxo-index bench did not pass --repeat and --scan"
grep -q 'owner approved' "$tmp/ubench.out" || fail "utxo-index bench --scan did not warn about the heavy scan"
grep -q 'Scan at height 900: batch of 2 41.5 s, first wallet alone 20.25 s' "$tmp/ubench.out" || fail "utxo-index bench did not print the scan timings"
grep -q 'alpha  *7  *7  *1234  *1234  *true' "$tmp/ubench.out" || fail "utxo-index bench did not print the scan match"
grep -q 'beta .*n/a' "$tmp/ubench.out" || fail "utxo-index bench did not print n/a for the fallback wallet"

reset_ubench
export FAKE_BENCH_SCENARIO=refused
ubench utxo-index bench "$bench_wallets" >"$tmp/ubench.out" 2>&1 && fail "a refused bench exited 0"
grep -q 'refused: the index is catching_up, not ready' "$tmp/ubench.out" || fail "utxo-index bench did not print the refusal"
grep -q 'Per wallet' "$tmp/ubench.out" && fail "utxo-index bench printed tables for a refused run"

reset_ubench
export FAKE_BENCH_SCENARIO=none
BENCH_TIMEOUT=2 ubench utxo-index bench "$bench_wallets" >"$tmp/ubench.out" 2>&1 && fail "a bench with no result exited 0"
grep -q 'returned no result' "$tmp/ubench.out" || fail "utxo-index bench did not explain the timeout"
grep -q ' dashboard rm -f /state/utxo-bench.request' "$FAKE_LOG" || fail "the bench timeout left the request behind"

reset_ubench
export FAKE_BENCH_SCENARIO=none
(cd "$utxo_dir" && exec env HOME="$tmp" BITCOIN_STACK_UTXO_BENCH_POLL_SECONDS=1 BITCOIN_STACK_UTXO_BENCH_TIMEOUT_SECONDS=60 PATH="$utxo_stub:$stub:$PATH" ./stack utxo-index bench "$bench_wallets") >/dev/null 2>&1 &
ubench_pid=$!
for _ in $(seq 1 50); do bench_written 2>/dev/null && break; sleep 0.1; done
kill -TERM "$ubench_pid"
wait "$ubench_pid" 2>/dev/null || true
grep -q ' dashboard rm -f /state/utxo-bench.request' "$FAKE_LOG" || fail "a signal during the bench wait left the request behind"
reset_ubench
rm -f "$utxo_dir"/utxo-bench-*.json

# measure-block-latency.sh: blocks match the first refresh at that height or higher, split by index and block update.
cat >"$tmp/block.log" <<'EOF'
2026-11-15T06:30:00Z UpdateTip: new best=aa height=100 version=0x1 log2_work=1 tx=1 date='x' progress=1.0 cache=1.0MiB(1txo)
2026-11-15T06:31:00Z UpdateTip: new best=bb height=101 version=0x1 log2_work=1 tx=1 date='x' progress=1.0 cache=1.0MiB(1txo)
2026-11-15T06:32:00Z UpdateTip: new best=cc height=102 version=0x1 log2_work=1 tx=1 date='x' progress=1.0 cache=1.0MiB(1txo)
2026-11-15T06:40:00Z UpdateTip: new best=dd height=103 version=0x1 log2_work=1 tx=1 date='x' progress=1.0 cache=1.0MiB(1txo)
EOF
cat >"$tmp/dash.log" <<'EOF'
2026-11-15T06:30:20.123456789Z 2026/11/15 06:30:20 watch index: outcome=ok outpoints=7 height=100 took=0.500s
2026-11-15T06:31:40.000000001Z 2026/11/15 06:31:40 watch block update: wallets=2 blocks=1 height=101 took=2.0s
2026-11-15T06:33:30.5Z 2026/11/15 06:33:30 watch index: outcome=ok outpoints=7 height=102 took=0.100s
EOF
"$repo_root/scripts/measure-block-latency.sh" --since 2026-11-15T06:00:00Z --bitcoin-log "$tmp/block.log" --dashboard-log "$tmp/dash.log" >"$tmp/latency.out" || fail "measure-block-latency.sh failed"
grep -q 'Blocks seen: 4, matched to a refresh: 3, with no refresh yet: 1' "$tmp/latency.out" || fail "measure-block-latency.sh miscounted the blocks"
grep -Eq 'Served by the index  *2  *20.0  *90.0  *90.0' "$tmp/latency.out" || fail "measure-block-latency.sh got the index row wrong"
grep -Eq 'Fell back to the block update  *1  *40.0  *40.0  *40.0' "$tmp/latency.out" || fail "measure-block-latency.sh got the block update row wrong"
grep -Eq '^All  *3  *40.0  *90.0  *90.0' "$tmp/latency.out" || fail "measure-block-latency.sh got the overall row wrong"
"$repo_root/scripts/measure-block-latency.sh" --bitcoin-log "$tmp/block.log" --dashboard-log "$tmp/dash.log" >/dev/null 2>&1 && fail "measure-block-latency.sh ran without --since"

# stop, down and restart open a parked dump once; a running seed is cancelled first.
for action in stop down restart; do
  reset_useed
  export FAKE_RPCINFO='{"active_commands":[{"method":"getrpcinfo"},{"method":"dumptxoutset"}]}'
  useed "$action" >/dev/null || fail "$action failed with a parked dump"
  awk -v action="$action" '/timeout 5 cat \/data\/utxo-seed.dat.incomplete/ { u=NR } $0 ~ ("docker compose .* " action) { a=NR } END { exit !(u && a && u < a) }' "$FAKE_LOG" ||
    fail "$action did not unpark the dump before stopping bitcoind"
  reset_useed
  export FAKE_RPCINFO='{"active_commands":[{"method":"getrpcinfo"},{"method":"dumptxoutset"}]}' FAKE_DASH_SEEDING=1
  useed "$action" >/dev/null || fail "$action failed while seeding"
  awk -v action="$action" '/utxo-seed.cancel/ { c=NR } /timeout 5 cat \/data\/utxo-seed.dat.incomplete/ { u=NR } $0 ~ ("docker compose .* " action) { a=NR } END { exit !(c && u && a && c < u && u < a) }' "$FAKE_LOG" ||
    fail "$action did not cancel the seed and unpark the dump before stopping bitcoind"
  reset_useed
  useed "$action" >/dev/null || fail "$action failed with no dump"
  grep -q 'timeout 5 cat' "$FAKE_LOG" && fail "$action opened the FIFO with no dump running"
done
reset_useed
export FAKE_RPCINFO='{"active_commands":[{"method":"dumptxoutset"}]}' FAKE_DASH_SEEDING=1 FAKE_DASH_STUCK=1
started=$SECONDS
useed stop >/dev/null || fail "stop failed while the dashboard ignored the cancel"
[ $((SECONDS - started)) -lt 30 ] || fail "stop waited too long for a stuck seed to cancel"
grep -q 'timeout 5 cat' "$FAKE_LOG" || fail "stop did not unpark the dump after the cancel wait ran out"
reset_useed
export FAKE_RPCINFO='{"active_commands":[{"method":"dumptxoutset"}]}'
useed stop -t 5 >/dev/null
grep -q 'timeout 5 cat' "$FAKE_LOG" || fail "stop -t 5 did not unpark the dump"
: >"$FAKE_LOG"
useed stop dashboard >/dev/null
grep -q 'timeout 5 cat' "$FAKE_LOG" && fail "stopping only the dashboard unparked the dump"
useed stop bitcoin >/dev/null
grep -q 'timeout 5 cat' "$FAKE_LOG" || fail "stopping bitcoin did not unpark the dump"
reset_useed
ltc_dir=$tmp/utxo-seed-litecoin
mkdir -p "$ltc_dir"
cp stack configure.sh "$ltc_dir/"
printf '1.2.3\n' >"$ltc_dir/VERSION"
echo '{"bitcoin": {"enabled": true}, "litecoin": {"enabled": true, "utxo_index": true}, "dashboard": {"password": "utxoseedtest12"}}' >"$ltc_dir/config.json"
(cd "$ltc_dir" && HOME="$tmp" PATH="$stub:$PATH" ./stack init >/dev/null 2>&1) || fail "litecoin utxo test setup init failed"
grep -q '^COMPOSE_FILE=.*docker-compose.litecoin-utxo-index.yml' "$ltc_dir/.env" || fail "litecoin.utxo_index did not render its overlay"
grep -q 'docker-compose.utxo-index.yml' "$ltc_dir/.env" && fail "litecoin.utxo_index rendered the Bitcoin overlay"
lseed() { (cd "$ltc_dir" && HOME="$tmp" BITCOIN_STACK_UTXO_SEED_POLL_SECONDS=1 BITCOIN_STACK_UTXO_SEED_ACK_SECONDS=2 PATH="$utxo_stub:$stub:$PATH" ./stack "$@"); }
reset_useed
lseed utxo-seed litecoin -y </dev/null >"$tmp/lseed.out" 2>&1 || {
  cat "$tmp/lseed.out" "$FAKE_LOG"
  fail "utxo-seed litecoin failed on the happy path"
}
grep -q 'Seeding the Litecoin balance index' "$tmp/lseed.out" || fail "utxo-seed litecoin did not print its own warning"
grep -q 'Seed finished' "$tmp/lseed.out" || fail "utxo-seed litecoin did not report success"
grep -q ' exec -T litecoin litecoin-cli -datadir=/data getrpcinfo' "$FAKE_LOG" || fail "utxo-seed litecoin did not ask litecoind for running commands"
grep -q ' exec -T litecoin mkfifo -m 600 /data/utxo-seed.dat.incomplete' "$FAKE_LOG" || fail "utxo-seed litecoin did not make the FIFO in the litecoin container"
grep -q 'cat > /state/.litecoin-utxo-seed.request.tmp' "$FAKE_LOG" || fail "utxo-seed litecoin did not write its prefixed request"
grep -q ' exec -T bitcoin ' "$FAKE_LOG" && fail "utxo-seed litecoin touched the bitcoin container"
lseed utxo-index remove litecoin -y >"$tmp/lremove.out" 2>&1 && fail "utxo-index remove litecoin ran while the index was on"
grep -q 'litecoin.utxo_index is still on' "$tmp/lremove.out" || fail "utxo-index remove litecoin did not name the litecoin setting"

echo "PASS: test_cli.sh"
