# Litecoin reverse outpoint key: disk per coin

Date: 2026-11-16. Measured for phase P2 of #238, on an Apple M5 Max. These are not reference-node or mainnet numbers.

Litecoin 0.21's `getblock` gives no prevout scripts, so the index also stores a reverse key for each coin. The key is `'o' || txid || vout` (37 B), and its value is the coin's 16-byte script hash (16 B). A store that holds these keys uses schema 2. Bitcoin stores stay on schema 1, and each schema refuses to open the other.

The undo journal does not grow. Each reverse key is derived from the forward coin key, so a block disconnect rebuilds the reverse keys from the coin undo ops already stored.

## Method

- **Regtest fixture:** `build/dashboard/testdata/utxo_litecoin/dump.dat`, with 449 coins, ingested into a schema-1 store and a schema-2 store. Sizes count the SST files only, and the logical bytes are the sum of key and value lengths. Run the probe with `UTXO_REVERSE_MEASURE=1 go test -run TestUtxoReverseKeyFixtureMeasure -v`.
- **Synthetic:** the committed harness `TestUtxoIngestLargeMeasure`, with random scripts. Its sizes cover the whole index directory: the SSTs plus MANIFEST, OPTIONS and WAL. Run it as follows:

  ```bash
  UTXO_INGEST_BENCH=5000000 [UTXO_INGEST_BENCH_REVERSE=1] go test -run TestUtxoIngestLargeMeasure -v
  ```

  The peaks are sampled every 500 ms, so they are lower bounds.

## Results

| Input | Store | Logical | On disk, final | Peak during ingest |
| --- | --- | --- | --- | --- |
| Fixture, 449 coins (SST files only) | coins only | 64.0 B/coin | 46.8 B/coin (21,006 B) | not measured |
| Fixture, 449 coins (SST files only) | coins + reverse | 117.0 B/coin | 86.5 B/coin (38,826 B) | not measured |
| Synthetic, 1M coins (whole directory) | coins only | 64 B/coin | 64.2 B/coin | missed by the sampler |
| Synthetic, 1M coins (whole directory) | coins + reverse | 117 B/coin | 102.3 B/coin | missed by the sampler |
| Synthetic, 5M coins (whole directory) | coins only | 64 B/coin | 64.0 B/coin | 127.7 B/coin |
| Synthetic, 5M coins (whole directory) | coins + reverse | 117 B/coin | 102.1 B/coin | 220.4 B/coin |

## Findings

1. **The reverse key adds about 38 to 40 B per coin on disk.** The logical size is 53 B; SST prefix compression saves the rest.
   - On random scripts that is +59% (64.0 to 102.1 B/coin).
   - On the fixture it is +85%. The percentage is higher because about 430 regtest coinbase coins share one mining script. Their coin keys then share a prefix and compress unusually well, which the reverse keys, sorted by txid, do not.
2. **The seed peak rises by about 93 B per coin** (127.7 to 220.4 B/coin). During the merge, the run files for both streams sit on disk next to the new SSTs.
3. **Seed memory is unchanged by design (not measured).** In a schema-2 store, each stream gets half of the run buffer.
4. **The undo journal adds 0 B per block.** The P0 report inferred about 2x; deriving the reverse ops avoids that cost.
5. **Mainnet totals are still unknown.** Litecoin's mainnet coin count is not measured, so these numbers give no total yet. Multiply the per-coin figures by `gettxoutsetinfo`'s `txouts` once mini-1's Litecoin node is synced.
