# Litecoin balance index on mainnet (mini-1)

Date: 2026-11-23 (seed) and 2026-11-24 (checks), UTC. The first mainnet run of the Litecoin balance index. It covers the automatic seed, `utxo-index verify`, the lookup benchmark with and without `--scan`, and a check of well-known public addresses against two block explorers.

- **Host:** mini-1, the reference node (4 cores, 7.6 GiB RAM, turbo off), running node-manager 1.40.2 from the APT feed.
- **Litecoin Core:** 0.21.5.8, pruned (10.4 GB on disk), capped at 1 CPU and 4 GiB, 10 peers.
- **Dashboard:** under the `litecoin.utxo_index` overlay, so 0.5 CPU and 1 GiB.
- **Chain:** synced; heights 3,218,340 to 3,218,349 during the checks. The UTXO set held 53,822,839 coins.
- **Other chains:** Monero, Tari and both wallet containers running throughout. Bitcoin, Dogecoin and Bitcoin Cash were stopped. Nothing was paused for the runs.
- **Wallets:** public data only, now in [`litecoin-bench-wallets.json`](litecoin-bench-wallets.json). No owner wallet was used, and no address, key or balance appears here.
- **Sampling:** about every 7 s during each check: the hottest thermal zone, Litecoin's health, and CPU and memory of the Litecoin and dashboard containers (131 samples).

**Runs**

| Run | Time (Z) | Command |
| --- | --- | --- |
| Seed | 2026-11-23 11:50:09 to 11:55:56 | automatic, once the node had synced |
| Verify | 00:43:25 to 00:44:41 | `sudo node-manager utxo-index verify --chain litecoin -y` |
| Plain | 00:47:23 | `utxo-index bench … --chain litecoin --repeat 20`, five busiest addresses and three descriptors |
| Scan | 00:47:52 to 00:55:21 | the same wallets with `--scan -y` |
| Known wallets | 00:55:56 to 01:01:58 | eight rich-list and proof-of-reserves addresses with `--scan -y`, then two explorers |

## Results

Times are in seconds unless marked.

| Target | Measured | Met |
| --- | --- | --- |
| Seed | 346 s from `seeding` to `ready`, outcome ok; the import started 41 s in. No heat pause and no node restart. The index went ready 0.3 s after the seed | No target |
| Index size | 4.52 GB for 53.8 million coins, about 84 bytes per coin. Bitcoin's index is about 49 bytes per coin (derived: the 7.5 GiB final index, which is 8.05 GB, over the 163.3 million coins in [ADR 0001](../adr/0001-bitcoin-utxo-index.md), counted at an earlier height; the Bitcoin seed run did not record its coin count); Litecoin also stores a reverse key for each coin ([reverse key](litecoin-reverse-key.md)) | No target |
| Index and node agree (`verify`) | PASS at 3,218,340: 53,822,839 coins and the same total amount in both. The index count took 68.6 s, Litecoin's `gettxoutsetinfo none` 44.5 s, the command 76 s | Met |
| Add an indexed wallet under 1 s (descriptor, cold) | `derive` + `first`: wpkh 0.52, sh-wpkh 0.54, pkh 0.50 on the plain run; 0.37 to 0.40 on the scan run | Met |
| Add an indexed single address | `first` 0.1 to 12 ms, p50 under 0.1 to 8 ms; the largest address, 6,526 coins, p50 5 to 8 ms | Met |
| Add a wallet by scan (each wallet alone) | 38.7 to 58.5 per wallet, one `scantxoutset` each | Baseline: against the index p50, 658 to 1,664 times faster for a descriptor and 4,900 to over a million times for a single address |
| Batch of 1, 5 and 8 wallets, p50 | 0.004, 0.006 to 0.008, and 0.30 for the busiest and descriptor wallets (6,005 scripts); 0.0001 for the eight known addresses | No target |
| Batch scan of the same wallets | 59.0 for the busiest and descriptor wallets, 41.9 for the eight known addresses | Baseline: about 50 times slower than the index with derivation (1.19 s) and 200 times slower than the lookup alone; about 5,000 times slower for the known addresses |
| Scan and index agree on every wallet | 16 of 16 identical in coins and sats, at blocks 3,218,344 to 3,218,349 | Met |
| Public explorers agree | 8 of 8 known addresses identical in confirmed sats and coins on two explorers | Met |
| Descriptor shapes are indexed | 0 fallbacks; every batch's `scripts` equals `expected`. `tr` is refused (below) | Met for wpkh, sh-wpkh and pkh |
| Peak temperature | 76 C during verify, 81 and 82 C during the scan runs (medians 75 and 78 C); no sample at 85 C or above | No target |
| Litecoin health | healthy in 131 of 131 samples | Met |
| Peak memory and CPU | litecoind at its 1 CPU cap during walks and scans, 3,895 MiB of its 4 GiB at the peak, mostly page cache from reading the coin set; dashboard 237 MiB of 1 GiB at its 0.5 CPU cap | No target |
| New block to balance | not measured | |
| Restart to ready | not measured | |

## Wallets

The plain and scan runs used eight wallets: the busiest output address of each script type (P2WPKH, P2PKH, P2SH and P2WSH, plus the second busiest P2PKH) in the 300 blocks before 3,218,340, read from the node itself, and the `wpkh`, `sh-wpkh` and `pkh` descriptors over the BIP32 test vector 1 key, about 2,000 scripts each. They hold 9 to 6,526 coins; the descriptors hold none.

The known-wallet run used eight well-known addresses:

- **Rich list:** ranks 1, 2, 3, 4, 7 and 9 of the [CoinCarp Litecoin rich list](https://www.coincarp.com/currencies/litecoin/richlist/), read on 2026-11-24. They hold 1 to 106 coins. Two of them had been nearly emptied since the list's figures were taken, and the list's balances were out of date for most.
- **Proof of reserves:** the two LTC addresses in [CoinSwitch's fifth proof of reserves](https://coinswitch.co/building-blocks/coinswitch-publishes-fifth-edition-of-proof-of-reserves/) (cut-off 2025-05-06). Both are empty now, which checks that an empty address comes back as zero.

Each address was checked first with Litecoin Core's `validateaddress`, so none was mistyped.

## Public explorers

The litecoinspace.org API (confirmed funded minus spent outputs) and Atomic Wallet's Blockbook (`litecoin.atomicwallet.io`) were read at tip 3,218,347 before the run and 3,218,349 after it. Each address's transaction count was the same both times, so nothing moved during the run. For all eight, both explorers, Litecoin's own `scantxoutset` and the index gave the same confirmed amount in litoshis and the same coin count. Blockchair refused automated requests; bitinfocharts and CryptoID are behind a bot check, so neither was used.

## Notes

- **`tr` is refused, not a fallback.** Litecoin has no Taproot, and a bench file with a `tr` descriptor is refused with `wallet 8 is not accepted as a public watch key`; the number is the wallet's position, counting from 1. Leave `tr` out of Litecoin files.
- **One scan is about 40 s here.** Litecoin's set is about a third of the 163.3 million Bitcoin coins in ADR 0001 (53.8 million), and the node has 1 CPU against Bitcoin's 0.5, so a single-address scan takes 39 to 58 s against Bitcoin's 164.
- **Heat stayed under the seed pause.** With Monero and Tari running, the walks and scans peaked at 82 C, under the 85 C seed pause and the 88 C throttle. The Litecoin checks needed no Tari pause.
