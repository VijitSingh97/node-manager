#!/bin/sh
set -eu
go list -deps ./... | grep '\.' | sort -u >/tmp/pebble-bench-deps.txt
go list -m -f '{{.Path}}|{{.Version}}|{{.Dir}}' all | tail -n +2 | while IFS='|' read -r p v d; do
  [ -n "$d" ] || {
    echo "$p $v | not downloaded (module graph only)"
    continue
  }
  f=$(ls "$d" | grep -iE '^(LICEN[SC]E|COPYING)' | head -1 || true)
  t=none
  [ -n "$f" ] && t=$(grep -m1 -oE 'Apache License|MIT License|BSD|Permission is hereby granted|Redistribution and use|Mozilla Public|ISC License' "$d/$f" || echo unknown)
  l=no
  grep -q "^$p" /tmp/pebble-bench-deps.txt && l=yes
  echo "$p $v | ${f:-no file} | $t | linked=$l"
done
rm -f /tmp/pebble-bench-deps.txt
