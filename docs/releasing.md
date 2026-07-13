# Releasing Vivero

This is the maintainer runbook for tagged releases.

## Before tagging

Start from a clean `main`:

```sh
git checkout main
git pull --ff-only origin main
git status --short --branch
```

Run the local confidence ladder that proves the release path, not just unit tests:

```sh
make certify
make live-cloud-browser-smoke
```

`make certify` runs the deterministic pre-release ladder: audit, canonical example E2E, Docker and real Compose integration fixtures, nasty integration fixtures, example config validation, and release package smoke. `make audit` runs the local quality ratchet inside that ladder: formatting, vet, tests, race tests, coverage, staticcheck, dead-code checks, stale-marker scans, script-reference checks, ignored-artifact checks, and package-boundary checks.

`make live-cloud-browser-smoke` is intentionally not required on every PR. It is now a required tag gate in the Release workflow; run it locally before cutting a release when Docker, `cloudflared`, npm/Playwright, Chrome, and network access are available.

## Cut the tag

Use SemVer tags with a leading `v`:

```sh
version=v0.1.1
git tag -a "$version" -m "Vivero $version"
git push origin "$version"
```

The tag triggers the Release workflow. It first verifies the tag points at current `origin/main`, then runs live cloud/browser smoke with Chrome, Playwright, and Cloudflare quick tunnels. Only after that gate passes does it install GoReleaser, run `make certify`, build the GoReleaser archives, smoke the packaged binary, publish the GitHub release, render and upload `vivero.rb`, generate and upload `vivero_sbom.spdx.json`, create GitHub artifact attestations, and publish the formula to `gianfrancopiana/homebrew-tap`. A macOS postflight job then verifies the published installer and Homebrew surfaces from the user-facing release assets.

## Watch the workflow

```sh
gh run list --repo gianfrancopiana/vivero --workflow Release --limit 5
gh run watch <run-id> --repo gianfrancopiana/vivero --exit-status
```

If the workflow fails before publishing a release, fix the issue on `main`, delete the failed local/remote tag, and retag the fixed commit:

```sh
git tag -d "$version"
git push origin ":refs/tags/$version"
```

If a GitHub release was already published, do not silently reuse the same tag. Either delete the failed draft/release and tag with a fixed commit before users can consume it, or cut the next patch version.

## Postflight

After the workflow succeeds, verify the exact surfaces users install:

```sh
GH_CLI=gh scripts/release-postflight.sh v0.1.1
```

Use opt-in flags for side-effecting or Docker-backed checks:

```sh
GH_CLI=gh scripts/release-postflight.sh v0.1.1 --example-e2e --install-homebrew
```

`--example-e2e` runs the certified `examples/agent-demo` preview E2E with the checksum-installed release binary by setting `VIVERO_BIN` for `scripts/example-e2e.sh`. `--install-homebrew` mutates the maintainer machine by installing or reinstalling `gianfrancopiana/tap/vivero`; use it on a disposable CI runner or a machine where replacing the local Vivero formula is acceptable.

The postflight script checks:

- GitHub release metadata.
- Required release assets are present: all macOS/Linux archives, `checksums.txt`, `vivero.rb`, and `vivero_sbom.spdx.json`.
- Downloaded archive checksums against `checksums.txt`.
- The SPDX SBOM structure, root package version, dependency package list, and release-asset SHA256 values.
- GitHub artifact attestations for the host archive, `checksums.txt`, `vivero.rb`, and `vivero_sbom.spdx.json`.
- The checksum-verifying installer into a temporary bin dir.
- The certified preview E2E with that installed binary when `--example-e2e` is passed.
- The Homebrew tap formula version.
- The Homebrew-installed binary when `--install-homebrew` is passed.

For a read-only Homebrew check, omit `--install-homebrew`. The script still calls `brew info`, so Homebrew may update local tap metadata according to the user's Homebrew configuration.

You can also run it through Make:

```sh
GH_CLI=gh VERSION=v0.1.1 make release-postflight
GH_CLI=gh VERSION=v0.1.1 RELEASE_POSTFLIGHT_FLAGS="--example-e2e --install-homebrew" make release-postflight
```

## Smoke the released binary

For a release intended to be called production-ready, run a preview E2E with the released binary, not `go build`:

```sh
GH_CLI=gh VERSION=v0.1.1 RELEASE_POSTFLIGHT_FLAGS="--example-e2e" make release-postflight
```

`scripts/release-postflight.sh --example-e2e` installs the release via the checksum-verifying installer into a temporary bin dir, then runs `scripts/example-e2e.sh` through `VIVERO_BIN=<temp>/vivero`. This proves the installable artifact without rebuilding from the local checkout. Add `--install-homebrew` only when you also want to mutate-check the tap-installed binary.

## Upgrade cadence

- Patch release: bug fix, docs fix, packaging fix, or small compatibility improvement.
- Minor release: new CLI surface, config schema addition, or behavior that needs release notes.
- Major release: breaking CLI/config/schema behavior.

For minor releases that touch app-operations behavior, include release notes for the preview/evidence lane contract, build cache config, cache commands, warm volume/cache evidence, and compatibility aliases. Keep the notes clear that app repos still own Dockerfiles, scripts, secrets, and provider-specific infrastructure.

Every release should leave these true:

- `brew install gianfrancopiana/tap/vivero` installs the tagged version.
- `scripts/install.sh --version <tag>` installs the tagged version.
- `vivero version --json --no-input` shows version, commit, and build date.
- `checksums.txt` verifies all archives.
- GitHub attestations verify for the host archive, `checksums.txt`, `vivero.rb`, and `vivero_sbom.spdx.json`.
- `vivero_sbom.spdx.json` validates with `scripts/verify-release-sbom.py` and matches the published release assets.
