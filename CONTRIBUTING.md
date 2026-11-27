# Contributing

Small stack, small rules. PRs welcome.

## Dev setup

```bash
git clone https://github.com/VijitSingh97/node-manager.git
cd node-manager
pip install -r build/dashboard/parity/reference/requirements.txt pytest
```

Go 1.27 and Docker are needed for the full test suite. Lint tools run from
digest-pinned images, so nothing else needs installing.

## Running tests

```bash
tests/run.sh
```

That runs [scripts/lint.sh](scripts/lint.sh) (shellcheck, ruff, actionlint,
gofmt), the `configure.sh` end-to-end test, the
docker-compose contract test, Go race/vet/unit checks, the frozen Python
reference suite, and the same HTTP contract tests against both runtimes.
Anything the machine can't run is skipped with a `SKIP:` line. CI
([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs the same suite
on every push and PR.

### Dashboard UI flow tests

`build/dashboard/ui/*.test.mjs` drive the compiled dashboard through the real
page in headless Chrome, with no dependencies beyond Node 22 (built-in
`WebSocket` and `fetch`). Each flow starts its own dashboard against a fake
Bitcoin RPC and a temporary state directory, signs in through `/login`, clicks
and types with real input events, and checks the files the dashboard writes
(`notification_settings.json`, `node-control.request`) as well as the page. A
small fake host helper in `ui/harness.mjs` consumes the request files and updates
the capability and status files the way the installed helper does.

```bash
node --test "build/dashboard/ui/*.test.mjs"          # builds the dashboard with Go
DASHBOARD_BIN=/tmp/dashboard node --test --test-timeout=600000 "build/dashboard/ui/*.test.mjs"   # reuse a built binary
CHROME=/path/to/chrome node --test "build/dashboard/ui/*.test.mjs"        # pick a browser
node --test --test-name-pattern="Telegram" "build/dashboard/ui/*.test.mjs"
```

Chrome is found from `$CHROME`, `google-chrome`, `google-chrome-stable`,
`chromium`, or the macOS app. Without Chrome or a dashboard binary the flows skip
locally with a message; with `CI=true` they fail instead. `tests/run.sh` runs
them with the dashboard binary it builds. New flows go in a new
`*.test.mjs` file using `flow()` from `ui/harness.mjs`.

This is phase 1: the Settings dialog and node Activate, Deactivate and Start.
Not yet covered through the page: the dashboard password form, the LAN port
rows, credential reveal and reset, sign out and the `/login` page itself, the
theme toggle, the per-card currency selects, and wallet add and remove. Without
`DASHBOARD_BIN` each test file builds its own binary into a temporary directory
that it removes on exit, so set `DASHBOARD_BIN` to build once.

The UTXO dump parser tests read small regtest fixtures committed in
`build/dashboard/testdata/utxo/`, so CI and `go test` need no bitcoind. To
regenerate them from the pinned bitcoin image, or to re-check the harness
(local only, not run in CI), use Docker and Python 3:

```bash
tests/utxo_regtest.py build/dashboard/testdata/utxo
UTXO_REGTEST=1 go test -run RegtestLive ./...   # from build/dashboard
UTXO_REGTEST=1 go test -run UtxoIngestRegtestLive ./...   # ingest vs scantxoutset
UTXO_REGTEST=1 go test -run UtxoStorageFullTmpfsLive ./...   # real full tmpfs: ENOSPC and low-disk handling
UTXO_INGEST_BENCH=25000000 go test -run UtxoIngestLargeMeasure -v ./...   # large synthetic ingest
```

Native daemon checks run separately for Monero, Tari, Litecoin, Dogecoin,
Bitcoin Cash, and Zcash in CI. To test one locally:

```bash
RUN_NATIVE_NODE_SMOKE=1 NATIVE_NODE_CHAIN=litecoin tests/test_native_nodes.sh
```

This builds and starts an isolated Tor, dashboard, and native daemon with fresh
temporary data. It checks the real status API, native fee responses, Tor routing,
and the published RPC or gRPC endpoint. It does not download a complete chain or
qualify steady-state resource limits. Run one chain at a time on a small host.
Successful runs remove their containers, images, and data; failed runs preserve
private diagnostics at the printed local path. Do not upload that directory.

The Tari client uses a minimal upstream protocol subset in
`build/dashboard/node-stack-tari.proto`. With protoc 7.36.2,
protoc-gen-go 1.36.10, and protoc-gen-go-grpc 1.5.1 installed, regenerate it with:

```bash
cd build/dashboard
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative node-stack-tari.proto
```

Keep `THIRD_PARTY_NOTICES` current when changing the protocol or Go dependencies.

## Ground rules

- Keep it minimal. This is a starter stack — prefer deleting over adding,
  and stdlib/native features over new dependencies.
- Every change to `configure.sh`, the compose file, or the dashboard needs
  a matching test in `tests/` or `build/dashboard/tests/`.
- No credentials or per-box values in tracked files. Anything user-specific
  flows through `config.json` → `.env` (gitignored).
- Docs live in [docs/](docs/) and must stay true — if a change makes a doc
  stale, fix the doc in the same PR.
