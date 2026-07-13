---
name: vivero
version: 0.1.0
vivero_cli: 0.1.0
schema: 1
license: MIT
description: >
  Use the `vivero` CLI for preview-first app operations: isolated previews, public/local QA evidence, screenshots, recordings, reports, teardown, cache visibility, and source iteration through worktrees. Trigger when a task needs a running health-checked app preview, Docker-compatible exec/logs/screenshots, QA artifacts, cache inspection, or a final proof bundle. Vivero owns orchestration, safety gates, state, command contracts, and evidence; app repos own Dockerfiles, scripts, migrations, secrets, provider behavior, and infra.
---

# Vivero CLI

Vivero is a local-first, preview-first app-operations runtime for agents. Its primary lane is preview/evidence: start a realistic local or public app preview, run health/smoke/QA checks, collect screenshots and recordings, produce proof artifacts, and tear down cleanly.

- preview: safe, disposable environments for development and QA;
- evidence/QA: target-aware logs, events, smoke, screenshots, recordings, reports, and final proof bundles;
- support/cache: CLI discovery, project config validation, skill freshness, secret-key management, and explicit cache visibility.

## Mental model

The agent chooses intent and reports evidence. Vivero executes the repeatable runtime contract.

```text
Agent:
  Chooses project, source ref/path, preview lane, checks, evidence target,
  and teardown policy.

Vivero:
  Loads thin project config, prepares sources, starts preview services, injects
  secret keys without printing values, waits for health, records events,
  captures evidence, reports cache state, and tears down safely.

App repo:
  Owns Dockerfiles, compose/build scripts, migrations, seed data, secrets,
  provider behavior, and infrastructure.
```

Keep `vivero.yml` as thin orchestration metadata. Put project-specific routes, selectors, QA scopes, browser flows, and restart hooks there. Prefer one app-owned preview Compose service that starts its own dependencies; otherwise reference app-owned images, Dockerfiles, prebuild commands, or app-owned Compose services/backings (`runtime: compose` + `compose.file`/`compose.service`). Do not copy Dockerfiles, compose files, dependency service images, env contracts, volumes, setup scripts, or restart/bootstrap recipes into YAML when the app repo already owns them. Inline Dockerfiles are intentionally unsupported. Compose-backed services/backings get Vivero network aliases, so a Vivero service may point at an app-owned service with a different name while still being reachable by the Vivero name.

Compose services may add Vivero-owned `dependencyVolumes` when an expensive cache is not already modeled by the app stack, and `setup.afterSeeds` may invoke app-owned setup scripts through the Compose target while preserving per-preview, once-per-project, and once-per-fingerprint policies. Normal teardown/retry retains Compose volumes; only explicit `--discard` removes preview-lifetime volumes. Compose overrides are temporary, inherit environment values by key, and are deleted after Compose consumes them. Always set an explicit `health.timeout` for a large Compose stack. `doctor config` verifies local Compose files and targets, warns when that timeout is missing, and reports stripped dependency host ports plus services outside the target's `depends_on` closure.

## App-agnostic runtime command contract

Vivero does not know Rails, Node, Postgres, Redis, or any other app framework. It only runs commands declared by the app config:

- `command: "npm run dev -- --host 0.0.0.0"` is shell form and runs as `/bin/sh -lc <command>` for compatibility.
- `command: ["postgres", "-c", "max_connections=200"]` is exec/argv form and is passed to Docker without shell wrapping.
- The same scalar-or-array rule applies to service commands, backing-service commands, `health.command`, and `setup.afterSeeds[].command`.
- Keep runtime behavior app-owned: prefer an app script, image entrypoint, or Dockerfile command when the repo already has one.

When startup fails, inspect the generic diagnostics instead of adding framework-specific branches:

```sh
vivero preview diagnose startup preview:<id> --json --no-input
vivero evidence logs preview:<id> <service> --json --no-input
```

Diagnostics should identify the service, runtime, image, container, command/health command, log path, and recent redacted container logs. Fix the app-owned command or image from that evidence.

## First checks

Before operating on an unfamiliar install or project, inspect the live contract and config health:

```sh
vivero capabilities --json --no-input
vivero commands --json --no-input
vivero doctor --json --no-input
vivero doctor config <project-path> --json --no-input
vivero project inspect <project> --json --no-input
vivero skill doctor --json --no-input
```

Use `vivero commands --json --no-input` as the source of truth for command paths, flags, lanes, side effects, and examples. Use `vivero project inspect <project> --json --no-input` to learn sources, services, profiles, health checks, smoke tests, public routes, QA scopes, screenshot breakpoints, artifact paths, restart commands, dependency volume lifetimes, and setup policies.

## Choose the lane

| Lane | Use when | Primary commands | Safety rule |
| --- | --- | --- | --- |
| Preview lane | You need a running local app, live source iteration, service exec, diffs, or teardown. | `vivero preview up`, `vivero preview inspect`, `vivero preview wait`, `vivero preview exec`, `vivero preview sync`, `vivero preview diff`, `vivero preview down` | URL means healthy. Never announce a preview URL until inspect/up reports the service healthy. |
| Evidence/QA lane | You need logs, events, smoke, screenshots, app-agnostic browser flows, recordings, QA reports, or startup diagnosis. | `vivero evidence logs`, `vivero evidence events`, `vivero evidence smoke`, `vivero evidence screenshot`, `vivero evidence flow`, `vivero evidence qa run`, `vivero preview qa final`, `vivero preview diagnose startup` | Report exact artifact paths and target refs. Do not substitute screenshots or manual browser notes for declared QA evidence. |
| Support lane | You need CLI discovery, schema, project sync/inspect, cache state, skill freshness, or secret-key management. | `vivero capabilities`, `vivero commands`, `vivero schema`, `vivero doctor`, `vivero projects sync`, `vivero project inspect`, `vivero cache inspect`, `vivero skill doctor`, `vivero secrets list` | Treat secrets as write-only. Use schema/doctor output before guessing. |

Prefer namespaced preview commands for new guidance. Older root preview aliases may exist for compatibility, but new guidance should use the preview namespace.

## Tiny invariant fixture matrix

Keep Vivero proof small and invariant-led. The bundled examples are not a framework zoo; they are a tiny matrix of behaviors frontier agents can trust across unfamiliar repos.

- **Preview invariants:** a URL is only reportable after health passes; sources stay isolated by preview ID; app and backing services share the preview network; public route planning is explicit; warm volumes and caches are visible; teardown is intentional. Prove the canonical path with `make example-e2e`, broader lifecycle behavior with `make integration-fixtures`, real app-owned Compose concurrency and volume behavior with `make compose-integration-fixtures`, and messy shapes with `make nasty-integration-fixtures`.
- **Evidence invariants:** preview evidence reports target-aware JSON and artifact paths for events, logs, screenshots, app-agnostic evidence flows, QA reports, recordings, cache state, startup timing, and handoff files. Prefer `vivero evidence logs preview:<id> <service> --json --no-input`, `vivero evidence flow preview:<id> --steps-file qa/visual-flow.yaml --target local --dry-run --json --no-input`, and `vivero evidence qa run preview:<id> --scope smoke --target local --json --no-input` when collecting evidence.

Add or document a fixture only when it proves a distinct invariant failure mode. Otherwise extend the smallest existing fixture.

## Frontier-agent recipes

Use these recipes when you are a coding agent operating a repo you do not already know.

### Discover the live contract

```sh
vivero capabilities --json --no-input
vivero commands --json --no-input
vivero schema preview up --json --no-input
vivero schema evidence logs --json --no-input
vivero schema evidence flow --json --no-input
vivero schema preview qa final --json --no-input
```

Trust the installed CLI contract over stale memory. If a command is missing, do not invent it; fall back to the closest manifest-listed command.

### Start from a thin config

```sh
vivero init /path/to/project --name <project-name> --json --no-input
vivero doctor config /path/to/project --json --no-input
vivero projects sync /path/to/project --json --no-input
```

Keep Dockerfiles, compose files, migrations, secrets, selectors, and app-specific QA behavior app-owned. `vivero.yml` should point at those assets and declare the agent contract.

### Collect target-aware evidence

```sh
vivero preview up <project> --id <project>-local --wait --timeout 5m --json --no-input --quiet
vivero evidence logs preview:<project>-local web --json --no-input
vivero evidence flow preview:<project>-local --steps-file qa/visual-flow.yaml --target local --dry-run --json --no-input
vivero evidence flow preview:<project>-local --steps-file qa/visual-flow.yaml --target local --video --json --no-input --quiet
vivero evidence qa run preview:<project>-local --scope smoke --target local --json --no-input --quiet
vivero evidence screenshot preview:<project>-local web / --target local --json --no-input --quiet
```

Report the target ref, command, status, and artifact paths. Do not replace evidence with manual notes.

### Leave a handoff

```sh
vivero evidence events preview:<project>-local --tail --json --no-input
vivero evidence qa report preview:<project>-local --out qa/report.md --json --no-input --quiet
vivero preview down <project>-local --archive-patch --json --no-input --quiet
```

End with what changed, which target was tested, exact evidence paths, and the teardown state.

## Speed model

Treat fast repeat operations as part of the runtime contract:

- Use stable preview IDs such as `<project>-pr<id>` or `<project>-local` so Vivero can reuse local state, smart volumes, and evidence paths predictably.
- Pass `--metadata branch=<name>` or `--metadata ref=<sha-or-ref>` on preview starts when smart warm volumes need baseline-vs-branch behavior.
- Prefer explicit build cache config under `services.<name>.build.cache` when repeat image builds matter; app-owned Dockerfiles still control layer ordering and BuildKit cache mounts.
- Use `vivero cache inspect <project> --json --no-input` before guessing about Docker layers, warm volumes, or image state. Use `vivero cache warm` and `vivero cache prune` only when those commands are available in `vivero commands --json --no-input` for the installed CLI.
- For warm volume/cache evidence, look for cache inventory, image build durations, smart baseline/derived events, app-owned prebuild results, and artifact paths.

Do not promise wall-clock speed. Report concrete cache state, timing fields, and artifact paths from JSON output.

## Preview flow

Start with a stable preview ID, exact source refs or explicit local source paths, and readiness waiting:

```sh
vivero preview up webapp   --id webapp-pr42   --source app.ref=<exact-source-sha>   --wait --timeout 5m   --json --no-input --quiet
```

For an existing checkout, override the source path:

```sh
vivero preview up webapp   --id webapp-local   --source app.path=/path/to/webapp   --wait --timeout 5m   --json --no-input --quiet
```

Inspect before reporting a URL or retrying a failed start:

```sh
vivero preview inspect webapp-local --json --no-input
vivero preview wait webapp-local --timeout 5m --json --no-input --quiet
vivero preview diagnose startup preview:webapp-local --json --no-input
vivero preview events preview:webapp-local --tail --json --no-input
vivero evidence logs preview:webapp-local web --json --no-input
```

Live iteration should mutate source through Vivero-managed worktrees or explicit external paths, not by editing container files:

```sh
vivero preview sync webapp-local app path/to/file --from ./path/to/file --json --no-input
vivero preview diff webapp-local app --json --no-input
vivero preview exec preview:webapp-local web --json --no-input -- npm test
```

### Runtime truth and reuse

Treat `vivero preview inspect` and `vivero list` as live observations, not SQLite snapshots. They reconcile tracked Docker/Compose resources and the reported service URL; dead resources are demoted, dead local rewrite proxies are restarted, and ghost rows do not consume preview capacity. `vivero preview wait` probes the reported URL, not only the container origin.

Use `--reuse` only when reusing the same effective preview is intended. Vivero reuses an existing preview only when its effective profiled config and secret digest, source/profile/public shape, service set, containers/Compose project, and tracked side processes still match and are live; otherwise it replaces the preview. Re-running `up --reuse` after an env, secret, command, image, health, or port change therefore starts fresh runtime resources.

## Evidence/QA flow

Preview target refs identify a running preview, for example `preview:<id>` or the bare preview ID where the command expects a preview. QA screenshot and recording evidence defaults to `--target local`, which uses the local/proxy preview URL and is fastest. Use `--target public` only when explicitly validating the public tunnel.

Prefer generated Vivero/Playwright evidence commands for reproducible screenshots, recordings, traces, and CI-safe artifacts. Use Chrome MCP or other browser-driving tools only for exploratory debugging/manual inspection, then convert the finding into a Vivero evidence command when it needs to be shared.

```sh
vivero preview qa plan preview:webapp-local --scope smoke --target local --json --no-input
vivero evidence events preview:webapp-local --tail --json --no-input
vivero evidence logs preview:webapp-local web --json --no-input
vivero evidence smoke preview:webapp-local --json --no-input
vivero evidence screenshot preview:webapp-local web / --target local --json --no-input --quiet
vivero preview screenshot preview:webapp-local web / --breakpoints --target local --json --no-input --quiet
vivero preview qa record preview:webapp-local --scope smoke --target local --json --no-input --quiet
vivero evidence qa run preview:webapp-local --scope smoke --target local --json --no-input --quiet
vivero preview qa final preview:webapp-local --scope smoke --target local --json --no-input --quiet
```

Use `vivero evidence flow` for ad-hoc, app-agnostic browser walkthroughs. When a user asks for feature QA beyond the declared `vivero.yml` QA scopes, create a temporary or repo-owned JSON/YAML steps file, dry-run it, then call the CLI with video, screenshots, waits, and slow motion so the artifact is reviewable:

```sh
vivero evidence flow preview:webapp-local --steps-file /tmp/feature-flow.yaml --target local --dry-run --json --no-input
vivero evidence flow preview:webapp-local --steps-file /tmp/feature-flow.yaml --target local --video --screenshots --console --slow-mo-ms 250 --wait-ms 750 --json --no-input --quiet
```

Flow files declare start page, actions, variants, screenshot capture points, video preferences, and console/network capture. They support `visit`/`goto`, `click`, `fill`, `press`, `scroll`, `wait`/`waitMs`, `waitForSelector`, `expectText`, `expectNoText`, `expectSelector`, `expectNoSelector`, `expectUrl`, `expectUrlNot`, and named screenshots. Video evidence defaults to a visible in-page arrow pointer/click highlight (`record.pointer: true`) so review recordings show where clicks happened; screenshots hide that pointer overlay automatically so PNG artifacts stay cursor-free. Set `record.pointer: false` when even the video should be pixel-clean. Each run preserves input artifacts (`steps.json`, `plan.json`, generated `playwright.js`) and failure screenshots when an assertion fails. Use explicit waits, scroll actions, negative postconditions, and multiple screenshot checkpoints; a short smoke video that only lands on the homepage is not useful QA evidence. Always keep the flow file in the app repo when it encodes app-specific selectors or routes.

Minimal ad-hoc scroll flow:

```yaml
name: feature-scroll-review
start: { service: web, path: /feature }
variants:
  - name: desktop-light
    viewport: { width: 1440, height: 1000 }
    colorScheme: light
record: { video: true, screenshots: true, console: true, pointer: true }
actions:
  - waitForSelector: body
  - screenshot: { name: top, fullPage: false }
  - scroll: { direction: down, pixels: 800 }
  - wait: 750
  - screenshot: { name: after-scroll, fullPage: false }
```

## Cache/speed flow

Inspect caches before guessing about slow startup or rebuilds:

```sh
vivero cache inspect webapp --json --no-input
vivero cache warm webapp --source app.ref=main --json --no-input
vivero cache prune webapp --kind build --yes --json --no-input
```

Use cache commands deliberately. `cache warm` can run app-owned prebuild steps; `cache prune` deletes project-scoped cache resources and requires `--yes`.

## Multi-port and team-access previews

Named service ports use dynamic loopback bindings by default. Set `ports.<name>.hostIp` only when a port should bind to a specific LAN or Tailscale address. Set `proxyListenHost` when the Vivero rewrite/multiplex proxy itself must listen on that interface.

For a secondary HTTP or WebSocket origin, declare a unique path route instead of hard-coding the primary public host:

```yaml
ports:
  http:
    container: 3000
  cable:
    container: 8080
    publicPath: /cable
    publicOrigins:
      - ws://cable.localhost:8080/cable
primaryPort: http
```

`publicPath` is matched on path-segment boundaries and routes to that named port; `publicOrigins` are rewritten to the same reported preview origin and path, including `ws://` to `wss://` when the preview is HTTPS. Named ports always belong to that service container. With Compose, do not point a target port at a different Compose service: reverse-proxy that daemon through the target or model it as its own Vivero service.

Bound long-running commands explicitly: `vivero exec preview:webapp-local web --timeout 10m --json --no-input -- bin/rails db:migrate`. On timeout, JSON keeps partial `stdout` and `stderr`, sets `timedOut: true`, and returns exit code 124. Options after `--` are passed through unchanged.

## Failure playbooks

### Preview does not start

```sh
vivero preview inspect preview:webapp-local --json --no-input
vivero preview diagnose startup preview:webapp-local --json --no-input
vivero evidence logs preview:webapp-local web --json --no-input
vivero doctor config /path/to/project --json --no-input
```

Check health configuration, command form, Dockerfile paths, Compose service names, network aliases, and secret-key requirements. Fix the app-owned runtime asset first.

Health waits fail immediately when a target container or required Compose dependency exits. Before failed-start cleanup, Vivero snapshots redacted log tails for every tracked service and every container in the affected Compose project. `vivero evidence logs` falls back to the durable `logPath` after the container has been removed, so use it for post-mortem evidence too.

### Browser evidence fails

```sh
vivero evidence flow preview:webapp-local --steps-file qa/visual-flow.yaml --target local --dry-run --json --no-input
vivero evidence qa run preview:webapp-local --scope smoke --target local --json --no-input --quiet
vivero evidence screenshot preview:webapp-local web / --target local --json --no-input --quiet
```

Check the resolved URL, target, storage state, selectors, screenshot breakpoints, postconditions, console/pageerror output, and artifact paths. Prefer updating project-owned QA steps or declarative preview rewrite config over adding app-specific behavior to Vivero core. For public-preview host routing where some paths are app-global and others are subdomain-specific, use project config such as `publicRewrite.basePaths` (for example `/checkout`) to pin those paths to the base public origin without changing the app. If screenshots/video are blank, verify whether Playwright captured a real blank app state by inspecting `console.json`, `pageerror`, HTML props, and `network.json` before blaming media delivery.

### Local state looks stale

```sh
vivero doctor --json --no-input
vivero list --json --no-input
vivero preview down webapp-local --archive-patch --json --no-input --quiet
```

Archive patches before deleting preview worktrees unless the operator explicitly wants a discard. After changing a mounted preview `.env`, prefer `vivero preview up <project> --id <id> --reuse --wait --timeout 5m --public --json --no-input` over `docker restart <container>` so Vivero refreshes dynamic host ports, proxy process, and public-router state together.

## Teardown and safety

A task is not done until the preview state is explicit:

```sh
vivero preview down webapp-local --archive-patch --json --no-input --quiet
vivero preview down webapp-local --discard --json --no-input --quiet
```

Use `--archive-patch` when the preview worktree may contain useful changes. Managed worktrees are unregistered during replacement or teardown unless explicitly kept. Use `--discard` only when the caller accepts losing preview-only edits and preview-lifetime Compose volumes; normal teardown retains Compose volumes for a warm retry.

## Secrets rules

Vivero tracks secret keys, not secret values, in normal output.

```sh
vivero secrets list webapp --json --no-input
vivero secrets set webapp API_KEY=value --json --no-input
vivero secrets unset webapp API_KEY --json --no-input
```

Do not print secrets in chat, logs, screenshots, QA reports, or config examples. Prefer the user's secret manager as the source of truth and only inject values into local runtime state when needed.

## Verification gates

Use the smallest gate that proves the change, then the full ladder before release-facing changes:

```sh
make example-e2e
make integration-fixtures
make compose-integration-fixtures
make nasty-integration-fixtures
make example-configs
make release-smoke
make certify
```

`make certify` is the deterministic pre-release ladder and runs audit, canonical example E2E, Docker and real Compose integration fixtures, nasty integration fixtures, example config validation, and release package smoke. `make cover` enforces the coverage ratchet. `make compose-integration-fixtures` proves concurrent app-owned Compose previews and discard-only volume deletion; `make nasty-integration-fixtures` covers messy preview shapes.

For install trust after publishing a Vivero CLI release, use `release-postflight` with checksums, attestations, the installer, and Homebrew verification. `RELEASE_POSTFLIGHT_FLAGS="--example-e2e"` runs the checksum-installed release binary through the certified preview E2E:

```sh
GH_CLI=gh VERSION=v0.1.1 make release-postflight
GH_CLI=gh VERSION=v0.1.1 RELEASE_POSTFLIGHT_FLAGS="--example-e2e" make release-postflight
```
