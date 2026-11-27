#!/usr/bin/env bash
# Run the full local test suite. Skips what the machine can't run.
set -euo pipefail
cd "$(dirname "$0")/.."
exec </dev/null

if [ "${CI:-}" = true ] || docker info >/dev/null 2>&1; then
  scripts/lint.sh
else
  echo "SKIP: lint (Docker not running)"
fi

tests/test_configure.sh
tests/test_cli.sh
tests/test_host_install.sh
tests/test_homebrew.sh
tests/test_brew_data.sh
tests/test_release.sh
tests/test_package.sh
tests/test_package_upgrade.sh
tests/test_runtime_images.sh
tests/test_resource_profile.sh
tests/test_port_control.sh
tests/test_node_control.sh
tests/test_zcash.sh
tests/test_native_nodes.sh
tests/test_apt.sh
tests/test_compose.sh
tests/test_tor_supervisor.sh
tests/test_tor_health.sh
tests/test_tor_watchdog.sh
tests/test_tor_image.sh
tests/test_e2e.sh
tests/test_upgrade.sh
tests/test_postsync.sh
python3 -m unittest tests.test_developer_bot tests.test_tor_boundary tests.test_pages tests.test_node_dependencies

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
dashboard_bin=

if command -v go >/dev/null; then
  (
    cd build/dashboard
    go test -race ./...
    go test -count=1 -run '^TestBenchIndexMatchesScanAndIsFaster$' .
    go vet ./...
  )
  (cd build/dashboard && go build -o "$work/dashboard" .)
  dashboard_bin="$work/dashboard"
  for module in tari-wallet hostd; do
    (
      cd "build/$module"
      go test -race ./...
      go vet ./...
    )
  done
  echo "PASS: Go tests, race detector, vet, and build (dashboard, tari-wallet, hostd)"
else
  echo "SKIP: Go checks (Go not installed)"
fi

if python3 -c "import pytest, flask, waitress" 2>/dev/null; then
  (cd build/dashboard && python3 -m pytest tests -q)
  python3 build/dashboard/parity/generate.py --check
  python3 build/dashboard/parity/test_http.py
  if [ -n "$dashboard_bin" ]; then
    DASHBOARD_COMMAND="[\"$dashboard_bin\"]" python3 build/dashboard/parity/test_http.py
  fi
else
  echo "SKIP: dashboard Python reference tests (pip install -r build/dashboard/parity/reference/requirements.txt pytest)"
fi

if command -v node >/dev/null; then
  DASHBOARD_BIN="$dashboard_bin" node --test --test-timeout=600000 "build/dashboard/ui/*.test.mjs"
  node --test build/dashboard/static/*.test.mjs
else
  echo "SKIP: UI flows and frontend unit tests (node not installed)"
fi

echo "All tests passed."
