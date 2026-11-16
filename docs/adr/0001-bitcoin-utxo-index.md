# ADR 0001: Bitcoin UTXO index for instant watch-only balances

- Status: Accepted 2026-11-14. The owner chose to build the index: opt-in, balances only, filled by a one-time export once the node is synced. Building it during initial sync (#267) is not planned, by the owner's decision on 2026-11-16.
- Date: 2026-11-14
- Issue: #219, part of epic #213. Inputs: #216 and #217, both measured.
- Scope: Bitcoin on pruned nodes. Litecoin follows in #238. Monero and Tari are out of scope.
- Built: #265, #268, #269 and #274 to #282. The sections below describe what was built. Where the build differs from the original decision, the text says so.
- Measured on mainnet: the first seed, on mini-1 on 2026-11-15 (`docs/measurements/utxo-seed-mainnet.md`). Numbers from that run are labelled "measured, mainnet seed".

## How to read this ADR

Every number carries one label.

- **Measured** with a citation (#216 or #217). Someone ran it on the reference node: a 2018 Mac mini, 0.5 CPU for bitcoind, pruned at 10000 MB.
- **Derived.** Arithmetic on measured numbers. The arithmetic is shown once, in "Sizing and timing".
- **Reasoned.** From reading code or from how a tool behaves. Not run.

Each decision is **Decided** (evidence exists) or **Provisional** (reasoned, stands unless a later result contradicts it). Some measurements are still outstanding, mostly on mainnet. They are listed in Open items.

## Context

Watch-only balances on a pruned Bitcoin node use `scantxoutset`, a full walk of the UTXO set (`build/dashboard/pruned_scan.go`). A newly added wallet shows pending until a scan finishes.

PR #215 already changed the picture. A wallet with a cached scan result follows new blocks. `applyBlocks` reads each new block with `getblock` verbosity 2 and updates the cached outpoints. For one block and two wallets this took 0.8 s and 1.5 s (measured, earlier read-only comment on #216). It falls back to a full scan when the cache is more than `prunedDeltaBlocks` (144) blocks behind, or when any step fails.

Facts, all from the reference node. Rows sourced to the "earlier #216 comment" come from the read-only run before the supervised one; the supervised results record them only as unchanged.

| Fact | Value | Source |
| --- | --- | --- |
| UTXO count | 163,325,604 (`coins_written`); `scantxoutset` saw 163,325,996 | measured, #216 |
| `scantxoutset` full walk of one script (`raw(6a)`, matches nothing): a single-script baseline | 153 s | measured, #216 |
| Real wallet scan time: 2000 scripts (two `wpkh` descriptors with range 0-999, as `pruned_scan.go` builds them, on a public BIP32 test-vector key) | 162 s with the Tari base node running, no RPC block, peak 83 C. Matching thousands of scripts adds about 9 s to the walk | measured, #219 |
| RPC during `scantxoutset` | not blocked: 0 of 30 probes failed, p50 212 ms | measured, #216 |
| `dumptxoutset` wall time | 592 s to a file, 613 s to a FIFO | measured, #216 |
| Dump file size | 9,319,541,901 bytes (8.68 GiB), 57.1 B per coin, mode 0600, uid 1000 | measured, #216 |
| RPC during `dumptxoutset` | failing for about 300 s from the start (33 of 88 probes timed out), then degraded but answering for the rest of the dump (p50 206 ms against 72 ms idle) | measured, #216 |
| Why RPC fails for the first 300 s only | `cs_main` is held through the hash pass and released for the write. #216 says the timings "fit" this; nobody checked the locking | reasoned |
| Compose health during the dump | bitcoind unhealthy for about 2.5 min, recovered by itself | measured, #216 |
| Restarts during the run | none: `StartedAt` unchanged, `RestartCount` 0 before and after, no watchdog or `depends_on` action, across two dumps with bitcoind unhealthy about 2.5 min each | measured, #216 (one run, two dumps) |
| Node-down alert | one "down" and one "recovered" per dump (06:16:49 / 06:18:08 and 06:34:46 / 06:36:06) | measured, #216 |
| Read of the dump through the dashboard's read-only mount | works as uid 1000; 100 s for a full read (about 93 MB/s) | measured, #216 |
| FIFO created by the bitcoin container, read by the dashboard through the read-only mount | works end to end: 9,319,542,342 bytes received, Core renamed the FIFO on completion | measured, #216 |
| Peak host temperature by phase | dump 80 C, uncapped dashboard full read 92 C, FIFO dump 89 C, scan 81 C | measured, #216 |
| Stack thermal throttle | capped bitcoind to 0.25 CPU and monerod to 0.50 at 91 C (released at 75 C) and again at 88 C (released at 73 C) | measured, #216 |
| Chain | tip 975705 at the earlier read (975750 to 975752 during the supervised run), pruneheight 970251, 5455 retained blocks at prune_mb 10000 | measured, earlier #216 comment (read-only run, 2026-11-14); #216 results say "unchanged" |
| `getblock` v3 on oldest and newest retained block | succeeds; 1.8-3.0 s and 17.7-19.4 MB per block. The time includes `docker exec` overhead, so it is an upper bound for an in-process RPC call | measured, earlier #216 comment |
| Chainstate | 11 GB | measured, earlier #216 comment |
| Dashboard can read the datadir and `/sys/class/thermal/thermal_zone0/temp` | yes, uid 1000 | measured, earlier #216 comment (the datadir read is also in #216 results) |
| Healthcheck time to unhealthy | about 3 min (60 s interval, 3 retries, 15 s timeout) | repo |
| Docker's restart policy never acts on an unhealthy container, only on exit | reasoned from Docker semantics. It holds only while no autoheal-style watchdog is present. The run did not test other stacks, and one run does not prove the policy | reasoned |
| Node-down alert threshold | 3 consecutive minutely RPC failures | repo, `settings.go:22` |
| Dashboard `mem_limit` and `cpus` | none set | `docker-compose.yml` |
| Dashboard datadir mount | read-only; `/state` is a writable named volume; no docker socket | `docker-compose.yml` |
| Pinned Bitcoin Core | `bitcoin/bitcoin:32.1` by digest | `docker-compose.yml` |

Pebble benchmark (#217, branch `spike/pebble-utxo-bench` at 1e5aa59, Pebble v1.1.5, `CGO_ENABLED=0`, 64 MiB block cache, `GOMEMLIMIT` 700 MiB, `nice 19`, synthetic entries, measured):

| Item | 10M, one pinned core | 100M, real 0.5 CPU cgroup quota |
| --- | --- | --- |
| Generate and sort runs | 8.0 s | 3 min 35 s (50 runs) |
| Merge to sorted SSTs | 5.6 s | 2 min 26 s (42 files) |
| Ingest | 0.25 s | 80 ms |
| Pipeline wall | 13.8 s | 6 min 1 s |
| Peak temp disk (runs plus SSTs) | 1.10 GiB | 10.94 GiB |
| Final size | 0.58 GiB, 62.4 B per entry | 5.72 GiB, 61.5 B per entry |
| Peak RSS | 0.22 GiB | 0.25 GiB |
| Peak host temperature | 74 C | 79 C |
| Cold prefix lookup p50 / p99 / p99.9 / max | 65 us / 148 us / n/a / n/a | 75.6 us / 163 us / 414 us / 67 ms |
| Warm prefix lookup p50 / p99 | 8.4 us / 28 us | 10.8 us / 103 us |

Block cache hit rate at 100M was 66.5%. Keys in the benchmark use an 8-byte prefix, not the 16 bytes chosen below. No SQLite variant was built. The 100M run did not include a dump read or a bitcoind running next to it.

## Sizing and timing (all arithmetic, once)

Per entry at the benchmark's 8-byte prefix: a sort run holds 56 B (44 B key plus 12 B value, the logical size). The final index is 61.5 B (measured, 5.72 GiB over 100M). The measured peak is 10.94 GiB x 1.0737 = 11.75 GB, which over 100M entries is 117.5 B. That equals 56 + 61.5, so the working model is: runs and SSTs coexist at the peak.

This is a consistency check, not an independent test. The 117.5 B comes from the one measured total. The split between run bytes and SST bytes was not logged by phase, so the model is inferred from the total. The 16 B runs since recorded final size (#268) and peak and final size (#276), not size by phase, so the split is still inferred.

At the chosen 16-byte prefix (derived, and it assumes the extra 8 B of hash bytes do not compress, in both the runs and the SSTs): run 56 + 8 = 64 B, index 61.5 + 8 = 69.5 B, peak 64 + 69.5 = 133.5 B per entry. Synthetic 16 B runs since, on an Apple M5 Max and not the reference node (measured): 30M coins through the ingest path peaked at 127.3 B per coin with a 63.6 B final index (#276), and 10M entries through the store took 72.5 B per entry including the undo journal, about 69 B for coins (#268). Neither ran at 163.3M entries or at 0.5 CPU.

Scaled to 163.3M entries (measured count, linear scaling as stated in #217):

| Quantity | 8 B prefix (as benchmarked) | 16 B prefix (chosen) |
| --- | --- | --- |
| Final index | 61.5 x 163.3M = 10.0 GB (9.35 GiB) | 69.5 x 163.3M = 11.35 GB (10.57 GiB) |
| Peak temp disk (runs plus SSTs, includes the index) | 117.5 x 163.3M = 19.2 GB (17.9 GiB) | 133.5 x 163.3M = 21.8 GB (20.3 GiB) |

Pre-seed free-space threshold, derived from the 16 B column with a 20% margin (judgment):

- FIFO seed (no dump file): 21.8 GB x 1.2 = 26.2 GB. **Check for 27 GB free.**
- File seed (dump on disk too, the rejected path): (9.32 + 21.8) x 1.2 = 37.3 GB. Would need 38 GB.

Seed time at 0.5 CPU (derived). The 100M pipeline took 361 s. Scaled by 1.633 it is 589 s (9.8 min), a floor because sorting is n log n. Of that, run generation is 215 s x 1.633 = 351 s and merge is 146 s x 1.633 = 238 s.

- FIFO: run generation overlaps the part of the dump after the hash pass (613 - 300 = 313 s) and needs 351 s, so it ends about 38 s after the dump. Then the merge. Total about 613 + 38 + 238 = 889 s, about 15 min.
- File: 592 s dump, then 100 s read, then 589 s pipeline, about 1281 s, 21 min. The post-hash write phase of a file dump is 592 - 300 = 292 s.

Both totals are floors, and three assumptions sit under them. None was measured.
1. Parsing the dump is no slower than the benchmark's synthetic run generation.
2. FIFO: the dump write phase takes the measured 313 s. With a FIFO the writer waits on the reader, and the reader runs at 0.5 CPU, so the dump itself may lengthen. That would also stretch the degraded-RPC period.
3. File: the 100 s read was uncapped (it hit 92 C). A capped read is slower.

The first mainnet seed took 1179 s (measured, mainnet seed): a 306 s hash pass, about 685 s of write and import with 8 heat pauses, and about 180 s of merge. Assumption 2 held: the writer waited on the 0.5 CPU reader, and RPC kept answering through the write, only slower. User-facing text now says about 20 minutes, longer if heat pauses it.

Lookup batch (derived): a wallet window of 1000 external plus 1000 internal scripts is 2000 lookups. At the 100M cold p50, 2000 x 75.6 us = 0.15 s. At the cold p99, 2000 x 163 us = 0.33 s. The single worst lookup was 67 ms.

## Decision drivers

1. Stay small. `AGENTS.md` and `CONTRIBUTING.md` prefer deleting over adding and stdlib over new dependencies. The dashboard module has only grpc and protobuf as direct dependencies. It builds with `CGO_ENABLED=0` on alpine.
2. No runtime change for installs that do not opt in, apart from the #234 hint: no new volume, memory limit or restart. The image and binary are a separate question (see "Dependency ships to everyone"): a dependency compiled in is not runtime-free.
3. Never wrong silently. Any doubt about the index falls back to `scantxoutset`.
4. Do not hurt the live node. `cs_main` stalls, heat and disk are shared with the other chains on the box.
5. Targets from #213: add an indexed wallet under 1 s, cached read under 100 ms, batch registration, incremental blocks, works on pruned nodes.
6. Privacy: wallet data is sensitive, chain data is not.

## Is a full index needed at all?

The measurements make the existing path better than the draft assumed.

What the scan path gives today, with no new dependency, disk or restart:
- A wallet that has been scanned once follows each new block in about a second (measured, #216 read-only comment).
- A first scan takes about 162 s for a 2000-script wallet (measured, #219) and does not block RPC (measured, #216). The old worry that every scan stalls the node was wrong.
- Balances survive restarts through the JSON scan cache.
- Wallets are scanned in batches, so several wallets cost one walk.

What the full index would still add:
1. A newly added wallet shows a balance in under 1 s (0.15-0.33 s of lookups, derived) instead of waiting for a scan. The scan takes about 162 s for a 2000-script wallet (measured, #219); one script takes 153 s, so matching thousands of scripts adds little.
2. No periodic full scan after a reorg deeper than the cache can follow, after a gap of more than 144 blocks, or after a failed delta.

It adds nothing for wallets already tracked. It adds no history. It does not help unpruned nodes.

What it costs:

| Cost | Value |
| --- | --- |
| Steady disk | 11.35 GB (derived, 16 B prefix) |
| Peak disk while seeding | 21.8 GB, 20.3 GiB (derived); 31.1 GB if a dump file is used |
| Seed time | 19.7 min (measured, mainnet seed); about 15 min was the no-pause floor (derived) |
| RPC during a seed | failing for about 300 s (measured), then degraded but answering (p50 206 ms) for about 313 s more on a FIFO, 292 s on a file (derived: 613 - 300, 592 - 300) |
| Node-down alert and unhealthy compose state | one alert, unhealthy about 2.5 min (measured), per seed |
| Heat | an uncapped consumer reached 92 C and triggered the stack's throttle (measured) |
| Code | 23 new linked modules (7 to 30 on amd64 and arm64), a dashboard binary about 42% larger and a larger image for every install (measured, #268), a longer THIRD_PARTY_NOTICES, and roughly 18 child issues (#220-#237) |

The index's own seed blocks RPC for longer than any scan ever does. Scans are the cheaper way to hold `cs_main`: they do not hold it at all.

### #213 targets, by choice

| #213 target | Defer (scan path plus #215) | Build the Pebble index, opt-in |
| --- | --- | --- |
| Add an already-indexed wallet under 1 s | No. About 162 s for a 2000-script wallet (measured) | Yes. 0.15-0.33 s of lookups (derived), plus derivation, 380-422 ms once per wallet on regtest (measured, #275), cached afterwards |
| Cached read under 100 ms | Reasoned yes: it is a read of the JSON cache. Not benchmarked | Yes (lookups far below 100 ms) |
| Batch registration | Partly. One scan serves a batch, but each batch costs about 162 s. Option E queues adds into one scan | Yes |
| Incremental blocks | Yes, #215 (measured) | Yes |
| Pruned nodes | Yes | Yes |
| Opt-in, no runtime change on upgrade, scan fallback | Yes | Yes for runtime (overlay). Not for the image: the binary carries Pebble for everyone unless built otherwise (see "Dependency ships to everyone") |

Only the first row separates the two choices, and only the index meets it. Option C below meets the same rows as deferring. Option E below is the concrete content of the defer path. It does not meet the first row, but it shortens the felt wait and improves batch registration at little cost.

### Decision

**Accepted 2026-11-14: build the index, balances only.** The owner's goal: a user can enter any wallet and see its balance as close to instantly as possible. Only option A meets it. A new wallet's balance comes from 0.15-0.33 s of lookups (derived) instead of a 162 s scan (measured). The wallet's balance sparkline is drawn from the same data. Today it plots the wallet's unspent coins by arrival height (`chainHistorySeries` in `watch_view.go`), and the index stores each coin's amount and height. So the sparkline appears with the balance, with no extra query.

Transaction history of spent coins is out of scope. A pruned node no longer holds those blocks, and an all-address history index would be very roughly 150-300 GB (estimate).

The index is opt-in (section 3). It is filled by the one-time export (`dumptxoutset` through a FIFO), section 4, once the node is synced, on fresh and existing installs alike.

Building it during initial sync instead (#267) is not planned, by the owner's decision on 2026-11-16. The dashboard would follow blocks from height 0 with Core in manual prune mode, pruning only behind its own indexed height. A plan for it estimated 6 to 8 days of work. At the measured 1.8 to 3.0 s per recent block for `getblock` verbosity 3, it would add roughly 8 to 13 days of CPU to a full sync on the reference node (inferred). A raw-block reader would be faster but needs a reverse outpoint map of about 9 GB more (inferred). It would replace a seed that took 20 minutes on the same node (measured).

The owner accepts the costs listed under "Is a full index needed at all?" for this goal. Option E (progress UI and queued-add batching) remains useful as the fallback while the index is off, not seeded or not ready. It needs its own issue.

## Options considered

### A. Pebble, full scriptPubKey-hash index (the design if built)

Pure Go key-value store (`github.com/cockroachdb/pebble`). Seeded from a `dumptxoutset` snapshot, then followed with `getblock` verbosity 3.

- Pros:
  - Measured at 100M entries under a 0.5 CPU quota: 6 min pipeline, 61.5 B per entry, 0.25 GiB peak RSS, cold prefix lookup p50 75.6 us and p99 163 us (#217).
  - Sorted SST ingest is nearly free (80 ms at 100M). The cost is the external sort.
  - Pure Go, so `CGO_ENABLED=0` still works. Writes are atomic through batches and a WAL. Every commit uses `pebble.Sync`, so the WAL is fsynced before a block is reported applied, and a committed block survives a kill or power loss (#222).
- Cons:
  - New dependency with 23 new linked modules (measured in the build, #268; the spike counted 26, below), including Sentry, Prometheus and protobuf client code pulled in through `cockroachdb/errors`.
  - Pebble can ratchet its on-disk format version, so rolling back the dashboard image may leave the index unreadable. The version policy below handles this (invalid, re-seed).
  - The dashboard binary grows by about 42%: +5.9 MB on amd64 and +5.4 MB on arm64 (measured, #268).
  - The 16-byte prefix and a dump read under the CPU cap were not run.

Transitive set and licences (measured by `licenses.sh` in the spike, unchanged at 100M): `go list -m all` shows 127 modules, 26 are compiled into the static linux/amd64 binary with `CGO_ENABLED=0`. All are permissive. The merged build counted 7 to 30 linked modules on amd64 and arm64, 23 new (#268).

| Licence | Linked modules |
| --- | --- |
| BSD-3-Clause | cockroachdb/pebble, gogo/protobuf, golang/protobuf, google.golang.org/protobuf, golang/snappy, klauspost/compress, rogpeppe/go-internal, golang.org/x/exp, x/sys, x/text |
| Apache-2.0 | cockroachdb/errors, fifo, logtags, redact, tokenbucket; prometheus/client_golang, client_model, common, procfs; matttproud/golang_protobuf_extensions |
| MIT | beorn7/perks, cespare/xxhash/v2, getsentry/sentry-go, kr/pretty, kr/text |
| BSD-2-Clause | pkg/errors |

No copyleft. `DataDog/zstd` (cgo, BSD-3) is not linked.

THIRD_PARTY_NOTICES impact: `build/dashboard/THIRD_PARTY_NOTICES` was 602 lines before Pebble. Pebble adds a notice and licence text for each linked module that requires one. #268 updated the file in the same PR, as `CONTRIBUTING.md` requires, to 3382 lines.

### B. SQLite through modernc.org/sqlite

- Pros: single file, simple transactions, strong crash safety, easy to inspect, pure Go (transpiled C), so `CGO_ENABLED=0` works.
- Cons: the dependency is also large (modernc libc, memory and mathutil plus the generated SQLite source), so it does not clearly beat Pebble on the small-stack driver. Loading 163.3M rows into a B-tree is slower than ingesting sorted SSTs unless rows go in key order into a `WITHOUT ROWID` table. No compression. A 52 B key and 12 B value plus page and cell overhead is likely 70-80 B per row, about 11.4-13.1 GB at 163.3M rows (derived: 70 and 80 x 163.3M).
- Status: **rejected, reasoned.** No variant was built. It is reopened only if Pebble's dependency weight is rejected at review (#222).

### C. Watch-list-only forward tracker in JSON under /state

Keep #215's model: store each wallet's scriptPubKey set and outpoints in JSON (`writePrivateFileAtomic`), follow blocks, no full UTXO index.

- Pros: no new dependency, no volume, no bitcoind change, no seeding.
- Cons: an unknown wallet still needs a full `scantxoutset`. There is no cheap backfill. Reading the 5455 retained blocks at 1.8-3.0 s each is 5455 x 1.8 = 2.7 h to 5455 x 3.0 = 4.5 h (derived) and covers about five weeks of chain, so it would give wrong balances.
- Status: **rejected for the goal, reasoned and partly measured.** A new wallet still waits for a full scan. The scan path stays as the fallback in every outcome.

### E. Progress UI and queued-add batching (the fallback)

Improve the wait instead of removing it. Show scan progress or a time estimate for a pending wallet. Queue wallets added while a scan runs and serve them with one scan.

- Pros: no new dependency, disk, volume or seed. Meets "batch registration" fully, because queued adds share one walk. Makes the wait visible, which is where #215 and the measured 162 s point. Needs no owner decision on Pebble.
- Cons: a new wallet still waits for a full scan. It does not meet "add an indexed wallet under 1 s".
- #213 rows it improves: batch registration (Partly to Yes) and, as felt latency only, the wallet-add wait. It leaves the other rows unchanged.
- Status: **kept as the fallback while the index is off, not seeded or not ready.** Reasoned. Not built or measured. It needs its own issue.

### D. Separate index service (own container)

- Pros: isolates memory and crashes, gives the index its own limits and lifecycle, keeps the dashboard dependency-free.
- Cons: a new image, a new fixed IP on the btc_net network, a new healthcheck, another RPC credential path, another component to release, and the same Pebble dependency. It changes the compose network configuration, which #213 asks us to avoid.
- Status: **rejected, reasoned.** The 100M run peaked at 0.25 GiB RSS (measured, #217), well under the 600 MiB threshold set for reopening this.

### RocksDB

`grocksdb` needs cgo. `build/dashboard/Dockerfile:1-24` builds with `CGO_ENABLED=0` on alpine. **Rejected, measured** (the Dockerfile settings are the evidence). Bbolt and Badger were not benchmarked.

## Design

### 1. Engine, placement and layout

| Topic | Decision | Status |
| --- | --- | --- |
| Engine | Pebble v1.1.5, pinned in `build/dashboard/go.mod`. Measured at 100M (#217): peak RSS 0.25 GiB against the 600 MiB threshold, cold p99 163 us against 1 ms, final size 5.72 GiB (6.14 GB), peak temp 10.94 GiB (11.75 GB). Derived to 163.3M at the chosen 16 B prefix: final 11.35 GB (10.57 GiB) against 15 GB, peak 21.8 GB (20.3 GiB) against 22 GB. The peak is within about 1% of the threshold in GB (21.8 / 22 = 0.991), a near miss | Decided for RSS and lookup (#217). Provisional for size and peak: synthetic 16 B runs came in under the derived numbers (#268, #276), not yet run at 163.3M on the reference node |
| Process placement | Inside the dashboard process, behind a small interface so a separate service stays possible | Decided (#217 memory) |
| Key | `SHA-256(scriptPubKey)[:16] \|\| txid (32 B) \|\| vout (4 B, big endian)` | Decided |
| Value | `amount (8 B) \|\| height (4 B)` | Decided |
| Spends | `getblock` verbosity 3 includes the prevout scriptPubKey from undo data, so a spend deletes `hash(prevout script)[:16] \|\| txid \|\| vout` with no reverse map | Decided (v3 measured on retained blocks) |
| Unspendable outputs | Outputs whose script starts with OP_RETURN (0x6a) or is longer than 10,000 bytes are not stored (`utxoSpendable`). Other unspendable forms are stored | Decided |
| Indexed by | scriptPubKey hash, never by address string | Decided (#213) |

Why 16 bytes and not the benchmark's 8: a 64-bit prefix gives a collision chance of about 1/1.8e19 per distinct script, so 163.3M / 1.8e19 = 9e-12 for one wallet script (derived). That is tiny, but it would produce a silently wrong balance, which breaks driver 3. A 128-bit prefix removes the case in practice. It costs 8 B per entry (see Sizing). #268 and #276 re-measured size at 16 bytes on synthetic data (see Sizing). Cold lookup latency at 16 bytes on the reference node is still open.

### 2. Wallet queries and the cost of adding an unknown wallet

Lookup is a prefix scan per wallet script. The derived batch cost is 0.15 s at p50 and 0.33 s at p99 for 2000 scripts (see Sizing). That fits the 1 s target before derivation. Deriving 2000 scripts took 380 to 422 ms on regtest with bitcoind at 0.5 CPU (measured, #275), and the result is cached per wallet (section 9).

| Choice | Cost of adding an unknown wallet |
| --- | --- |
| Pebble index | Derive scripts once (380-422 ms on regtest, measured, #275; cached afterwards), then 0.15-0.33 s of lookups (derived from #217) |
| SQLite index | Reasoned: similar once built. No measurement |
| Defer / watch-list JSON | A full `scantxoutset`: about 162 s for 2000 scripts (measured, #219), no RPC stall. The wallet shows pending meanwhile |
| Separate service | Same as Pebble plus a local network hop |

### 3. Opt-in mechanism and upgrade behavior

**Decided: an optional compose overlay, `docker-compose.utxo-index.yml`, enabled by a `configure.sh` option, default off.**

With FIFO seeding the overlay no longer touches the bitcoin service. It adds, only when enabled: the `utxo_index` named volume, and on the dashboard a `mem_limit` of 1g, `cpus: 0.5`, and the `UTXO_INDEX_ENABLED` and `UTXO_INDEX_DIR` environment values. The Pebble settings are constants in the dashboard code. This is the same pattern as `docker-compose.tari-grpc.yml`.

Why not unconditional: the dashboard `mem_limit` and `cpus` would change every install, and cannot be conditional without an overlay. Rejected, reasoned.

Owner decision (2026-11-15, #234): the one change a non-opt-in install sees is a one-line dashboard hint, shown only when the index is off and the Bitcoin node is pruned, naming `bitcoin.utxo_index` and linking `docs/watch-only.md` on GitHub. Nothing else changes for installs that do not opt in.

What existing installs get on upgrade: no runtime change. No new volume, no limit, no bitcoind change, no restart. `scantxoutset` and the #215 path stay. They do get the image, see below. An install that enables the option gets the volume and the dashboard limits, and an index in the "not seeded" state until the user starts a seed. bitcoind is not recreated (this is a change from the draft, which needed a writable dump mount).

**Dependency ships to everyone.** Pebble and its 23 new modules compile into the one dashboard binary and image that every install pulls, opted in or not. That means a larger image, a longer THIRD_PARTY_NOTICES and new supply-chain surface for all installs. Measured in #268 on amd64 and arm64: linked modules went from 7 to 30, and the binary from 14.11 to 20.04 MB on amd64 (+42%) and from 13.17 to 18.61 MB on arm64 (+41%). #213 says a non-opt-in install gets no new behavior. A larger binary is not behavior, but it is a change; the #234 hint is user-visible behavior that the owner accepted.

Two ways to handle it. The default is the first, revisited if the owner finds the measured growth unacceptable:
- Accepted cost (the ADR's default): one binary for all. The owner signs off on the image growth and notices, measured on amd64 and arm64 in #268.
- Build tag or separate binary: non-opt-in images stay unchanged. The overlay then also selects the image or binary, which adds a second build to release and test. #222 must state this if chosen.

Touch points, owned by #223:
- `configure.sh` appends the overlay to `COMPOSE_FILE`, and the `stack` script allowlist (`stack:216`) rejects unknown overlay files for package installs.
- Deb: the package ships the overlay (`scripts/build-deb.sh`) and `tests/test_package.sh` checks it. Homebrew: the formula installs `docker-compose*.yml` by glob, so the overlay ships unchanged. Neither has been tried as a real install with the overlay on (open item).
- `docs/configuration.md`, and the required-files list in `tests/test_release.sh` so the overlay ships.

### 4. Seeding path

**Decided: FIFO, not a shared writable volume.**

The FIFO worked end to end in #216: the bitcoin container created `utxo-fifo.dat.incomplete`, the dashboard read it through its existing read-only datadir mount, and Core renamed it on completion. 613 s against 592 s for a file (+21 s, +3.5%), with the same RPC block (measured, #216).

| | FIFO | Shared writable volume |
| --- | --- | --- |
| Change to the bitcoin service | none | new mount, bitcoind recreated once |
| Dump file on disk | none | 9.32 GB (measured) |
| Peak disk | 21.8 GB (derived) | 31.1 GB (derived) |
| Cleanup of a 9.3 GB file | none | dashboard must delete it |
| Seed time | 19.7 min (measured, mainnet seed) | about 21 min (derived) |
| A failed parse | the dump must run again: 10 min plus a 5 min RPC block | re-read the file |
| Reader dies mid-dump | clean RPC error, no restart (regtest, #280) | not applicable |

FIFO wins on every row that touches the live node. Its costs are two, both stated:

1. **A host-side step creates the FIFO.** The dashboard cannot: the datadir is mounted read-only and there is no docker socket. The `stack` script creates it in the bitcoin container (`docker compose exec`). It removes any stale `utxo-seed.dat` and `utxo-seed.dat.incomplete` first (Core refuses to overwrite). When the seed ends it opens a still-parked dump once, then removes both names. A Ctrl-C after the dashboard has taken the request only detaches, and leaves the names until the next run. There is no seed button. The trigger is the host command `./stack utxo-seed`, and the dashboard card shows it (Decided, #282). The command creates the FIFO first, then hands the dashboard a single-use request. The dashboard refuses the seed if the FIFO is missing or is not a named pipe.
2. **The reader's failure modes, measured on regtest (#280).** The dashboard opens the FIFO `O_RDONLY|O_NONBLOCK`, then a keeper writer, before it issues `dumptxoutset`; a blocking read-open would deadlock, because the open waits for a writer that only the RPC creates. It runs as bitcoind's uid (the datadir is mode 0700) and requires the path to be a named pipe, because with no FIFO Core writes a regular file. With no reader, the dump parks one RPC thread and other RPCs stay fast. A reader killed mid-write ends the RPC with a clean error and no restart, so cancel closes the reader instead of draining. A reader stalled for 120 s blocks only the write. One wedge: `stop` does not finish while the dump is parked waiting for a reader, until something opens the FIFO, so when a dump is running, `stop`, `down` and `restart` first cancel a running seed (waiting up to 15 s for the dashboard to close the FIFO) and then open it once (`timeout 5 cat`) before stopping bitcoind. Mainnet's 300 s hash pass was not covered; during it there is no pause lever.

Rejected: dumping into the datadir (the dashboard cannot delete it, and it eats the chain volume).

### 5. Maintenance window, alerts, thermal and re-seed

- **Trigger: user-started only.** No automatic seeding. The UI says what will happen: bitcoin RPC failing for about 5 minutes (measured), then answering slowly until the seed ends (measured, mainnet seed), and a node-down state. It says the seed takes about 20 minutes, longer if heat pauses it, and that the index takes about 7.5 GiB afterwards and up to about 17 GiB while it runs. The 27 GB free check stays the requirement. (Decided.)
- **Maintenance-window rule.** The user picks a quiet period. The dashboard does not schedule. It enforces these preconditions, all required (#281, #282): the index is not seeded or needs a new seed; the node answers, is not in initial block download, has headers equal to blocks and a tip under 2 hours old; no `dumptxoutset` or `scantxoutset` is running; the chain is main or regtest; at least 27 GB is free on the index filesystem (section 7); the dashboard CPU cap reads at or below 0.5; the thermal start gate passes; the FIFO exists as a named pipe, no `utxo-seed.dat` exists, and the dashboard user owns the data directory; and the pruned scanner is idle and held off (its status probe fails the batch if another scan runs, so the seed takes the scanner's lock). The dashboard lists every failed precondition together. The scanner hold is taken last, only when the rest pass. It recommends pausing other heavy chains. The measured run had the Tari base node paused for the whole window, so the effect of not pausing it is unmeasured. This is a recommendation, not a check. (Decided.)
- **Node-down alert: suppress during a user-started seed, with a cap.** One user-triggered seed raises exactly one "down" and one "recovered" alert (measured, #216), and the user knows why. When the dashboard sends the dump it writes `alerts_suppressed_until` to `/state/utxo-seed.json`, and the monitor skips the bitcoin node until that time passes, so no node-down or recovered alert fires. The hold ends at most 15 minutes after the seed started. A successful seed ends it early. A failed or cancelled seed keeps it to the cap, because Core may still be hashing with RPC stalled (Decided, #282). The cap is 15 min: 1.5 x the 613 s FIFO dump (derived, 920 s). The cap covers RPC failing, which ended about 300 s in during #216, not the whole seed, so a consumer pause later does not need it to grow. That is reasoned, since a slow reader's effect on the write phase was not measured. If RPC is still failing at the cap, the alert fires normally. On the first mainnet seed RPC failed for about 306 s and no alert or recovery message was sent (measured, mainnet seed). The UI says alerts are suppressed. Compose will show bitcoind unhealthy for about 2.5 min. Nothing restarted it in the measured run (#216). That Docker's restart policy ignores unhealthy is reasoned, and it holds only without an autoheal-style watchdog. The seed docs must say to check for one first. (Decided.)
- **CPU cap: mandatory.** The seed consumer runs in the dashboard container under `cpus: 0.5` from the overlay. It does not lower its own priority; the draft's "low priority" was not built. This is the quota #217 measured (79 C peak, 0.25 GiB RSS). The uncapped container read of 9.3 GB reached 92 C (measured, #216). A build that cannot confirm the cap refuses to seed: the dashboard reads its cgroup CPU limit and refuses above 0.5 or when it cannot read it. (Decided.)
- **Thermal gate: mandatory, no override. Provisional thresholds.** Source: the hottest of all `/sys/class/thermal/thermal_zone*/temp`, the same reading as the stack throttle, readable by the dashboard (measured). If no zone is present or any is unreadable, seeding is refused, and an unreadable sensor during the seed pauses the consumer until it reads again. The start check runs again just before the dump. This replaces the draft's "proceed after explicit confirmation". On such hosts the fallback scan path keeps working. (Decided.)
  - Start only at or below 75 C. The measured start was 61 C, but with the Tari base node paused. With all chains running (Bitcoin, Monero, and Tari block-syncing) mini-1 read 73 C at a load average of 0.5 (measured 2026-11-14). After its thermal paste was renewed it read a median of 66 C (mean 68.2 C, short spikes to 87 C) with every chain running (measured 2026-11-15, mainnet seed doc). The start gate passes either way. Before the repaste the 72 C resume point sat under that reading, so a seed paused at 85 C might not resume until other chains were paused. After it, typical readings sit below the resume point, but spikes reach 87 C, and a seed with every chain running is still unmeasured (open item).
  - Pause the consumer at 85 C, below the 88 C where the stack's throttle acts.
  - Resume at or below 72 C, below the measured release temperatures, so the seed never resumes while bitcoind is still held at 0.25 CPU.
  - Tested together on mainnet with Tari stopped (measured, mainnet seed): the dump plus the capped reader peaked at 86 C. The gate paused the reader 8 times during the write phase, each pause resumed, Core never failed the dump, and the stack's 88 C throttle never fired. A pause during the write phase is safe. The separate runs before had peaked at 80 C (dump alone, #216) and 79 C (capped pipeline alone, #217).
- **Throttle interaction.** The stack's thermal throttle caps bitcoind to 0.25 CPU and monerod to 0.50 when the host reaches 88 C, and releases at 73-75 C (measured, #216). Three consequences. The seed gate pauses before the throttle fires, so the seed does not trigger it. A throttle capping bitcoind during the hash pass would lengthen the dump and the RPC block; this is unmeasured, which is one reason the alert cap is 15 min and not 10. The consumer must not resume into a throttled host, which the 72 C resume point prevents. In #216 the throttle was triggered by the uncapped consumer, not by bitcoind, which is capped at 0.5 CPU (measured). The heat of a capped read and parse is unmeasured (open item).
- **Re-seed policy: manual only.** A re-seed is offered, never forced, when the format version changed, a reorg is deeper than the journal, the store failed its integrity check, or the index fell behind past the retained block window. Until then the fallback serves. An invalid index shows "Needs a new seed" with the same host command, and starting it resets the store first. A cancel, an error, a dump mismatch or a dashboard restart during a seed discards the partial index and records the outcome in `last_seed`.

### 6. Tip following and reorgs

- **Poll, not ZMQ.** The dashboard already polls (10 s, 30 s, 1 min). A block arrives about every 10 minutes, so ZMQ would save seconds and would add `-zmqpub` flags and a port to bitcoind for everyone who enables the feature. Rejected, reasoned.
- **Poll interval: 10 s**, on the index manager's own ticker, not the existing node poll. Each tick calls `getblockchaininfo` and `getbestblockhash`, and `getblockheader` only when the index tip differs from the node tip. One block is one `getblockhash` plus one `getblock` v3 call (1.8-3.0 s, measured) plus a batch write. Catching up 144 blocks takes 144 x 1.8 = 4.3 min to 144 x 3.0 = 7.2 min (derived).
- **Reorg journal depth: 144 blocks**, matching `prunedDeltaBlocks`. Each connected block stores its own undo record, so a disconnect never depends on Core still holding that block. At about 5k outputs and 5k inputs per block that is roughly 0.5 MB per block and under 100 MB in total (reasoned, unmeasured). A reorg deeper than 144, or one that reaches below the seed's base block (a fresh seed has no undo records until blocks are followed), invalidates the index and the user re-seeds. (Provisional, owned by #228.)
- **Prune safety:** the follower must stay inside the retained window, 5455 blocks on the reference node (measured). An index further behind than the oldest retained block cannot catch up from `getblock`, so it is invalid. (Decided.)

### 7. Budgets: disk, memory, CPU

| Component | Value | Basis |
| --- | --- | --- |
| Final index | 7.5 GiB (7,635 MiB); 11.35 GB derived | measured, mainnet seed; derived (Sizing), 16 B prefix |
| Peak temp disk including the index | at least 16.7 GiB (17,117 MiB, sampled every 30 s); 21.8 GB (20.3 GiB) derived | measured, mainnet seed; derived (Sizing) |
| Dump file | none with FIFO; 9.32 GB if the shared-volume fallback is ever used | measured, #216 |
| Margin | 20% | judgment |

- **Pre-seed check:** free space on the filesystem holding the index volume must be at least **27 GB** (21.8 x 1.2, derived). The temp directory is `ingest-tmp` inside the index directory, so one free-space check on the index directory covers both. The seed is refused and the dashboard shows the numbers if the check fails. The synthetic 16 B peak (127.3 B per coin, #276) is under the 133.5 B used here, so the figure stands until a mainnet seed measures it (#226). (Decided.)
- **Runtime floor:** following pauses and the index goes degraded below 2 GB free. Below 2 GB free while following, the index stops applying blocks and reports `degraded`. Below 2 GB free during a seed, the seed aborts. An out-of-space error while following marks the index invalid (persisted), and the scan serves until the user re-seeds. (Decided.)
- **Dedicated volume:** `utxo_index`, separate from `dashboard_state`, so removing the index never touches the wallet list or scan cache. (Decided.)
- **Memory:** dashboard `mem_limit` 1g, Pebble block cache 64 MiB, memtable 16 MiB, one concurrent compaction (constants in `utxo_store.go`). No `GOMEMLIMIT` is set; the draft's 700 MiB was not built. Peak RSS was 0.25 GiB at 100M entries and stayed flat with entry count (measured, #217). The dashboard's own baseline on mini-1, without the index, was 26 MiB RSS with a 78 MiB high-water mark after 18 hours (measured 2026-11-14), so baseline plus 0.25 GiB stays far under the 1g limit. (Decided for the Pebble side.)
- **CPU:** `cpus: 0.5` for the dashboard in the overlay, see section 5. (Decided.)

### 8. Schema and format versioning

- The store keeps a metadata record: schema version, Pebble format version, chain identity (chain name and genesis hash), seed base height and hash, current tip height and hash, an `ingesting` flag for an interrupted seed, and the persisted `invalid` reason (`utxoMeta`).
- A version bump, a chain mismatch, or a failed integrity check means the index is **invalid**: `scantxoutset` serves, the UI offers a re-seed, and the user starts it. **No silent migration. No automatic re-seed.** (Decided.)
- Pebble's own on-disk format version is pinned to `FormatFlushableIngest` and recorded as `pebble_format`. A mismatch with the running binary counts as a schema bump and makes the index invalid. (Decided.)
- A downgrade of the dashboard image that cannot read the store is also invalid, not an error: the index is ignored and the fallback serves.
- A re-seed takes about 20 minutes of wall time (1179 s on the first mainnet seed, against a derived floor of about 15 min) and the same RPC block as the first seed (measured, Sizing).

### 9. Gap-limit semantics and derivation

- **The gap limit stays the fixed window used today: indexes 0 to 999 per descriptor**, as in `deriveRangeStart` and `deriveRangeEnd` (`utxo_derive.go`), used by both the scan and the index resolver, and the `descriptorRangeCovers` check in `watch_provision.go`. A wallet that used an index above 999 is undercounted, exactly as today. #237 documents this. A different window would make index balances disagree with the fallback. (Decided.)
- **Derivation stays delegated to bitcoind** (`deriveaddresses` and descriptor validation). The dashboard does not implement BIP32, xpub parsing or descriptor checksums in Go. Addresses come from `getdescriptorinfo` and `deriveaddresses` over indexes 0 to 999, sent as JSON-RPC batches of 20 calls. Go decodes each derived address to its script (segwit, P2PKH and P2SH only) and hashes it. Only `addr(`, `pkh(`, `wpkh(`, `sh(wpkh(`, `wsh(` and `tr(` descriptors are resolved; any other shape, such as `pk(` or `combo(`, makes the wallet fall back to scan (#231, #275). (Decided.) A script cache exists (next bullet), so derivation runs once per wallet and again only when its descriptors change. Deriving 2000 scripts took 380 to 422 ms on regtest with bitcoind at 0.5 CPU (measured, #275). On the first mainnet seed the first wallet lookup took 0.877 s including derivation, at the 0.5 CPU cap (measured, mainnet seed), so "under 1 s" held (#231).
- Derived script sets are cached per watched wallet in `utxo_scripts.json` in the state directory, next to the scan cache, written by `writePrivateFileAtomic` (mode 0600). A record holds the descriptor-set id with its range, a `fallback` flag and the scripts. It is re-derived when the id changes and dropped when the wallet is removed.

### 10. Privacy

- **Persisted in the index volume:** hashed scripts (16 B prefixes), txids, vouts, amounts and heights for every UTXO. This is public chain data. It does not say who owns what.
- **Persisted elsewhere:** the registered wallet script sets link one person's scripts, so they are sensitive. They are not written into Pebble. They live in a JSON registry under `/state`, written with `writePrivateFileAtomic` (mode 0600), next to the watch list and scan cache. (Decided.)
- **File modes:** Pebble creates its files 0644 inside the index directory. The dashboard sets that directory to 0700 when it opens the index, including an existing Docker volume mount point, which was 0755 on mini-1 before that fix. With FIFO seeding no dump file exists. (Decided.)
- **Logs:** counts, heights, durations, states and error classes only. No addresses, descriptors, xpubs, script hashes or prefixes, and no per-wallet amounts. This matches the existing `watch scan:` line. (Decided.)
- `docs/privacy.md` gets a short section in #237.

### 11. Fallback rules

- **Opt-in default: off.**
- The index holds confirmed coins only. Unconfirmed and mempool activity are out of scope, as for the scan.
- The index serves a balance only when it is enabled, valid, seeded, not seeding, healthy, and its tip is the node's best block. The index reports `ready` within 2 blocks of the tip, but a wallet balance is taken from it only at 0 blocks behind with the same tip hash, and only for a wallet whose scripts derived without fallback (`fromIndex`, `pruned_index.go`).
- In every other state (off, not_seeded, seeding, catching_up, degraded, invalid, or ready but not at the node tip) the existing `scantxoutset` and #215 path serve, unchanged. Each watch row in the API reports `balance_source` as `index` or `scan`, and a row served from the index says "From the balance index" (#286).
- If registering a wallet's scripts fails, that wallet falls back to scan.

## Consequences

- Good: a new wallet shows a balance in 0.15-0.33 s of lookups (derived). Full-set walks leave the steady state. No runtime change for installs that do not opt in, apart from the #234 hint. The fallback is the default.
- Bad: 23 more linked modules (7 to 30, measured on amd64 and arm64, #268), a dashboard binary about 42% larger (+5.9 MB on amd64, +5.4 MB on arm64, measured, #268), a THIRD_PARTY_NOTICES that grew from 602 to 3382 lines, and a larger image for every install unless a build tag is used. 11.35 GB steady disk and 21.8 GB peak (derived, provisional). One 5-minute RPC block with an alert, and a heat load that needs a CPU cap and a thermal gate. A large subsystem to maintain.
- Neutral: no transaction history of spent coins (owner decision). The balance sparkline still works, from the index. Unpruned nodes are unchanged. No bitcoind recreate.

## Open items

Everything measured is recorded above. What remains:

| Item | Needed when | What it decides | Owner |
| --- | --- | --- | --- |
| Option E: progress UI and queued-add batching | while the index is off or seeding | The fallback's user experience | none yet |

Design items still to measure:

| Item | What it decides | If it goes badly | Issue |
| --- | --- | --- | --- |
| Reader dies or never opens the FIFO on mainnet (regtest done in #280: no wedge except `stop` while parked; the normal path passed on mainnet) | Whether the 300 s hash pass changes the regtest result | Core wedges: revert to the shared volume (9.32 GB file, bitcoind recreate) | #226, #236 |
| Seed resume with all chains running (idle was 73 C on mini-1, above the 72 C resume point; median 66 C with spikes to 87 C after a thermal paste renewal, measured 2026-11-15) | Whether a paused seed resumes without pausing other chains | It never resumes: pause other chains for the window, or move the resume point | #236 |
| Effect of the stack throttle on a running dump | The 15 min alert cap | A dump over 15 min: raise the cap | #236 |
| Per-coin size on mainnet and cold lookup latency for a large wallet on the reference node (mainnet index 7.5 GiB final and at least 16.7 GiB peak, under the derived numbers, but the coin count was not recorded; first mainnet lookups: 0.877 s for one wallet including derivation, 0.025 s for a 5,116-coin address) | The key length and the 27 GB threshold | Size or latency worse than the derived numbers by over 10%: re-decide 16 versus 12 bytes and the threshold | #222 |
| A real Deb and Homebrew install with the overlay on (both ship it and the `stack` allowlist accepts it, #269; no real install was run) | Packaging | n/a | #223 |
| The accepted-cost versus build-tag call, and size on targets other than amd64 and arm64 (Pebble pinned at v1.1.5, notices added, growth measured in #268) | Release readiness, and the accepted-cost versus build-tag call | Size or count unacceptable: build tag or separate binary | #222 |
| SQLite (modernc) variant | Whether the rejection of option B stands | Only if the owner rejects Pebble's dependency weight | none yet |

