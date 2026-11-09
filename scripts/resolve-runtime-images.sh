#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
version=${1:-$(cat VERSION)}
case "$version" in '' | [!0-9]* | *[!A-Za-z0-9.-]* | *[.-]) echo 'Invalid runtime image version.' >&2; exit 1 ;; esac
[ "${#version}" -le 128 ] || exit 1
result='{}'
for service in tor dashboard monero tari-wallet litecoin dogecoin bitcoincash; do
  image="ghcr.io/vijitsingh97/node-manager-$service:$version"
  manifest=$(docker buildx imagetools inspect "$image" --format '{{json .Manifest}}')
  digest=$(printf '%s' "$manifest" | jq -er '
    select(any(.manifests[]; .platform.os == "linux" and .platform.architecture == "amd64")) |
    select(any(.manifests[]; .platform.os == "linux" and .platform.architecture == "arm64")) |
    .digest | select(test("^sha256:[a-f0-9]{64}$"))')
  result=$(printf '%s' "$result" | jq --arg service "$service" --arg digest "$digest" '. + {($service): $digest}')
done
printf '%s\n' "$result"
