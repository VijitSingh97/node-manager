# Performance history

Every measured run on the reference node, mini-1 (4 cores, 7.6 GiB RAM, turbo off), newest first. Each row links to its write-up, which has the method, the conditions and every number. After a run, write it up in this folder and add a row here.

| Date (UTC) | Release | Chain | Height | Run | Headline | Write-up |
| --- | --- | --- | --- | --- | --- | --- |
| 2026-11-25 | 1.42.1 | Bitcoin Cash | 978,850 to 978,851 | Lookup bench with `--scan`, rich-list addresses against Bitcore and Blockchair | Single address p50 7.4 ms or less (6,667 coins) against 44 to 82 s per scan. 16 of 16 wallets match the scan, 8 of 8 match Bitcore in sats and coins. 1.42.0's `--scan` was refused (fixed in 1.42.1). Peak 80 C | [Bitcoin Cash balance index on mainnet](bitcoincash-index-mainnet.md) |
| 2026-11-24 | 1.41.0 | Bitcoin Cash | 978,795 | `verify`; the lookup bench refuses Bitcoin Cash wallets | Verify PASS, 58,783,710 coins (index 82.5 s, node 60.1 s). Index ready 20 s after the upgrade's restart. Peak 79 C | [Bitcoin Cash balance index on mainnet](bitcoincash-index-mainnet.md) |
| 2026-11-24 | 1.40.3 | Bitcoin Cash | synced at 978,726 | First balance index seed, automatic, from a chainstate copy | 530 s, 5.23 GB, about 89 bytes per coin; node down about 2 min 20 s, 120 s of it waiting for Tor | [Bitcoin Cash balance index on mainnet](bitcoincash-index-mainnet.md) |
| 2026-11-24 | 1.40.2 | Litecoin | 3,218,340 to 3,218,349 | `verify`, lookup bench with `--scan`, known addresses against two explorers | Verify PASS, 53,822,839 coins (index 68.6 s, node 44.5 s). Descriptor add 0.37 to 0.54 s, single address p50 under 8 ms against 39 to 58 s per scan. 16 of 16 wallets match the scan, 8 of 8 match both explorers. Peak 82 C | [Litecoin balance index on mainnet](litecoin-index-mainnet.md) |
| 2026-11-23 | 1.40.2 | Litecoin | synced | First balance index seed, automatic | 346 s, 4.52 GB, no heat pause | [Litecoin balance index on mainnet](litecoin-index-mainnet.md) |
| 2026-11-16 | 1.36.0 | Bitcoin | 976,140 to 976,168 | Lookup bench with `--scan`, two explorers, an hour of block following | Descriptor add 0.68 to 2.07 s (1 s target not met). Genesis address first lookup 0.09 to 0.18 s against a 163.6 s scan. 13 of 13 match the scan, 9 of 9 match both explorers. New block to balance median 5 s | [Balance index lookups on mainnet](lookup-bench-mainnet.md) |
| 2026-11-15 | 1.34.0 | Bitcoin | 975,866 | First balance index seed, by hand, Tari stopped | 1,179 s with 8 heat pauses, 7.5 GiB (16.7 GiB at the peak), RPC down about 306 s, peak 86 C | [First mainnet UTXO seed](utxo-seed-mainnet.md) |

How to run the checks: [Seeding the balance index](../operations.md#seeding-the-bitcoin-balance-index), [Checking a balance index against the node](../operations.md#checking-a-balance-index-against-the-node) and [the lookup benchmark](lookup-bench.md).

## Other measurements

These are not reference-node or mainnet numbers.

| Date (UTC) | Where | What | Write-up |
| --- | --- | --- | --- |
| 2026-11-16 | Apple M5 Max, regtest | Replay seeding, blocks per second | [Balance index replay](utxo-replay.md) |
| 2026-11-16 | Apple M5 Max | Litecoin reverse outpoint key, disk per coin | [Litecoin reverse key](litecoin-reverse-key.md) |
| 2026-11-14 | regtest | Seed FIFO wedge matrix | [UTXO seed FIFO wedge](utxo-fifo-wedge.md) |
