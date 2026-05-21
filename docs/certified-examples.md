# Certified examples

Certified examples are committed fixtures that CI or local Make targets prove. If an example appears in the README as a real path, it must either be covered here or be clearly marked illustrative.

## Golden path fixture

- Path: `examples/agent-demo`
- Shape: web app
- Proves: config doctor, project sync, Docker preview, health wait, `qa final`, startup diagnosis, teardown, and clean tracked files
- Gate: `make example-e2e`

Use it when you want the smallest complete preview loop:

```sh
vivero projects sync examples/agent-demo --json --no-input
vivero preview up agent-demo --id agent-demo-local --wait --timeout 3m --json --no-input
vivero qa final agent-demo-local --scope smoke --no-record --no-screenshots --json --no-input
vivero preview down agent-demo-local --discard --json --no-input
```

## Runtime lifecycle fixtures

- Path: `examples/integration-stack`
- Shape: app + database-style backing service with smart warm volume behavior
- Proves: backing service health before app startup, app service networking, warm baseline/derived volumes, setup skip policy, final QA proof paths, and cleanup
- Gate: `make integration-fixtures`

- Path: `examples/nasty-integration`
- Shapes: static-only, app + database, monorepo app-owned Dockerfile, named public route planning, invalid route rejection, warm volumes, and cleanup/routing edge cases
- Proves: messy preview config patterns that tend to break before real users do
- Gate: `make nasty-integration-fixtures`

The `examples/nasty-integration` profiles are intentionally explicit:

- `static-only`: static web app
- `app-with-db`: app + database
- `monorepo`: monorepo app-owned Dockerfile
- `full`: combined matrix

## Deploy/release fixtures

- Path: `examples/deploy-command`
- Shape: command deploy
- Proves: `deploy plan`, `deploy apply`, smoke gating, idempotent reapply, `release status`, `release events`, `release logs`, `release smoke`, and rollback
- Gate: `make deploy-fixtures`

- Path: `examples/deploy-blue-green`
- Shape: blue/green deploy
- Proves: active-slot discovery, target-slot planning, prepare/smoke/promote phases, status evidence, and rollback to previous slot
- Gate: `make deploy-fixtures`

Both deploy examples use fake registry images and app-owned shell scripts. They do not provision production infrastructure; they exercise Vivero's release state machine and command contract.

## Dogfood examples

- Path: `examples/helper-host-products`
- Shape: host app with product-specific profiles
- Gate: `make dogfood-configs`

- Path: `examples/gumroad`
- Shape: real-product-style app config that references app-owned runtime assets
- Gate: `make dogfood-configs`

These are config-quality fixtures. They should keep project-specific selectors, routes, scripts, and runtime ownership in `vivero.yml` or app repos, not in Vivero core.

## Full local proof ladder

```sh
make verify
make example-e2e
make integration-fixtures
make nasty-integration-fixtures
make dogfood-configs
make deploy-fixtures
```

For release/package confidence, add:

```sh
make release-smoke
```

For external runtime confidence, run the live cloud/browser smoke manually or through the scheduled workflow:

```sh
make live-cloud-browser-smoke
```

## Certification rule

- README golden-path commands use certified examples.
- Certified examples load through `loadProjectConfig` tests.
- Preview examples are covered by fixture scripts.
- Deploy examples are copied into temporary projects and exercised by `make deploy-fixtures`.
- Any new documented example must name its gate here before it is treated as certified.
