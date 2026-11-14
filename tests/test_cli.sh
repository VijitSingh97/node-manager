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
  *' ps -aq '*'service=bitcoin'*) [ "${FAKE_BITCOIN_DOWN:-0}" = 1 ] || echo bitcoin-id ;;
  *' ps -aq '*'service=dashboard'*) echo dashboard-id ;;
  *' inspect --format {{.State.Running}} '*) echo true ;;
  *' exec -T bitcoin bitcoin-cli -datadir=/data getrpcinfo '*)
    [ "${FAKE_RPC_FAIL:-0}" != 1 ] || exit 1
    printf '%s\n' "${FAKE_RPCINFO:-$rpcinfo}"
    ;;
  *'.utxo-seed.request.tmp'*) cat >"$FAKE_UTXO/request.json" ;;
  *' exec -T -u 1000 dashboard cat /state/utxo-seed.json '*)
    if [ "${FAKE_DASH_SEEDING:-0}" = 1 ]; then echo '{"seed_state":"seeding"}'; exit 0; fi
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
      noack) printf '{"request_id":"someone-else","seed_state":"ok"}\n' ;;
    esac
    ;;
esac
exit 0
EOS
chmod +x "$utxo_stub/docker"
useed() { (cd "$utxo_dir" && HOME="$tmp" BITCOIN_STACK_UTXO_SEED_POLL_SECONDS=1 BITCOIN_STACK_UTXO_SEED_ACK_SECONDS=2 PATH="$utxo_stub:$stub:$PATH" ./stack "$@"); }
reset_useed() {
  rm -f "$FAKE_UTXO/request.json" "$FAKE_UTXO/n" "$FAKE_LOG"
  unset FAKE_RPCINFO FAKE_RPC_FAIL FAKE_DASH_SEEDING FAKE_SEED_SCENARIO FAKE_BITCOIN_DOWN
}
pipe_removals() { grep -c ' rm -f /data/utxo-seed.dat /data/utxo-seed.dat.incomplete' "$FAKE_LOG" || true; }

reset_useed
useed utxo-seed -y </dev/null >"$tmp/useed.out" 2>&1 || {
  cat "$tmp/useed.out"
  fail "utxo-seed failed on the happy path"
}
grep -q 'at least 15 minutes' "$tmp/useed.out" || fail "utxo-seed did not print the duration warning"
grep -q '27 GB' "$tmp/useed.out" || fail "utxo-seed did not print the disk warning"
grep -q 'alerts are held for up to 15 minutes' "$tmp/useed.out" || fail "utxo-seed did not print the alert warning"
grep -q 'importing 40%' "$tmp/useed.out" || fail "utxo-seed did not follow the progress"
grep -q 'Seed finished' "$tmp/useed.out" || fail "utxo-seed did not report success"
awk '/getrpcinfo/ { r=NR } / rm -f \/data\/utxo-seed.dat/ && !f { f=NR } /mkfifo -m 600 \/data\/utxo-seed.dat.incomplete/ { m=NR } /utxo-seed.request.tmp/ { q=NR } END { exit !(r && f && m && q && r < f && f < m && m < q) }' "$FAKE_LOG" ||
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

# stop, down and restart open a parked dump once, but never while the dashboard is seeding.
for action in stop down restart; do
  reset_useed
  export FAKE_RPCINFO='{"active_commands":[{"method":"getrpcinfo"},{"method":"dumptxoutset"}]}'
  useed "$action" >/dev/null || fail "$action failed with a parked dump"
  awk -v action="$action" '/timeout 5 cat \/data\/utxo-seed.dat.incomplete/ { u=NR } $0 ~ ("docker compose .* " action) { a=NR } END { exit !(u && a && u < a) }' "$FAKE_LOG" ||
    fail "$action did not unpark the dump before stopping bitcoind"
  reset_useed
  export FAKE_RPCINFO='{"active_commands":[{"method":"getrpcinfo"},{"method":"dumptxoutset"}]}' FAKE_DASH_SEEDING=1
  useed "$action" >/dev/null || fail "$action failed while seeding"
  grep -q 'timeout 5 cat' "$FAKE_LOG" && fail "$action unparked a dump the dashboard is seeding"
  reset_useed
  useed "$action" >/dev/null || fail "$action failed with no dump"
  grep -q 'timeout 5 cat' "$FAKE_LOG" && fail "$action opened the FIFO with no dump running"
done
reset_useed
export FAKE_RPCINFO='{"active_commands":[{"method":"dumptxoutset"}]}'
useed stop dashboard >/dev/null
grep -q 'timeout 5 cat' "$FAKE_LOG" && fail "stopping only the dashboard unparked the dump"
useed stop bitcoin >/dev/null
grep -q 'timeout 5 cat' "$FAKE_LOG" || fail "stopping bitcoin did not unpark the dump"
reset_useed
echo "PASS: test_cli.sh"
