# Vivero

Vivero is Spanish for “nursery”: a place to grow app changes until they are ready.

For coding agents, Vivero is a preview-first app-ops runtime. Its core job is to start a realistic local or public preview, run health and smoke checks, collect screenshots/recordings/QA reports, produce a final proof bundle, and tear down cleanly.

Vivero is local-first. The boundary is simple: **the app owns how it runs; Vivero owns orchestration, safety gates, local state, command contracts, and evidence.** Keep Dockerfiles, scripts, migrations, env contracts, secrets, provider behavior, and infra logic in the app repo. Use `vivero.yml` to point at them and describe how agents should operate the app.

## What Vivero gives agents

- **Preview lane:** isolated, disposable previews for local repos, branches, worktrees, and public QA proof.
- **Evidence/cache lane:** reusable logs, events, smoke checks, screenshots, app-agnostic evidence flows, QA reports, recordings, and cache controls.
- JSON output for every agent-facing workflow.
- Unique ports, networks, and state per preview.
- Fast-path primitives: Docker build cache, warm dependency volumes, setup/prebuild reuse, cache inspection, and timing evidence.
- Warm dependency volumes, with baseline refs like `main` feeding branch-local copies.
- Multi-service previews and profiles for coupled apps.
- Local URLs by default; public URLs only when `public:` is configured.
- Local-only remote control unless `VIVERO_ALLOW_REMOTE_CONTROL=1`.

## Install

Install with Homebrew:

```sh
brew install gianfrancopiana/tap/vivero
```

Or install the latest release directly with checksum verification:

```sh
curl -fsSL https://raw.githubusercontent.com/gianfrancopiana/vivero/main/scripts/install.sh | bash
```

See [docs/install.md](docs/install.md) for pinned installs, manual checksum verification, Homebrew, and `gh attestation verify`. Maintainers should use [docs/releasing.md](docs/releasing.md) for tag, postflight, and upgrade-cadence checks.

## Golden path: preview, prove, iterate, and tear down

Start with the certified example. It is small, local, and CI-proven:

```sh
make example-e2e
```

That target runs `examples/agent-demo` through config doctor, project sync, Docker preview startup, `vivero preview qa final`, startup diagnosis, teardown, and clean-file checks. Browser screenshot/video evidence is opt-in:

```sh
VIVERO_EXAMPLE_BROWSER_QA=1 make example-e2e
```

Then use the same shape on your app: start a preview, collect evidence, iterate on source, and archive or discard the preview cleanly.

### 1. Preview a change

```sh
vivero projects sync /path/to/project --json --no-input
vivero preview up webapp --id webapp-local --source app.path=/path/to/webapp --wait --timeout 5m --json --no-input --quiet
vivero preview inspect webapp-local --json --no-input
vivero preview qa final webapp-local --scope smoke --json --no-input --quiet
vivero preview down webapp-local --archive-patch --json --no-input --quiet
```

Root commands such as `vivero up`, `vivero inspect`, and `vivero down` remain compatibility aliases. Docs teach `preview ...` first because it is the clearer lane.

### 2. Read evidence when something fails

Evidence commands return one stable target-aware JSON shape. Use preview IDs or `preview:<id>` target refs:

```sh
vivero evidence logs preview:webapp-local web --json --no-input
vivero preview diagnose startup preview:webapp-local --json --no-input
vivero evidence screenshot preview:webapp-local web / --target local --json --no-input
vivero evidence flow preview:webapp-local --steps-file qa/visual-flow.yaml --target local --dry-run --json --no-input
vivero evidence flow preview:webapp-local --steps-file qa/visual-flow.yaml --target local --video --json --no-input --quiet
vivero evidence qa run preview:webapp-local --scope smoke --target local --json --no-input
```

Use `vivero evidence flow` for app-agnostic browser walkthroughs that do not belong in Vivero core. The `--steps-file` declares the start page, actions, variants, screenshot points, and recording preferences. `--dry-run` validates target/URL resolution and planned artifacts without launching a browser; a real run can write screenshots, video, console, and optional network artifacts per variant.

## Fast paths

Vivero treats speed as part of the product contract, not an accidental Docker side effect:

- **Docker build cache:** `services.<name>.build.cache` can declare BuildKit cache specs for repeat image builds while app-owned Dockerfiles still control layer ordering.
- **Runtime dependency volumes:** `dependencyVolumes` with `lifetime: project` or `lifetime: smart` keep expensive package, database, or tool caches out of disposable source trees.
- **Setup/prebuild cache:** app-owned prebuild and setup commands can write to durable volumes or artifacts; Vivero records when they run, skip, or fail.
- **Timing/evidence:** preview startup, image builds, warm events, logs, screenshots, QA reports, and recordings remain visible through JSON.

Use `vivero cache inspect`, `vivero cache warm`, and `vivero cache prune` when you need explicit cache state instead of guessing from Docker or volume names.

## Certified examples

Certified examples are real committed fixtures, not aspirational snippets. See [docs/certified-examples.md](docs/certified-examples.md).

- `examples/agent-demo`: web app; proven by `make example-e2e`.
- `examples/integration-stack`: app + backing service + warm volume; proven by `make integration-fixtures`.
- `examples/compose-integration`: app-owned Compose target + dependency + omitted worker; proven against real Docker Compose by `make compose-integration-fixtures`.
- `examples/nasty-integration`: static-only, app + database, monorepo app-owned Dockerfile, public-route planning, and messy config edges; proven by `make nasty-integration-fixtures`.

## Tiny invariant fixture matrix

The fixture set stays intentionally small. Each fixture exists because it proves an invariant class that frontier agents can reuse without memorizing project-specific behavior.

- **Preview invariants:** health-gated URLs, isolated source state, service networking, public-route planning, warm volumes, cleanup, and real Compose concurrency/volume retention. Prove the boring path with `make example-e2e`, the Compose path with `make compose-integration-fixtures`, and messy preview shapes with `make nasty-integration-fixtures`.
- **Evidence invariants:** target refs, stable JSON, logs, events, screenshots, app-agnostic evidence flows, QA reports, recordings, cache/timing fields, and handoff paths. Use `vivero evidence logs preview:<id> <service> --json --no-input`, `vivero evidence flow preview:<id> --steps-file qa/visual-flow.yaml --target local --dry-run --json --no-input`, and `vivero evidence qa run preview:<id> --scope smoke --target local --json --no-input` instead of ad hoc notes.

Add a new fixture only when it proves a new invariant class. Do not grow a framework zoo of example apps that all prove the same thing.

## `vivero.yml` at a glance

Keep the file small. It should select sources, services, health checks, profiles, warm volume rules, smoke tests, QA flows, and optional public URL policy. It should reference app-owned runtime assets instead of copying them.

```yaml
project:
  name: webapp

sources:
  app:
    mode: external
    path: ~/src/webapp
    defaultRef: main

services:
  web:
    source: app
    build:
      context: .
      dockerfile: docker/dev/Dockerfile
    command: ./script/server --port 3000
    port: 3000
    health:
      path: /
      expectStatus: 200

agent:
  defaultPreviewService: web
  smokeTests:
    - name: homepage
      service: web
      path: /
      expectStatus: 200
```

Add `profiles:` for small/full preview modes, `warm:` for expensive dependency volumes, `agent.qa:` for browser evidence, and `public:` only when a preview should expose a non-local URL. Routes, selectors, QA flows, and restart commands belong in project config, not in the generic skill or Vivero core.

For apps that already own a Compose stack, keep runtime internals in the app repo and let Vivero overlay only preview networking/health. The thinnest shape is one app-owned preview service that starts its own dependencies:

```yaml
services:
  web:
    source: app
    runtime: compose
    compose:
      file: docker/docker-compose-preview.yml
      service: web
    ports:
      http:
        container: 3000
      cable:
        container: 8080
        # Bind a specific LAN/Tailscale IP instead of loopback when teammates
        # should be able to open this port directly.
        hostIp: 127.0.0.1
        # Multiplex the secondary origin through the primary preview URL.
        publicPath: /cable
        publicOrigins:
          - ws://cable.localhost:8080/cable
    # Optional: bind Vivero's rewrite/multiplex proxy to a specific interface.
    proxyListenHost: 127.0.0.1
    health:
      path: /
      expectStatus: 200
      timeout: 10m
```

Use separate `backingServices:` entries only when the app does not have a single Compose service that owns dependency startup. Vivero generates a temporary Compose override with the per-preview network, labels, network aliases, and dynamic loopback port mappings, then deletes it immediately after Compose consumes it. Environment values are inherited from the Compose process instead of being persisted in the override. Vivero replaces target port bindings and strips host ports from dependency services, so concurrent previews do not contend for fixed ports; `doctor config` reports each stripped binding and every service outside the target's `depends_on` closure.

Compose services may declare Vivero-owned `dependencyVolumes` for caches that are not already modeled by the app stack. Vivero injects those as external named volumes, retains Compose and dependency volumes during normal teardown/retry, and removes preview-lifetime volumes only on explicit `--discard`. `setup.afterSeeds` may target a Compose service: Vivero uses `compose run` before the target starts or `compose exec` when it is already running, while preserving the normal per-preview, once-per-project, and once-per-fingerprint marker policies. Keep the commands themselves in app-owned scripts, and set an explicit `health.timeout` for large Compose stacks.

Do not duplicate Compose services, env contracts, app-owned volumes, or setup script bodies in `vivero.yml`.

Named ports bind to loopback by default. Set `ports.<name>.hostIp` to an explicit LAN or Tailscale IP when direct team access is intended. A non-primary port can declare a unique `publicPath` plus one or more `publicOrigins`; Vivero then routes that path to the port and rewrites those HTTP/WebSocket origins to the reported preview URL. All named ports belong to the declared service container; for Compose, a separate WebSocket or dev-server service must be reverse-proxied by the target service or modeled as a separate Vivero service instead of being declared as a target port. `proxyListenHost` controls which interface that local routing proxy listens on.

## CLI contract

Vivero follows a small, test-ratcheted CLI contract:

- Human help is examples-first: `vivero --help`, `vivero help <command>`, and grouped help such as `vivero help preview qa final`, `vivero help evidence qa`, or `vivero help cache inspect`.
- Machines can discover commands with `vivero commands --json --no-input` and schemas with `vivero schema <command> --json --no-input`.
- JSON command output goes to stdout; JSON errors go to stderr with `code`, `message`, `hint`, and `details` when available.
- `vivero version --json --no-input` and `vivero --version` expose version, commit, and build date for release provenance.
- `vivero exec <preview> <service> --timeout 10m -- <command>` bounds container execution. Timeout results preserve partial `stdout`/`stderr`, return `timedOut: true`, and use exit code 124; flags after `--` belong to the in-container command.

## Bundled skill

```sh
vivero skill print
vivero skill install --target ~/.agents/skills/vivero --json --no-input
vivero skill doctor --json --no-input
```

The bundled skill tells coding agents how to use Vivero. It stays generic; project-specific behavior belongs in `vivero.yml`.

## Confidence gates

Run the strongest local non-live ladder before release-facing changes:

```sh
make certify
```

`make certify` expands to the deterministic release ladder: audit, canonical example E2E, Docker and real Compose integration fixtures, nasty integration checks, example config validation, and snapshot release smoke. CI runs the same surfaces as split jobs. The Release workflow also gates tags on live Docker + Cloudflare quick tunnel + Playwright evidence before publishing.

After a tag publishes, run the install trust postflight against the exact release:

```sh
GH_CLI=gh VERSION=v0.1.1 make release-postflight
```

That verifies release metadata, required assets, checksums, the SPDX SBOM, GitHub artifact attestations, the checksum-verifying installer, and the Homebrew tap formula. For release-candidate confidence, run the same postflight with the checksum-installed release binary through the certified preview E2E:

```sh
GH_CLI=gh VERSION=v0.1.1 RELEASE_POSTFLIGHT_FLAGS="--example-e2e" make release-postflight
```

## Limits

- Local-first: Vivero stores state locally and defaults to loopback preview URLs.
- Docker-compatible runtime required for Docker preview fixtures.
- Public URLs require configured tunnel/provider support.
- Vivero orchestrates previews and evidence; app repos own runtime and infrastructure behavior.
- Secrets and credentials belong outside `vivero.yml`.
- Remote control is local-only unless `VIVERO_ALLOW_REMOTE_CONTROL=1` is set deliberately.
- Windows is intentionally out of current release scope.

## License

[MIT](LICENSE)
