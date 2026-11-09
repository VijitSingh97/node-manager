#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

version=${PACKAGE_VERSION:-$(cat VERSION)}
: "${APT_SIGNING_FINGERPRINT:?set APT_SIGNING_FINGERPRINT to the dedicated project key}"
printf '%s\n' "$APT_SIGNING_FINGERPRINT" | grep -Eq '^[0-9A-Fa-f]{40}$' || {
  echo "APT_SIGNING_FINGERPRINT must be a full 40-character fingerprint." >&2
  exit 1
}
mkdir -p public
rm -f public/*.deb public/Packages public/Packages.gz public/Release public/Release.gpg public/InRelease public/node-manager.asc public/bitcoin-starter-stack.asc
cp "dist/node-manager_${version}_all.deb" "dist/bitcoin-starter-stack_${version}_all.deb" public/
cd public
gpg --batch --list-secret-keys "$APT_SIGNING_FINGERPRINT" >/dev/null
gpg --batch --armor --export "$APT_SIGNING_FINGERPRINT" >node-manager.asc
cp node-manager.asc bitcoin-starter-stack.asc
apt-ftparchive packages . >Packages
gzip -n -k -f Packages
# Renaming Origin/Label makes existing hosts refuse "apt update" until manually acknowledged.
apt-ftparchive -o APT::FTPArchive::Release::Origin=bitcoin-starter-stack \
  -o APT::FTPArchive::Release::Label=bitcoin-starter-stack \
  -o 'APT::FTPArchive::Release::Architectures=amd64 arm64 all' \
  release . >Release
gpg --batch --yes --armor --local-user "$APT_SIGNING_FINGERPRINT" \
  --detach-sign --output Release.gpg Release
gpg --batch --yes --armor --local-user "$APT_SIGNING_FINGERPRINT" \
  --clearsign --output InRelease Release
