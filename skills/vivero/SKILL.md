---
name: vivero
version: 0.1.0
vivero_cli: 0.1.0
schema: 1
license: MIT
description: >
  Use the `vivero` CLI to create, inspect, iterate on, verify, QA, and tear down local preview environments, or to plan/apply app-owned deploys and inspect release evidence. Trigger when a task needs a running app preview, a URL that has passed health checks, Docker-compatible container exec/logs/screenshots, seed-backed app state, live source iteration through a Git worktree, project-specific browser QA context, or deploy/release plan/status/events/logs/smoke/rollback. Do not trigger for general issue or PR management unless a running preview or release operation is also needed; Vivero owns app runtime/release orchestration, not PR, CI, or chat workflow.
---

# Vivero CLI

Use `vivero` as a local-first runtime for preview environments. It runs services through a Docker-compatible engine such as Docker Desktop or OrbStack.

## Mental model

An agent supplies intent. Vivero prepares the preview.

```text
Agent:
  Chooses project, source refs or paths, checks, and teardown policy.

Vivero:
  Loads project config, prepares sources, starts services through the container
  engine, injects secrets, waits for health, exposes URLs, records events,
  captures evidence, and tears down safely.
```

Vivero is project-agnostic. Project-specific routes, selectors, restart commands, QA scopes, and browser flows belong in `vivero.yml`, not in this generic skill. Keep `vivero.yml` as thin orchestration metadata: do not copy Dockerfiles, compose files, env contracts, or setup scripts into YAML when the app repo already owns them. Reference app-owned images, Dockerfiles, or prebuild commands instead. Inline Dockerfiles are intentionally unsupported.

Vivero has separate preview and deploy/release lanes. Production operations must use the deploy/release namespace: run `vivero doctor production` first, then `vivero deploy plan`, and only apply a non-blocked plan. Deploy implementation belongs to app-owned commands configured under `deploy.environments` in `vivero.yml`; do not overload preview `up`/`down` or quick tunnels for production. For `strategy: blue-green`, Vivero models slots and enforces prepare → smoke → promote before recording the new live slot.

## First checks

Before operating on an unfamiliar install or project, inspect the live contract:

```sh
vivero capabilities --json --no-input
vivero commands --json --no-input
vivero doctor --json --no-input
vivero doctor config <project-path> --json --no-input
vivero doctor production --project <project-path> --json --no-input
vivero deploy plan <project-path> --environment production --json --no-input
vivero project inspect <project> --json --no-input
vivero skill doctor --json --no-input
```

Use project inspection to learn the available sources, services, profiles, health checks, smoke tests, useful routes, QA scopes, screenshot breakpoints, artifact paths, restart commands, dependency volume lifetimes, and setup-step policies.

## Production deploy strategy notes

- Omitted `strategy` means the default app-owned command strategy: Vivero runs `applyCommand`, optional `smokeCommand`, optional `statusCommand`, and `rollbackCommand` from `deploy.environments.<env>`. If `smokeCommand` is set, deploy apply must pass smoke before the release becomes current.
- `strategy: blue-green` expects `deploy.environments.<env>.blueGreen` with exactly two slots, `activeSlotCommand`, `prepareCommand`, required `smokeCommand`, `promoteCommand`, optional `statusCommand`, and `rollbackCommand`.
- Blue/green apply runs `prepareCommand`, then `smokeCommand`, then `promoteCommand`. If smoke fails, Vivero exits before promote and records only release history, not a new current release.
- Use `release events release:<id>` and `release logs release:<id>` for release-scoped debugging evidence; use `release smoke <project>` to rerun the configured current-release smoke gate.
- Blue/green commands receive `VIVERO_BLUE_GREEN_ACTIVE_SLOT`, `VIVERO_BLUE_GREEN_TARGET_SLOT`, `VIVERO_BLUE_GREEN_PREVIOUS_SLOT`, `VIVERO_BLUE_GREEN_SLOTS`, `VIVERO_DEPLOY_PLAN_ID`, and `VIVERO_RELEASE_ID`.
- Use `release status` after apply and `release rollback <project> <release-id>` for status checks and slot-aware rollback; do not manually edit Vivero release state.
- Deploy/release state is versioned and audited. Treat `planId`, `releaseId`, `stateVersion`, audit events, and command-output artifacts as the durable contract for recovery/debugging.
- Production apply/rollback is guarded by project/environment locks and idempotency checks. If an operation is already applied or rolled back, prefer re-reading status/history over rerunning app-owned commands manually.

## Repo quality gates

Before claiming a Vivero runtime/release change is ready, run the focused gate for the surface you touched and then the repo gate:

```sh
make verify
make cover
make example-e2e
make integration-fixtures
make nasty-integration-fixtures
make dogfood-configs
make deploy-fixtures
make release-smoke
```

- `make cover` enforces the coverage ratchet (`COVER_MIN`, default 72.0).
- `make nasty-integration-fixtures` covers messy project shapes: static-only, app+database, monorepo Dockerfile, warm volumes, profiles, named tunnels, invalid public routes, and fingerprint-path failures.
- `make dogfood-configs` validates committed examples plus live Helper/Flexile/Chetear/self checkouts when present under `VIVERO_DOGFOOD_ROOT`.
- `make deploy-fixtures` proves deploy plan/apply/status/rollback, idempotency, audit records, locks, and blue/green prepare/smoke/promote/rollback.
- `make release-smoke` builds snapshot artifacts, verifies `checksums.txt`, extracts the host archive, checks `vivero version --json` provenance, and validates packaged config examples.

## Agent invariants

- Pass `--json` when consuming command output programmatically.
- Pass `--no-input` so commands fail instead of blocking for prompts.
- Use `--quiet` when progress text is not needed.
- Use `--wait --timeout <duration>` when readiness matters.
- Prefer exact commit SHAs or explicit local source paths.
- Use stable preview IDs, usually `<project>-<purpose>` or `<project>-pr<id>`.
- Use `--metadata branch=<name>` or `--metadata ref=<sha-or-ref>` when smart warm volumes need baseline-vs-branch behavior.
- Use `--label KEY=VALUE` for caller-owned bookkeeping only; do not put secrets in labels.
- Pass `--profile <name>` when the project has profiles and the task needs a non-default service set. If `profiles.default` exists, omitted `--profile` uses it.
- Never announce a preview URL until `vivero up` or `vivero inspect` reports the relevant service healthy. The contract is **URL = works**.
- Mutate source through Vivero-managed host worktrees or explicit external paths, not by editing container files.
- Use `vivero inspect`, `vivero events`, and `vivero logs` before guessing why a preview failed.
- Before tearing down, check whether managed worktrees are dirty. Do not destroy dirty work unless committing, archiving a patch, keeping the worktree, or explicitly discarding.
- Treat secret values as write-only. Keep them out of logs, events, URLs, labels, comments, and command history where possible.

## Common flow: run a preview

```sh
vivero up webapp \
  --id webapp-pr42 \
  --source app.ref=<exact-source-sha> \
  --wait --timeout 5m \
  --json --no-input --quiet
```

For an existing checkout, override the source path:

```sh
vivero up webapp \
  --id webapp-local \
  --source app.path=/path/to/webapp \
  --wait --timeout 5m \
  --json --no-input --quiet
```

For a non-default profile, add `--profile`:

```sh
vivero up helper-host-products \
  --id helper-gumroad \
  --profile gumroad \
  --source helper.path=/path/to/helper \
  --source gumroad.path=/path/to/gumroad \
  --wait --timeout 5m \
  --json --no-input --quiet
```

Profiles should keep the default boring. For a helper-style app, make `profiles.default` run only the app's local clone shape, then add explicit host-product profiles such as `gumroad` or `flexile`. Use `serviceEnv` to point the helper service at the selected host product service by Docker service name, for example `GUMROAD_URL=http://gumroad-web:3310`.

Expected shape:

```json
{
  "preview": {
    "id": "webapp-local",
    "project": "webapp",
    "profile": "default",
    "status": "running",
    "services": {
      "web": {
        "status": "healthy",
        "url": "http://127.0.0.1:3000"
      }
    }
  }
}
```

If the preview is not running, inspect before retrying:

```sh
vivero inspect webapp-local --json --no-input
vivero events webapp-local --tail --json --no-input
vivero logs webapp-local <service> --since 10m --json --no-input
```

## Warm dependency volumes and setup policy

Project config can make expensive container volumes reusable:

```yaml
warm:
  baselineRefs: [main]
  fingerprint:
    paths:
      - Gemfile.lock
      - package-lock.json
      - db/migrate
services:
  web:
    dependencyVolumes:
      - name: bundle_path
        target: /bundle_path
        lifetime: smart
      - name: node_modules
        target: /app/node_modules
        lifetime: smart
setup:
  afterSeeds:
    - service: web
      policy: once-per-fingerprint
      command: bundle install && npm install
      fingerprint:
        paths:
          - Gemfile.lock
          - package-lock.json
    - service: web
      policy: once-per-project
      command: bundle exec rails db:seed
resources:
  maxStartupConcurrency: 4
```

- `lifetime: preview` is the default and is removed by `vivero down --discard`.
- `lifetime: project` survives preview teardown and is shared by all previews of the project.
- `lifetime: smart` lets baseline refs update a canonical warm volume while branch previews receive copied preview-local volumes. Use this for branch-sensitive DB/index/cache state.
- `warm.fingerprint.paths` should list project-relative lockfiles, migrations, schemas, and seed files that invalidate smart warm setup markers and can also be reused by `once-per-fingerprint` setup steps.
- `policy: once-per-project` skips a setup command after it has succeeded once for the matching project/config step. With smart volumes, markers are fingerprint-aware so changed migrations or lockfiles rerun setup on the affected warm volume.
- `policy: once-per-fingerprint` skips only when the same service, command, selected fingerprint paths, and selected path contents already succeeded. Put explicit paths under `setup.afterSeeds[].fingerprint.paths`, or let it fall back to `warm.fingerprint.paths`. Use it only when the target service writes durable setup output to a `project` or `smart` dependency volume; runtime rejects it otherwise.
- Set `resources.maxStartupConcurrency` to bound service startup parallelism. `0` means Vivero's conservative default (`4`), `1` keeps sequential startup, and larger values are capped by service count. Vivero starts backing services in a bounded batch, runs setup sequentially, then starts app services in a bounded batch.
- Avoid running two baseline previews for the same project at once; they use the same canonical smart volumes. For throwaway checks against a local path while a baseline preview is already running, pass a non-baseline `--metadata ref=<check-name>` or a branch `--source <source>.ref=...` so Vivero creates preview-local derived volumes.

## Project-local configs from examples

When the caller wants to run a real local checkout from a bundled example, copy the example to a local config directory, rewrite only project/source identity, then sync that directory:

```sh
mkdir -p ~/.vivero/configs/<preview-project>
cp examples/<example>/vivero.yml ~/.vivero/configs/<preview-project>/vivero.yml
python3 - <<'PY'
from pathlib import Path
p = Path.home() / '.vivero/configs/<preview-project>/vivero.yml'
s = p.read_text()
s = s.replace('name: <example-project>', 'name: <preview-project>', 1)
s = s.replace('path: ~/src/<repo>', 'path: /absolute/path/to/<repo>')
p.write_text(s)
PY
vivero projects sync ~/.vivero/configs/<preview-project> --json --no-input
```

Keep the example's service graph, profiles, dependency volume lifetimes, setup policies, health checks, smoke tests, and QA routes intact. Rewrite only project/source identity and other local path/ref values. Do not copy runtime facts from an app-owned Dockerfile, compose file, Makefile, or env contract into long-lived YAML just to make an agent-generated config feel complete. For real app previews, prefer `build.dockerfile` pointing at an existing app Dockerfile when it can build the preview image directly, or `image`/`prebuild` pointing at an app-owned build output. Inline Dockerfiles are unsupported; move that content into the app repo first. For coupled previews, put each app under `services`, use `profiles:` to choose which services/backing services/smoke tests are active, apply profile-specific service env through `serviceEnv`, and use service names, such as `http://app-web:3000`, for container-to-container URLs. Use `--source <source>.path=...` or `--source <source>.ref=...` at `vivero up` time when only the checkout/ref changes.

## Live iteration

Sync a changed file into the source used by the running preview:

```sh
vivero sync webapp-local app src/components/Header.tsx \
  --from ./src/components/Header.tsx \
  --json --no-input --quiet
```

Remove a file explicitly:

```sh
vivero rm webapp-local app src/old-file.ts \
  --json --no-input --quiet
```

Inspect the preview worktree diff:

```sh
vivero diff webapp-local app --json --no-input
```

Commit with Git from the source path returned by `vivero inspect` or `vivero up`.

## Verification

Run a command in a service:

```sh
vivero exec webapp-local web --json --no-input -- npm test -- --runInBand
```

View logs:

```sh
vivero logs webapp-local web --since 10m --json --no-input
```

Take a viewport screenshot:

```sh
vivero screenshot webapp-local web / \
  --width 1280 --height 800 \
  --json --no-input --quiet
```

Capture declared breakpoints from project config:

```sh
vivero screenshot webapp-local web /dashboard \
  --breakpoints \
  --json --no-input --quiet
```

Screenshots default to exact viewport capture. Add `--crop` only when whitespace trimming is more useful than true viewport evidence. Screenshots default to the local/proxy preview URL for speed; add `--public` or `--target public` only when public tunnel screenshot evidence is required. Recordings use the local/proxy preview URL.

Run project-declared smoke tests:

```sh
vivero smoke webapp-local --json --no-input --quiet
```

## QA flow

Ask Vivero for the QA plan first:

```sh
vivero qa plan webapp-local --scope public --json --no-input --quiet
```

Treat the plan JSON as the source of truth for:

- services and URLs;
- pages and flows;
- checks and severities;
- browser driver preference;
- artifact paths;
- screenshot and recording commands derived from `agent.qa.evidence`;
- optional authenticated QA context from `agent.qa.auth.sessions`, including generated `--storage-state` flags for scoped evidence commands.

Run deterministic Vivero-owned QA:

```sh
vivero qa run webapp-local --scope public --json --no-input --quiet
```

This runs smoke tests, captures declared page screenshots at project breakpoints and configured color schemes, and writes a report scaffold.

Record declared QA flows as browser video evidence through the local/proxy preview URL:

```sh
vivero qa record webapp-local --scope public --json --no-input --quiet
```

For reproducible evidence, use the Playwright-backed commands and driver metadata from `vivero qa plan`; use `evidence.recordings.commands` for recordings. Use Chrome MCP or another live browser driver only for exploratory debugging. Do not hardcode project-specific routes, selectors, breakpoints, color schemes, or flows in this generic skill.

For authenticated QA, the app/operator provides a project-relative Playwright storage-state file under `agent.qa.auth.sessions.<name>.storageState` and attaches it to scopes with either `scopes: [...]` on the session or `authSession: <name>` on the QA scope. Vivero includes the resolved storage state in `qa plan` and generated screenshot/recording commands, but it does not store credentials or run app-specific login flows unless the app declares those commands in its own `vivero.yml`.

After QA, generate or refresh the report scaffold:

```sh
vivero qa report webapp-local --out qa/report.md --json --no-input --quiet
```

For handoff or release evidence, run the final proof command:

```sh
vivero qa final webapp-local --scope public --json --no-input --quiet
```

Use the produced artifacts as evidence wherever the calling workflow needs them.

## Teardown

Clean teardown when no live edits need saving:

```sh
vivero down webapp-local --discard --json --no-input --quiet
```

Safer teardown when edits might exist:

```sh
vivero inspect webapp-local --json --no-input
vivero down webapp-local --archive-patch --json --no-input --quiet
```

Use `--keep-worktree` when source should remain available after containers are gone.

## Secrets

Set or unset secrets only when required for the project to run:

```sh
vivero secrets set webapp API_TOKEN=<value> --json --no-input --quiet
vivero secrets list webapp --json --no-input
vivero secrets unset webapp API_TOKEN --json --no-input --quiet
```

Never attempt to print secret values. `vivero secrets list` returns keys only.

## Failure checklist

When a preview fails:

1. Run `vivero inspect <preview-id> --json --no-input`.
2. Run `vivero diagnose startup <preview-id> --json --no-input` to identify slow startup phases and the first failure without exposing secret-looking metadata.
3. Run `vivero events <preview-id> --tail --json --no-input` for the raw event stream.
4. Check service logs with `vivero logs`.
5. Confirm source paths and refs in the preview record.
6. Confirm secrets are present by key, not by value.
7. Re-run smoke or QA only after the underlying runtime issue is clear.
