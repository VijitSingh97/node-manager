# ADR 0001: Bitcoin UTXO index for instant watch-only balances

- Status: Proposed (draft). Not final until the #216 and #217 results are recorded.
- Date: 2026-11-14
- Issue: #219, part of epic #213. Depends on #216 and #217.
- Scope: Bitcoin on pruned nodes. Litecoin follows in #238. Monero and Tari are out of scope.

## How to read this draft

Every decision is one of three kinds.

- **Decided.** Backed by a measurement or a source reading that exists today.
- **Provisional.** Reasoned, not measured. It stands unless a later result contradicts it.
- **Pending #216 / Pending #217.** It cannot be settled until the owner's maintenance window produces the named measurement. Each one states the threshold that would change the decision.

"Measured" means someone ran it. "Reasoned" means it comes from reading code or from arithmetic on measured numbers. The labels are kept on purpose.

## Context

Watch-only balances on a pruned Bitcoin node use `scantxoutset`, a full walk of the UTXO set (`build/dashboard/pruned_scan.go`). A newly added wallet shows pending until a scan finishes. `docs/watch-only.md` says a first scan "commonly takes minutes". No scan duration has been measured on mini-1 (the #214 log line has not fired yet).

PR #215 already changed the picture. A wallet that has a cached scan result follows new blocks. `applyBlocks` reads each new block with `getblock` verbosity 2 and updates the cached outpoints for every watched address. On mini-1 this took 0.8 s and 1.5 s for one block and two wallets (measured, #216 comment). It falls back to a full scan when the cache tip is more than `prunedDeltaBlocks` (144) blocks behind, or when any step fails.

Facts gathered so far:

| Fact | Value | Source |
| --- | --- | --- |
| Chain at measurement | tip 975705, pruneheight 970251, 5455 retained blocks at prune_mb 10000 | #216 comment, measured |
| Undo data for retained blocks | present, 68 rev files (1.3 GB) | measured |
| `getblock` v3 on oldest and newest retained block | succeeds | measured |
| `getblock` v3 time and size | 1.8-3.0 s, 17.7-19.4 MB per block (includes `docker exec` overhead) | measured |
| `getblock` v2 time and size | 1.5-1.9 s, 13.6-15.4 MB | measured |
| Chainstate | 11 GB | measured |
| Free disk on mini-1 | 1.5 TB of 1.7 TB | measured |
| Dashboard can read the datadir and `/sys/class/thermal/thermal_zone0/temp` | yes, uid 1000 | measured |
| Host temperature, light load | 57-69 C | measured |
| Healthcheck time to unhealthy | about 3 min (60 s interval, 3 retries, 15 s timeout) | repo |
| Restart policy acts on unhealthy | no, only on exit | reasoned from compose behavior, confirmed by sampling in #216 |
| Node-down alert | 3 consecutive minutely RPC failures | repo, `settings.go:22` |
| `dumptxoutset` holds `cs_main` for the hash pass | yes | source reading only |
| UTXO count | 170-190M | estimate |
| Dump file size | 8-12 GB | estimate |
| Dashboard `mem_limit` and `cpus` | none set | `docker-compose.yml` |
| Dashboard datadir mount | read-only; `/state` is a writable named volume | `docker-compose.yml` |
| Pinned Bitcoin Core | `bitcoin/bitcoin:32.1` by digest | `docker-compose.yml` |

Pebble spike (#217, branch `spike/pebble-utxo-bench`, Pebble v1.1.5, 64 MiB block cache, one pinned core, nice 19, `GOMEMLIMIT` 700 MiB, 10M synthetic entries on mini-1, measured):

| Item | Result |
| --- | --- |
| Pipeline wall (gen+sort 8.0 s, merge 5.6 s, ingest 0.25 s) | 13.8 s, about 1.4 s per million entries |
| Peak temp disk | 1.10 GiB |
| Final size | 0.58 GiB, 62.4 B per entry (44 B key, 12 B value) |
| Peak RSS | 0.22 GiB |
| Cold prefix lookup | p50 65 us, p99 148 us |
| Warm prefix lookup | p50 8.4 us, p99 28 us |
| Host temperature | 60 C before, 74 C after, guard 88 C |

Caveats of the spike: one pinned core is not a 0.5 CPU cgroup quota; "cold" only applies `posix_fadvise`; no concurrent bitcoind load; 10M entries, not 100M or more; keys use an 8-byte prefix; no SQLite variant was built.

## Decision drivers

1. Stay small. `AGENTS.md` and `CONTRIBUTING.md` say prefer deleting over adding and stdlib over new dependencies. The dashboard module has only grpc and protobuf as direct dependencies. It builds with `CGO_ENABLED=0` on alpine.
2. No behavior change for installs that do not opt in: no new volume, memory limit or restart.
3. Never wrong silently. Any doubt about the index falls back to `scantxoutset`.
4. Do not hurt the live node. `cs_main` stalls, heat and disk are shared with the other chains on the box.
5. Targets from #213: add an indexed wallet under 1 s, cached read under 100 ms, batch registration, incremental blocks, works on pruned nodes.
6. Privacy: wallet data is sensitive, chain data is not.

## Is a full index needed at all?

This is the first question, because #215 already shipped part of the value.

What #215 already gives, with no new dependency, disk or restart:
- A wallet that has been scanned once follows each new block in about a second (measured).
- Balances survive restarts through the JSON scan cache.

What #215 does not give:
1. **Adding an unknown wallet.** The first balance still needs a `scantxoutset`. Its cost is unmeasured (Pending #216).
2. **Node stalls.** Every first scan, and every fallback to a full scan, walks the whole UTXO set. That holds `cs_main` and burns CPU on a throttled box.
3. **Recovery after long downtime or a failed delta.** More than 144 blocks behind, or any RPC error mid-delta, means a full scan again.
4. **Batching.** Wallets are scanned in batches, but each new batch is still a full walk.

What the full index adds beyond #215, stated plainly: it makes adding a new wallet instant, and it removes full-set walks from the steady state. It does not improve balances for wallets already tracked. It does not add history. It does not help unpruned nodes.

What it costs: about 13 GB of disk (arithmetic below), about 35 GB at peak during seeding, one `dumptxoutset` run that stalls the node, a change to the bitcoin service (a shared dump volume, so bitcoind is recreated once), a new Go dependency with a transitive set, and a large new subsystem (parser, sort, store, reorg journal, versioning, fallback). That is more than 20 child issues of work.

**Provisional decision.** Build the full index as an opt-in feature, but gate the build on Pending #216. If a timed `scantxoutset` on mini-1 finishes in 120 seconds or less and its `cs_main` stall stays below the 3-minute node-down threshold, stop at #215: the index buys too little for its cost, and option C below becomes the decision. If it takes 10 minutes or more, or stalls RPC for more than 3 minutes, build the index. Between 2 and 10 minutes it is the owner's call, because the stated goal (instant add) is only met by the index. The 120 s and 10 min figures are judgment, to be confirmed by the owner once the baseline exists.

## Options considered

### A. Pebble, full scriptPubKey-hash index (proposed)

Pure Go key-value store (`github.com/cockroachdb/pebble`). Seeded from a `dumptxoutset` snapshot, then followed with `getblock` verbosity 3.

- Pros:
  - Measured on mini-1 at 10M entries: 13.8 s pipeline, 62.4 B per entry, 0.22 GiB peak RSS, prefix lookups 65 us cold p50 and 148 us p99.
  - Sorted SST ingest is nearly free, so the cost is the external sort.
  - Pure Go, so `CGO_ENABLED=0` still works. Writes are atomic through batches and a WAL.
- Cons:
  - New dependency with 26 linked modules (below), including Sentry, Prometheus and protobuf client code pulled in through `cockroachdb/errors`.
  - The 100M-entry behavior is unmeasured (Pending #217).
  - Pebble can ratchet its on-disk format version, so rolling back the dashboard image may leave the index unreadable. The version policy below handles this (invalid, re-seed).
  - Binary size growth is unmeasured.

Transitive set and licences (measured by `licenses.sh` in the spike): `go list -m all` shows 127 modules, 26 are compiled into the static linux/amd64 binary with `CGO_ENABLED=0`. All are permissive.

| Licence | Linked modules |
| --- | --- |
| BSD-3-Clause | cockroachdb/pebble, gogo/protobuf, golang/protobuf, google.golang.org/protobuf, golang/snappy, klauspost/compress, rogpeppe/go-internal, golang.org/x/exp, x/sys, x/text |
| Apache-2.0 | cockroachdb/errors, fifo, logtags, redact, tokenbucket; prometheus/client_golang, client_model, common, procfs; matttproud/golang_protobuf_extensions |
| MIT | beorn7/perks, cespare/xxhash/v2, getsentry/sentry-go, kr/pretty, kr/text |
| BSD-2-Clause | pkg/errors |

No copyleft. `DataDog/zstd` (cgo, BSD-3) is not linked.

THIRD_PARTY_NOTICES impact: `build/dashboard/THIRD_PARTY_NOTICES` is 602 lines today. Adding Pebble adds a notice and licence text for each linked module that requires one (BSD and Apache require the notice to travel with binaries). #222 must update the file in the same PR, as `CONTRIBUTING.md` requires. This lengthens the file noticeably, which is itself a cost of this option.

### B. SQLite through modernc.org/sqlite

- Pros: single file, simple transactions, strong crash safety, easy to inspect, pure Go (transpiled C), so `CGO_ENABLED=0` works.
- Cons: the dependency is also large (the modernc libc, memory and mathutil modules plus the generated SQLite source), so it does not clearly beat Pebble on the "small stack" driver. Bulk-loading 170-190M rows into a B-tree is slower than ingesting pre-sorted SSTs, unless rows go in key order into a `WITHOUT ROWID` table. No compression, so size is expected at or above Pebble's: a 44 B key and 12 B value plus page and cell overhead is likely 70-80 B per row, about 13-15 GB.
- Status: **rejected, reasoned.** No variant was built, so there are no SQLite numbers. This is the weakest rejection in the ADR. It is reopened if the owner rejects Pebble's dependency weight at review, or if Pebble fails a #217 threshold (see Open items). A bake-off would cost one more spike.

### C. Watch-list-only forward tracker in JSON under /state

Keep #215's model and extend it: store each wallet's scriptPubKey set and outpoints in JSON (written with `writePrivateFileAtomic`), follow blocks, no full UTXO index.

- Pros: no new dependency, no new volume, no bitcoind change, no seeding. It is what #215 already is, keyed by address. Moving to scriptPubKey keys is a small change.
- Cons: an unknown wallet still needs a full `scantxoutset`. There is no cheap backfill. Reading the 5455 retained blocks at 1.8-3.0 s each is 2.7-4.5 hours of v3 calls (arithmetic on measured timings) and covers only about five weeks of chain, so it would miss older UTXOs and give wrong balances.
- Status: **rejected as the final answer, reasoned and partly measured** (block timings measured, the rest arithmetic). It does not meet the instant-add target. It is also the fallback in every outcome, and it becomes the decision if #216 shows scans are cheap (see the gate above).

### D. Separate index service (own container)

- Pros: isolates memory and crashes from the dashboard, gives the index its own limits, image and lifecycle, and keeps the dashboard dependency-free.
- Cons: a new image and build, a new fixed IP on the btc_net network, a new healthcheck, another RPC credential path, another component to update every release, and the same Pebble dependency anyway. It changes the compose network configuration, which #213 asks us to avoid.
- Status: **rejected for now, reasoned.** It becomes the choice if Pending #217 shows the dashboard cannot hold the index within its memory limit.

### RocksDB

`grocksdb` needs cgo. `build/dashboard/Dockerfile:1-24` builds with `CGO_ENABLED=0` on alpine. **Rejected, measured** (the Dockerfile settings are the evidence). Bbolt and Badger were not benchmarked and were not considered further: Pebble has numbers, and the others would each need their own spike.

### Engine decision

**Provisional: Pebble (option A), Pending #217.** It is the only option with numbers. It is conditional on the 100M-entry run and on the owner accepting the dependency weight and notices cost.

## Decision

### 1. Engine, placement and layout

| Topic | Decision | Status |
| --- | --- | --- |
| Engine | Pebble, pinned to the spike's version (v1.1.5) until #222 re-checks upstream | Provisional, Pending #217 |
| Process placement | Inside the dashboard process, behind a small interface so a separate service stays possible | Provisional, Pending #217 (memory) |
| Key | `SHA-256(scriptPubKey)[:16] \|\| txid (32 B) \|\| vout (4 B, big endian)` | Decided |
| Value | `amount (8 B) \|\| height (4 B)` | Decided |
| Spends | `getblock` verbosity 3 includes the prevout scriptPubKey from undo data, so a spend deletes `hash(prevout script)[:16] \|\| txid \|\| vout` with no reverse map | Decided (v3 measured on retained blocks) |
| Unspendable outputs | OP_RETURN and other provably unspendable scripts are not stored | Provisional |
| Indexed by | scriptPubKey hash, never by address string | Decided (#213) |

Why 16 bytes and not the spike's 8: a 64-bit prefix gives a collision chance of about 1e-11 per wallet script against 180M distinct scripts. That is tiny, but it would produce a silently wrong balance, which breaks driver 3. A 128-bit prefix removes the case in practice. It costs 8 more bytes per entry: about 70 B per entry and about 12.6 GB at 180M entries (arithmetic on the measured 62.4 B). The spike used 8 bytes, so #222 must re-measure size and lookup speed with 16.

Size budget: 62.4 B (measured) x 170-190M = 10.6-11.9 GB at an 8 B prefix, and about 11.9-13.3 GB at 16 B. Plan on 13 GB plus compaction headroom. Pending #217 (100M run) for the real number and for write amplification after follow-mode churn.

### 2. Wallet queries and the cost of adding an unknown wallet

Lookup is a prefix scan per wallet script. A wallet with a 1000-index window on external and internal chains is up to 2000 scripts per descriptor. At the measured cold p50 of 65 us on 10M entries, 2000 sequential lookups take about 0.13 s, and about 0.30 s at p99 (arithmetic on measured values at 10M). That fits the 1 s target before counting derivation time, which is unmeasured.

| Option | Cost of adding an unknown wallet |
| --- | --- |
| A. Pebble index | Derive scripts (RPC, unmeasured), then about 0.1-0.3 s of lookups at 10M entries. Pending #217 at 100M. Instant once registered. |
| B. SQLite index | Reasoned: similar once built. No measurement. |
| C. Watch-list JSON | A full `scantxoutset`. Duration unmeasured, "commonly takes minutes" per docs. Pending #216. The wallet shows pending and the node is stalled for the scan. |
| D. Separate service | Same as A plus a local network hop. |

Pending #217 threshold: if a cold batch of 2000 lookups at 100M entries exceeds 1 s (about 500 us each, well above the 10M p99), or cold p99 for one prefix exceeds 1 ms, the "under 1 s" target needs a cache or a smaller window, and the engine choice is reopened.

### 3. Opt-in mechanism and upgrade behavior

**Decided: an optional compose overlay, `docker-compose.utxo-index.yml`, enabled by a `configure.sh` option, default off.**

The overlay adds only when enabled: the named volumes (index and dump), a writable mount of the dump volume in the bitcoin service, the index mount and a `mem_limit` on the dashboard, and the Pebble settings. This is the same pattern as `docker-compose.tari-grpc.yml` and the other overlays.

Why not an unconditional volume: an empty named volume costs no disk, but the dashboard `mem_limit` and the writable dump mount on bitcoind would change every install, and neither can be conditional without an overlay. Rejected, reasoned.

What existing installs get on upgrade: nothing. No new volume, no memory limit, no bitcoind change, no behavior change. `scantxoutset` and the #215 path stay in place. An install that turns the option on gets the volumes, a one-time recreate of the bitcoin service (new mount), and an index in the "not seeded" state until the user starts a seed.

Touch points found while tracing, owned by #223:
- `configure.sh` writes `COMPOSE_FILE` (line 788), and the `stack` script allowlist (`stack:216`) rejects unknown overlay files for package installs.
- Deb and Homebrew installers and their tests: not traced yet. Open item.
- `docs/configuration.md`, and the required-files list in `tests/test_release.sh` so the overlay ships.

Default: off. Fallback rules are in section 11.

### 4. Seeding path

**Decided: a shared writable named volume (`utxo_dump`) mounted in both bitcoin and dashboard, with an absolute `dumptxoutset` path.** A relative path would resolve into the datadir, which the dashboard mounts read-only. Mounting the volume in bitcoin changes the bitcoin service, so enabling the overlay recreates bitcoind once. The opt-in prompt says so.

- bitcoind writes `<dump>.incomplete` and renames it on completion (source reading, `blockchain.cpp:3162-3210`). The dashboard reads only the final name.
- **Who deletes the file: the dashboard.** It deletes after the ingest commits, after a failed seed, after a cancel, and at startup if a stale dump exists. The dump volume is writable in the dashboard for that reason. bitcoind never cleans it.
- The dump volume needs room for the whole file (section 7).

Alternatives:
- **FIFO.** Rejected for now, reasoned. It avoids the 8-12 GB file, but #216 has not shown that the pinned image under uid 1000 with `no-new-privileges` can use one. It ties bitcoind's write pace to the reader, and a crashed reader wastes the whole stall with no resume. Pending #216: if FIFO works and the section 7 disk check would block real users, FIFO becomes the low-disk path.
- **Dump into the datadir.** Rejected, reasoned. The dashboard cannot delete it, and it eats the chain volume.

### 5. Maintenance trigger, off-peak, thermal and re-seed

- **Trigger: user-started only.** No automatic seeding. The UI says the node will stall for the length of the dump. (Decided.)
- **Preconditions, all must hold:** not in initial block download; tip fresh; bitcoind healthy; the pruned scanner idle and held off (its status probe fails the batch if another scan runs, so seeding takes the scanner's lock); disk check passes; temperature below the start limit.
- **Off-peak:** the UI recommends a quiet period. It does not schedule one. No timer-based start, to keep the feature small. (Decided.)
- **Thermal data source:** `/sys/class/thermal/thermal_zone0/temp`, readable by the dashboard on mini-1 (measured). **Fallback when the file is missing or unreadable** (VPS, other kernels, Docker Desktop): do not seed without an explicit user confirmation that names "no thermal check available". The bitcoind health check is then the only abort signal. (Decided.)
- **Limits:** start only at or below 75 C. Pause the ingest phase at 85 C and resume below 78 C. A running `dumptxoutset` cannot be paused, so the limit applies before the dump and during the ingest. 88 C is the hard abort used by the spike and by #216. The 75/85/78 values are judgment against the measured 57-69 C light-load range and the 80 C reading recorded on mini-1 in #213. Pending #216: if the dump pushes the host past 85 C, lower the start limit or require an idle host.
- **Node-down alert:** the stall may exceed the 3-minute alert threshold. Pending #216 (stall duration). If it exceeds 3 minutes, the seed flow suppresses node-down alerts for its own duration and says so in the UI. If it stays under 3 minutes, no change is needed.
- **Re-seed policy: manual only.** A re-seed is offered, never forced, when the format version changed, a reorg is deeper than the journal, the store failed its integrity check, or the index fell behind past the retained block window. The user starts it. Until then the fallback serves.

### 6. Tip following and reorgs

- **Poll, not ZMQ.** The dashboard already polls (10 s, 30 s, 1 min). A block arrives about every 10 minutes, so ZMQ would save seconds. It would add `-zmqpub` flags and a port to the bitcoind command line (none today) for everyone who enables the feature. Rejected, reasoned.
- **Poll interval: 10 s**, reusing the existing node poll. Processing a block is one `getblock` v3 call (1.8-3.0 s, measured) plus a batch write. Catching up 144 blocks takes about 4-7 minutes (arithmetic on measured timings).
- **Reorg journal depth: 144 blocks**, matching `prunedDeltaBlocks`. Each connected block stores its own undo record (the entries it removed and added), so a disconnect never depends on Core still holding that block's data. At about 5k outputs and 5k inputs per block that is roughly 0.5 MB per block and under 100 MB in total (arithmetic, unmeasured). A reorg deeper than 144 invalidates the index and the user re-seeds. (Provisional, owned by #228.)
- **Prune safety:** the follower must stay inside the retained window, which is 5455 blocks on mini-1. An index further behind than the oldest retained block cannot catch up from `getblock`, so it is invalid. (Decided.)

### 7. Budgets: disk, memory, volume

Disk (Pending #216 for the dump size, Pending #217 for spill and index size):

| Component | Estimate | Basis |
| --- | --- | --- |
| Dump file | about 10 GB (8-12) | estimate, Pending #216 |
| Sort spill plus SSTs at peak | about 18 GB at 180M | linear from 1.10 GiB at 10M, Pending #217 |
| Final index | about 13 GB | section 1 |
| Margin | 20% | judgment |

The dump is deleted after the ingest and the spill after the SSTs are placed. The peak is dump plus spill and SSTs, about 28 GB before margin and about 35 GB with it. The steady state is about 13 GB.

- **Pre-seed check:** free space on the filesystem holding the dump volume and on the one holding the index volume must cover `(dump + spill + index) x 1.2`. Until measured, use 35 GB. If both volumes share a filesystem (the default for Docker named volumes), check once against the sum. The seed button refuses and shows the numbers if the check fails.
- **Runtime floor:** following pauses and the index goes degraded below 2 GB free. An out-of-space error discards the failed write and falls back to scan, with no partial state served (#230). (Provisional.)
- **Dedicated volume:** `utxo_index`, separate from `dashboard_state`, so removing the index never touches the wallet list or scan cache. (Decided.)
- **Memory:** dashboard `mem_limit` of 1g in the overlay, `GOMEMLIMIT` 700 MiB, Pebble block cache 64 MiB, as in the spike. The spike's peak RSS was 0.22 GiB at 10M with these settings (measured). The dashboard's own baseline RSS is not measured. Pending #217: if the 100M run's peak RSS exceeds 600 MiB at a 64 MiB cache, or the dashboard baseline plus the seed peak exceeds 800 MiB, then either raise `mem_limit` to 1.5g or move the index to a separate service (option D). Do not raise it silently.
- **CPU:** no new `cpus` limit on the dashboard. The seed runs at low priority and obeys the thermal limits. The spike pinned one core, not a 0.5 CPU quota, so seed wall time under the quota is unmeasured (Pending #217). For planning: sorting 180M entries at 1.4 s per million is about 4 minutes, plus parse time, which was not measured.

### 8. Schema and format versioning

- The store keeps a metadata record: schema version, chain identity (network magic and snapshot base hash), seed base height and hash, and current tip height and hash.
- A version bump, a chain mismatch, or a failed integrity check means the index is **invalid**: `scantxoutset` serves, the UI offers a re-seed, and the user starts it. **No silent migration. No automatic re-seed.** (Decided.)
- Pebble's own on-disk format version is pinned and recorded. Upgrading Pebble across a format change counts as a schema bump. (Provisional.)
- A downgrade of the dashboard image that cannot read the store is also invalid, not an error: the index is ignored and the fallback serves.
- A re-seed takes tens of minutes (estimate from the sort and dump, unmeasured, Pending #216 and #217).

### 9. Gap-limit semantics and derivation

- **The gap limit stays the fixed window used today: indexes 0 to 999 per descriptor**, the same as `rangeValue := []int{0, 999}` in `pruned_scan.go:549` and the `descriptorRangeCovers` check in `watch_provision.go`. A wallet that used an index above 999 is undercounted, exactly as it is today. #237 documents this. We do not change it here, because a different window would make index balances disagree with the fallback. (Decided.)
- **Derivation stays delegated to bitcoind** (`deriveaddresses` and descriptor validation). The dashboard does not implement BIP32, xpub parsing or descriptor checksums in Go, which would be security-sensitive code and new surface. Go only hashes the final script. #231 chooses how to get the scriptPubKey from bitcoind (for example batched JSON-RPC) and measures the batch cost. (Decided.) Pending: derivation time for 2000 scripts, measured in #231 or #236. If it alone exceeds 1 s, the "under 1 s" target needs a script cache keyed by descriptor.
- Derived script sets are cached per descriptor in the registry file (section 10).

### 10. Privacy

- **Persisted in the index volume:** hashed scripts (16 B prefixes), txids, vouts, amounts and heights for every UTXO. This is public chain data. It does not say who owns what.
- **Persisted elsewhere:** the registered wallet script sets are the sensitive part, because they link one person's scripts. They are not written into Pebble. They live in a JSON registry under `/state`, written with `writePrivateFileAtomic` (mode 0600), next to the existing watch list and scan cache. (Decided.)
- **File modes:** Pebble creates its own files. The index directory is created 0700 so other uids on the host cannot read it. The dump is public chain data but is created by bitcoind, and is deleted as soon as the ingest commits. The wallet registry is 0600 like the scan cache. #222 and #223 verify the actual modes. (Provisional.)
- **Logs:** counts, heights, durations, states and error classes only. No addresses, descriptors, xpubs, script hashes or prefixes, and no per-wallet amounts. This matches the existing `watch scan:` line. (Decided.)
- `docs/privacy.md` gets a short section in #237.

### 11. Fallback rules

- **Opt-in default: off.**
- The index serves a balance only when it is enabled, valid, seeded, not seeding, healthy, and its tip is within 2 blocks of the node tip.
- In every other state (off, invalid, seeding, behind, degraded, error) the existing `scantxoutset` and #215 path serve, with no change from today. The UI says which source answered.
- If registering a wallet's scripts fails, that wallet falls back to scan.

## Consequences

Good:
- An already-indexed wallet can be added in well under 1 s (10M measured, 100M pending).
- Full-set walks leave the steady state.
- No change for installs that do not opt in.
- The fallback is always available and is the default.

Bad:
- A new dependency set (26 linked modules) and a longer THIRD_PARTY_NOTICES.
- About 13 GB steady disk and about 35 GB peak on opted-in installs.
- One `cs_main` stall on the live node, of unmeasured length, and a bitcoind recreate to add the dump mount.
- A large new subsystem to maintain, for a benefit that depends on how often wallets are added.

Neutral:
- No transaction history. The index holds balances only (#240 decides history).
- Unpruned nodes are unchanged.

## Open items

Pending #216 (needs the owner's written approval of a window; `bitcoin-cli -rpcclienttimeout=0`; abort at 88 C):

| Measurement | Decision it settles | Threshold that changes the decision |
| --- | --- | --- |
| Timed `scantxoutset` with an owner-approved descriptor or a dedicated test descriptor | Whether to build the index at all | 120 s or less and stall under 3 min: stop at #215. 10 min or more: build. In between: owner decides. |
| `dumptxoutset` wall time, file size, `coins_written`, `base_height`, with temperature, health and RPC sampled every 5 s | Disk thresholds, node-down alert handling, thermal limits | Stall over 3 min: suppress node-down alerts during the seed. Peak temperature over 85 C: lower the start limit. File over 12 GB: raise the 35 GB check. |
| Container StartedAt, RestartCount and `./stack events` Host: lines before and after | The assumption that the restart policy ignores unhealthy | Any restart: the seed needs a stronger guard or a different method. |
| FIFO dump to a throwaway reader as uid 1000 with `no-new-privileges` | Whether FIFO is an option | Works and real installs lack 35 GB: FIFO becomes the low-disk path. |
| Whether the dashboard can read and delete the dump on a shared writable volume | The shared volume design | Cannot: revisit mount modes. |
| UTXO count (from `coins_written`) | The size model | Over 190M: scale the budgets. |

Pending #217 (100M entries, mini-1, off-peak; the spike stays on `spike/pebble-utxo-bench`, so no dependency enters main):

| Measurement | Decision it settles | Threshold that changes the decision |
| --- | --- | --- |
| Pipeline wall, peak temp disk, final size, peak RSS at `-n 100000000 -max-disk-gb 16` | Engine, disk budget, memory limit | Peak RSS over 600 MiB: raise the limit or choose option D. Final size over 15 GB or peak spill over 22 GB: raise the thresholds. |
| Cold and warm prefix latency at 100M with a 64 MiB cache and a CPU quota near 0.5 | Whether "under 1 s" holds | Cold p99 over 1 ms, or a 2000-script batch over 1 s: add a cache or reduce the window, reopen the engine. |
| Same run with a 16-byte prefix (needs a small change to the spike) | The key length decision | Size or latency worse than the arithmetic by more than 10%: re-decide 16 versus 12 bytes. |
| Optional SQLite (modernc) variant | Whether the rejection of option B stands | Within 25% of Pebble on seed time and size with a smaller dependency set: reopen B. |

Other open items, not blocked by measurement:
- Deb and Homebrew installer handling of the overlay, and the `stack` allowlist (#223).
- Notice texts for the linked modules, the Pebble version to pin, and binary size growth (#222).
- Derivation batch cost for 2000 scripts (#231).

The ADR becomes final when both tables are filled. Then edit this file in place, change Status to Accepted, replace each Pending line with the measured value, and record the date.
