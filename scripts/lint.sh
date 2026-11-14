#!/usr/bin/env bash
# The one lint entrypoint for CI and tests/run.sh; tool pins live in .github/linters/Dockerfile.
set -euo pipefail
cd "$(dirname "$0")/.."

lint() {
  local tool=$1 image
  shift
  image=$(sed -n "s/^FROM \([^ ]*@sha256:[0-9a-f]\{64\}\) AS $tool\$/\1/p" .github/linters/Dockerfile)
  [ -n "$image" ] || {
    echo "FAIL: no digest-pinned $tool image in .github/linters/Dockerfile" >&2
    exit 1
  }
  docker run --rm --network none -v "$PWD":/src:ro -w /src "$image" "$@"
  echo "PASS: $tool"
}

lint shellcheck configure.sh stack build/*/*.sh packaging/postinst scripts/*.sh \
  scripts/node-control scripts/node-manager scripts/node-port-control scripts/node-resource-profile tests/*.sh
lint hadolint hadolint build/*/Dockerfile
lint ruff check --no-cache .
lint actionlint

if command -v gofmt >/dev/null; then
  unformatted=$(git ls-files -z '*.go' ':!:experiments' | xargs -0 gofmt -l)
  [ -z "$unformatted" ] || {
    printf 'FAIL: gofmt -w these files:\n%s\n' "$unformatted" >&2
    exit 1
  }
  echo "PASS: gofmt"
else
  echo "SKIP: gofmt (Go not installed)"
fi
