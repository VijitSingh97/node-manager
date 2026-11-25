# Lookup benchmark on mini-1

How to measure balance lookups with and without a balance index on the reference host: Bitcoin for the targets in #236, and every indexed chain for #367. The harness uses public test wallets only, needs no dashboard password and does not change the watched-wallet roster, the scan cache or the script registry.

## What it measures

`utxo-index bench` hands a wallet list to the dashboard through the state volume, the same way `utxo-seed` does. `--chain` picks the index: `bitcoin` (the default), `litecoin` or `bitcoincash`. Each chain has its own request and result files (`utxo-bench.request`, `litecoin-utxo-bench.request`, `bitcoincash-utxo-bench.request`), and a request that names another chain is refused. The dashboard, with that chain's index ready and level with its node's tip:

- derives each wallet's scripts the way a watch does, without saving them, and times it. On Litecoin the node also returns each derived address's script through `validateaddress`, in batches of 20, because the dashboard decodes only Bitcoin addresses; that time is part of `derive`. Bitcoin Cash Node has no `getdescriptorinfo` or `deriveaddresses`, so on Bitcoin Cash each wallet is one address, its script comes from `validateaddress`, and a descriptor is refused;
- times the index lookup of each wallet once cold, then `--repeat` times in all (default 20): first, p50, p99 and max seconds, with the coin count;
- times one lookup over the combined scripts of the first 1, 5 and 20 wallets, or as many as the file has;
- on Bitcoin only, times `/api/watch`'s cached read (`ListWithChain`) `--repeat` times over the wallets in the owner's watch roster, not the bench wallets, and records the roster size;
- with `--scan`, holds the chain's watch scanner and measures the time without the index: one `scantxoutset` over every indexed wallet, then one over each indexed wallet alone. A wallet that fell back is not scanned, and the table shows it as `fallback`. Right before each wallet's own scan it looks the wallet up in the index again, and compares the coin count and total sats when the scan and that lookup answered at the same block hash; otherwise `match` is `n/a` because a block arrived in between. The lookup does not wait for the index to show `ready`: a node slowed by the previous scan can briefly flip it to `catching_up` without changing the index, and the block hash check is what makes the comparison valid. A `false` adds a `failures` line, and the command prints it as `FAILED:` and exits non-zero. A run that compared no wallet at all is refused, so it cannot pass unchecked. The scan totals are not capped at Bitcoin's 21 million coins, so a large Litecoin wallet is counted as it is.

The result goes to `utxo-bench.result` in the state volume (mode 600). The command prints it as tables and saves a copy as `utxo-bench-<UTC time>.json` in the state directory, also mode 600. The dashboard log gets two lines with counts and durations only.

## Prerequisites

- mini-1 runs a release that includes `utxo-index bench`, with `bitcoin.utxo_index` (or `litecoin.utxo_index` or `bitcoincash.utxo_index` for that `--chain`) true and applied.
- The index is `ready` and `blocks_behind` is 0 (the card, or `/api/utxo-index`, with `?chain=litecoin` or `?chain=bitcoincash` for those chains). The run refuses otherwise and says why.
- `jq` is installed. Run as root on APT (`sudo node-manager ...`), as the install user otherwise.
- For the block-latency step, `scripts/measure-block-latency.sh` is copied to the host too, and the user can run `docker`.
- The wallet file is on the host: copy `docs/measurements/bench-wallets.json` from the repository. It holds public data only: nine well-known addresses (below) and descriptors over the BIP32 test vector 1 master xpub as `wpkh`, `sh(wpkh)`, `tr` and `pkh`, each about 2,000 scripts. Add no owner wallet to it without approval.

For Litecoin, write each address as `addr(<address>)`: a bare Litecoin address is not accepted as a watch key. The `wpkh`, `sh-wpkh` and `pkh` vector rows work unchanged, since Litecoin Core uses Bitcoin's `xpub` version bytes. Leave `tr` out: Litecoin has no Taproot, and a file with it is refused with `wallet N is not accepted as a public watch key`. [`litecoin-bench-wallets.json`](litecoin-bench-wallets.json) holds public Litecoin wallets: well-known rich-list and proof-of-reserves addresses, the busiest output address of each script type in a recent block range, and those three descriptors. The first run is in [Litecoin balance index on mainnet](litecoin-index-mainnet.md). For Bitcoin Cash, write each address as `addr(<address>)` too; descriptors such as the vector rows are refused, since the node cannot expand them. [`bitcoincash-bench-wallets.json`](bitcoincash-bench-wallets.json) holds public Bitcoin Cash wallets: rich-list addresses and the busiest output addresses of each script type in a recent block range, two of them spelled without the prefix or in uppercase. The first run is in [Bitcoin Cash balance index on mainnet](bitcoincash-index-mainnet.md).

The nine addresses cover every script type the index serves except Taproot, which the `tr` vector covers. Their sizes range from a few coins to thousands, and they compare one-script lookups of very different sizes. Each address was checked with a local checksum decoder and with bitcoind 32.1's `validateaddress` on 2026-11-16, and each label is stated by at least two independent sources. Most labels come from public rich lists (Arkham, Bitinfocharts) and are not confirmed by the owner. Sizes were not looked up.

| Name in the file | Type | Sources |
| --- | --- | --- |
| Genesis block coinbase address | P2PKH | [Bitcoin Wiki](https://en.bitcoin.it/wiki/Genesis_block), [Decrypt](https://decrypt.co/resources/the-bitcoin-genesis-block-how-it-all-started) |
| Binance cold wallet 1 (rich-list label); also the dashboard's public example | P2SH | [CoinCodex](https://coincodex.com/article/31694/bitcoin-rich-list), [Bitcoin.com News](https://news.bitcoin.com/inside-the-top-12-bitcoin-addresses-who-holds-1-35-million-btc/) |
| Binance cold wallet 2 (rich-list label) | P2SH | CoinCodex, Bitcoin.com News (as above) |
| Robinhood cold wallet (rich-list label) | P2WPKH | CoinCodex, Bitcoin.com News |
| Bitfinex cold wallet (rich-list label) | P2WSH | CoinCodex, Bitcoin.com News |
| Tether cold wallet (rich-list label) | P2WSH | CoinCodex, Bitcoin.com News |
| Silk Road funds address (2020 forfeiture) | P2PKH | [The Register](https://www.theregister.com/2020/11/05/us_bitcoin_1bn), [Security Affairs](https://securityaffairs.com/110498/uncategorized/silk-road-funds-transferred.html) |
| Mt. Gox hack wallet (rich-list label) | P2PKH | CoinCodex, Bitcoin.com News |
| Bitfinex 2016 hack funds held by the FBI (rich-list label) | P2WPKH | CoinCodex, Bitcoin.com News |

## Runs

An indexed run is light: derivation is a few batched RPC calls per wallet and the lookups are local reads.

```
sudo node-manager utxo-index bench bench-wallets.json
sudo node-manager utxo-index bench bench-wallets.json --repeat 50
sudo node-manager utxo-index bench litecoin-bench-wallets.json --chain litecoin
sudo node-manager utxo-index bench bitcoincash-bench-wallets.json --chain bitcoincash
```

A request older than two minutes is refused by the dashboard. A run is stopped after 8 minutes. With `--scan` the limit is 8 minutes plus 5 per scan, and never less than 40. A `--scan` run needs an owner-approved window and asks for confirmation, or `-y` on a non-interactive shell. `scantxoutset` walks the whole UTXO set: minutes of CPU on a 0.5-CPU node, with the node's RPC slow meanwhile, and the chain's watch scanner is held until it ends. It scans once for the whole file and once per wallet. On mini-1 one Bitcoin scan took 164 to 281 s, so the 13-wallet file takes about 40 to 65 minutes, and a file with one address and one descriptor about 10 to 15. Before it, follow the cautions in [Operations](../operations.md#seeding-the-bitcoin-balance-index): pause Tari, check the temperature, and confirm no seed or other scan is running. The command refuses when a scan or a refresh is running.

```
sudo node-manager utxo-index bench bench-wallets.json --scan -y --repeat 20
```

With `--scan` the output ends with one row per wallet: the block height of its scan, the scan seconds, the index p50, how many times faster the index is, and both coin and sats totals with `match`.

Ctrl-C removes a request the dashboard has not taken yet; one it has taken finishes and leaves its result in the state volume.

## Mapping results to the #236 targets

| Target | Where to read it |
| --- | --- |
| Add an indexed wallet under 1 s | Per wallet, the rows named `Vector wpkh`, `Vector sh-wpkh`, `Vector tr` and `Vector pkh` (about 2,000 scripts each): add `derive` and `first` yourself. `first` is the first lookup of that wallet, not a true cold read: the OS page cache and earlier wallets have warmed the index. The nine address rows are one script each and say nothing about derivation; the `Genesis block coinbase address` row is the many-coins case. |
| Add a wallet without the index | `--scan`: each wallet's `scan` seconds, and `faster` against its index p50. |
| Cached read under 100 ms | "Cached read": p50 and p99. It reads the owner's roster, so it counts only when `roster wallets` is the size of interest (1, 5 or 20). With an empty roster the figure is near zero and means nothing. |
| Batched registration of 1, 5 and 20 | "Batched lookups": the p50 for each size, plus the sum of `derive` for those wallets. With `--scan`, the batch scan against that sum. |
| New block to balance | `scripts/measure-block-latency.sh`, below. |
| Restart to ready | Below. |
| Resource use, healthcheck during follow | Host sampling, below. |
| Index and scan agree | With `--scan`: `match` true for every wallet. `n/a` marks a wallet whose shape the index does not support (it falls back to the scan), or one whose scan ran at another block than its lookup; repeat the run in the second case. `false` is an index bug: the command prints `FAILED:` and exits non-zero. |
| Descriptor shapes are indexed | Per wallet: no `fallback`. The command prints a warning with the count when any wallet fell back, and the batches table shows `scripts` against `expected` and the `fallback` count. A fallback of a Vector row means the shape is not indexed on that node; its batch figures are then too low. |

New block to balance, over a window of live blocks:

```
scripts/measure-block-latency.sh --since 2026-11-16T06:00:00Z
```

It reads bitcoind's `UpdateTip` lines and the dashboard's `watch index: outcome=ok` and `watch block update:` lines, matches each block to the first wallet refresh at that height or higher, and prints counts, median, p90 and max for refreshes served by the index, those that fell back to the block update, and all of them. Medians and p90 are nearest-rank. Keep the window to blocks after the node is synced. Dashboard logs keep 3 x 10 MB, so use a window that fits them: the script warns when the logs start after `--since`, and it exits non-zero when it parses no blocks or no refresh lines.

Restart to ready: restart the dashboard (`sudo node-manager restart dashboard`) and take the time from the container start to the `utxo index: state=ready` log line. For the full-host figure, restart bitcoind as well, with the owner's approval.

```
docker logs --timestamps "$(docker ps -q --filter label=com.docker.compose.service=dashboard)" 2>&1 | grep -E 'utxo index: state=(catching_up|ready)'
docker inspect --format '{{.State.StartedAt}}' "$(docker ps -q --filter label=com.docker.compose.service=dashboard)"
```

## Host sampling

Run this in a second terminal during a bench, a seed, or an hour of follow. It prints no wallet data.

```
bitcoin=$(docker ps -q --filter label=com.docker.compose.service=bitcoin)
while true; do
  temp=$(sort -n /sys/class/thermal/thermal_zone*/temp 2>/dev/null | tail -n 1)
  health=$(docker inspect --format '{{.State.Health.Status}}' "$bitcoin")
  echo "$(date -u +%FT%TZ) temp_milliC=${temp:-?} bitcoin_health=$health"
  docker stats --no-stream --format '{{.Name}} cpu={{.CPUPerc}} mem={{.MemUsage}}' | grep -E 'bitcoin|dashboard'
  sleep 5
done | tee sample.log
```

The index size is `docker system df -v` for the `utxo_index` volume. Journal bytes per block is that size at two heights divided by the blocks between them.

## Results

Fill in one row per target from a run. Mark each met or not met against the number. The first run, on mini-1 with 1.36.0, is in [Balance index lookups on mainnet](lookup-bench-mainnet.md), the first Litecoin run in [Litecoin balance index on mainnet](litecoin-index-mainnet.md), and the first Bitcoin Cash run in [Bitcoin Cash balance index on mainnet](bitcoincash-index-mainnet.md). Then add a row to [the performance history](performance-history.md).

| Target | Measured | Met |
| --- | --- | --- |
| Add an indexed wallet under 1 s (descriptor, cold) | | |
| Add an indexed wallet (genesis address, cold) | | |
| Add a wallet by scan (each wallet alone) | | |
| Cached read under 100 ms, p50 | | |
| Cached read, p99 | | |
| Batch of 1 wallet, p50 | | |
| Batch of 5 wallets, p50 | | |
| Batch of 20 wallets, p50 (or all provided) | | |
| Batch scan of the same wallets | | |
| Scan and index agree on every wallet | | |
| New block to balance, median and p90 | | |
| Restart to ready | | |
| Peak dashboard memory and CPU while following | | |
| Peak temperature | | |
| bitcoind health during follow | | |

Record the host, release, chain height, other chains running and the date, as the other measurements here do. Keep addresses, keys and balances out of the write-up.

## CI test per indexed chain

`TestBenchIndexMatchesScanAndIsFaster` (`build/dashboard/utxo_bench_chain_test.go`) runs the bench with `--scan` and `--repeat 20` for every chain in `utxoChains` except Dogecoin, plus Bitcoin, through that chain's own bench hook. A chain without a `Descriptors` list (Bitcoin Cash) runs with the single addresses only, against a fake node that answers the descriptor RPCs with `Method not found`, spells each address its own way and returns `scantxoutset` results as Bitcoin Cash Node 29.2.0 does (the coin count as `searched_items`, no `desc` for each coin), and its run also checks that a descriptor is refused. Each run builds a synthetic index of 200,000 coins: six single addresses (one holds 1,500 coins), three 2,000-script `pkh` descriptor wallets, and noise on 40,000 other scripts. Coin counts, amounts and positions are pseudo-random from a fixed seed per chain, so a failure reproduces. A fake node answers the descriptor RPCs and `scantxoutset` over the same coins. Its scan walks every coin and takes at least 1 µs per coin, 0.2 s in all. That is mini-1's rate: the 163.6 s single-address scan in [Balance index lookups on mainnet](lookup-bench-mainnet.md), over the 163.3 million coins counted in ADR 0001 (#216, at an earlier height). It is a model, not a measurement.

The test fails unless:

- every wallet is indexed with the expected script count;
- the index and the scan both equal the coins the test generated, and `match` is true;
- the index p50 is at least 50 times faster than the wallet's scan for a single address, and at least 2 times for a 2,000-script wallet;
- removing one coin from the fake node makes that wallet fail with `FAILED`.

The margins are low because the synthetic set is about 800 times smaller than mainnet's, and a scan grows with the set while a lookup barely does. The 2,000-script limit is 0.1 s, chosen for slow CI runners: here the test catches a 20-times per-script lookup slowdown but not a 10-times one. On mainnet the index was about 1,000 times faster for one address, and 70 times for the 13-wallet batch with derivation included.

Synthetic, on an Apple M5 Max (go1.27.1, darwin/arm64), load average 10 to 12 on 18 cores, 2026-11-17, five runs in a row of both chains: the 30 samples of 2,000-script wallets had a p50 of 5.3 to 15.9 ms against scans of at least 0.2 s, so 13 to 38 times faster, against the 2-times margin. Each run took 15 to 18 s.

The race detector slows Pebble lookups about 40 to 80 times (the 2,000-script p50 rose to 0.14 to 0.28 s), so the test skips under `-race`. `tests/run.sh` runs it in a separate `go test` without `-race`, and the CI `test` job runs that script.

### Adding a chain

Every chain in `utxoChains` gets a bench host from `newChainUtxo` (`chainBenchHost`). The host reads wallets through the node's `getdescriptorinfo`, `deriveaddresses` and `validateaddress`, or, for a chain with no `Descriptors` list, through `validateaddress` alone, as Bitcoin Cash's watch card does. It uses the node's `scantxoutset` as the baseline. To plug a chain into the bench and the CI test:

- give its `utxoChainParams` a `Descriptors` list of the descriptor shapes its node derives; with none, the bench takes only `addr(<address>)` keys and refuses descriptors;
- leave `LocalScripts` false unless the dashboard's own decoder (`addressScript`, Bitcoin only) reads the chain's addresses; false asks the node's `validateaddress` for each script;
- check that its node answers those RPCs and `scantxoutset` as Bitcoin Core does, and give the fake node the real node's result fields: a field the fake sends but the node does not passes CI and fails only on mainnet; a node without them needs its own `validate`, `resolve` and `scan` in its bench host, and a nil `scan` makes `--scan` refuse with "no scan to compare against";
- on the `stack` side, `--chain` accepts any chain `utxo_select_chain` knows; list the chain in the bench usage strings.

Dogecoin is not in the bench or its CI test, and `--chain dogecoin` is refused: Dogecoin Core 1.14.9 has no `getdescriptorinfo`, `deriveaddresses` or `scantxoutset`, so there is nothing to read wallets with or compare against. Bitcoin Cash has no `Descriptors` list, so its CI test uses single addresses only. The Dogecoin index is checked instead by `TestUtxoDogecoinChainstateSeedRegtestLive`, which compares every output on a regtest chain with the node's `gettxout`.
