# Balance index replay: blocks per second on regtest

Date: 2026-11-16. Measured for the shared replay seeder (#326) on an Apple M5 Max under Docker Desktop (8 VM CPUs). These are regtest numbers. They are not reference-node or mainnet numbers.

A chain with no UTXO export (Bitcoin Cash, Dogecoin) seeds its index by applying every block from genesis through the follower. On those chains the follower fetches up to 32 blocks ahead on one goroutine and commits them in batches. A batch ends at 1,000 blocks or 200,000 coin operations, whichever comes first, with one sync and one meta write. The blocks fetched ahead and not yet taken into a batch stay within 200,000 coin operations; a single larger block goes through alone. Bitcoin and Litecoin still commit one block at a time, which the "per block" rows below measure.

The replay needs every block, so a pruned node is refused before it starts. The replay runs as a seed job until the index is within 10,000 blocks of the node's tip. From there the follower takes over, batching the same way. A node restart, an RPC timeout or a reorg near the tip is retried within the replay, 10 seconds apart; any committed progress resets the count, so the replay gives up and waits for the hourly check only after about five minutes of failures in a row. Storage and integrity errors stop it at once.

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

## Findings

1. **Batching nearly doubles the rate on small blocks.** Per-block overhead dominates there: two RPC round trips, a sync, an undo record and a meta rewrite. Pipelining and batching take most of it away.
2. **Busy blocks gain about 10%, which is within the run-to-run spread.** Their cost is per-coin work in the capped dashboard, not per-block overhead. Where that work goes (JSON decoding, script hashing, Pebble writes) is not profiled.
3. **The disk here is fast.** The store sits on the Docker VM's disk. The disk's sync cost is part of the per-block overhead, so a slower disk would likely widen the small-block gain. That is an inference; it is not measured.
4. **Mainnet replay times are unknown.** Mainnet blocks differ in size and in transaction mix from both workloads here. Bitcoin Cash and Dogecoin replay rates will be measured on their own nodes in their phases.
