# Balance index replay: blocks per second on regtest

Date: 2026-11-16. Measured for the shared replay seeder (#326) on an Apple M5 Max under Docker Desktop (8 VM CPUs). These are regtest numbers. They are not reference-node or mainnet numbers.

A chain with no UTXO export (Bitcoin Cash, Dogecoin) seeds its index by applying every block from genesis through the follower. On those chains the follower fetches up to 32 blocks ahead on one goroutine and commits them in batches. A batch ends at 1,000 blocks or 200,000 coin operations, whichever comes first, with one sync and one meta write. The blocks fetched ahead plus the open batch stay within 400,000 coin operations; a larger block waits until everything before it is committed, so at most one such block is applied while the next is fetched. Bitcoin and Litecoin still commit one block at a time, which the "per block" rows below measure.

The replay needs every block, so a pruned node is refused before it starts. The replay runs as a seed job until the index is within 10,000 blocks of the node's tip. From there the follower takes over, batching the same way.

## Method

`UTXO_REGTEST=1 go test -run TestUtxoReplayRegtestRate -v` in `build/dashboard`:

1. It starts the pinned Bitcoin Core 32.1 image on regtest.
2. It mines 20,001 coinbase-only blocks, then 300 blocks that each carry one 200-output `sendmany`.
3. It runs the test binary in a container capped like the production dashboard: `--cpus 0.5 --memory 1g`, sharing the node's network.
4. That binary replays the chain from genesis into a fresh store twice, once per block and once batched. Both stores must match `scantxoutset` on 18,818 coins.

The node itself is not capped. Each figure is the median of three runs.

## Results

| Blocks | Per block | Batched | Batched / per block |
| --- | --- | --- | --- |
| 20,001 coinbase-only | 2,094 blocks/s (2,088 to 2,303) | 3,828 blocks/s (3,291 to 3,958) | 1.83x |
| 300 with a 200-output transaction | 231 blocks/s (226 to 244) | 256 blocks/s (231 to 260) | 1.11x |

## Bitcoin Cash Node 29.2.0

The same test with `UTXO_REPLAY_CHAIN=bitcoincash BCH_REGTEST_IMAGE=ghcr.io/vijitsingh97/node-manager-bitcoincash:1.34.0` replays a BCHN regtest chain with the Bitcoin Cash chain parameters, under the same caps. Each figure is the median of three runs.

| Blocks | Per block | Batched | Batched / per block |
| --- | --- | --- | --- |
| 20,001 coinbase-only | 2,853 blocks/s (2,106 to 3,042) | 5,848 blocks/s (4,783 to 6,328) | 2.05x |
| 300 with a 200-output transaction | 456 blocks/s (419 to 533) | 544 blocks/s (215 to 607) | 1.19x |

Both modes matched `scantxoutset` on 18,795 coins in every run. One batched busy run fell to 215 blocks/s, and the other two ran at 544 and 607 blocks/s. Bitcoin Cash blocks decode faster than Bitcoin's here, since they carry no witness data.

`TestUtxoBitcoinCashReplayRegtestLive` also replays through the dashboard's seed path, with CashTokens outputs. A minting NFT with fungible tokens is created, then moved by a second transaction. The index matched `scantxoutset` after the replay and again after a one-block reorg. The token output's BCH is counted for its holder, and the spent token output leaves the index.

### Large Bitcoin Cash blocks

Bitcoin Cash's 2018 stress tests produced mainnet blocks of 8 to 32 MB. `UTXO_BCH_LARGE=1 BCH_REGTEST_IMAGE=… go test -run TestUtxoBitcoinCashLargeBlockReplay -v` builds four regtest blocks of 31.0 MB each. Each block holds 908 transactions of 1,000 outputs. A shell probe on one such block measured the node's memory with `docker stats`, sampled while `getblock` ran.

| `getblock` of one 31.0 MB block | Response | BCHN memory |
| --- | --- | --- |
| verbosity 0 (raw hex) | 62.1 MB | 308 MiB before, about 347 MiB at peak |
| verbosity 3 (JSON with prevouts) | 332.6 MB over RPC | 308 MiB before, about 2,956 MiB at peak |

1. **The node pays about 2.6 GiB to serve one such block at verbosity 3.** In the four-block run, BCHN closed the connection on the fourth block's request. A Bitcoin Cash node under the default 2 GiB steady memory limit would likely be killed. The raw form costs it about 40 MiB.
2. **The JSON is about 10.7 times the block's size, five times the 64 MiB RPC body limit.** The dashboard would also need roughly 0.8 GB to hold the body, its copy and the decoded block, against a 1 GB limit.
3. **The replay of those blocks was not reached.** The test stopped when the node dropped the fourth request, so the dashboard's peak memory on large blocks is not measured.

## Findings

1. **Batching nearly doubles the rate on small blocks.** Per-block overhead dominates there: two RPC round trips, a sync, an undo record and a meta rewrite. Pipelining and batching take most of it away.
2. **Busy blocks gain about 10%, which is within the run-to-run spread.** Their cost is per-coin work in the capped dashboard, not per-block overhead. Where that work goes (JSON decoding, script hashing, Pebble writes) is not profiled.
3. **The disk here is fast.** The store sits on the Docker VM's disk. The disk's sync cost is part of the per-block overhead, so a slower disk would likely widen the small-block gain. That is an inference; it is not measured.
4. **Mainnet replay times are unknown.** Mainnet blocks differ in size and in transaction mix from both workloads here. Bitcoin Cash and Dogecoin replay rates will be measured on their own nodes in their phases.
