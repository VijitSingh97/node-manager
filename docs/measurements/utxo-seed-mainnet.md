# First mainnet UTXO seed (mini-1)

Date: 2026-11-15 (UTC). This is the first real run of ADR 0001's seed on mainnet, for #226 and #236.

- **Host:** mini-1, the ADR's reference node, running node-manager 1.34.0 from the APT feed.
- **Bitcoin Core:** 32.1, the image pinned in `docker-compose.yml`. Pruned at 10000 MiB, dbcache 512 MiB, capped at 0.5 CPU.
- **Dashboard:** under the `bitcoin.utxo_index` overlay, so 0.5 CPU and 1 GiB.
- **Chain:** height 975,866 at the start, synced, prune height 970,485.
- **Other chains:** Monero running (1 CPU cap). The Tari base node was stopped for the window, as in #216. With Tari running, this host idles at 73 C (ADR section 5).
- **Start conditions:** 58 to 62 C, after 45 minutes with Tari stopped, and 81 GB free on the Docker volume filesystem.
- **Trigger:** `sudo node-manager utxo-seed -y` at 00:47:59.
- **Sampling,** 218 samples in all, with no addresses, keys or balances recorded:
  - every 5 s:
    - the hottest thermal zone;
    - a `getblockcount` round trip through `docker exec` (6 s timeout);
    - the dashboard cgroup's anon memory, total memory and CPU time;
    - bitcoind's CPU time;
  - every 30 s: the index volume size.

## Timeline (UTC)

| Phase | From | To | Duration | Basis |
| --- | --- | --- | --- | --- |
| Request taken, checks passed, dump sent | 00:48:03 | | | dashboard log `utxo index: state=seeding` |
| Hash pass, RPC failing | 00:48:04 | 00:53:10 | about 306 s | every sample failed in this span, and the next one answered |
| Write and import | about 00:53:15 | about 01:04:40 | about 685 s | the index volume grows from 00:53:35; the CLI shows `merging` by 01:04:38 |
| Merge | about 01:04:40 | 01:07:42 | about 180 s | |
| Seed result | 00:48:03 | 01:07:42 | 1179 s | `utxo index: seed outcome=ok duration=1179s` |
| Index ready | 01:07:42 | 01:07:52 | 10 s | caught up 4 blocks, to 975,870 |

## Results

| Quantity | Measured | ADR expectation |
| --- | --- | --- |
| Total seed time | 1179 s (19.7 min) | about 889 s (15 min), a floor with no pauses (derived) |
| RPC failing | about 306 s | about 300 s (#216) |
| RPC while degraded | 169 of 169 samples answered; mean 106 ms, max 227 ms, against about 70 ms before the seed | degraded for about 5 more minutes |
| Write and import | about 685 s, with 8 heat pauses | 313 s write and 351 s run generation, overlapped (derived) |
| Merge | about 180 s | 238 s (derived) |
| Peak temperature | 86 C at 01:03:26; 5 of 218 samples at or above 85 C | likely above 85 C (reasoned) |
| Reader pauses | 8, at 12, 30, 31, 33, 34, 36, 44 and 45% (CLI trace); each resumed | untested |
| Stack throttle at 88 C | did not fire | |
| Alerts | none: no node-down, recovered or throttle event from 00:47 to 01:09 | one down and one recovered without the hold (#216) |
| bitcoind CPU | 0.50 during the hash pass, 0.25 during the write | capped at 0.5 |
| Dashboard CPU | 0.07 during the hash pass, 0.47 during the import, 0.50 during the merge | capped at 0.5 |
| Dashboard memory | anon peak 289 MiB; the cgroup total reached the 1 GiB limit from page cache, with no OOM or restart | 0.25 GiB peak RSS (#217) |
| Index on disk, peak | at least 17,117 MiB (16.7 GiB), sampled every 30 s, during the merge | 20.3 GiB (derived) |
| Index on disk, final | 7,635 MiB (7.5 GiB) | 10.57 GiB (derived) |
| Leftovers | none: no `utxo-seed.dat` or `.incomplete`, and the alert hold cleared on success | |

## Correctness

Both watched wallets moved to `balance_source` `index` on the first refresh after `ready`. The dashboard logged:

- `watch index: outcome=ok outpoints=7 height=975870 took=0.877s`, the first lookup, including script derivation;
- `watch index: outcome=ok outpoints=5116 height=975870 took=0.025s`.

Before the seed, the scan cache held the same two wallets at height 975,861, with 7 and 5,116 outpoints. A sha256 over each wallet's sorted `txid:vout:sats` list was the same before (scan) and after (index). Neither wallet changed between the two heights, so this is a direct equality check, not a reconciliation.

## Findings

1. **The design held on mainnet.** The FIFO seed finished, matched the scan, left nothing behind and raised no alert.
2. **The 0.5 CPU reader paces Core's write.** The write phase took about 685 s instead of 313 s; this is the ADR's assumption 2. RPC kept answering through it, only slower. So the 15-minute alert cap only had to cover the 306 s failure.
3. **The 85 and 72 C gate worked with Tari stopped.** There were eight pauses during the write phase and each resumed. Core never failed the dump, and the host stayed under the 88 C throttle. A pause during the write phase is safe.
4. **The index is smaller than derived:** 7.5 GiB final and at least 16.7 GiB peak, against 10.57 and 20.3 GiB. The 27 GB pre-seed check has more margin than planned. The coin count was not recorded, so per-coin sizes are not computed here.
5. **The dashboard's page cache fills the 1 GiB cgroup limit during the import.** Anon memory stays under 300 MiB, and the kernel reclaimed the cache; nothing failed.
6. **The index directory was 0755, not 0700.** A Docker volume mount point already exists, so `MkdirAll` left its mode alone. The dashboard now sets 0700 when it opens the index.

## Not tested

- The seed with all chains running. Tari was stopped, so whether a paused seed resumes while mini-1 idles at 73 C is still open.
- A cancel or a stop during a real paused write.
- The stack throttle firing during a dump.
- Any host other than mini-1.
