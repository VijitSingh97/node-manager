#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

usage() {
  echo "Usage: scripts/build-homebrew-formula.sh [--url RELEASE_URL] [--output FORMULA] ARCHIVE" >&2
  exit 2
}

release_url=
output=dist/Formula/node-manager.rb
while [ "$#" -gt 0 ]; do
  case "$1" in
    --url)
      [ "$#" -ge 2 ] || usage
      release_url=$2
      shift 2
      ;;
    --output)
      [ "$#" -ge 2 ] || usage
      output=$2
      shift 2
      ;;
    --*) usage ;;
    *)
      [ "$#" -eq 1 ] || usage
      archive=$1
      shift
      ;;
  esac
done

[ -n "${archive:-}" ] || usage
[ -f "$archive" ] || { echo "release archive not found: $archive" >&2; exit 1; }
contents=$(mktemp)
trap 'rm -f "$contents"' EXIT
tar -tzf "$archive" >"$contents" 2>/dev/null || {
  echo "release archive is not a readable tar.gz" >&2
  exit 1
}
if grep -Eq '(^/|(^|/)\.\.(/|$))' "$contents"; then
  echo "release archive contains an unsafe path" >&2
  exit 1
fi
for required in \
  node-manager/stack \
  node-manager/configure.sh \
  node-manager/VERSION \
  node-manager/docker-compose.yml \
  node-manager/docker-compose.tari-wallet.yml \
  node-manager/build/dashboard/Dockerfile \
  node-manager/build/bitcoin/bitcoin.conf \
  node-manager/build/tari/config.toml \
  node-manager/build/tari/entrypoint.sh \
  node-manager/build/tari-wallet/Dockerfile \
  node-manager/build/tari-wallet/go.mod \
  node-manager/build/tari-wallet/launcher.go \
  node-manager/build/tari-wallet/config.toml \
  node-manager/build/zcash/zakurad.toml \
  node-manager/build/zcash/healthcheck.sh \
  node-manager/scripts/node-manager \
  node-manager/scripts/node-control \
  node-manager/scripts/node-port-control \
  node-manager/scripts/node-resource-profile; do
  grep -Fxq "$required" "$contents" || {
    echo "release archive is missing $required" >&2
    exit 1
  }
done
version=${PACKAGE_VERSION:-$(cat VERSION)}
printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || {
  echo "VERSION must be plain semantic version" >&2
  exit 1
}
release_url=${release_url:-https://github.com/VijitSingh97/node-manager/releases/download/v${version}/node-manager.tar.gz}
case "$release_url" in
  https://* | file:///*) ;;
  *) echo "release URL must use https:// or an absolute file:// URL" >&2; exit 1 ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
  checksum=$(sha256sum "$archive" | awk '{print $1}')
else
  checksum=$(shasum -a 256 "$archive" | awk '{print $1}')
fi
case "$checksum" in
  '' | *[!0-9a-f]*) echo "could not calculate SHA-256" >&2; exit 1 ;;
esac
[ "${#checksum}" -eq 64 ] || { echo "could not calculate SHA-256" >&2; exit 1; }

escape_sed() { printf '%s' "$1" | sed 's/[\\&|]/\\&/g'; }
version_escaped=$(escape_sed "$version")
url_escaped=$(escape_sed "$release_url")
checksum_escaped=$(escape_sed "$checksum")
mkdir -p "$(dirname "$output")"
sed -e "s|@VERSION@|$version_escaped|g" \
  -e "s|@RELEASE_URL@|$url_escaped|g" \
  -e "s|@SHA256@|$checksum_escaped|g" \
  Formula/node-manager.rb.in >"$output"
echo "Wrote $output for v$version ($checksum)"
