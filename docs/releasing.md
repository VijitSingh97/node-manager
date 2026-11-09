# Releasing

How this stack is versioned and released. The pipeline is
[`scripts/release.sh`](../scripts/release.sh) plus the
[Release workflow](../.github/workflows/release.yml); preview any release
with `scripts/release.sh --dry-run`.

## One product, one version

The stack is released as a single product. The components are upstream
projects pinned by digest — Bitcoin Core (`bitcoin/bitcoin` in
`docker-compose.yml`), the tor and dashboard base images (Dockerfiles),
  and the Go build toolchain. The first-party code is the dashboard, the
orchestration (`docker-compose.yml`, `configure.sh`, `./stack`), and the
configs. A release is one tag with one changelog and one upgrade path.

The pins are the ingredients manifest of each release: bumping any of
them — including a Bitcoin Core security update — is a normal release
(bump the pin, cut a patch, the gate re-tests the composed set).

Multi-arch images (amd64 and arm64/Raspberry Pi) for seven first-party runtime
components — `tor`, `dashboard`, `monero`, `tari-wallet`, `litecoin`,
`dogecoin`, and `bitcoincash` — are built and published to GHCR
(`ghcr.io/vijitsingh97/node-manager-*`) per release by the `images`
job in the [Release workflow](../.github/workflows/release.yml). Bitcoin Core,
the Tari base node, and Zakura use pinned upstream images rather than duplicate
first-party builds.
`./stack pull` downloads these tags with the host's Docker settings;
`./stack up` starts only locally available images and never builds implicitly.
The release artifact is also a source tarball. Source builds need a controlled
build environment with its own Tor egress; see [Privacy](privacy.md).

## Single source of truth

The version lives in the top-level [`VERSION`](../VERSION) file: plain
text, one line, [SemVer](https://semver.org/). Nothing else hardcodes it —
`configure.sh` renders it into `.env`, the dashboard's update checker
compares it against the latest GitHub release, and the release workflow
refuses a tag that disagrees with it.

Versioning rules:

- **Patch** — fixes and pin bumps, no config changes.
- **Minor** — new features and new (defaulted) `config.json` keys.
- **Major** — anything that breaks an existing deployment: removed or
  renamed config keys, subnet changes, data-directory migrations.

## Cutting a release

1. Land the changes on `main` through a PR, including in the same PR:
   - bump `VERSION`
   - add the `## [X.Y.Z]` section to `CHANGELOG.md` (with its link at the
     bottom of the file)
2. From an up-to-date `main`:

   ```bash
   scripts/release.sh --dry-run   # preflight + preview the notes
   scripts/release.sh             # tag vX.Y.Z and push it
   ```

   Preflight blocks on: dirty tree, invalid semver, an already-existing
   tag (local or remote), or a missing changelog section. It warns (but
   does not block) on a non-`main` branch or red CI — the workflow gate
   re-runs everything on the tag regardless.

3. The tag push triggers the [Release workflow](../.github/workflows/release.yml):
   the **full CI suite** (shellcheck, unit tests, compose contract, e2e
   boot with the egress audit) runs against the tagged commit, and only
   then is the GitHub Release published — title, notes extracted from the
   changelog section, and a `node-manager.tar.gz` source tarball
   (`git archive`, filtered by `.gitattributes`).

The tarball contains runtime source and Docker inputs, configuration, the license,
security policy, changelog, and user guides. Tests, Python parity references,
benchmark experiments, screenshots, maintainer scripts, and development metadata
stay in Git. The production dashboard image contains the compiled Go binary and
runtime web assets; test modules are excluded from the Docker build context.

Nothing is published until the gate is green. A red gate leaves the tag
without a release — fix on `main`, bump the patch version, tag again
(release tags are protected from deletion and rewrite by a repo ruleset).

## APT feed

Version `v1.32.0` is the first APT release. Its dedicated public-key fingerprint
is committed as `72FD656754BD7534E274360A37070C2730C0B4EA`. Before tagging it,
confirm all of the following repository settings:

1. `APT_SIGNING_KEY` contains the armored private key for that fingerprint.
2. `APT_SIGNING_FINGERPRINT` contains the exact 40-hex fingerprint above.
3. GitHub Pages uses **GitHub Actions** as its deployment source for the renamed
   `node-manager` repository.
4. The `github-pages` environment permits the release workflow to deploy.

The APT job waits for both the source release and all multi-architecture images.
It resolves each first-party manifest digest and bakes those exact references into
the signed Debian package. Local package builds require a digest map:

```bash
scripts/resolve-runtime-images.sh > /tmp/node-manager-runtime-images.json
RUNTIME_IMAGE_DIGESTS_FILE=/tmp/node-manager-runtime-images.json scripts/build-deb.sh
```

A first installation remains inert. On an actual version upgrade, the package's
`postinst` activates images only for an initialized, running package deployment.
It preserves approved settings, data, volumes, and deliberately stopped services.
Activation failures are reported for retry with `node-manager package-upgrade`;
Docker or registry outages do not leave dpkg unconfigured.
It builds the architecture-independent package, creates and signs the feed,
verifies that feed with an isolated APT client, uploads the package and checksum
to the GitHub Release, and deploys the feed to Pages. Do not advertise APT
availability until that Pages deployment succeeds.

The static feed omits `Valid-Until`, so releases remain installable between
releases but old signed snapshots do not expire. Adding an expiry requires
monitored periodic re-signing so an idle project does not break every user's
`apt update`.

## How users get a release

- **Tarball:** obtain and unpack `node-manager.tar.gz`, then run
  `./stack init` to download missing images and start.
- **APT:** after the signed Pages deployment succeeds, install the package from
  `https://vijitsingh97.github.io/node-manager` and run
  `sudo node-manager init` explicitly.
- **Homebrew:** after the generated formula PR is merged, install from the
  legacy-named `vijitsingh97/node-manager` tap, whose explicit remote
  is the canonical `VijitSingh97/node-manager` repository.
- **Git:** users select the published release tag themselves, then pull its
  images and apply it. Source builds stay in a separately controlled environment.
- Running stacks learn about new releases from the dashboard's daily
  update check (badge + optional Telegram 🆕 alert) — see
  [Notifications](notifications.md). Notices link to manual APT, Homebrew, and
  GitHub update instructions; the application has no self-updater.

## Homebrew formula

The release uploads a generated `node-manager.rb` beside the runtime
archive. The formula pins that exact archive's SHA-256; it is not a development
checkout. After both the release and its multi-architecture container images
publish, the developer bot opens a PR updating `Formula/node-manager.rb`
on `main`. The release is **not available through Homebrew until that PR is
reviewed and merged**.
The job uses the same `DEVELOPER_BOT_APP_ID` variable (or secret) and
`DEVELOPER_BOT_PRIVATE_KEY` secret as dependency updates; until configured,
the formula asset is still generated but no tap-update PR is opened.

To generate the same formula manually from a downloaded release archive:

```bash
scripts/build-homebrew-formula.sh node-manager.tar.gz
```

The output is `dist/Formula/node-manager.rb`. Never point a public
formula at a local test archive or publish a new formula before its images exist.
