# Pebble UTXO ingest and lookup micro-benchmark

Throwaway spike for issue #217 (part of #213). It measures whether Pebble can
hold a UTXO index shaped like the planned layout under the dashboard's small CPU
and cache budget. It is a separate Go module so the dashboard module gains no
dependency.

## What it does

- Entry: key = 8-byte scripthash prefix || 32-byte txid || 4-byte vout (44 B),
  value = 8-byte amount || 4-byte height (12 B). About two entries per prefix.
- Externally sorts the synthetic entries (sorted in-memory runs on disk, k-way
  merge), writes them as non-overlapping sstables with `sstable.Writer`, and
  ingests them with `DB.Ingest`.
- Opens the database with a block cache of `-cache-mb` and a prefix bloom filter
  (`Split` at 8 bytes), then runs random prefix lookups (`SeekPrefixGE` plus
  `Next`) and random point `Get`s, cold then warm.
- Reports wall times, peak temp disk, final size, latency percentiles and peak
  RSS (`getrusage`). `-time-limit`, `-max-disk-gb` and a thermal-zone guard
  (`-max-temp-c`, default 88) abort the run; `-clean` removes the data.

## Run

```sh
go run . -n 10000000 -dir ./data -clean
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o pebble-utxo-bench .
GOMEMLIMIT=700MiB nice -n 19 taskset -c 3 ./pebble-utxo-bench -n 10000000 \
  -dir /path/to/data -clean -time-limit 25m -max-disk-gb 4 -cache-mb 64
```

`./licenses.sh` prints every module in `go list -m all` with its licence file
type and whether it is linked. For 100M entries expect about 11 GiB of runs plus
SSTs at peak and several minutes of wall time; pass `-n 100000000` and a larger
`-max-disk-gb`.

Cold means the process was freshly started on the existing database with
`posix_fadvise(DONTNEED)` applied to every file (Linux only; on macOS the OS
cache is not dropped). Warm means after 200k warm-up lookups.

## Results

Pebble v1.1.5, Go 1.27, 64 MiB block cache, snappy compression, bloom 10 bits.

| Run | Entries | Gen+sort | Merge to SSTs | Ingest | Pipeline wall | Peak temp disk | Final size | Peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| macOS arm64 laptop (18 cores, validation) | 5M | 2.9 s | 1.9 s | 38 ms | 4.8 s | 0.55 GiB | 0.29 GiB (62.6 B/entry) | 0.27 GiB |
| mini-1, one core (taskset), nice 19, GOMEMLIMIT 700 MiB | 10M | 8.0 s | 5.6 s | 252 ms | 13.8 s | 1.10 GiB | 0.58 GiB (62.4 B/entry) | 0.22 GiB |

Lookup latency on mini-1 (10M entries, 100k lookups per row, one core):

| Phase | p50 | p90 | p99 | p99.9 |
| --- | --- | --- | --- | --- |
| cold prefix | 64.9 us | 79.4 us | 148 us | 528 us |
| cold point | 9.3 us | 65.5 us | 83 us | 171 us |
| warm prefix | 8.4 us | 12.4 us | 28 us | 220 us |
| warm point | 7.5 us | 10.0 us | 18 us | 120 us |

Final size is about 62.4 B per entry for 44 B keys and 12 B values (random
hashed keys do not compress), so 170-190M UTXOs extrapolate to roughly 10-11 GiB
before any compaction churn; the 12-15 GB estimate is the right order. Ingest of
pre-sorted SSTs is nearly free (the files are placed into the bottom level), so
the cost is the external sort, which is about 1.4 s per million entries on one
throttled core.

The 100M run was not done on mini-1 (box is production and runs hot); it is left
for the owner's window. The 10M run kept the box under the 88 C guard (post-run
reading 74 C against 60 C before) and its data was deleted afterwards.

Caveats: one pinned core approximates but does not enforce the 0.5 CPU cgroup
quota; the page cache is not dropped on mini-1 beyond fadvise, so "cold" still
benefits from readahead of the host; lookups run with no concurrent bitcoind
load. A SQLite (modernc) variant was not built.

## Dependencies and licences

`go list -m all` lists 127 modules (the module graph, mostly Pebble's test and
tooling requirements); only 26 are compiled into the static linux/amd64 binary.
Run `./licenses.sh` for the full per-module table. With `CGO_ENABLED=0` the cgo
zstd binding `DataDog/zstd` (BSD-3) is not linked; `klauspost/compress` is.

Linked modules and licences, all permissive:

| Module | Licence |
| --- | --- |
| cockroachdb/pebble | BSD-3-Clause |
| cockroachdb/errors, fifo, logtags, redact, tokenbucket | Apache-2.0 |
| beorn7/perks, cespare/xxhash/v2, getsentry/sentry-go, kr/pretty, kr/text | MIT |
| gogo/protobuf, golang/protobuf, google.golang.org/protobuf, golang/snappy | BSD-3-Clause |
| klauspost/compress, rogpeppe/go-internal, golang.org/x/exp, x/sys, x/text | BSD-3-Clause |
| pkg/errors | BSD-2-Clause |
| matttproud/golang_protobuf_extensions, prometheus/client_golang, client_model, common, procfs | Apache-2.0 |

No copyleft licences. Pebble pulls in Sentry, Prometheus and protobuf client
code through `cockroachdb/errors`, which is the notable weight for a "small
stack" argument.
