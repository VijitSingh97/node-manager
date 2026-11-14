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
plus a full image build on every push and PR.

The UTXO dump parser tests read small regtest fixtures committed in
`build/dashboard/testdata/utxo/`, so CI and `go test` need no bitcoind. To
regenerate them from the pinned bitcoin image, or to re-check the harness
(local only, not run in CI), use Docker and Python 3:

```bash
tests/utxo_regtest.py build/dashboard/testdata/utxo
UTXO_REGTEST=1 go test -run RegtestLive ./...   # from build/dashboard
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
