# RFC: Production Hosting

## Decision

Vivero stays preview-first. Production behavior must remain in a separate, explicit `deploy`/`release` namespace and must not be smuggled into preview commands.

The current production posture is: **app-owned deploy command surface with a first-class blue/green strategy, not a general production host**. Vivero can plan/apply/status/rollback by running commands supplied by the app repo after `doctor production` passes. For `strategy: blue-green`, it models slots, prepares the inactive slot, requires a smoke gate, promotes traffic, records the active/previous slot, and rolls back to the previous slot. It still does not provide a production control plane, ingress manager, secret backend, backup system, or general orchestrator by itself.

## Why

Preview runtimes and production deploys optimize for different failure modes:

- Previews can use mutable worktrees, branch-local volumes, quick tunnels, disposable state, and `down --discard`.
- Production needs immutable releases, explicit environments, durable state, authenticated operations, conservative rollback, and incident recovery.

Mixing those semantics into `vivero preview up` / `vivero preview down` would make a safe preview tool too easy to misuse.

## Future tracks

1. **Preview-only Vivero** — keep improving previews, diagnostics, QA evidence, and local automation.
2. **App-owned deploy wrapper** — current scope. Vivero records deploy plans and release history, then runs app-provided apply/status/rollback commands for trusted operators.
3. **Single-node/personal production supervisor** — possible after more hardening. Scope would be one trusted operator plus documented backups, auth, and recovery.
4. **General PaaS/orchestrator** — out of scope unless Vivero integrates with an existing orchestrator instead of replacing one.

## Shared primitives

These Vivero pieces may transfer to a production track:

- `vivero.yml` parsing and config validation.
- Service orchestration and health checks.
- Events, logs, smoke checks, screenshots, QA plans, and reports.
- Typed command manifest and stable JSON output.
- Diagnostics such as `doctor config` and `doctor production`.

## Preview-only primitives

These are intentionally not production primitives:

- Mutable source paths and Git worktrees.
- Quick tunnels as ingress.
- Branch warm volumes and disposable preview volumes.
- `vivero preview down --discard` cleanup semantics.
- Preview IDs as deploy/release identities.

## Production-only requirements

A production track must have all of these before Vivero can claim production hosting:

- Immutable image references or release artifacts, preferably digest-pinned.
- Explicit environments separate from preview profiles.
- Release history, deploy plans, apply steps, status, rollback, and migration policy.
- Backup and restore checks for stateful volumes or external stores.
- Secret backend references instead of inline secret values.
- Ingress and TLS ownership that does not rely on quick tunnels.
- Authenticated remote control plane if remote operations exist.
- Observability: health, logs, events, metrics, and alerting hooks.
- Restart policy, graceful shutdown, resource limits, and bounded startup/readiness.
- Auditability and incident recovery guidance.
- Upgrade path for Vivero itself.

## CLI namespace

Production commands are separate and explicit:

```sh
vivero deploy plan <project> --environment production --json --no-input
vivero deploy apply <plan-id> --json --no-input
vivero release status <project> --environment production --json --no-input
vivero release rollback <project> <release-id> --environment production --json --no-input
```

Do not overload `vivero preview up`, `vivero preview down`, or quick-tunnel flows for production.

## Readiness doctor

`vivero doctor production --project <path> --json --no-input` is read-only. It answers: “Can this config produce a deploy plan that is safe enough to hand to app-owned production commands?” It does not deploy or mutate production state.

The first checks are intentionally conservative:

- Error on mutable sources or runtime builds instead of immutable release artifacts.
- Error on quick tunnels for public production exposure.
- Warning on tag-only images instead of digest-pinned images.
- Warning on missing resource limits and health timeout policy.
- Warning on persistent volumes without backup/restore policy.
- Warning on likely inline secret values.

Capabilities may advertise `preview-runtime`, `production-readiness-doctor`, `app-owned-deploy-surface`, and `blue-green-deploy`, but must not advertise `production-hosting` until the production-only requirements are implemented and tested.
