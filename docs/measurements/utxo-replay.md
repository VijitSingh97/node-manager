# Balance index replay: blocks per second on regtest

Date: 2026-11-16. Measured for the shared replay seeder (#326) on an Apple M5 Max under Docker Desktop (8 VM CPUs). These are regtest numbers. They are not reference-node or mainnet numbers.

A chain with no UTXO export (Bitcoin Cash, Dogecoin) seeds its index by applying every block from genesis through the follower. On those chains the follower fetches up to 32 blocks ahead on one goroutine and commits them in batches. Each block costs one unit per coin operation plus one per KiB of output script, so a block of a few huge outputs counts too. A batch ends at 1,000 blocks or a cost of 200,000, whichever comes first, with one sync and one meta write. The blocks fetched ahead plus the open batch stay within a cost of 400,000; a larger block waits until everything before it is committed, so at most one such block is applied while the next is fetched. A parsed raw block keeps only its output scripts, not the raw bytes. Bitcoin and Litecoin still commit one block at a time, which the "per block" rows below measure.

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

## Bitcoin Cash Node 29.2.0

The same test with `UTXO_REPLAY_CHAIN=bitcoincash BCH_REGTEST_IMAGE=ghcr.io/vijitsingh97/node-manager-bitcoincash:1.34.0` replays a BCHN regtest chain the way the index does: raw blocks and the reverse key, in a container capped at 0.5 CPU and 2 GiB with `GOMEMLIMIT=1600MiB`. Measured 2026-11-17; each figure is the median of three runs.

| Blocks | Per block | Batched | Batched / per block |
| --- | --- | --- | --- |
| 20,001 coinbase-only | 3,420 blocks/s (2,844 to 3,424) | 7,751 blocks/s (7,674 to 8,163) | 2.27x |
| 300 with a 200-output transaction | 710 blocks/s (668 to 734) | 933 blocks/s (870 to 1,006) | 1.31x |

Both modes matched `scantxoutset` on 18,969 coins in every run.

- **The machine was not idle.** The load average read 2.0 when the run started and 6.0 when it ended, with Spotlight indexing at the end.
- **Bitcoin, right after, under similar load** (5.0 to 6.0): on coinbase-only blocks 2,375 blocks/s per block and 3,330 batched; on busy blocks 242 and 243.
- **On busy blocks the raw Bitcoin Cash path ran about 3.8 times Bitcoin's batched rate** in the same session. Decoding verbose JSON is the likely difference; that is inferred, not profiled.
- **The Bitcoin Cash figures first published here** came from the verbose-JSON design and were removed.

`TestUtxoBitcoinCashRawReplayRegtestLive` checks the raw parser against BCHN's own verbose view of every block, with CashTokens activated at height 200 (`-upgrade9activationheight=200`).

- **Before activation** it plants, by rewriting output scripts, a fungible-only prefix, an NFT without a commitment, an NFT with one, a fungible NFT, and a malformed prefix.
- **After activation** the wallet writes the same shapes: fungible only, an NFT with a commitment, a mutable NFT without one, and a minting NFT with a commitment and an amount. A second transaction moves the fungible tokens.
- **Parity:** the parser matched `getblock` verbosity 3 on all 226 outputs of 211 blocks. BCHN reads a well-formed prefix before activation too, so the parser strips it at any height; a parser gated on the activation height fails at height 111. A malformed prefix stays the whole script.
- **After activation BCHN refuses a malformed prefix** with `bad-txns-vout-tokenprefix`, and accepts the same transaction without it.
- **Seed path:** replayed through the dashboard's seed path, the index matched `scantxoutset` on 219 coins across 12 scripts, and on 220 after a one-block reorg. Every token holder's output is counted under its plain script, and the spent token output leaves the index.

### Large Bitcoin Cash blocks

Bitcoin Cash's 2018 stress tests produced mainnet blocks of 8 to 32 MB. `UTXO_BCH_LARGE=1 BCH_REGTEST_IMAGE=… go test -run TestUtxoBitcoinCashLargeBlockReplay -v` builds four regtest blocks of 31.0 MB each. Each block holds 908 transactions of 1,000 outputs. A shell probe on one such block measured the node's memory with `docker stats`, sampled while `getblock` ran.

| `getblock` of one 31.0 MB block | Response | BCHN memory |
| --- | --- | --- |
| verbosity 0 (raw hex) | 62.1 MB | 308 MiB before, about 347 MiB at peak |
| verbosity 3 (JSON with prevouts) | 332.6 MB over RPC | 308 MiB before, about 2,956 MiB at peak |

1. **The node pays about 2.6 GiB to serve one such block at verbosity 3.** In the four-block run, BCHN closed the connection on the fourth block's request. A Bitcoin Cash node under the default 2 GiB steady memory limit would likely be killed. The raw form costs it about 40 MiB.
2. **The JSON is about 10.7 times the block's size, five times the 64 MiB RPC body limit.** The dashboard would also need roughly 0.8 GB to hold the body, its copy and the decoded block, against a 1 GB limit.
3. **The index reads raw blocks.** Raw reading is the design the Bitcoin Cash index ships with: verbosity 0, parsed in Go, with spends resolved through the reverse outpoint key.

The raw replay of the saved chain is measured below: 115 blocks and 3,621,114 coins, at 0.5 CPU. `UTXO_BCH_LARGE_DATA=<dir>` reuses a saved chain, and `UTXO_BCH_LARGE_MEM` and `UTXO_BCH_LARGE_GOMEMLIMIT` set the replay container's limits. The live heap is `runtime/metrics` `/gc/heap/live:bytes`, sampled every 200 ms; BCHN's memory comes from `docker stats`, sampled every second.

| Dashboard limit | Result | Replay time | Peak RSS (VmHWM) | Largest live heap | BCHN peak |
| --- | --- | --- | --- | --- | --- |
| 1 GiB, no `GOMEMLIMIT` | killed, exit 137 | n/a | n/a | n/a | n/a |
| 1 GiB, `GOMEMLIMIT=850MiB` | still running at the 60-minute timeout | over 3,600 s | n/a | n/a | n/a |
| uncapped, before copy reductions | finished | 1,259 s | 2,145,016 KiB (2.05 GiB) | not sampled | about 1,100 MiB |
| uncapped | finished | 1,482 s | 2,050,576 KiB (1.96 GiB) | not sampled | about 1,090 MiB |
| uncapped, live heap sampled | finished | 2,138 s | 1,877,744 KiB (1.79 GiB) | 951 MiB | about 1,248 MiB |
| 2 GiB, `GOMEMLIMIT=1600MiB`, before the script copy | finished | 2,017 s | 1,559,216 KiB (1.49 GiB) | 953 MiB | about 1,239 MiB |
| 2 GiB, `GOMEMLIMIT=1600MiB` (shipped until #363) | finished | 1,265 s | 1,577,776 KiB (1.50 GiB) | 954 MiB | about 1,239 MiB |
| 2 GiB, `GOMEMLIMIT=1600MiB`, rerun 2026-11-20 | finished | 966 s | 1,647,324 KiB (1.57 GiB) | 945 MiB | about 1,090 MiB |
| **1 GiB, no `GOMEMLIMIT`, applied in pieces (#363, shipped)**, run 1 | finished | 109 s | 719,204 KiB (0.69 GiB) | 296 MiB | about 1,160 MiB |
| **1 GiB, no `GOMEMLIMIT`, applied in pieces (#363, shipped)**, run 2 | finished | 78 s | 811,236 KiB (0.77 GiB) | 296 MiB | about 1,171 MiB |

The three 2026-11-20 runs used the same saved chain, one after another, with the host's load average between 15 and 34 from other work.

4. **Applying one 31 MB block whole needs about 950 MiB of live heap.** That covers the block's coin operations, its Pebble batch and its undo record, all held for the whole block. Under 1 GiB without `GOMEMLIMIT` that replay is killed, and with a `GOMEMLIMIT` under 1 GiB it crawled. Since #363 a block of more than 50,000 coin operations is applied in pieces of 50,000, each committed on its own, and the same replay fits 1 GiB with no `GOMEMLIMIT`. It peaked at 296 MiB of live heap and 0.69 to 0.77 GiB RSS, against 945 MiB and 1.57 GiB for whole blocks under 2 GiB in the same session.
   - **Where the whole-block heap went.** An in-process probe applied 908,000-output blocks one after another, outside the container. With whole blocks, 280 to 470 MiB beyond the next block stayed live between blocks; with pieces it was about 20 to 40 MiB. Pebble keeping each committed block's batch in memory until it is flushed is the likely holder; that is inferred, not profiled.
   - **Speed.** The pieced replay finished in 78 to 109 s, against 966 s for whole blocks in the same session. The cause is not profiled; garbage collection close to the `GOMEMLIMIT` at 0.5 CPU is the likely difference. In-process at full CPU, the pieced apply took about 1.3 times as long as whole blocks. Once a block's earlier pieces are on disk, the reads that check for existing coins go to sstables. Reading a piece's new coins in key order brought that down from about 3 times.
5. **Both peaks are regtest lower bounds.** In the shipped runs the dashboard peaked at 0.69 to 0.77 GiB RSS and BCHN at about 1.14 GiB. Mainnet blocks and BCHN's mainnet memory are not measured, so plan the dashboard at up to its 1 GiB cap on top of the node's own plan in [Nodes](../nodes.md).
6. **Applied whole, the index came to 635.9 MB for 3,621,114 coins: 175.6 bytes per coin.** Applied in pieces it came to 279.0 to 279.8 MB, 77 bytes per coin, both measured right after the replay. Why the whole-block index was larger at that moment is not measured; data not yet compacted is the likely difference. Both include the reverse key and the undo journal of every block in this chain. The chain is synthetic: every coin has a 25-byte P2PKH script, and the four large blocks carry nearly all of them. Bitcoin Cash's mainnet coin count and per-coin cost are not measured.
7. **Replay times vary between runs of the same chain**, from 1,259 to 2,138 s. The cause is not measured; other load on the machine is likely. The host's load average read 3.4 when the shipped run started and 8.9 when it ended.
8. **A block over 1.2 million coin operations stops the index.** The parsed block stays whole while its pieces apply. It measured about 110 bytes per operation (98 MiB for 908,000 outputs with 25-byte scripts), so the cap keeps it near 130 MiB (derived, not measured at the cap). Ordinary outputs of 32 to 34 bytes fit about 1 million in 32 MB, under the cap, but a miner could fill a block with about 3.5 million outputs that have empty scripts (9 bytes each). Such a block, or one too large for the 64 MiB RPC read, parks the index at that height without invalidating it.

## Findings

1. **Batching nearly doubles the rate on small blocks.** Per-block overhead dominates there: two RPC round trips, a sync, an undo record and a meta rewrite. Pipelining and batching take most of it away.
2. **Busy blocks gain about 10%, which is within the run-to-run spread.** Their cost is per-coin work in the capped dashboard, not per-block overhead. Where that work goes (JSON decoding, script hashing, Pebble writes) is not profiled.
3. **The disk here is fast.** The store sits on the Docker VM's disk. The disk's sync cost is part of the per-block overhead, so a slower disk would likely widen the small-block gain. That is an inference; it is not measured.
4. **Mainnet replay times are unknown.** Mainnet blocks differ in size and in transaction mix from both workloads here. Bitcoin Cash and Dogecoin replay rates will be measured on their own nodes in their phases.
