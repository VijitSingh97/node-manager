# Dogecoin balance index on mainnet (mini-1)

Date: 2026-11-26 and 2026-11-27, UTC. The first mainnet runs of the Dogecoin balance index. They cover the automatic seed from a copy of the node's chainstate, `utxo-index verify`, and the index-only lookup benchmark, with recent-block and rich-list addresses checked against two block explorers. Dogecoin Core 1.14.9 has no `scantxoutset`, so there is no scan to compare against; `verify` is the full check.

- **Host:** mini-1, the reference node (4 cores, 7.6 GiB RAM, turbo off), from the APT feed: node-manager 1.42.1 for the seed and 1.42.2 for `verify` and the bench.
- **Dogecoin Core:** 1.14.9, pruned (10.4 GB on disk), capped at 1 CPU and 4 GiB, 8 peers.
- **Dashboard:** 0.5 CPU and 1 GiB.
- **Chain:** `verify` ran at 6,475,191 and the bench at 6,475,200. The UTXO set held 202,097,463 coins at 6,475,191.
- **Other chains:** Bitcoin, Litecoin, Bitcoin Cash, Monero, Tari and both wallet containers were running for every run. Nothing was paused.
- **Wallets:** public data only, in [`dogecoin-bench-wallets.json`](dogecoin-bench-wallets.json). No owner wallet was used.
- **Sampling:** the seed was not sampled; its times come from the seed state file and the node's `debug.log`. The verify and the bench were sampled about every 7 s: the hottest thermal zone, Dogecoin's health, and CPU and memory of the Dogecoin and dashboard containers (43 samples for verify, 12 for the bench).

**Runs**

| Run | Time (Z) | Command |
| --- | --- | --- |
| First seed attempt | 2026-11-26 05:16:38 to 05:17:55 | automatic, once the node had synced; the node stopped for the copy at 05:16:38 and was back at 05:18:30. The dashboard was killed for memory at 05:17:55, during the import (see Notes) |
| Seed | 11:17:58 to 11:45:29 | the automatic retry; outcome ok |
| Upgrade | 2026-11-27 15:44 to 15:55 | APT upgrade from 1.42.1 to 1.42.2 |
| Verify | 16:12:53 to 16:18:03 | `sudo node-manager utxo-index verify --chain dogecoin -y` |
| Bench | 16:20:07 to 16:21:26 | the index-only bench, 16 wallets, `--repeat 20`, run through the dashboard's bench request file because the 1.42.2 command refuses `--chain dogecoin` (see Notes) |
| P2PK probe | 16:24 | the same bench with one coinbase address paid by a P2PK output |

## Results

Times are in seconds unless marked.

| Target | Measured | Met |
| --- | --- | --- |
| Seed | 1,618 s of import (11:18:30 to 11:45:29), outcome ok, from a chainstate copy | No target |
| Node stop for the copy | Dogecoin Core shut down at 11:17:58 to 11:18:01 and had loaded again at 11:19:43, about 1 min 45 s down | No target |
| Index size | 18.36 GB (18,362,859,760 bytes, read after the bench) for 202.1 million coins, about 91 bytes per coin | No target |
| Index and node agree (`verify`) | PASS at 6,475,191: 202,097,463 coins and 156,924,412,860.00806072 DOGE in both. The index count took 305.1 s and Dogecoin Core's `gettxoutsetinfo` 304.6 s (1.14.9 has no hash type, so it hashes the whole set); the command took 310 s | Met |
| Restart to ready | After the 1.42.2 upgrade recreated the dashboard at 15:53:06, all four indexes were ready by 15:54:43. The log does not say which chain each line is for, so Dogecoin's own time is not known | No target |
| Add an indexed single address | For the 15 addresses holding 10 to 84,092 coins: `first` 0.5 to 114 ms, p50 under 1 ms up to 2,555 coins, then 6.1 ms (22,746 coins), 13.6 ms (47,253) and 74.7 ms (84,092). The busiest P2SH address holds 917,097 coins: `first` 1.44 s, p50 0.84 s | Met for all but the 917,097-coin address |
| Batch of 1, 5 and 16 wallets, p50 | 0.78, 0.89 and 1.20; each batch includes the 917,097-coin address (917,097, 1,049,122 and 1,077,518 coins) | No target |
| Scan and index agree | not possible: Dogecoin Core 1.14.9 has no `scantxoutset` | |
| Public explorers agree | 8 of 8 rich-list addresses identical in confirmed sats on dogechain.com and BlockCypher, read before and after the bench. The eight recent-block addresses kept receiving coins; each matched at least one explorer read to the sat except the two busiest P2PKH addresses, which moved between the reads. The 917,097-coin address matched both | Met |
| Peak temperature | 87 C during verify (median 82 C while the node walked the set); 68 C during the bench; no sample at 88 C, where the throttle starts | No target |
| Dogecoin health | healthy in 43 of 43 verify samples and 12 of 12 bench samples | Met |
| Peak memory and CPU | during verify the node was at its 1 CPU cap with up to 2.9 GiB of its 4 GiB, and the dashboard used 361 MiB of 1 GiB at its 0.5 CPU cap. During the bench the dashboard rose from 462 to 611 MiB, and it still used 637 MiB a minute later | No target |
| New block to balance | not measured | |

## Wallets

The bench used sixteen wallets. Eight are the busiest output addresses in the 150 blocks 6,475,049 to 6,475,198, read from the node itself: the busiest P2SH, the five busiest P2PKH and the second and third busiest P2SH. They hold 57 to 917,097 coins. The other eight are ranks 1 to 6 and 8 of Blockchair's Dogecoin rich list, read on 2026-11-27 at height 6,475,194 (rank 8 is the Dogeparty burn address), and the Dogecoin Foundation address that the dashboard ships as its example Dogecoin wallet. They hold 10 to 22,746 coins. The node's `validateaddress` accepted all sixteen before the run.

## Public explorers

dogechain.com (`/api/v1/address/balance`) and BlockCypher (`api.blockcypher.com/v1/doge/main/addrs/<address>/balance`) were read at 16:19:16 and 16:21:54, before and after the bench at 6,475,200. Neither gives a coin count, so only sats are compared. Blockchair blocked further automated requests after the rich-list read, and Trezor's Blockbook is behind a Cloudflare check.

## Notes

- **The bench ran without the command.** 1.42.2's `utxo-index bench` refuses `--chain dogecoin`, but the dashboard serves a Dogecoin bench request: wallets are read through `validateaddress`, as Bitcoin Cash's are. The run wrote the same request the command writes (`/state/dogecoin-utxo-bench.request`, `scan` false) into the dashboard container as uid 1000 and read `dogecoin-utxo-bench.result`. From the next release the command accepts `--chain dogecoin` without `--scan`.
- **P2PK outputs are not counted for an address.** 38 of the 150 coinbase transactions in the sample paid a P2PK output (about 380,000 DOGE). A wallet given as `addr(<address>)` is looked up under its P2PKH script only, so those coins are left out, while explorers count them under the address: the coinbase address of block 6,475,198 holds 10,004.37501155 DOGE on BlockCypher and 0 in the index. The index holds the coins under their own script, and `verify` counts them. Tracked in #494.
- **The first seed attempt was killed for memory.** The dashboard (1 GiB memory limit) was killed at 05:17:55 while it imported the chainstate copy, which the node had written on shutdown right after its initial sync. The automatic retry at 11:17 finished. A fix for the import's memory use is in progress.
- **The example wallet showed a stale balance.** Before these runs the dashboard showed the Dogecoin Foundation example as "Cached balance; a fresh scan is unavailable", scanned at 11:45:30 when the seed finished, although the index was ready. The bench returned the same 118,295.18962480 DOGE as both explorers. The cause was in the watch code, not the index; see the CHANGELOG.
- **Memory after the bench.** The dashboard's memory rose by about 150 MiB during the bench, which looked up the 917,097-coin address 41 times, and had not come down a minute later. The samples do not show whether that is reclaimable cache.
