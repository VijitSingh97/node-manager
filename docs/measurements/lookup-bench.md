# Lookup benchmark on mini-1

How to measure balance lookups with and without the Bitcoin balance index on the reference host, for the targets in #236. The harness uses public test wallets only, needs no dashboard password and does not change the watched-wallet roster, the scan cache or the script registry.

## What it measures

`utxo-index bench` hands a wallet list to the dashboard through the state volume, the same way `utxo-seed` does. The dashboard, with the index ready and level with the node tip:

- derives each wallet's scripts the way a watch does, without saving them, and times it;
- times the index lookup of each wallet once cold, then `--repeat` times in all (default 20): first, p50, p99 and max seconds, with the coin count;
- times one lookup over the combined scripts of the first 1, 5 and 20 wallets, or as many as the file has;
- times `/api/watch`'s cached read (`ListWithChain`) `--repeat` times over the wallets in the owner's watch roster, not the bench wallets, and records the roster size;
- with `--scan`, holds the watch scanner, runs one `scantxoutset` over every wallet and another over the first wallet alone, and compares the coin count and total sats per wallet with the index result. The comparison counts only when the index tip and both scan heights are equal; otherwise `match` is `n/a` and the output says a block arrived during the run.

The result goes to `utxo-bench.result` in the state volume (mode 600). The command prints it as tables and saves a copy as `utxo-bench-<UTC time>.json` in the state directory, also mode 600. The dashboard log gets two lines with counts and durations only.

## Prerequisites

- mini-1 runs a release that includes `utxo-index bench`, with `bitcoin.utxo_index` true and applied.
- The index is `ready` and `blocks_behind` is 0 (the card, or `/api/utxo-index`). The run refuses otherwise and says why.
- `jq` is installed. Run as root on APT (`sudo node-manager ...`), as the install user otherwise.
- For the block-latency step, `scripts/measure-block-latency.sh` is copied to the host too, and the user can run `docker`.
- The wallet file is on the host: copy `docs/measurements/bench-wallets.json` from the repository. It holds public data only: nine well-known addresses (below) and descriptors over the BIP32 test vector 1 master xpub as `wpkh`, `sh(wpkh)`, `tr` and `pkh`, each about 2,000 scripts. Add no owner wallet to it without approval.

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
```

A request older than two minutes is refused by the dashboard, and a run is stopped after 8 minutes (40 with `--scan`). A `--scan` run needs an owner-approved window and asks for confirmation, or `-y` on a non-interactive shell. `scantxoutset` walks the whole UTXO set: minutes of CPU on a 0.5-CPU node, with bitcoind RPC slow meanwhile, and the watch scanner is held until it ends. It scans twice, once for the whole file and once for the first wallet, so allow up to 45 minutes. Before it, follow the cautions in [Operations](../operations.md#seeding-the-bitcoin-balance-index): pause Tari, check the temperature, and confirm no seed or other scan is running. The command refuses when a scan or a refresh is running.

```
sudo node-manager utxo-index bench bench-wallets.json --scan -y --repeat 20
```

Ctrl-C removes a request the dashboard has not taken yet; one it has taken finishes and leaves its result in the state volume.

## Mapping results to the #236 targets

| Target | Where to read it |
| --- | --- |
| Add an indexed wallet under 1 s | Per wallet, the rows named `Vector wpkh`, `Vector sh-wpkh`, `Vector tr` and `Vector pkh` (about 2,000 scripts each): add `derive` and `first` yourself. `first` is the first lookup of that wallet, not a true cold read: the OS page cache and earlier wallets have warmed the index. The nine address rows are one script each and say nothing about derivation; the `Genesis block coinbase address` row is the many-coins case. |
| Add a wallet without the index | `--scan`: "first wallet alone" seconds. |
| Cached read under 100 ms | "Cached read": p50 and p99. It reads the owner's roster, so it counts only when `roster wallets` is the size of interest (1, 5 or 20). With an empty roster the figure is near zero and means nothing. |
| Batched registration of 1, 5 and 20 | "Batched lookups": the p50 for each size, plus the sum of `derive` for those wallets. With `--scan`, the batch scan against that sum. |
| New block to balance | `scripts/measure-block-latency.sh`, below. |
| Restart to ready | Below. |
| Resource use, healthcheck during follow | Host sampling, below. |
| Index and scan agree | With `--scan`: `match` true for every wallet. `n/a` marks a wallet whose shape the index does not support (it falls back to the scan), or a run where a block arrived during the scan; repeat the run in the second case. `false` at equal heights is an index bug. |
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

Fill in one row per target from a run. Mark each met or not met against the number.

| Target | Measured | Met |
| --- | --- | --- |
| Add an indexed wallet under 1 s (descriptor, cold) | | |
| Add an indexed wallet (genesis address, cold) | | |
| Add a wallet by scan (first wallet alone) | | |
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
