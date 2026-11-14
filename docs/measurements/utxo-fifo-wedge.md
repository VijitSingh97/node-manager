# UTXO seed FIFO wedge matrix (regtest)

Date: 2026-11-14. Closes the "reader dies or never opens the FIFO" open item of ADR 0001 section 4 (#226), for regtest only.

- Image: `bitcoin/bitcoin:32.1@sha256:d2454aaf2ff4ce1ebbc49f4aa05ed044c907b849a80a3f4c0ad23e488e50bbe0`, Core `/Satoshi:32.1.0/`.
- Harness: `python3 -I tests/utxo_fifo_regtest.py [results.json]`. One bitcoind container (`--restart on-failure`, named volume as datadir) and one short-lived reader container per scenario mounting the volume read-only at `/ro`, as the same image with `bash`/`perl`, running as uid 101 (the `bitcoin` user). `mkfifo` runs inside the bitcoind container. All containers and the volume are removed on exit.
- Chain: 109 blocks, 8109 UTXOs, dump 330,680 bytes (about 5x the 64 KiB pipe buffer). Every scenario's stream has the same sha256 `7ece3634...a403f`, header base hash and coin count 8109 match the chain.
- Not a mainnet result: the hash pass takes about 0 s here, so nothing about the 300 s `cs_main` block is measured.

Pass criterion: no wedge, no restart (RestartCount and StartedAt unchanged), and a fresh dump works afterwards.

| Scenario | Verdict | Key numbers |
| --- | --- | --- |
| S0 baseline | PASS | dump 0.26 s, 330,680 bytes, header ok, FIFO renamed to `utxo-seed.dat` (still a fifo), `.incomplete` gone |
| S1 no reader | PASS | parked 30 s; `getrpcinfo` active_commands `["dumptxoutset","getrpcinfo"]`; `getblockchaininfo`/`getrpcinfo` max 0.51 s over 3 rounds; `timeout 5 cat` unparks it, dump completes, rc 0 |
| S2 reader SIGKILLed after 98,304 B | PASS | RPC returns immediately, rc 1; bitcoind alive (0.17 s), RestartCount 0; fresh dump ok |
| S3 reader stalled 120 s | PASS | 12 probe rounds, 0 failures, max 0.4 s; dump still running at 100 s; Core log `completed (120.01s)`; stream sha equals S0 |
| S4 no FIFO | PASS | `.incomplete` observed as a regular file; final `utxo-seed.dat` is a regular file, 330,680 B |
| S5 stop while parked | FAIL (recoverable) | `bitcoin-cli stop` returns in 0.1 s, "Bitcoin Core stopping", but bitcoind did not exit in 45 s; a `cat` reader unparks it and it exits in 0.6 s, exit 0; restart clean, `verifychain 1 0` true; SIGKILL while parked: restart in 6.7 s, `verifychain` true |
| S6 keeper writer | PASS | see findings 5 |

## Verbatim strings

- S2 RPC error: `error code: -1` / `error message:` / `AutoFile::write: write failed: iostream error`
- S5 dump client when stopped while parked: `error code: -9` / `error message:` / `Shutting down`
- Core log, one pair per dump: `WriteUTXOSnapshot: writing UTXO snapshot at height 109 (<hash>) to file /home/bitcoin/.bitcoin/utxo-seed.dat (via /home/bitcoin/.bitcoin/utxo-seed.dat.incomplete) started` then `... completed (0.29s)`. In S1 and S3 the `started` line is logged after the reader opens, so Core opens the FIFO before logging. Core wrote no error line for the S2 failure; the error reaches only the RPC client.
- S6 perl open results: `wronly_nonblock_no_reader=err:No such device or address`, `rdonly_nonblock=ok`, `wronly_with_reader=ok`.

## Findings

1. Core ignores SIGPIPE. A reader that closes early gives a clean RPC error (S2), no crash, no restart. The `.incomplete` FIFO stays behind (`fifo 0`) and `utxo-seed.dat` is not created, so the next seed must remove and recreate it.
2. A dump with no reader parks one RPC thread inside `fopen`; other RPCs stay fast (S1) and a stalled writer does not hold `cs_main` on regtest (S3). Both states are escaped by opening the FIFO for reading.
3. While parked, `stop` does not complete until a reader opens the FIFO (S5). `docker stop` would escalate to SIGKILL, and recovery from that was clean.
4. A dashboard with a different uid cannot reach the FIFO at all: the datadir is `drwx------` bitcoin, so a uid 65534 reader got `Permission denied` and bitcoind parked (the first run of S0 hung for 180 s before the reader uid was fixed). The reader needs the bitcoin uid, or root.
5. A reader on the read-only mount can open the FIFO `O_RDONLY|O_NONBLOCK`, and `O_WRONLY` once a reader exists (S6). A non-blocking reader plus a keeper writer idled 8 polls (4.21 s) with no EOF before Core wrote, then received 330,680 bytes, saw EOF after the keeper was closed on the first byte, and matched the baseline sha. `O_WRONLY|O_NONBLOCK` with no reader fails ENXIO.
6. S4: Core writes a regular file at `<path>.incomplete` when nothing is there, so the reader must `lstat` it and require a named pipe.

## Not tested

Mainnet size and the 300 s hash pass; stop while the writer is blocked mid-write on a stalled reader; a reader that closes before Core has opened the FIFO; a different reader uid with a group-accessible datadir; leaving the stale `.incomplete` FIFO in place before the next dump; Docker on Linux (this ran on Docker Desktop for Mac, arm64); the dashboard code itself.
