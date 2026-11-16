# Balance index lookups on mainnet (mini-1)

Date: 2026-11-16 (UTC). The first run of [the lookup benchmark](lookup-bench.md) on a real node, for #236. It covers the plain runs, the owner-approved `--scan` comparison, a check against two public block explorers, an hour of block following, and one run with a larger dashboard CPU cap.

- **Host:** mini-1, the reference node (4 cores, 7.6 GiB RAM, turbo off), running node-manager 1.36.0 from the APT feed, installed at 14:25 to 14:33Z.
- **Bitcoin Core:** 32.1, pruned (prune height 970,733), capped at 0.5 CPU.
- **Dashboard:** under the `bitcoin.utxo_index` overlay, so 0.5 CPU and 1 GiB.
- **Chain:** heights 976,140 to 976,168, synced.
- **Wallets:** `docs/measurements/bench-wallets.json` only: nine public addresses and four descriptors over the BIP32 test vector 1 key, about 2,000 scripts each. No owner wallet was used, and no address, key or balance of the owner appears here.

**Runs**

| Run | Time (Z) | Other chains | Command |
| --- | --- | --- | --- |
| Plain, coldest | 14:35 | Monero, Tari, Litecoin running | `utxo-index bench … --repeat 20` |
| Plain | 14:37 | the same | `--repeat 50`, with the dashboard's cgroup CPU counters read before and after |
| Follow | 16:33 to 17:34 | the same | no bench; a sample every 25 s of temperature, bitcoind health, CPU and memory |
| Scan | 18:08 to 18:16 | Monero, Tari and Litecoin stopped | `--scan -y --repeat 20`, sampled every 10 s |
| 1.0 CPU | 18:18 | stopped | `--repeat 20` with the dashboard briefly at 1.0 CPU, then set back to 0.5 |
| External check | 18:19 | just restarted | `--repeat 1`, then mempool.space and blockstream.info at the same tip |

The commands passed the wallet file as an absolute path, to work around the bug fixed in #317.

## Results

Times are in seconds unless marked. Where several figures are given, they are the coldest plain run, then the other runs.

| Target | Measured | Met |
| --- | --- | --- |
| Add an indexed wallet under 1 s (descriptor, cold) | `derive` + `first`, coldest run: wpkh 1.29, sh-wpkh 1.07, tr 1.82, pkh 1.36. Other runs: 0.68 to 1.27, 0.88 to 1.54, 0.88 to 2.07 | Not met: pkh and sh-wpkh can finish under 1 s on a warm run, tr and wpkh never did |
| Add an indexed wallet (genesis address, about 83,490 coins, cold) | `first` 0.09 to 0.18, then p50 0.08 to 0.10 | Met |
| Add a wallet by scan (first wallet alone) | 163.6 for the genesis address | Baseline: the index is about 1,000 times faster |
| Cached read under 100 ms, p50 | 1.7 to 3.5 ms | Met |
| Cached read, p99 | 2 to 78 ms | Met. The owner's roster had 2 wallets in the morning runs and 1 in the afternoon runs |
| Batch of 1 wallet, p50 (0.5 CPU) | 0.09 to 0.11, plus 0.001 to 0.002 derivation | No target |
| Batch of 5 wallets, p50 (0.5 CPU) | 0.09 to 0.10, plus 0.006 derivation | No target |
| Batch of all 13 wallets (8,009 scripts), p50 | 0.97 to 1.29, plus 2.3 to 2.7 derivation; 0.41 at 1.0 CPU | No target |
| Batch scan of the same wallets | 281.1 | Baseline: the index is about 70 times faster, derivation included |
| Scan and index agree on every wallet | 13 of 13 identical in coins and sats at block 976,165 | Met |
| Public explorers agree | 9 of 9 addresses identical in coins and sats on mempool.space and blockstream.info at block 976,168; 80 descriptor addresses (20 per descriptor) empty on both, as in the index | Met |
| Descriptor shapes are indexed | 0 fallbacks; every batch's `scripts` equals `expected` | Met |
| New block to balance, median and p90 | 5 s and 6 s (max 9 s) over 12 blocks; 1.35.1 had 17 s and 25 s over 50 blocks | No target; 3.4 times faster at the median |
| Restart to ready | 1.1 from dashboard start to `ready`, bitcoind kept running (the 1.36.0 upgrade); 21 after a full power cycle (2026-11-15) | No target |
| Peak dashboard memory and CPU while following | 248 MiB of 1 GiB (median 148 MiB); CPU median 0.01, max 0.47, over 133 samples in an hour | No target |
| Peak temperature | 68 C while following (median 50 C), 67 C during the scans; no sample at 85 C or above | No target |
| bitcoind health during follow | healthy in 133 of 133 samples, and in 42 of 42 during the scans | Met |
| Index size and growth | 7.70 GiB at 976,150 and 7.73 GiB at 976,165: 26.2 MB over 15 blocks, about 1.75 MB per block. This is the whole store, so it includes the journal and compaction, not the journal alone | No target |

## Scan against index

The scan run held the watch scanner, ran one `scantxoutset` over all 13 wallets (281.1 s) and one over the first wallet alone (163.6 s), and compared each wallet with the index:

- **The batch scan and the index lookups were both at block 976,165.** Every wallet had the same coin count and the same total in sats.
- **The tool printed `match n/a`.** Blocks 976,166 (18:12:05Z) and 976,167 (18:14:51Z) arrived during the single-wallet scan, and its level check also requires that scan and the final index tip to be at the same height. The batch comparison is still level.

During the scans bitcoind sat at its 0.5 CPU cap. Its container memory reached 6.3 GiB of the host's 7.6 GiB, mostly page cache from reading the coin set. That is another reason the runbook pauses the other chains first.

## Public explorers

At 18:19Z the index answered at block 976,168 for all 13 wallets. Then both explorers were asked for each address's confirmed funded minus spent outputs, with their tip read before and after; both stayed at 976,168.

- All nine addresses had the same unspent count and sats on both explorers as in the index.
- The genesis address agrees too: like Bitcoin Core, both explorers leave out the unspendable 50 BTC genesis reward.
- For each test-vector descriptor, 20 derived addresses showed no coins on either explorer, which matches the index's 0 over all 2,000 scripts.
- Earlier, at block 976,165, the dashboard's public example wallet matched both explorers: 5,116 coins.

Only the bench file's public addresses and test-vector addresses were sent to the explorers.

## Where the descriptor time goes

A descriptor add is two steps:

- **Derivation** of the 2,000 scripts runs in bitcoind's `deriveaddresses`, at 0.5 CPU: 0.47 to 1.54 s, and `tr` is the slowest.
- **The first lookup** of those 2,000 scripts runs in the dashboard: 0.6 to 0.8 s right after the upgrade, and 0.15 to 0.84 s across the later runs, depending on what the OS page cache already holds.

With the dashboard at 1.0 CPU instead of 0.5, the first lookups did not get faster (0.41 to 0.84 s). Repeated lookups halved (p50 0.06 to 0.08 s), the 13-wallet batch dropped from 1.29 to 0.41 s, and the cached read's p99 from 78 to 2 ms. So the cold descriptor add is bound by derivation in bitcoind and by cold reads, not by the dashboard's CPU cap.

During the 50-repeat run at 0.5 CPU, the dashboard used 45.1 CPU-seconds in 91.9 s (0.49 of a CPU) and was throttled in 892 of 919 scheduler periods.

Single addresses take about 1 ms, and the 5,116-coin address takes 1 to 5 ms on its first lookup.

## New block to balance

`scripts/measure-block-latency.sh --since 2026-11-16T14:33:00Z`, run at 16:42Z after 12 blocks on 1.36.0:

| First refresh after a block | Blocks | Median | p90 | Max |
| --- | --- | --- | --- | --- |
| Served by the index | 10 | 5 s | 6 s | 9 s |
| Fell back to the block update | 2 | 4 s | 5 s | 5 s |
| All | 12 | 5 s | 6 s | 9 s |

The same script on 1.35.1 earlier that day, over 50 blocks: median 17 s and p90 25 s for all blocks; the index served 35 of them at a median of 21 s. It warned that the dashboard logs started after `--since`, so that window may be biased. #304, in 1.36.0, waits for the new block, applies it to the index at once, and then refreshes the wallets. Twelve blocks is a small sample.

## Findings

1. **Every accuracy check passed.** The index matched `scantxoutset` for all 13 wallets and both public explorers for every address checked.
2. **The index makes adds and batches 70 to 1,000 times faster than a scan**, and cached reads and new blocks meet their targets.
3. **The one missed target is a cold add of a 2,000-script descriptor.** Derivation in bitcoind takes about half of it, and raising the dashboard's CPU cap does not help. Likely next steps, not yet measured: derive scripts in the dashboard instead of through RPC, or start with a smaller range and extend it by gap limit.
4. **A 1.0 CPU dashboard cap makes warm lookups, batches and cached reads two to three times faster.** That is worth considering for hosts with spare cores.
5. **The scan comparison's level check is stricter than it needs to be:** it skips the comparison when a block arrives during the single-wallet scan, even though the batch scan and the lookups were at the same height.

## Not tested

- A batch of 20 wallets: the file has 13.
- The cached read with a 5- or 20-wallet roster.
- Any host other than mini-1.
