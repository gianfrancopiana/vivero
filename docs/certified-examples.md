# Certified examples

Certified examples are committed fixtures that CI or local Make targets prove. If an example appears in the README as a real path, it must either be covered here or be clearly marked illustrative.

## Golden path fixture

- Path: `examples/agent-demo`
- Shape: web app
- Proves: config doctor, project sync, Docker preview, health wait, `vivero preview qa final`, startup diagnosis, teardown, and clean tracked files
- Gate: `make example-e2e`

Use it when you want the smallest complete preview loop:

```sh
vivero projects sync examples/agent-demo --json --no-input
vivero preview up agent-demo --id agent-demo-local --wait --timeout 3m --json --no-input
vivero preview qa final preview:agent-demo-local --scope smoke --no-record --no-screenshots --json --no-input
vivero preview down agent-demo-local --discard --json --no-input
```

## Runtime lifecycle fixtures

- Path: `examples/integration-stack`
- Shape: app + database-style backing service with smart warm volume behavior
- Proves: backing service health before app startup, app service networking, warm baseline/derived volumes, setup skip policy, final QA proof paths, and cleanup
- Gate: `make integration-fixtures`

- Path: `examples/nasty-integration`
- Shapes: static-only, app + database, monorepo app-owned Dockerfile with explicit BuildKit build cache specs, named public route planning, invalid route rejection, warm volumes, and cleanup/routing edge cases
- Proves: messy preview config patterns that tend to break before real users do, including build cache config next to runtime dependency volumes
- Gate: `make nasty-integration-fixtures`

The `examples/nasty-integration` profiles are intentionally explicit:

- `static-only`: static web app
- `app-with-db`: app + database
- `monorepo`: monorepo app-owned Dockerfile
- `full`: combined matrix

## Tiny invariant fixture matrix

Keep the matrix tiny and invariant-led. A fixture earns its place only when it proves a behavior class that a frontier agent must be able to rely on in unfamiliar repos.

- **Preview invariants:** startup waits for health before URL handoff, source state stays isolated, Docker service names resolve on the preview network, configured public routes plan deterministically, warm volumes are visible, and teardown preserves or discards work intentionally. Covered by `make example-e2e`, `make integration-fixtures`, and `make nasty-integration-fixtures`.
- **Deploy/release invariants:** production doctor runs before plan/apply, plans are reviewable before side effects, app-owned prepare/apply/smoke/status/rollback commands get the expected environment, command execution has timeouts and capped/redacted output, release state is locked and auditable, failed smoke does not promote, and rollback keeps history consistent. Covered by `make deploy-fixtures`.
- **Evidence invariants:** every lane returns target-aware JSON with artifact paths for events, logs, screenshots, QA reports, recordings, and release command output. Preview evidence should work through `preview:<id>` targets, and release evidence should work through `release:<id>` targets.

Do not add a parallel example just because a framework is popular. Extend the smallest existing fixture unless the new case creates a distinct invariant failure mode.

## Deploy/release fixtures

- Path: `examples/deploy-command`
- Shape: command deploy
- Proves: `deploy plan`, `deploy apply`, `prepareCommand`, deploy cache hints, smoke gating, idempotent reapply, `release status`, `release events`, `release logs`, `release smoke`, and rollback
- Gate: `make deploy-fixtures`

- Path: `examples/deploy-blue-green`
- Shape: blue/green deploy
- Proves: active-slot discovery, target-slot planning, prepare/smoke/promote phases, status evidence, and rollback to previous slot
- Gate: `make deploy-fixtures`

Both deploy examples use fake registry images and app-owned shell scripts. They do not provision production infrastructure; they exercise Vivero's release state machine and command contract.

## Fast-path signals

Certified examples prove cache and evidence contracts by asserting deterministic state, not brittle wall-clock wins. When reading fixture output, look for:

- image build duration and whether the build used Docker's default cache or configured BuildKit cache specs;
- cache enabled/disabled fields for build cache, warm volumes, and app-owned setup/prebuild phases;
- `cache inspect` output showing configured build cache dirs, warm volumes, project volumes, and Vivero-tagged images;
- warm baseline/derived events that show when a baseline volume was reused or copied for a branch/ref;
- deploy phase durations for prepare, apply, smoke, status, promote, and rollback;
- artifact paths for logs, screenshots, QA reports, recordings, release command output, and final handoff JSON.

Fixture gates should assert those fields and paths directly. They should not claim a precise speedup unless the test controls the environment tightly enough to make timing meaningful.

## Dogfood examples

- Path: `examples/helper-host-products`
- Shape: host app with product-specific profiles
- Gate: `make dogfood-configs`

- Path: `examples/gumroad`
- Shape: real-product-style app config that references app-owned runtime assets
- Gate: `make dogfood-configs`

These are config-quality fixtures. They should keep project-specific selectors, routes, scripts, and runtime ownership in `vivero.yml` or app repos, not in Vivero core.

## Real-app dogfood

- Source: local `chetear.com` checkout copied into a temporary workspace
- Shape: Astro/Node app with Docker preview, target-aware preview evidence, app-owned command deploy, release smoke/status/logs/events, and rollback
- Gate: `make chetear-real-dogfood`

This target is maintainer-only because it depends on the private/local Chetear workspace. It skips when that checkout is unavailable unless `VIVERO_REAL_DOGFOOD_REQUIRE=1` is set. It is deliberately outside `make certify` so public CI remains deterministic, but it should be run before release-facing changes that claim real-app confidence.

## Full local proof ladder

```sh
make certify
```

`make certify` runs audit, canonical example E2E, integration fixtures, nasty integration fixtures, dogfood config validation, deploy fixtures, and release package smoke (`make release-smoke`). It is the deterministic pre-release certification target.

For external runtime confidence, run the live cloud/browser smoke manually or through the release/scheduled workflow:

```sh
make live-cloud-browser-smoke
```

For private maintainer confidence against a real app, run:

```sh
make chetear-real-dogfood
```

## Certification rule

- README golden-path commands use certified examples.
- Certified examples load through `loadProjectConfig` tests.
- Preview examples are covered by fixture scripts.
- Deploy examples are copied into temporary projects and exercised by `make deploy-fixtures`.
- Any new documented example must name its gate here before it is treated as certified.
