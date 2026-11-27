# Watch-only balances

Bitcoin, Litecoin, and Bitcoin Cash read confirmed unspent balances from **your
own node**, so no third-party block explorer receives your addresses. Bitcoin
accepts public keys only; Litecoin and Bitcoin Cash accept one public address per
row. Monero uses a private view key plus optional signed key images. None of these
paths accepts a private spending key or can send funds. Tari accepts one public
spend key plus private view key and likewise cannot sign or send.

Bitcoin **watch-only** entries hold public keys only. The node can see
your balance and derive your addresses, but it has no private keys and
**cannot spend** anything.

## Add a wallet from the dashboard

The dashboard has a **Watch-only wallets** card with an add form:

1. **Label** (optional) — any name you like; the dashboard picks the first free
   `Wallet N` name when omitted.
2. **Key** — an extended public key, public output descriptor, or address (see below).
   For a bare `xpub`, choose the address type your wallet uses.
3. **Birthday** (optional, archival nodes only) — bounds a Core rescan. The
   default pruned UTXO-set scan does not show or use it.

Click **Add wallet**. On the default pruned node, the row stays pending while Bitcoin
Core's `scantxoutset` checks the live UTXO set, then shows the confirmed balance
and caches it with the scan height and time. A first scan commonly takes minutes,
but its time depends on hardware and current workload. Later reads use the cache
and are fast; the displayed scan time shows how fresh the result is. Remove
a wallet with the **✕** next to it.

Each scan logs its outcome and timing to the dashboard log, without any address
or key, for example `watch scan: outcome=ok trigger=add wallets=1 outpoints=7 height=975700
scan=84.2s waited=86.0s`. `scan` is the `scantxoutset` call itself and `waited`
is the time from the wallet being queued (by **Add wallet**, or by a new block) to the
result. Litecoin and the other family chains log one `<chain> watch scan:` line
per address scan. Read them with
`docker logs bitcoin-starter-stack-apt-dashboard-1 2>&1 | grep 'watch scan'`.

The public wallet list, confirmed scan cache, and balance snapshots persist across
restarts in the private `dashboard_state` volume. Existing Core watch-only wallets
remain supported. No file editing is required.

After the first scan, each new block updates the cached balance in place: spent
coins drop out and new payments to the wallet's first 1,000 receive and change
addresses are added, so a balance follows the chain within about 30 seconds of a
block without another full scan. A full `scantxoutset` runs again only when an
update cannot be applied safely: after a chain reorganization, when the cache is
more than 144 blocks behind, or when a needed block is no longer available.

The chart shows our saved balance observations, not historical transactions.
Overlapping confirmed UTXOs are counted once in a combined total. When Core
wallets and scan results are mixed, the dashboard withholds the combined total
because it cannot establish whether they overlap.

The same public key cannot be added twice under different labels. If an older
saved list contains exact duplicate keys, only its first entry is used, keeping
the balance from being counted twice; the original file is preserved until the
next wallet change.

Wallet changes are confirmed only after the roster is saved. If the state volume
cannot be written or the roster cannot be read, the manager reports an error and
preserves the existing file. A damaged roster is never reset to an empty list.
Restart the dashboard after correcting the file or state-volume problem.

If the saved scan cache is corrupt, unreadable, or unwritable, the dashboard
reports it as unavailable and stops expensive scans until storage is repaired
and the dashboard restarts. The wallet list and snapshot history stay intact.
The rebuildable `watch_scan_cache.json` can be backed up and removed from the
state volume to request a new scan; keep `wallets.json` and
`balance_history.json` intact.

Personal wallet views and add/remove controls require dashboard authentication.
Fresh Bitcoin state includes one clearly marked public example; see
[Bitcoin public example](#bitcoin-public-example-and-seeding-from-configjson).

## Litecoin and Bitcoin Cash addresses

The Litecoin and Bitcoin Cash cards require one public address; the optional
label defaults to **Litecoin wallet** or **Bitcoin Cash wallet**. After
the node is fully synced, the dashboard validates the address with that daemon and
scans every address that is due in one native `scantxoutset` walk. The result is a
confirmed UTXO snapshot at the displayed scan height, not transaction history.
Extended public keys, descriptors, Litecoin MWEB addresses, private keys, and seeds
are not accepted in these forms. The Litecoin and Bitcoin Cash cards each hold up
to 50 addresses.

Scans run in the background and a confirmed result is reused for up to one hour.
When the Litecoin balance index (Settings → Balance index → "Instant Litecoin
balances", or `litecoin.utxo_index`) is on and at the node's tip, Litecoin balances come
from the index instead and update within a minute of each block; the scan is the
fallback while the index seeds or catches up. Litecoin balances cover transparent
outputs only: funds inside MWEB are not counted, and the peg-in pool shows as one
non-wallet coin. Balances that update on each block (Litecoin from the index, and
Zcash transparent addresses) keep one chart point per hour, showing the latest
balance within that hour.
The row is **Pending** before its first successful scan. If the node is syncing or
a later scan fails, a prior confirmed amount remains visible as **Stale** or
**Error**; an unverified failure is never displayed as a zero balance. Saved
snapshot charts and watch lists survive dashboard restarts.

The Bitcoin Cash balance index (Settings → Balance index → "Instant Bitcoin Cash
balances", or `bitcoincash.utxo_index`) works on a pruned node. Bitcoin Cash Node
has no coin-set export, so the index seeds from a copy of the node's chainstate
taken during a short node stop, and then follows raw blocks; the chainstate rows
(`C` + txid + output index, one coin each, CashTokens data unwrapped to the real
script) were checked against a Bitcoin Cash Node 29.2.0 regtest chainstate. An
archival node with no host helper can instead replay every block from genesis,
which reads raw blocks
(`getblock` verbosity 0), which costs the node far less memory than verbose JSON on
Bitcoin Cash's 32 MB stress-test blocks (measured on regtest: about 1.1 to 1.2 GiB
at peak while replaying four 31 MB blocks, against about 2.9 GiB serving one of them
as JSON), and resolves spends through a reverse outpoint
key, as the Litecoin index does. That is a long one-time job: the regtest rates are in
[the replay measurement](measurements/utxo-replay.md), and a mainnet replay has not
been timed. When the index is at the node's tip, Bitcoin Cash balances come from it
and update within a minute of each block; until then the scan answers. Outputs
that carry CashTokens count their BCH like any other output; token amounts are
not shown. Without the index a pruned Bitcoin Cash node keeps the hourly scan.

With the Bitcoin Cash index on, the dashboard's memory limit is 1 GiB, the same
as for the Bitcoin and Litecoin indexes. Plan for the dashboard using up to 1 GiB
on top of what [Nodes](nodes.md) plans for Bitcoin Cash Node itself. A block of
more than 50,000 coin operations is applied in pieces of 50,000, each committed
on its own, so the dashboard never holds a whole large block's database batch and
undo record. Only the last piece moves the index's tip, and pieces left by a stop
or crash are rolled back when the index reopens, so a half-applied block is never
served. On regtest, replaying four 31 MB blocks of about 0.9 million operations
each under 1 GiB with no `GOMEMLIMIT` peaked at 296 MiB of live heap and 0.69 to
0.77 GiB RSS in two runs, and Bitcoin Cash Node peaked at about 1.14 GiB. Applied
whole, the same blocks needed 954 MiB of live heap and a 2 GiB cap. These regtest
peaks are lower bounds, and mainnet is not measured.

The index applies a block of up to 1.2 million coin operations and 32 MB. The
parsed block stays in memory whole while its pieces apply, at about 110 bytes per
operation, and that cap bounds it. A larger block, such as a miner's block of
millions of tiny outputs, stops the index at that height: the card reads Degraded
with the block's height and operation count, the index keeps what it has and is
not marked invalid, and it tries again only after the dashboard restarts.

Fresh state includes two sourced public examples:

- **Linux Mint** for Litecoin, from its
  [official donors page](https://www.linuxmint.com/donors.php).
- **Bitcoin Cash Node** for Bitcoin Cash, from its
  [official project site](https://bitcoincashnode.org/en/).

Examples are clearly marked and excluded from personal totals. Removing one is
persistent; restarting the dashboard does not add it back.

## Coins paid directly to a public key (P2PK)

An address is looked up under its own script only: pay-to-pubkey-hash for a
legacy address, pay-to-script-hash for a `3...`, `M...` or `9...` address. Coins
that an older output type paid straight to the same public key (P2PK, common in
early and some current mining payouts) sit under a different script, so they
are not counted for Litecoin, Bitcoin Cash, Dogecoin or Bitcoin addresses. This
holds for the balance index and for the scan, so a bench or verify run agrees
with the card and an explorer can show more. It was seen on Dogecoin, where 38
of 150 recent coinbases paid P2PK (issue 494); Litecoin and Bitcoin Cash look
up the same way, and their share is not measured. The index does store those
outputs; counting them needs a second index key and a new seed, which is not
planned yet. On a pruned Bitcoin node, a `combo()` descriptor does count its
P2PK outputs. A test pins this behaviour so a change is deliberate. The family
cards show a one-line note about it.

## Monero view-only wallets

A Monero address alone cannot reveal incoming outputs. Open the Monero card,
then use **Watch-only wallets**:

1. Enter the primary mainnet address and private view key. The optional label
   defaults to **Monero wallet**. Under **Scan options**, restore height defaults
   to `0`, which scans safely from genesis but takes longer.
2. Or paste JSON in this form:

   ```json
   {"name":"Cold storage","address":"4...","view_key":"...","restore_height":0}
   ```

3. Submit it and wait while the wallet scans. **Pending** is not a zero balance.

The form never accepts a private spend key or seed phrase. The resulting wallet
cannot sign or send transactions. The private view key is write-only in the UI:
it creates an encrypted watch-only wallet file, then is not stored in dashboard
metadata or returned by the API. Do not place it in `config.json`, Git, or a shell
command. The label, address, scan state, and balance snapshots stay in private
mode-600 dashboard state. Encrypted wallet files stay in the private
`monero_wallets` Docker volume; removing a dashboard row stops monitoring but
does not delete that encrypted file. Stack backups, release archives, and Debian
packages do not copy this private volume. After a disaster restore, remove and
add the Monero row again from your original address and private view key.

### Received-only and current balances

A view key detects incoming outputs but cannot determine by itself whether an
output was later spent. Until complete key images are imported, the dashboard
labels the amount **Received only**, not current balance. This follows Monero's
[view-only wallet guide](https://www.getmonero.org/resources/user-guides/view_only.html),
which says outgoing transactions require matching key images for a correct balance.

Export signed key images from the spending wallet and paste the native wallet-RPC
JSON object:

```json
{"offset":0,"signed_key_images":[{"key_image":"...","signature":"..."}]}
```

A wrapper response containing that object under `result` is accepted too. The
GUI/CLI encrypted binary `.key_images` file is not accepted. Some hardware-wallet
view-only flows cannot export key images; in that case keep using received-only
amounts. Import key images again after receiving or spending, because new outputs
need new coverage. A snapshot is marked verified/current only after the wallet is
fully scanned and fresh key-image coverage is complete.

The stack uses Monero's authenticated
[`generate_from_keys`, `get_balance`, and `import_key_images` wallet RPCs](https://docs.getmonero.org/rpc-library/wallet-rpc/)
inside Docker. That wallet service has no host port and no public internet route.

On fresh Monero wallet state, the dashboard seeds the official **Monero General
Fund** as a public example using the address and published view key from Monero's
[contribution page](https://www.getmonero.org/get-started/contributing/). A view
key is not a spending key, but it reveals received outputs, so treat personal view
keys as sensitive. The example is always received-only: the project has not
published complete current key images, so the dashboard does not claim an
accurate current balance. Removing the example is persistent and it is not seeded
into an existing wallet roster.

## Tari view-only wallet

The Tari card manages one native view-only wallet alongside the pruned base node:

1. Enter the 64-character public spend key and 64-character private view key.
   The optional name defaults to **Tari wallet**.
2. Under **Scan options**, birthday is optional from `2022-01-01` through today
   and defaults to blank, which scans from genesis.
3. Or upload strict JSON with only these fields:

   ```json
   {"name":"Cold storage","public_spend_key":"...","view_key":"...","birthday":"2024-01-01"}
   ```

The form rejects seeds, private spend keys, unknown JSON fields, invalid dates,
and a second wallet. **Pending**, **Waiting**, and **Error** never mean zero. Once
the native scan verifies a current result, the card shows total confirmed funds
as available plus timelocked outputs, with available, timelocked, pending incoming,
and pending outgoing amounts kept separate. A stale row is a saved verified
snapshot, not a fresh result. The snapshot chart is not transaction history.

The private view key reveals wallet funds and history but cannot spend. During
creation it crosses the shared private volume in a mode-600 request file, which
is removed after the native wallet accepts it. Dashboard state keeps the name,
public spend key, birthday, state, and balance snapshots, never the private view
key. No private spend key or spending-wallet seed is imported. Tari may generate
its own internal master seed, but the imported view-only identity has no private
spend key and cannot sign for the monitored funds.

Tari v6.2 stores the private view key in plaintext inside its native SQLite
`wallet.db`, even when a wallet password is configured. The `tari_wallet_data`
Docker volume is owner-only and mounted only by the wallet service; the dashboard
shares only the `tari_wallet` control and public-metadata volume. Separate generated
passwords protect native wallet
startup and the internal gRPC service, but they do not encrypt that database at
rest. Use an encrypted host disk or encrypted Docker-volume backing store when
encryption at rest is required. Removing the dashboard wallet stops it and moves
its native database into the owner-only archive inside `tari_wallet_data`; it does
not delete the base-node chain data. Failed imports are also retained there for
recovery. Removing a row does not erase its archived private view key.

The dashboard reaches the wallet through authenticated gRPC at internal port
`18143`; the wallet reaches the base node's internal HTTP query service at port
`9000`. Neither port is published to the host. The optional LAN port `18142` is
the separate mining gRPC endpoint and does not expose this wallet.

The repeatable native test verifies offline view-only creation on v6.1, a
persisted upgrade to v6.2, authenticated gRPC reads, removal, storage isolation,
and failure on a missing database.
Run it explicitly with `RUN_TARI_WALLET_NATIVE_SMOKE=1 tests/test_tari_wallet_native.sh`.
A funded balance on a fully synced pruned node, and sustained sidecar operation
within its separate 0.5 CPU / 512 MiB ceiling, remain unqualified. Do not treat
startup or mocked API tests as those full-sync results. Tari pruning retains live
unspent outputs through its
[UTXO synchronization service](https://github.com/tari-project/tari/blob/v6.2.0/base_layer/core/src/base_node/sync/rpc/sync_utxos_task.rs),
and its [view-only initializer](https://github.com/tari-project/tari/blob/v6.2.0/applications/minotari_console_wallet/src/init/mod.rs)
stores no private spend key.

## Unavailable wallet balances

Dogecoin shows a reason (it cannot scan the coin set for an address) with no public reference
address or balance card by default, because its current pruned daemon cannot discover
arbitrary address UTXOs; the opt-in archival mode below changes that. Zcash shows a reason (checkpoint pruning skips the address index) when the
transparent address index described below is off, because Zakura's
checkpoint-pruned mode omits it; the index is on for new installs. The dashboard does not query an external
explorer to fill those gaps.

**Dogecoin:** an address watched before initial sync or before its first payment
can be tracked going forward. Adding an old address to an already-pruned node
cannot establish its full balance: `importaddress` refuses a historical rescan,
and skipping the rescan can miss older unspent funds. Upstream's proposed
[UTXO scanner](https://github.com/dogecoin/dogecoin/pull/3665) remains unmerged
as of October 31, 2026. Its current RPC requires a private spending key, so it
also fails this dashboard's public-key-only requirement. With the current released
daemon the only option is a full archival node with a watch-only wallet, offered
as the opt-in described in the next section.

**Zcash / Zakura:** this is a limitation of our default storage profile, not a
claim that every pruned Zcash implementation lacks wallet support. Zakura 1.7.0
can retain transparent address indexes while pruning transaction bodies, but
requires a fresh database with checkpoint sync disabled. Upstream describes
that mode as slower and less secure against some chain attacks; it also retains
extra indexes with no published sizing estimate. New dashboard and package installs turn the
index on, existing installs keep their setting; see the next section. See the [release notes](https://github.com/zakura-core/zakura/releases/tag/v1.7.0)
and [checkpoint configuration](https://github.com/zakura-core/zakura/blob/v1.7.0/crates/zakura-consensus/src/config.rs).
Shielded addresses additionally need a viewing key and wallet scanner; a public
address alone is insufficient. Zallet and lightwalletd are separate wallet/index
services, not a small switch that enables balances in our existing database.

## Dogecoin balance index

`dogecoin.utxo_index` (Settings → Balance index → "Instant Dogecoin balances", on
for Dogecoin installed from the dashboard) gives Dogecoin addresses instant
balances, on a pruned node too. Dogecoin Core 1.14.9 has neither `dumptxoutset`
nor `scantxoutset`, but its chainstate holds every unspent coin whether or not the
node prunes. To seed the index, the stack stops the Dogecoin node, copies its chainstate beside it
with the tables hardlinked, and starts it again; the copy itself is quick, and on
mini-1 the node was down about 1 min 45 s. The dashboard builds the index from the copy, then follows new blocks from
the copy's tip, reading merge-mined blocks raw. The copy is removed once the seed
has finished; until then it keeps the node's older tables on disk.

When the index is ready, Dogecoin balances come from it and update within a minute
of each block, and the archival watch wallet below is not needed. Without the watch
wallet, balances wait until the index is ready. If a pruned node drops blocks the
index still needs, the index is marked invalid and is seeded again automatically.
On mini-1 the seed took 27 minutes and the index holds 202 million coins in 18.4 GB
([measurement](measurements/dogecoin-index-mainnet.md)).

With the index on, the Dogecoin card holds up to 50 addresses; with it off, 25.
If you turn the index off while more than 25 are saved, they stay listed and can
be removed, but no more can be added until the list is back under 25.

## Dogecoin archival watch wallet (opt-in)

Set `"watch_wallet": true` and `"prune_mb": 0` under `dogecoin` in `config.json`.
The Dogecoin node then runs unpruned with its wallet enabled
(`disablewallet=1` is dropped; nothing else in its generated config changes), and
the dashboard accepts public Dogecoin addresses. Dogecoin Core 1.14.9 has no
`scantxoutset`, and `importaddress` refuses to rescan a pruned chain, so an
archival chain is the only released-code path.

How it works:

- Adding an address calls `importaddress <address> "" true` in the background,
  which rescans the whole chain. The row shows **Rescanning** until it ends.
  Only one rescan runs at a time; other addresses wait, and the dashboard refuses
  to add a new one meanwhile. The node's RPC can be slow or unresponsive during a
  rescan, which also pauses balance refreshes.
- The dashboard records a completed import per address on its state volume and
  shows a balance only after that. If the import fails or is interrupted (node or
  dashboard restart), no balance is shown and the import repeats after six hours.
- Once the import is recorded, the balance is the sum of `listunspent 1 9999999 [address]`
  (confirmed outputs) and is cached for an hour like the other chains.
- The roster is stored by the dashboard, so a restart does not lose it. If the
  dashboard restarts mid-rescan the import is repeated, because only a completed
  import counts.
- The wallet file lives on the Dogecoin data volume and holds watch-only
  addresses only, never a private key or seed. Removing an address from the
  dashboard does not remove it from the wallet. Back up the data volume if you
  want to avoid repeating rescans.
- Amounts are tracked with 8 decimals in a 64-bit integer, which holds up to 180
  billion DOGE; Dogecoin has no hard cap and is about 150 billion today.

Limits to accept before enabling it:

- A fresh, unpruned sync from genesis over Tor. [Dogepedia](https://dogecoin.com/dogepedia/articles/what-is-a-node/)
  says the chain is about 70 GB, but that is undated and this stack has not
  measured it; plan well above that on an expandable volume.
- Rescans can take hours, longer on small boxes.
- A pruned database cannot be converted. `configure.sh` refuses `prune_mb: 0` and
  `watch_wallet` when the data directory has a pruned chain (block index present,
  `blk00000.dat` missing) or the previous `.env` shows Dogecoin running pruned in
  the same `data_dir`; point `dogecoin.data_dir` at a new directory. It also refuses
  `watch_wallet` unless `prune_mb` is `0`.
- Node traffic stays Tor-only; address lookups are local RPC calls and no
  explorer is queried.

The dashboard seeds the Dogecoin Foundation address from the
[Foundation's About page](https://foundation.dogecoin.com/about/) as a public
example, excluded from personal totals. With the option on it is never imported
or scanned, so it does not delay your own addresses. Without the option the
balance index serves it like any other wallet and refreshes it hourly.

## Zcash transparent addresses

A Zcash node installed from the dashboard sets `"address_index": true` under `zcash`, so it syncs without checkpoints and the index is available from the start. Existing installs keep their setting; an upgrade never flips it. Setting `"address_index": true` under `zcash` in `config.json` runs Zakura with
`build/zcash/zakurad-address-index.toml`. It is the default config with
`checkpoint_sync = false` and the same pruned storage. The dashboard then accepts
transparent `t1` and `t3` addresses and reads each balance from your own node with
`getaddressbalance`. Shielded and unified addresses are rejected because they need
a viewing key and a wallet scanner. Balances cover confirmed transparent funds only.
Because the lookup is a local index read, each wallet is refreshed within a minute of
the node's best block changing instead of once an hour.

Limits to accept when the index is on, or before enabling it later:

- The node must resync from genesis into a fresh data directory. `configure.sh`
  refuses the option when `zcash.data_dir` already holds files and has no
  `.address-index` marker (written by an index-mode run), or when the previous
  `.env` shows Zcash running without it there; point `data_dir` at a new directory instead.
- If the node reports the transparent indexes as unavailable, the dashboard logs
  `outcome=index_unavailable` and shows Error; resync with a fresh `zcash.data_dir`.
- Each balance is accepted only if the node's best block hash is unchanged across the
  lookup; a block arriving mid-scan makes that scan fail and retry on the next cycle.
- Upstream calls disabling checkpoint sync slower and less secure against some
  chain attacks.
- Disk use for the retained indexes is not yet measured, and no sync time is published.
- Node traffic stays Tor-only; address lookups are local RPC calls.

With the option on, the dashboard seeds the Zcash Community Grants funding-stream
address from [ZIP 214](https://zips.z.cash/zip-0214) as a public example, excluded
from personal totals. Without it, Zcash has no example.

Tari has no sourced public example because no organisation publishes a Tari view key.

## Bitcoin: the Genesis address is a special case

The Genesis address `1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa` is not added automatically.
It can be watched manually, but it is a poor reference for comparing balances:
the original 50 BTC genesis coinbase is unspendable and is excluded from Bitcoin
Core's wallet balance. Explorers may attribute it to this address. That explains
a 50 BTC difference, but does not establish that every other difference is correct.

The dashboard displays the balance reported by your node without adding 50 BTC.
Existing saved entries are preserved.
Existing entries named Satoshi for this exact address display as Genesis without
renaming their Core wallet or losing their balance history.

## What to paste for the key

**An extended public key** — the friendly option. Copy the `xpub`/`ypub`/`zpub`
your wallet shows for an account:

| Prefix | Address type it's read as |
| --- | --- |
| `zpub` | Native SegWit (`bc1q…`) — BIP84 |
| `ypub` | Nested SegWit (`3…`) — BIP49 |
| `xpub` | Legacy (`1…`) — BIP44 |

The script type is inferred from the prefix. A plain `xpub` does not identify its
script type: for a SegWit account, select **Native SegWit** (`bc1q…`) or **Nested
SegWit** (`3…`) in the form. Legacy remains the default. A wallet-exported public
descriptor is more precise, and is required for Taproot.

Pruned-node scans derive receive and change indexes 0–999 from an account xpub.
Coins outside that bounded range need a more precise descriptor or a later wider
scan. Extended public keys are privacy-sensitive: anyone with one can follow the
derived addresses and balances.

**A full output descriptor** — the exact option. This is what "export" gives you
in Sparrow, Coldcard, Ledger Live, etc. It names the script type and derivation
explicitly; the result still covers only the supplied descriptor and scan range:

```
wpkh([a1b2c3d4/84h/0h/0h]xpub6CUGRUo.../<0;1>/*)
```

The `<0;1>` covers both the receive and change branches. Any checksum (`#abcd…`)
is fine to leave on or off.

**A single address** — paste an address (`bc1…`, `1…`, or `3…`) to watch just
that one address. Useful for a specific receive address, a donation address, or a
paper wallet. Note it tracks only that address, not a whole wallet — for your
account balance within the scanned range, use its xpub instead.

## Exchange / custodial accounts (Coinbase, River, Cash App…)

You **can't** get an xpub from a custodial service, and you can't watch a
custodial balance at all — the coins aren't held at addresses you control, they're
in the provider's pooled wallet, and the deposit addresses they show you rotate.
Watch-only balances only work for wallets where **you** hold the keys (a hardware
wallet, or a self-custody app like Sparrow/Electrum/BlueWallet). To make an
exchange balance watchable, withdraw it to your own wallet and add that wallet's
xpub.

## Requirements and scan limits

- The default pruned node needs no external address indexer. It asks Bitcoin Core
  to scan the current UTXO set, so it can find confirmed unspent balances without
  historical block files.
- The scan does not provide transaction history, spent outputs, or mempool activity.
  It reports confirmed unspent balance as of the displayed scan height.
- A new scan is asynchronous. Pending means no balance result has been confirmed
  yet; it never means zero. There is no guaranteed completion time.
- Wallets already imported into a synced Bitcoin Core wallet keep their normal,
  accurate cached balance and are fast to read.
- Only public addresses, public descriptors, and extended public keys are accepted.
  Never paste a seed phrase or private key; the dashboard rejects them.
- `blockfilterindex` remains a manual compatibility setting; on a pruned node it
  must be enabled before the first prune. It is not required for the default
  UTXO-set scan.

## Bitcoin balance index

On a pruned node, `bitcoin.utxo_index` keeps a local index of the coin set, so a
watched wallet's balance comes from a lookup instead of a `scantxoutset` walk.
New APT and Homebrew installs, and Bitcoin installed from the dashboard, turn it
on for a pruned node; other installs opt in. It starts empty and seeds itself
once the node has synced, or you can seed it with `./stack utxo-seed`, as
[Operations](operations.md#seeding-the-bitcoin-balance-index) describes. The
design and its measurements are in
[ADR 0001](adr/0001-bitcoin-utxo-index.md). Operations also covers the
measured seed budget, a "Needs a new seed" card, upgrades and removal.

- **When the index answers.** Only while it is `ready` (which needs the node itself to be
  synced with the network) and level with the node tip, and only for a wallet whose scripts it can derive. Every other wallet, and
  every wallet while the index is seeding, catching up, degraded or invalid,
  uses the scan above, which waits while the node is still syncing or its newest block is more than 2 hours old. Each wallet in `/api/watch` reports `balance_source` as
  `index` or `scan`, and its row says "From the balance index" when the index
  answered.
- **New blocks.** While the index is `ready`, the dashboard waits on bitcoind's
  `waitfornewblock` RPC, so the index applies a block as soon as the node has
  it, then refreshes the watched wallets straight away. In the regtest test a
  mined block was applied in well under a second. The wait holds one of
  bitcoind's 16 RPC threads. While the index catches up it checks every 10
  seconds as before. The wallet list in the browser still refreshes every 15
  seconds, so the page shows the new balance on its next refresh.
- **Same limits as the scan.** Confirmed unspent balance only: no transaction
  history, no spent outputs, no mempool or unconfirmed activity. Indexes 0 to
  999 of receive and change per descriptor, so a wallet that used a higher index
  is undercounted, as with the scan. Coins paid directly to a public key (P2PK) are
  not counted for an address, see
  [Coins paid directly to a public key](#coins-paid-directly-to-a-public-key-p2pk). Bitcoin only.
- **Supported keys.** Addresses and `pkh`, `wpkh`, `sh(wpkh)`, `wsh` and `tr`
  descriptors, including xpubs that resolve to them. Other shapes, such as
  `pk()` or `combo()`, keep using the scan.
- **What is stored.** The `utxo_index` volume holds public chain data: hashed
  script prefixes, txids, output numbers, amounts and heights. It does not say
  who owns what. Each wallet's derived scripts are cached in `utxo_scripts.json`
  in the private state directory (mode 0600), next to the scan cache. The index
  directory is 0700; the dashboard sets that when it opens the index. Logs carry
  counts, heights and durations only.
- **No new network path.** The index reads only from your own bitcoind on the
  internal network. It calls no external service, and the Compose network
  configuration is unchanged (`tests/test_compose.sh` asserts this).
- **Turning it on or off from the dashboard.** Settings → Balance index →
  "Instant Bitcoin balances" is a switch for `bitcoin.utxo_index`; pressing
  **Save** applies the change, restarts the dashboard, and asks first when turning off (the host
  refuses to turn it off while a seed runs; cancel it first). A badge
  in the heading of the watch-only wallets card shows the index state (ready,
  catching up, seeding, not seeded, needs a new seed, degraded or off) and links to the index
  card. After turning it on, the index seeds itself once the node has synced, and the node
  card shows a progress bar for it; there is no Seed button. The install dialog offers the
  same option, ticked, next to the data folder.
- **Turning it off.** Switch it off in Settings, or set `bitcoin.utxo_index` to
  false and run `./stack apply`.
  The scan serves again and the volume is kept; `./stack utxo-index remove`
  deletes it, see
  [Operations](operations.md#seeding-the-bitcoin-balance-index).

## Bitcoin public example and seeding from config.json

Fresh installs start with one public example address disclosed by Binance in
2022. It is labeled **Public example** and excluded from personal totals, so its
balance cannot be mistaken for yours or for Binance's total reserves. It is one
address, not proof of the exchange's solvency. Its first UTXO-set scan stays
pending until complete. You can remove it in the dashboard, or opt out before first start with
`"wallets": []`. See Binance's
[original address disclosure](https://www.binance.com/en-IN/blog/community/2895840147147652626).

You can instead predeclare your own wallets in `config.json`, which seeds the
list on first start:

```json
"wallets": [
    { "name": "Cold storage", "key": "zpub6r...", "birthday": "2021-03-15" }
]
```

After that first start the dashboard's own list (the roster) is authoritative —
edits you make in the UI stick, and further `config.json` changes are ignored.

## Privacy & safety

- **No spending keys.** Bitcoin, Litecoin, and Bitcoin Cash accept public material
  only. Monero and Tari accept private view keys, but reject private spend keys and
  seeds; none of these paths can sign or send.
- **An xpub is still sensitive.** Anyone who has it can follow the addresses and
  balances derived from that account. It's stored in the private wallet list;
  existing Core watch-wallet files can also contain it. Keep access to the box and dashboard
  controlled. Dashboard sign-in is required; use a VPN or HTTPS for remote access.
- **Wallet data stays on the box.** Balances come directly from your nodes;
  wallet keys and addresses are never sent to external services. The separate BTC
  and XMR price lookups send only asset and currency names to CoinGecko through Tor.
- **View keys are sensitive.** Monero and Tari view keys reveal funds and history.
  Tari v6.1 stores its key inside the native SQLite database, so use encrypted
  host storage if you require encryption at rest.

## Native wallet smoke test

Maintainers can opt into isolated Litecoin and Bitcoin Cash regtest proofs. The
test builds temporary local images, exercises funded and zero-balance addresses
through the authenticated dashboard API, and removes its owned resources:

```bash
RUN_NATIVE_WALLET_SMOKE=1 tests/test_native_wallets.sh
```

Set `NATIVE_WALLET_CHAIN=litecoin` or `bitcoincash` to run one daemon. This test
needs Docker and is intentionally skipped by default.
