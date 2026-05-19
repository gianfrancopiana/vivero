---
name: vivero
version: 0.1.0
vivero_cli: 0.1.0
schema: 1
license: MIT
description: >
  Use the `vivero` CLI to create, inspect, iterate on, verify, QA, and tear down local preview environments. Trigger when a task needs a running app preview, a URL that has passed health checks, Docker-compatible container exec/logs/screenshots, seed-backed app state, live source iteration through a Git worktree, or project-specific browser QA context. Do not trigger for general issue or PR management unless a running preview is also needed; Vivero owns the preview runtime, not PR, CI, or chat workflow.
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

## First checks

Before operating on an unfamiliar install or project, inspect the live contract:

```sh
vivero capabilities --json --no-input
vivero commands --json --no-input
vivero doctor config <project-path> --json --no-input
vivero project inspect <project> --json --no-input
vivero skill doctor --json --no-input
```

Use project inspection to learn the available sources, services, profiles, health checks, smoke tests, useful routes, QA scopes, screenshot breakpoints, artifact paths, restart commands, dependency volume lifetimes, and setup-step policies.

## Agent invariants

- Pass `--json` when consuming command output programmatically.
- Pass `--no-input` so commands fail instead of blocking for prompts.
- Use `--quiet` when progress text is not needed.
- Use `--wait --timeout <duration>` when readiness matters.
- Prefer exact commit SHAs or explicit local source paths.
- Use stable preview IDs, usually `<project>-<purpose>` or `<project>-pr<id>`.
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
      command: bundle install && npm install
    - service: web
      policy: once-per-project
      command: bundle exec rails db:seed
```

- `lifetime: preview` is the default and is removed by `vivero down --discard`.
- `lifetime: project` survives preview teardown and is shared by all previews of the project.
- `lifetime: smart` lets baseline refs update a canonical warm volume while branch previews receive copied preview-local volumes. Use this for branch-sensitive DB/index/cache state.
- `warm.fingerprint.paths` should list project-relative lockfiles, migrations, schemas, and seed files that invalidate smart warm setup markers.
- `policy: once-per-project` skips a setup command after it has succeeded once for the matching project/config step. With smart volumes, markers are fingerprint-aware so changed migrations or lockfiles rerun setup on the affected warm volume.
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
- screenshot and recording commands derived from `agent.qa.evidence`.

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

After QA, generate or refresh the report scaffold:

```sh
vivero qa report webapp-local --scope public --json --no-input --quiet
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
