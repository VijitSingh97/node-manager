# Bitcoin Cash balance index on mainnet (mini-1)

Date: 2026-11-24 and 2026-11-25, UTC. The first mainnet runs of the Bitcoin Cash balance index. They cover the automatic seed from a copy of the node's chainstate, `utxo-index verify`, the index across three release upgrades, and the lookup benchmark with and without `--scan`, with well-known public addresses checked against block explorers.

- **Host:** mini-1, the reference node (4 cores, 7.6 GiB RAM, turbo off), from the APT feed: node-manager 1.40.3 for the seed, 1.41.0 for `verify`, 1.42.0 for the first bench runs and 1.42.1 for the bench figures below.
- **Bitcoin Cash Node:** 29.2.0, pruned (10.2 GB on disk), capped at 1 CPU and 4 GiB, 8 peers.
- **Dashboard:** 0.5 CPU and 1 GiB.
- **Chain:** the initial sync finished at 978,726; `verify` ran at 978,795, the 1.42.0 bench at 978,837 and the 1.42.1 bench at 978,850 to 978,851. The UTXO set held 58,783,710 coins at 978,795.
- **Other chains:** Monero, Tari and both wallet containers were running for every run; each upgrade restarted all of them except the Tari base node. Bitcoin, Litecoin and Dogecoin were stopped. Nothing was paused for the runs.
- **Wallets:** public data only, now in [`bitcoincash-bench-wallets.json`](bitcoincash-bench-wallets.json). No owner wallet was used.
- **Sampling:** the seed ran on its own before the checks began and was not sampled; its times come from the dashboard log, the node's log and the host journal. The dashboard and node container logs were read before the upgrade recreated those containers, which clears their logs. The verify and the bench runs were sampled about every 7 s: the hottest thermal zone, Bitcoin Cash's health, and CPU and memory of the Bitcoin Cash and dashboard containers (12 samples for verify, 142 for the bench runs).

**Runs**

| Run | Time (Z) | Command |
| --- | --- | --- |
| Seed | 2026-11-24 17:42:57 to 17:51:47 | automatic, once the node had synced |
| Upgrade | 19:14 to 19:18 | APT upgrade from 1.40.3 to 1.41.0 |
| Verify | 19:20:13 to 19:21:40 | `sudo node-manager utxo-index verify --chain bitcoincash -y` |
| Bench probe | 19:22, twice | `utxo-index bench … --chain bitcoincash --repeat 5`, one recent output address and the `pkh` test-vector descriptor |
| Upgrade | 2026-11-25 00:20 to 00:26 | APT upgrade to 1.42.0 |
| Runs on 1.42.0 | 00:26:57 to 00:30:08 | the same three runs as on 1.42.1; the index lookups worked and both `--scan` runs were refused (see Notes) |
| Upgrade | 01:58 to 02:04 | APT upgrade to 1.42.1 |
| Plain | 02:05:11 to 02:05:17 | `utxo-index bench … --chain bitcoincash --repeat 20`, eight recent-block wallets |
| Scan | 02:05:47 to 02:13:32 | the eight recent-block wallets with `--scan -y --repeat 20` |
| Known wallets | 02:14:02 to 02:20:50 | eight rich-list addresses with `--scan -y --repeat 20`, then two explorers |

## Results

Times are in seconds unless marked.

| Target | Measured | Met |
| --- | --- | --- |
| Seed | 530 s from `seeding` to `ready`, outcome ok. The import started 2 s in; the first 10% took 159 s and each later 10% took 23 to 57 s. No heat pause logged and no node-down alert. The index went ready at once | No target |
| Node stop for the copy | Bitcoin Cash Node shut down cleanly at 17:42:46 to 17:42:49 and started again at 17:44:56; its block index loaded in 9.5 s. About 2 min 20 s down, 120 s of it the node waiting for a Tor onion name (below) | No target |
| Index size | 5.23 GB (5,225,602,531 bytes, read after the checks) for 58.8 million coins, about 89 bytes per coin. Like Litecoin (84 bytes per coin), Bitcoin Cash stores a reverse key for each coin ([reverse key](litecoin-reverse-key.md)) | No target |
| Index and node agree (`verify`) | PASS at 978,795: 58,783,710 coins and 20,120,959.95246403 BCH in both. The index count took 82.5 s, Bitcoin Cash Node's `gettxoutsetinfo none` 60.1 s, the command 87 s | Met |
| Restart to ready | After the 1.41.0, 1.42.0 and 1.42.1 upgrades recreated the dashboard, the index was ready 20 s, about 11 s and about 11 s after it started, with no re-seed | No target |
| Add an indexed single address | `first` 8.7 ms or less. p50 0.2 ms or less for every address but the busiest, which holds 6,667 coins: p50 2.3 ms on the plain run and 7.4 ms on the scan run, p99 67 to 70 ms | Met |
| Add a wallet by scan (each wallet alone) | 44.0 to 81.9 per wallet, one `scantxoutset` each; 15 of the 16 took 44 to 56 | Baseline: against the index p50, about 7,600 times faster for the busiest address and over 290,000 times for the rest |
| Batch of 1, 5 and 8 wallets, p50 | 0.0029, 0.0033 and 0.0049 for the recent-block wallets (6,667 to 7,235 coins); 0.0001 to 0.0003 for the rich-list addresses (395 to 577 coins) | No target |
| Batch scan of the same wallets | 48.1 for the recent-block wallets, 45.7 for the rich-list addresses | Baseline: roughly 10,000 and 150,000 times slower than the index batch |
| Scan and index agree on every wallet | 16 of 16 identical in coins and sats, at blocks 978,850 to 978,851 | Met |
| Public explorers agree | 8 of 8 rich-list addresses identical in confirmed sats and coin count on Bitcore, and in sats on Blockchair's rich list | Met |
| Peak temperature | 79 C during verify (median 75 C while the node walked the set); 78 and 80 C during the two 1.42.1 scan runs (medians 73 and 76 C); no sample at 85 C or above | No target |
| Bitcoin Cash health | healthy in 12 of 12 verify samples and 142 of 142 bench samples | Met |
| Peak memory and CPU | the node at its 1 CPU cap during the walk, 3.6 GiB of its 4 GiB at the peak and still there after the walk, so most of it is likely page cache from reading the coin set (the samples do not split it out); dashboard 200 MiB of 1 GiB at its 0.5 CPU cap. During the scans the node was again at its 1 CPU cap with up to 3.6 GiB, and the dashboard used at most 42% CPU and 51 MiB | No target |
| New block to balance | not measured | |

## Wallets

The plain and scan runs used eight wallets: the three busiest P2PKH output addresses, the two busiest P2SH and the two busiest P2SH32 (32-byte script hash) addresses in the 300 blocks 978,535 to 978,834, read from the node itself, plus the second busiest P2PKH again without its `bitcoincash:` prefix. They hold 0 to 6,667 coins.

The known-wallet run used ranks 1 to 6, 9 and 25 of Blockchair's Bitcoin Cash rich list, read on 2026-11-24 at height 978,835: five P2PKH and three P2SH addresses holding 4 to 395 coins. Rank 25 is written in uppercase. The bench checks every address with the node's `validateaddress` before it looks anything up.

## Public explorers

Bitcore's API (`api.bitcore.io`, balance and unspent outputs) was read at tip 978,837 before the 1.42.0 runs and at 978,852 after the 1.42.1 runs. Every balance and coin count was the same both times, so nothing moved in between. For all eight addresses, Bitcore, Bitcoin Cash Node's own `scantxoutset` and the index gave the same confirmed sats and the same coin count, and the balances on Blockchair's rich list matched to the sat. Trezor's Blockbook is behind a Cloudflare check and Blockchair refused further automated requests, so coin counts come from Bitcore alone.

A third API, `rest.bch.actorforth.org`, showed rank 6 holding 1,000 sats less. That address holds a 1,000-sat output carrying a fungible CashToken; the node, the index and Bitcore count it, and that API leaves token-carrying outputs out of its balance.

## Notes

- **The bench needed two fixes for Bitcoin Cash.** In 1.41.0 `utxo-index bench --chain bitcoincash` refused each wallet with `wallet N is not accepted as a public watch key` and exited 1, for an `addr(...)` key and a `pkh` descriptor alike: it checked wallets with the node's `getdescriptorinfo` and `deriveaddresses`, and Bitcoin Cash Node 29.2.0 answers both with `Method not found`. 1.42.0 reads `addr(...)` wallets through `validateaddress`, as the Bitcoin Cash watch card does, and its index-only runs worked. Both of its `--scan` runs were refused with `the batched scan failed or timed out` after one walk of the set (50 s and 66 s): the node's `scantxoutset` reports how many coins it searched as `searched_items`, not `txouts`, and gives no `desc` for each coin, and the bench's validator required both. 1.42.1 accepts that reply, and its runs are the scan figures above. The CI test had used a fake node modelled on Bitcoin Core's reply; running these checks against real regtest nodes is tracked in #479.
- **Address spellings.** The node turns an unprefixed (`q…`) and an uppercase (`BITCOINCASH:P…`) address into the canonical `bitcoincash:` form, and the bench looked both up under the canonical script: each returned the same coins and sats as the canonical spelling. In a batch the repeated address counts once, so the eight-wallet batch shows 7 scripts against 8 expected.
- **One scan is about 45 s here.** Bitcoin Cash's set (58.8 million coins) is a little larger than Litecoin's (53.8 million), and both nodes have 1 CPU, so a single-address scan took 44 to 56 s against Litecoin's 39 to 58 s. One recent-block scan took 81.9 s; the samples do not show why.
- **Scan without the index.** While the seed was importing, the dashboard's own Bitcoin Cash watch scan, one `scantxoutset` over the owner's watch list, took 48.7 s. That list is not recorded here, and it began about 2 s after the node restarted, while the seed was importing, so take it only as the order of a scan on this host.
- **The node waited 2 minutes for Tor at the copy's restart.** Its container started at 17:42:56, logged `Tor has not published this node's onion address; not advertising it.` at 17:44:56, and only then started the node: the entrypoint waits up to 120 s for that name. The Tor container had run since the 1.40.3 upgrade at 02:48, when Bitcoin Cash was not yet active, with no onion service for it, although the rendered configuration named Bitcoin Cash. Activating a node does not recreate Tor. Without the wait the node would have been down about 20 s (10 s to stop and copy, 9.5 s to load the block index). The 1.41.0 upgrade recreated Tor, and Bitcoin Cash now has its onion name.
- **Supply check.** The subsidy schedule had issued 20,121,237.5 BCH by height 978,795, 277.5 BCH more than the set holds. That is the expected size for coins no node keeps: the genesis coinbase, unclaimed block rewards and burned coins.
