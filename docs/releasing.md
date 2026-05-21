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
make verify
make release-smoke
make example-e2e
make live-cloud-browser-smoke
```

`make live-cloud-browser-smoke` is intentionally not required on every PR. Run it before cutting a release when Docker, `cloudflared`, npm/Playwright, Chrome, and network access are available.

## Cut the tag

Use SemVer tags with a leading `v`:

```sh
version=v0.1.1
git tag -a "$version" -m "Vivero $version"
git push origin "$version"
```

The tag triggers the Release workflow. It runs `make verify`, builds the GoReleaser archives, smokes the packaged binary, publishes the GitHub release, renders and uploads `vivero.rb`, creates GitHub artifact attestations, and publishes the formula to `gianfrancopiana/homebrew-tap`.

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
GH_CLI=gh scripts/release-postflight.sh v0.1.1 --install-homebrew
```

`--install-homebrew` mutates the maintainer machine by installing or reinstalling `gianfrancopiana/tap/vivero`; use it on a disposable CI runner or a machine where replacing the local Vivero formula is acceptable.

The postflight script checks:

- GitHub release metadata.
- Required release assets are present: all macOS/Linux archives, `checksums.txt`, and `vivero.rb`.
- Downloaded archive checksums against `checksums.txt`.
- GitHub artifact attestations for the host archive, `checksums.txt`, and `vivero.rb`.
- The checksum-verifying installer into a temporary bin dir.
- The Homebrew tap formula version.
- The Homebrew-installed binary when `--install-homebrew` is passed.

For a read-only Homebrew check, omit `--install-homebrew`. The script still calls `brew info`, so Homebrew may update local tap metadata according to the user's Homebrew configuration.

You can also run it through Make:

```sh
GH_CLI=gh VERSION=v0.1.1 make release-postflight
```

## Smoke the released binary

For a release intended to be called production-ready, also run a preview E2E with the released binary, not `go build`:

```sh
brew reinstall gianfrancopiana/tap/vivero
vivero version --json --no-input
make example-e2e
```

`make example-e2e` currently builds the local binary before running. If you need to prove the installed release binary specifically, use the commands from `scripts/example-e2e.sh` with `$(brew --prefix gianfrancopiana/tap/vivero)/bin/vivero` in a temporary `HOME`/`VIVERO_HOME`.

## Upgrade cadence

- Patch release: bug fix, docs fix, packaging fix, or small compatibility improvement.
- Minor release: new CLI surface, config schema addition, or behavior that needs release notes.
- Major release: breaking CLI/config/schema behavior.

Every release should leave these true:

- `brew install gianfrancopiana/tap/vivero` installs the tagged version.
- `scripts/install.sh --version <tag>` installs the tagged version.
- `vivero version --json --no-input` shows version, commit, and build date.
- `checksums.txt` verifies all archives.
- GitHub attestations verify for the host archive, `checksums.txt`, and `vivero.rb`.
