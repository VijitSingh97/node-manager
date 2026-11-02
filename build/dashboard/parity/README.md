# Watch and balance parity baseline

`oracle.json` is generated from the unchanged Python reference modules in
`reference/`. It contains compact cross-language vectors for
the behavior that the Go port must preserve: key and descriptor conversion,
helper formatting, provisioning RPC sequences, balance states, persisted wallet
CRUD, and balance history.

The Python code is a frozen test reference, not a production dependency. The
production image contains only the Go dashboard. The retained suite has 131
Python assertions, and `test_http.py` runs the same 14 public HTTP contracts
against both the reference server and the production Go binary.

Regenerate or verify it from the repository root:

```sh
python3 build/dashboard/parity/generate.py --write
python3 build/dashboard/parity/generate.py --check
```

The existing Python tests remain the detailed source of truth. The baseline at
commit `b530e7e1ebb50924d5c41ab2f53bc8911f05a014` was recorded with:

```sh
python3 -m pytest -q build/dashboard/tests/test_watch.py build/dashboard/tests/test_balance_history.py
python3 -m coverage run --branch -m pytest -q build/dashboard/tests/test_watch.py build/dashboard/tests/test_balance_history.py
python3 -m coverage report -m build/dashboard/parity/reference/watch.py build/dashboard/parity/reference/balance_history.py
```

Result: 39 tests passed. Branch coverage was 94% for `watch.py`, 100% for
`balance_history.py`, and 95% combined. Source and test hashes are recorded in
`baseline.json`.

The Go runtime deliberately tightens legacy behavior: saved wallet views and all
wallet mutations require a configured dashboard password, and descriptors containing
private keys are rejected before persistence. Existing private-bearing roster files are secured,
preserved, hidden from the API, and locked against changes until the operator removes
the private material out of band. The Go runtime requires dashboard authentication
on LAN installations too; the HTTP contracts account for this difference from the
frozen Python reference. Core wallet reuse verifies the imported public
descriptors; changing a key under the same label cannot read the old key's balance.

The Go runtime also supports public-only balance scans on pruned nodes, where
the frozen Python reference rejects new wallet entries. The pruned HTTP
contract checks that intentional difference; the Go path must return a cached,
confirmed UTXO balance without creating or importing a Core wallet. The other
contracts continue to cover shared behavior.
