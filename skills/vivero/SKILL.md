---
name: vivero
version: 0.1.0
vivero_cli: 0.1.0
schema: 1
license: MIT
description: >
  Use the `vivero` CLI for agent-safe app operations: preview, evidence, deploy, verify, and rollback. Trigger when a task needs a running local preview, health-checked URLs, Docker-compatible exec/logs/screenshots, QA artifacts, source iteration through worktrees, app-owned deploy plans/applies, or release evidence and rollback. Vivero owns orchestration, safety gates, state, and evidence; app repos own Dockerfiles, deploy scripts, migrations, secrets, and infra behavior.
---

# Vivero CLI

Vivero is a local-first app-operations runtime for agents. It has two separate operating lanes:

- preview: safe, disposable environments for development and QA;
- deploy/release: app-owned production operations with Vivero plans, locks, audit records, evidence, and rollback handles.

Do not treat deploy as “preview, but public.” Production work must use the deploy/release commands and the app-owned deploy config in `vivero.yml`.

## Mental model

The agent chooses intent and reports evidence. Vivero executes the repeatable runtime contract.

```text
Agent:
  Chooses project, source ref/path, lane, checks, evidence target, and teardown policy.

Vivero:
  Loads thin project config, prepares sources, starts preview services, injects
  secret keys without printing values, waits for health, records events, captures
  evidence, runs app-owned deploy commands, records releases, and tears down safely.

App repo:
  Owns Dockerfiles, compose/build scripts, migrations, seed data, deploy scripts,
  secrets, provider behavior, and production infrastructure.
```

Keep `vivero.yml` as thin orchestration metadata. Put project-specific routes, selectors, restart commands, QA scopes, browser flows, deploy commands, and release smoke checks there. Reference app-owned images, Dockerfiles, or prebuild commands instead; do not copy Dockerfiles, compose files, env contracts, or setup scripts into YAML when the app repo already owns them. Inline Dockerfiles are intentionally unsupported.

## First checks

Before operating on an unfamiliar install or project, inspect the live contract and config health:

```sh
vivero capabilities --json --no-input
vivero commands --json --no-input
vivero doctor --json --no-input
vivero doctor config <project-path> --json --no-input
vivero doctor production --project <project-path> --json --no-input
vivero project inspect <project> --json --no-input
vivero skill doctor --json --no-input
```

Use `vivero commands --json --no-input` as the source of truth for command paths, flags, lanes, side effects, and examples. Use `vivero project inspect <project> --json --no-input` to learn sources, services, profiles, health checks, smoke tests, public routes, QA scopes, screenshot breakpoints, artifact paths, restart commands, dependency volume lifetimes, and setup policies.

For deploy work, always run production doctor before planning:

```sh
vivero doctor production --project <project-path> --json --no-input
vivero deploy plan <project-path> --environment production --json --no-input
```

## Choose the lane

| Lane | Use when | Primary commands | Safety rule |
| --- | --- | --- | --- |
| Preview lane | You need a running local app, live source iteration, service exec, diffs, or teardown. | `vivero preview up`, `vivero preview inspect`, `vivero preview wait`, `vivero preview exec`, `vivero preview sync`, `vivero preview diff`, `vivero preview down` | URL means healthy. Never announce a preview URL until inspect/up reports the service healthy. |
| Evidence/QA lane | You need logs, events, smoke, screenshots, recordings, QA reports, or startup diagnosis. | `vivero logs`, `vivero smoke`, `vivero screenshot`, `vivero qa plan`, `vivero qa run`, `vivero qa record`, `vivero qa final`, `vivero diagnose startup` | Report exact artifact paths and target refs. Do not substitute screenshots or manual browser notes for declared QA evidence. |
| Deploy/release lane | You need production readiness checks, deploy planning/apply, release status, release evidence, smoke, or rollback. | `vivero doctor production`, `vivero deploy plan`, `vivero deploy apply`, `vivero release status`, `vivero release events`, `vivero release logs`, `vivero release smoke`, `vivero release rollback` | Plan first. Apply/smoke/rollback can run app-owned commands and normally require human approval. |
| Support lane | You need CLI discovery, schema, project sync/inspect, skill freshness, or secret-key management. | `vivero capabilities`, `vivero commands`, `vivero schema`, `vivero doctor`, `vivero projects sync`, `vivero project inspect`, `vivero skill doctor`, `vivero secrets list` | Treat secrets as write-only. Use schema/doctor output before guessing. |

Prefer namespaced preview commands for new guidance. Root commands such as `vivero up`, `vivero inspect`, and `vivero down` remain compatibility aliases.

## Preview flow

Start with a stable preview ID, exact source refs or explicit local source paths, and readiness waiting:

```sh
vivero preview up webapp \
  --id webapp-pr42 \
  --source app.ref=<exact-source-sha> \
  --wait --timeout 5m \
  --json --no-input --quiet
```

For an existing checkout, override the source path:

```sh
vivero preview up webapp \
  --id webapp-local \
  --source app.path=/path/to/webapp \
  --wait --timeout 5m \
  --json --no-input --quiet
```

For a non-default profile, pass it explicitly:

```sh
vivero preview up helper-host-products \
  --id helper-gumroad \
  --profile gumroad \
  --source helper.path=/path/to/helper \
  --source gumroad.path=/path/to/gumroad \
  --wait --timeout 5m \
  --json --no-input --quiet
```

Inspect before reporting a URL or retrying a failed start:

```sh
vivero preview inspect webapp-local --json --no-input
vivero preview wait webapp-local --timeout 5m --json --no-input --quiet
vivero diagnose startup webapp-local --json --no-input
vivero preview events webapp-local --tail --json --no-input
vivero logs webapp-local web --since 10m --json --no-input
```

Live iteration should mutate source through Vivero-managed worktrees or explicit external paths, not by editing container files:

```sh
vivero preview sync webapp-local app src/components/Header.tsx \
  --from ./src/components/Header.tsx \
  --json --no-input --quiet
vivero preview diff webapp-local app --json --no-input
vivero preview exec webapp-local web --json --no-input -- npm test -- --runInBand
vivero preview rm webapp-local app src/old-file.ts --json --no-input --quiet
```

Profiles should keep the default boring. For helper-style apps, make `profiles.default` run the app's local clone shape, then add explicit host-product profiles such as `gumroad` or `flexile`. Use `serviceEnv` to point one service at another by Docker service name, for example `GUMROAD_URL=http://gumroad-web:3310`.

Project config can make expensive container volumes reusable. Use `lifetime: project` for shared durable dependency volumes and `lifetime: smart` for baseline-vs-branch copied volumes. Put fingerprint paths on lockfiles, migrations, schemas, and seed files. Avoid running two baseline previews for the same project at once because they share canonical smart volumes.

When adapting a bundled example for a real checkout, copy the example config, rewrite only project/source identity and local paths, then sync:

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

Keep the example's service graph, profiles, dependency volume lifetimes, setup policies, health checks, smoke tests, QA routes, and evidence settings intact. Rewrite only local identity and path/ref values.

## Evidence/QA flow

Evidence commands operate against target refs and artifact targets:

- Preview target refs identify a running preview, for example `preview:<id>` or the bare preview ID where the command expects a preview.
- Release target refs identify recorded release evidence, for example `release:<id>`.
- QA/screenshot `--target local` uses the local/proxy preview URL and is fastest.
- `--target public` uses a public tunnel and should be reserved for public-route proof.
- `--target origin` proves the service origin directly when proxy behavior is not under test.

Run smoke and collect basic evidence first:

```sh
vivero smoke webapp-local --json --no-input --quiet
vivero screenshot webapp-local web /dashboard \
  --target local \
  --breakpoints \
  --json --no-input --quiet
vivero events webapp-local --tail --json --no-input
vivero logs webapp-local web --since 10m --json --no-input
```

Ask Vivero for the QA plan before choosing browser work manually:

```sh
vivero qa plan webapp-local --scope public --target local --json --no-input --quiet
```

Treat the plan JSON as the source of truth for services and URLs, pages and flows, checks and severities, browser driver preference, artifact paths, screenshot commands, recording commands, and optional authenticated storage-state context.

Run deterministic Vivero-owned QA and recordings:

```sh
vivero qa run webapp-local --scope public --target local --json --no-input --quiet
vivero qa record webapp-local --scope public --json --no-input --quiet
vivero qa report webapp-local --out qa/report.md --json --no-input --quiet
vivero qa final webapp-local --scope public --target local --json --no-input --quiet
```

For authenticated QA, the app/operator provides a project-relative Playwright storage-state file under `agent.qa.auth.sessions.<name>.storageState` and attaches it to scopes with `scopes: [...]` on the session or `authSession: <name>` on the QA scope. Vivero includes resolved storage-state flags in `qa plan`; it does not store credentials or run app-specific login flows unless the app declares those commands in its own config.

When reporting evidence, include the target ref, command, pass/fail status, and exact artifact paths. Do not say “screenshots taken” without paths.

## Deploy/release flow

Deploy implementation belongs to app-owned commands configured under `deploy.environments` in `vivero.yml`. Vivero provides the safety wrapper: production doctor, plan, locks, idempotency checks, audit events, command-output artifacts, release records, current-release pointers, smoke gates, and rollback handles.

Plan is the safe entry point. It is read-only with respect to production, but it writes a local Vivero deploy plan artifact:

```sh
vivero doctor production --project <project-path> --json --no-input
vivero deploy plan <project-path> --environment production --json --no-input --quiet
```

Only apply a non-blocked plan after reviewing diagnostics, app-owned commands, target environment, and expected release behavior. In normal agent workflows, ask for human approval before `vivero deploy apply` because it can run app-owned production commands:

```sh
vivero deploy apply deploy-plan-123 --json --no-input --quiet
```

Check release status and release-scoped evidence after apply:

```sh
vivero release status webapp --environment production --json --no-input
vivero release events release:release-123 --environment production --json --no-input
vivero release logs release:release-123 --environment production --json --no-input
vivero release smoke webapp --environment production --json --no-input --quiet
```

`vivero release smoke` can run app-owned smoke commands against the current release. Treat it as side-effect-capable and normally approval-gated when operating production.

Rollback is also approval-gated. Prefer Vivero rollback over manual state edits so release history, current pointers, locks, and audit records remain consistent:

```sh
vivero release rollback webapp release:release-123 --environment production --json --no-input --quiet
```

Default deploy strategy means Vivero runs `applyCommand`, optional `smokeCommand`, optional `statusCommand`, and `rollbackCommand`. If `smokeCommand` is set, deploy apply must pass smoke before the release becomes current. For `strategy: blue-green`, Vivero models two slots and enforces prepare → smoke → promote before recording the new live slot. If smoke fails, Vivero exits before promote and records release history without moving current release.

Blue/green app-owned commands receive `VIVERO_BLUE_GREEN_ACTIVE_SLOT`, `VIVERO_BLUE_GREEN_TARGET_SLOT`, `VIVERO_BLUE_GREEN_PREVIOUS_SLOT`, `VIVERO_BLUE_GREEN_SLOTS`, `VIVERO_DEPLOY_PLAN_ID`, and `VIVERO_RELEASE_ID`.

## Failure playbooks

Preview startup failure:

1. Inspect the preview record and service health.
2. Diagnose startup to find the slow phase or first failure.
3. Read tail events and service logs.
4. Confirm source paths, refs, profile selection, and secret keys.
5. Re-run smoke or QA only after the runtime issue is understood.

```sh
vivero preview inspect webapp-local --json --no-input
vivero diagnose startup webapp-local --json --no-input
vivero preview events webapp-local --tail --json --no-input
vivero logs webapp-local web --since 10m --json --no-input
vivero secrets list webapp --json --no-input
```

Evidence/QA failure:

- Re-read `vivero qa plan` and verify the scope, target, pages, flows, storage state, and generated commands.
- If public screenshots fail, retry `--target local` to separate app health from tunnel/public-route behavior.
- If recordings fail, check the browser driver from the plan and preserve partial artifact paths.

```sh
vivero qa plan webapp-local --scope public --target local --json --no-input --quiet
vivero qa run webapp-local --scope public --target local --json --no-input --quiet
vivero diagnose startup webapp-local --json --no-input
```

Deploy plan blocked:

- Read production doctor diagnostics first.
- Fix config, missing app-owned commands, health/smoke definitions, route conflicts, or secret-key references in the app repo/config.
- Generate a new plan instead of editing plan state manually.

```sh
vivero doctor production --project <project-path> --json --no-input
vivero deploy plan <project-path> --environment production --json --no-input --quiet
```

Deploy apply or smoke failure:

- Do not promote manually.
- Inspect release events and logs.
- Read current release status.
- If the failed release never became current, prefer fixing and applying a new plan.
- If current production is bad, request approval and use Vivero rollback.

```sh
vivero release events release:release-123 --environment production --json --no-input
vivero release logs release:release-123 --environment production --json --no-input
vivero release status webapp --environment production --json --no-input
vivero release rollback webapp release:release-123 --environment production --json --no-input --quiet
```

Rollback failure:

- Keep the failed rollback evidence.
- Re-read release status, events, and logs.
- Do not edit Vivero release state by hand unless doing explicit emergency recovery with operator approval.

```sh
vivero release status webapp --environment production --json --no-input
vivero release events release:release-123 --environment production --json --no-input
vivero release logs release:release-123 --environment production --json --no-input
```

Stale state or command confusion:

```sh
vivero commands --json --no-input
vivero schema deploy apply --json --no-input
vivero schema qa final --json --no-input
vivero skill doctor --json --no-input
```

## Teardown and safety

Before teardown, check whether managed worktrees are dirty. Do not destroy dirty work unless committing, archiving a patch, keeping the worktree, or explicitly discarding it.

Clean teardown for throwaway previews:

```sh
vivero preview down webapp-local --discard --json --no-input --quiet
```

Safer teardown when edits might exist:

```sh
vivero preview inspect webapp-local --json --no-input
vivero preview down webapp-local --archive-patch --json --no-input --quiet
```

Keep source available after containers are gone:

```sh
vivero preview down webapp-local --keep-worktree --json --no-input --quiet
```

Agent invariants:

- Pass `--json` when consuming output programmatically.
- Pass `--no-input` so commands fail instead of blocking for prompts.
- Use `--quiet` when progress text is not needed.
- Use `--wait --timeout <duration>` when readiness matters.
- Prefer exact commit SHAs or explicit local source paths.
- Use stable preview IDs such as `<project>-<purpose>` or `<project>-pr<id>`.
- Use `--metadata branch=<name>` or `--metadata ref=<sha-or-ref>` when smart warm volumes need baseline-vs-branch behavior.
- Use `--label KEY=VALUE` for caller-owned bookkeeping only; do not put secrets in labels.
- Pass `--profile <name>` when the project has profiles and the task needs a non-default service set.
- Never announce a preview URL until `vivero preview up` or `vivero preview inspect` reports the service healthy.

## Secrets rules

Treat secret values as write-only. Keep them out of logs, events, URLs, labels, comments, screenshots, command history, and PR text.

List keys, set values, and remove keys only when required for the project to run:

```sh
vivero secrets list webapp --json --no-input
vivero secrets set webapp API_TOKEN=<value> --json --no-input --quiet
vivero secrets unset webapp API_TOKEN --json --no-input --quiet
```

Never attempt to print secret values. `vivero secrets list` returns keys only. Prefer project/operator secret stores for production credentials; Vivero should receive references or runtime-injection commands through app-owned config.

## Verification gates

Before claiming a Vivero runtime, evidence, skill, or release change is ready, run the focused gate for the surface touched and then the repo gate.

For release-facing changes, use the deterministic certification target:

```sh
make certify
```

Focused surface gates:

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

For skill changes, also verify print, doctor, and install behavior:

```sh
vivero skill print --json --no-input
vivero skill doctor --json --no-input
vivero skill install --target /tmp/vivero-skill --force --json --no-input
```

`make certify` is the deterministic pre-release ladder and runs audit, canonical example E2E, integration fixtures, nasty integration fixtures, dogfood config validation, deploy fixtures, and release package smoke. `make cover` enforces the coverage ratchet. `make nasty-integration-fixtures` covers messy preview shapes. `make deploy-fixtures` proves deploy plan/apply/status/rollback, idempotency, audit records, locks, and blue/green prepare/smoke/promote/rollback. `make release-smoke` validates packaged release artifacts and config examples. After a tag publishes, use `VERSION=vX.Y.Z make release-postflight` to verify release metadata, checksums, attestations, the installer, and the Homebrew tap formula.
