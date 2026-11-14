#!/usr/bin/env bash
# Run the full local test suite. Skips what the machine can't run.
set -euo pipefail
cd "$(dirname "$0")/.."

if command -v docker >/dev/null; then
  scripts/lint.sh
else
  echo "SKIP: lint (Docker not installed)"
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
tests/test_e2e.sh
tests/test_upgrade.sh
tests/test_postsync.sh
python3 -m unittest tests.test_developer_bot

if command -v go >/dev/null; then
  (
    dashboard_check=$(mktemp)
    trap 'rm -f "$dashboard_check"' EXIT
    cd build/dashboard
    go test -race ./...
    go vet ./...
    go build -o "$dashboard_check" .
  )
  echo "PASS: dashboard Go tests, race detector, vet, and build"
else
  echo "SKIP: dashboard Go checks (Go not installed)"
fi

if python3 -c "import pytest, flask, waitress" 2>/dev/null; then
  python3 -m pytest build/dashboard/tests -q
  python3 build/dashboard/parity/generate.py --check
  python3 build/dashboard/parity/test_http.py
  if command -v go >/dev/null; then
    (
      dashboard_bin=$(mktemp)
      trap 'rm -f "$dashboard_bin"' EXIT
      (cd build/dashboard && go build -o "$dashboard_bin" .)
      DASHBOARD_COMMAND="[\"$dashboard_bin\"]" python3 build/dashboard/parity/test_http.py
    )
  fi
else
  echo "SKIP: dashboard Python reference tests (pip install -r build/dashboard/parity/reference/requirements.txt pytest)"
fi

if command -v node >/dev/null; then
  node --test build/dashboard/static/*.test.mjs
else
  echo "SKIP: frontend unit tests (node not installed)"
fi

echo "All tests passed."
