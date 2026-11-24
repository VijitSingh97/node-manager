# Bitcoin Cash balance index on mainnet (mini-1)

Date: 2026-11-24, UTC. The first mainnet run of the Bitcoin Cash balance index. It covers the automatic seed from a copy of the node's chainstate, `utxo-index verify`, the index across a release upgrade, and why the lookup benchmark cannot run on Bitcoin Cash yet.

- **Host:** mini-1, the reference node (4 cores, 7.6 GiB RAM, turbo off), from the APT feed: node-manager 1.40.3 for the seed, 1.41.0 for the checks.
- **Bitcoin Cash Node:** 29.2.0, pruned (10.2 GB on disk), capped at 1 CPU and 4 GiB, 8 peers.
- **Dashboard:** 0.5 CPU and 1 GiB.
- **Chain:** the initial sync finished at 978,726; the checks ran at 978,795. The UTXO set held 58,783,710 coins.
- **Other chains:** Monero, Tari and both wallet containers were running for the seed and for the checks; the upgrade between them restarted all of them except the Tari base node. Bitcoin, Litecoin and Dogecoin were stopped. Nothing was paused for the runs.
- **Sampling:** the seed ran on its own before the checks began and was not sampled; its times come from the dashboard log, the node's log and the host journal. The dashboard and node container logs were read before the upgrade recreated those containers, which clears their logs. The verify was sampled about every 7 s: the hottest thermal zone, Bitcoin Cash's health, and CPU and memory of the Bitcoin Cash and dashboard containers (12 samples).

**Runs**

| Run | Time (Z) | Command |
| --- | --- | --- |
| Seed | 2026-11-24 17:42:57 to 17:51:47 | automatic, once the node had synced |
| Upgrade | 19:14 to 19:18 | APT upgrade from 1.40.3 to 1.41.0 |
| Verify | 19:20:13 to 19:21:40 | `sudo node-manager utxo-index verify --chain bitcoincash -y` |
| Bench probe | 19:22, twice | `utxo-index bench … --chain bitcoincash --repeat 5`, one recent output address and the `pkh` test-vector descriptor |

## Results

Times are in seconds unless marked.

| Target | Measured | Met |
| --- | --- | --- |
| Seed | 530 s from `seeding` to `ready`, outcome ok. The import started 2 s in; the first 10% took 159 s and each later 10% took 23 to 57 s. No heat pause logged and no node-down alert. The index went ready at once | No target |
| Node stop for the copy | Bitcoin Cash Node shut down cleanly at 17:42:46 to 17:42:49 and started again at 17:44:56; its block index loaded in 9.5 s. About 2 min 20 s down, 120 s of it the node waiting for a Tor onion name (below) | No target |
| Index size | 5.23 GB (5,225,602,531 bytes, read after the checks) for 58.8 million coins, about 89 bytes per coin. Like Litecoin (84 bytes per coin), Bitcoin Cash stores a reverse key for each coin ([reverse key](litecoin-reverse-key.md)) | No target |
| Index and node agree (`verify`) | PASS at 978,795: 58,783,710 coins and 20,120,959.95246403 BCH in both. The index count took 82.5 s, Bitcoin Cash Node's `gettxoutsetinfo none` 60.1 s, the command 87 s | Met |
| Restart to ready | After the 1.41.0 upgrade recreated the dashboard, the index was ready 20 s after it started, with no re-seed | No target |
| Add a wallet, batch lookups, scan baseline | Not run: the bench refuses every Bitcoin Cash wallet (below) | Not measured |
| Peak temperature | 79 C during verify (median 75 C while the node walked the set); no sample at 85 C or above | No target |
| Bitcoin Cash health | healthy in 12 of 12 samples | Met |
| Peak memory and CPU | the node at its 1 CPU cap during the walk, 3.6 GiB of its 4 GiB at the peak and still there after the walk, so most of it is likely page cache from reading the coin set (the samples do not split it out); dashboard 200 MiB of 1 GiB at its 0.5 CPU cap | No target |
| New block to balance | not measured | |

## Notes

- **The bench cannot read Bitcoin Cash wallets yet.** `utxo-index bench --chain bitcoincash` refuses each wallet with `wallet N is not accepted as a public watch key` and exits 1, for an `addr(...)` key and a `pkh` descriptor alike. The bench checks each wallet with the node's `getdescriptorinfo` and derives its scripts with `deriveaddresses`, and Bitcoin Cash Node 29.2.0 answers both with `Method not found`. The Bitcoin Cash watch card works without them (`validateaddress` for each address, `scantxoutset` with `addr(...)`), so a bench built on those calls could measure Bitcoin Cash lookups. Until then, `verify` is the check: it compares the coin count and total amount of the whole set.
- **Scan without the index.** While the seed was importing, the dashboard's own Bitcoin Cash watch scan, one `scantxoutset` over the owner's watch list, took 48.7 s. That list is not recorded here, and it began about 2 s after the node restarted, while the seed was importing, so take it only as the order of a scan on this host.
- **The node waited 2 minutes for Tor at the copy's restart.** Its container started at 17:42:56, logged `Tor has not published this node's onion address; not advertising it.` at 17:44:56, and only then started the node: the entrypoint waits up to 120 s for that name. The Tor container had run since the 1.40.3 upgrade at 02:48, when Bitcoin Cash was not yet active, with no onion service for it, although the rendered configuration named Bitcoin Cash. Activating a node does not recreate Tor. Without the wait the node would have been down about 20 s (10 s to stop and copy, 9.5 s to load the block index). The 1.41.0 upgrade recreated Tor, and Bitcoin Cash now has its onion name.
- **Supply check.** The subsidy schedule had issued 20,121,237.5 BCH by height 978,795, 277.5 BCH more than the set holds. That is the expected size for coins no node keeps: the genesis coinbase, unclaimed block rewards and burned coins.
