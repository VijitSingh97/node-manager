#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

version=${PACKAGE_VERSION:-$(cat VERSION)}
case "$version" in
'' | [!0-9]* | *[!A-Za-z0-9.-]* | *[.-])
  echo "package version must be a Debian version and Docker-compatible tag: $version" >&2
  exit 1
  ;;
esac
if [ "${#version}" -gt 128 ]; then
  echo "package version exceeds Docker's 128-character tag limit" >&2
  exit 1
fi
image_digests=${RUNTIME_IMAGE_DIGESTS_FILE:?Set RUNTIME_IMAGE_DIGESTS_FILE to the published image digest JSON.}
jq -e '
  type == "object" and
  keys == (["tor", "dashboard", "monero", "tari-wallet", "litecoin", "dogecoin", "bitcoincash"] | sort) and
  all(.[]; type == "string" and test("^sha256:[a-f0-9]{64}$"))
' "$image_digests" >/dev/null || {
  echo "Invalid runtime image digest file." >&2
  exit 1
}
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}
export SOURCE_DATE_EPOCH
package=$(mktemp -d)
trap 'rm -rf "$package"' EXIT
root=$package/usr/lib/node-manager
docs=$package/usr/share/doc/node-manager

install -d "$package/DEBIAN" "$package/usr/bin" "$root" "$docs"
sed "s/@VERSION@/$version/g" packaging/postinst >"$package/DEBIAN/postinst"
chmod 755 "$package/DEBIAN/postinst"
install -m 755 scripts/node-manager "$package/usr/bin/node-manager"
ln -s node-manager "$package/usr/bin/bitcoin-stack"
install -m 755 stack configure.sh "$root/"
install -m 755 scripts/node-resource-profile "$root/node-resource-profile"
install -m 755 scripts/node-port-control "$root/node-port-control"
install -m 755 scripts/node-control "$root/node-control"
install -d "$package/usr/lib/systemd/system"
install -m 644 packaging/systemd/node-manager-resource-profile.service packaging/systemd/node-manager-resource-profile.timer \
  packaging/systemd/node-manager-port-control.service packaging/systemd/node-manager-port-control.timer \
  packaging/systemd/node-manager-port-control.path packaging/systemd/node-manager-node-control.service \
  packaging/systemd/node-manager-node-control.timer packaging/systemd/node-manager-node-control.path \
  "$package/usr/lib/systemd/system/"
install -m 644 docker-compose.yml docker-compose.rpc.yml docker-compose.monero-rpc.yml docker-compose.monero-zmq.yml \
  docker-compose.tari-wallet.yml docker-compose.tari-grpc.yml docker-compose.litecoin-rpc.yml docker-compose.dogecoin-rpc.yml docker-compose.bitcoincash-rpc.yml \
  docker-compose.zcash-rpc.yml docker-compose.utxo-index.yml docker-compose.litecoin-utxo-index.yml docker-compose.bitcoincash-utxo-index.yml docker-compose.dogecoin-utxo-index.yml VERSION "$root/"
printf '%s\n' "$version" >"$root/VERSION"
install -m 644 "$image_digests" "$root/runtime-images.json"
for service in tor dashboard monero tari-wallet litecoin dogecoin bitcoincash; do
  digest=$(jq -er --arg service "$service" '.[$service]' "$image_digests")
  compose_file=$root/docker-compose.yml
  count=1
  case "$service" in tari-wallet) compose_file=$root/docker-compose.tari-wallet.yml ;; monero) count=2 ;; esac
  image="ghcr.io/vijitsingh97/node-manager-$service:"
  awk -v image="$image" -v replacement="$image$version@$digest" -v expected="$count" '
    $1 == "image:" && index($2, image) == 1 {
      print substr($0, 1, index($0, image) - 1) replacement
      changed++
      next
    }
    { print }
    END { if (changed != expected) exit 1 }
  ' "$compose_file" >"$compose_file.pinned" || {
    echo "Could not pin $service runtime image." >&2
    exit 1
  }
  mv "$compose_file.pinned" "$compose_file"
done
install -d "$root/build/bitcoin" "$root/build/tari" "$root/build/zcash"
install -m 644 build/bitcoin/bitcoin.conf "$root/build/bitcoin/"
install -m 644 build/tari/config.toml "$root/build/tari/"
install -m 755 build/tari/entrypoint.sh "$root/build/tari/"
install -m 644 build/zcash/zakurad.toml build/zcash/zakurad-address-index.toml "$root/build/zcash/"
install -m 755 build/zcash/healthcheck.sh "$root/build/zcash/"
install -m 644 LICENSE "$docs/copyright"

cat >"$package/DEBIAN/control" <<CONTROL
Package: node-manager
Version: $version
Section: utils
Priority: optional
Architecture: all
Maintainer: Vijit Singh <VijitSingh97@users.noreply.github.com>
Depends: bash, ca-certificates, curl, docker-ce | docker.io, docker-compose-plugin (>= 2.33.1) | docker-compose-v2 (>= 2.33.1) | docker-compose (>= 2.33.1), iproute2, jq, openssl, util-linux
Recommends: node-manager-hostd
Provides: bitcoin-starter-stack
Replaces: bitcoin-starter-stack (<< $version)
Conflicts: bitcoin-starter-stack (<< $version)
Homepage: https://github.com/VijitSingh97/node-manager
Description: Private cryptocurrency nodes routed over Tor
 Runs selected pruned nodes, Tor, and a local dashboard through Docker Compose.
 Configuration lives in /etc; generated state and node data live in /var/lib.
CONTROL

find "$package" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
mkdir -p dist
dpkg-deb --root-owner-group --build "$package" "dist/node-manager_${version}_all.deb"

transitional=$(mktemp -d)
trap 'rm -rf "$package" "$transitional"' EXIT
install -d "$transitional/DEBIAN"
cat >"$transitional/DEBIAN/control" <<CONTROL
Package: bitcoin-starter-stack
Version: $version
Section: oldlibs
Priority: optional
Architecture: all
Maintainer: Vijit Singh <VijitSingh97@users.noreply.github.com>
Depends: node-manager (= $version)
Homepage: https://github.com/VijitSingh97/node-manager
Description: Transitional package for node-manager
 The package was renamed to node-manager. This empty package pulls it in
 and can be removed once the upgrade has completed.
CONTROL
find "$transitional" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
dpkg-deb --root-owner-group --build "$transitional" "dist/bitcoin-starter-stack_${version}_all.deb"

if ! command -v go >/dev/null 2>&1; then
  if [ "${HOSTD_REQUIRED:-0}" = 1 ]; then
    echo "HOSTD_REQUIRED is set but no Go toolchain is available." >&2
    exit 1
  fi
  echo "No Go toolchain: skipping the node-manager-hostd packages." >&2
  exit 0
fi
for arch in amd64 arm64; do
  hostd=$(mktemp -d)
  trap 'rm -rf "$package" "$transitional" "$hostd"' EXIT
  install -d "$hostd/DEBIAN" "$hostd/usr/lib/node-manager" "$hostd/usr/lib/systemd/system"
  (cd build/hostd && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -buildvcs=false -ldflags='-s -w' \
    -o "$hostd/usr/lib/node-manager/hostd" .)
  install -m 644 packaging/systemd/node-manager-host-stats.service packaging/systemd/node-manager-host-stats.timer \
    "$hostd/usr/lib/systemd/system/"
  for script in postinst prerm postrm; do
    install -m 755 "packaging/hostd-$script" "$hostd/DEBIAN/$script"
  done
  cat >"$hostd/DEBIAN/control" <<CONTROL
Package: node-manager-hostd
Version: $version
Section: utils
Priority: optional
Architecture: $arch
Maintainer: Vijit Singh <VijitSingh97@users.noreply.github.com>
Depends: node-manager (= $version)
Homepage: https://github.com/VijitSingh97/node-manager
Description: Host readings helper for Node Manager
 A small static helper that a systemd timer runs as root every 10 seconds
 to write the dashboard's host readings without spawning Docker commands.
 Without it, Node Manager falls back to its shell collector.
CONTROL
  find "$hostd" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
  dpkg-deb --root-owner-group --build "$hostd" "dist/node-manager-hostd_${version}_${arch}.deb"
  rm -rf "$hostd"
done
