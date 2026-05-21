# Vivero

Vivero is Spanish for “nursery”: a place to grow app changes until they are ready.

For coding agents, Vivero is an agent app-ops runtime. It has two first-class lanes — preview and deploy/release — plus one shared evidence/cache lane for logs, health checks, screenshots, QA reports, recordings, release events, command output artifacts, and cache visibility.

Vivero is local-first. The boundary is simple: **the app owns how it runs and deploys; Vivero owns orchestration, safety gates, local state, command contracts, and evidence.** Keep Dockerfiles, scripts, migrations, env contracts, secrets, and infra logic in the app repo. Use `vivero.yml` to point at them and describe how agents should operate the app.

## What Vivero gives agents

- **Preview lane:** isolated, disposable previews for local repos, branches, or worktrees.
- **Deploy/release lane:** explicit `deploy` and `release` commands for app-owned production logic.
- **Evidence/cache lane:** reusable logs, events, smoke checks, screenshots, QA reports, recordings, release artifacts, and cache controls.
- JSON output for every agent-facing workflow.
- Unique ports, networks, and state per preview.
- Fast-path primitives: Docker build cache, warm dependency volumes, setup/prebuild reuse, deploy prepare/cache hints, and timing evidence.
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

## Golden paths: preview fast, prove, deploy fast

Start with the certified example. It is small, local, and CI-proven:

```sh
make example-e2e
```

That target runs `examples/agent-demo` through config doctor, project sync, Docker preview startup, `qa final`, startup diagnosis, teardown, and clean-file checks. Browser screenshot/video evidence is opt-in:

```sh
VIVERO_EXAMPLE_BROWSER_QA=1 make example-e2e
```

Then use the same lane shape on your app: start a preview, collect evidence, and only then plan or apply a deploy.

### 1. Preview a change

```sh
vivero projects sync /path/to/project --json --no-input
vivero preview up webapp --id webapp-local --source app.path=/path/to/webapp --wait --timeout 5m --json --no-input --quiet
vivero preview inspect webapp-local --json --no-input
vivero qa final webapp-local --scope smoke --json --no-input --quiet
vivero preview down webapp-local --archive-patch --json --no-input --quiet
```

Root commands such as `vivero up`, `vivero inspect`, and `vivero down` remain compatibility aliases. Docs teach `preview ...` first because it is the clearer lane.

### 2. Read evidence when something fails

Evidence commands return one stable target-aware JSON shape. Use preview IDs, `preview:<id>`, or release targets like `release:<id>`:

```sh
vivero logs preview:webapp-local --json --no-input
vivero diagnose startup preview:webapp-local --json --no-input
vivero screenshot preview:webapp-local --page home --json --no-input
vivero qa final preview:webapp-local --scope smoke --json --no-input
```

The same debugging loop applies after deploys:

```sh
vivero release events release:<release-id> --json --no-input
vivero release logs release:<release-id> --json --no-input
vivero release smoke deploy-ready --environment production --json --no-input
```

### 3. Plan and apply a deploy

Vivero does not own your production infrastructure. It plans and records app-owned deploy commands.

```sh
vivero doctor production --project examples/deploy-command --json --no-input
vivero deploy plan examples/deploy-command --environment production --json --no-input
vivero deploy apply <plan-id> --json --no-input
vivero release status deploy-ready --environment production --json --no-input
vivero release rollback deploy-ready <release-id> --environment production --json --no-input
```

For blue/green deploys, configure app-owned slot commands and let Vivero enforce prepare → smoke → promote → rollback state transitions. The certified example lives at `examples/deploy-blue-green`.

## Fast paths

Vivero treats speed as part of the product contract, not an accidental Docker side effect:

- **Docker build cache:** `services.<name>.build.cache` can declare BuildKit cache specs for repeat image builds while app-owned Dockerfiles still control layer ordering.
- **Runtime dependency volumes:** `dependencyVolumes` with `lifetime: project` or `lifetime: smart` keep expensive package, database, or tool caches out of disposable source trees.
- **Setup/prebuild cache:** app-owned prebuild and setup commands can write to durable volumes or artifacts; Vivero records when they run, skip, or fail.
- **Deploy prepare/cache hints:** deploy configs can expose prepare phases and cache hints to app-owned scripts without Vivero provisioning production infrastructure.
- **Timing/evidence:** preview startup, image builds, warm events, deploy phases, logs, screenshots, QA reports, and release artifacts remain visible through JSON.

Use `vivero cache inspect`, `vivero cache warm`, and `vivero cache prune` when you need explicit cache state instead of guessing from Docker or volume names.

## Certified examples

Certified examples are real committed fixtures, not aspirational snippets. See [docs/certified-examples.md](docs/certified-examples.md).

- `examples/agent-demo`: web app; proven by `make example-e2e`.
- `examples/integration-stack`: app + backing service + warm volume; proven by `make integration-fixtures`.
- `examples/nasty-integration`: static-only, app + database, monorepo app-owned Dockerfile, public-route planning, and messy config edges; proven by `make nasty-integration-fixtures`.
- `examples/deploy-command`: command deploy; proven by `make deploy-fixtures`.
- `examples/deploy-blue-green`: blue/green deploy; proven by `make deploy-fixtures`.

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

Deploy config points to app-owned commands:

```yaml
deploy:
  environments:
    production:
      prepareCommand: ./script/deploy-prepare production
      applyCommand: ./script/deploy production
      smokeCommand: ./script/deploy-smoke production
      statusCommand: ./script/deploy-status production
      rollbackCommand: ./script/deploy-rollback production
      cache:
        build:
          from:
            - type=local,src=.vivero/cache/build/web
          to:
            - type=local,dest=.vivero/cache/build/web,mode=max
```

## CLI contract

Vivero follows a small, test-ratcheted CLI contract:

- Human help is examples-first: `vivero --help`, `vivero help <command>`, and grouped help such as `vivero help qa`.
- Machines can discover commands with `vivero commands --json --no-input` and schemas with `vivero schema <command> --json --no-input`.
- JSON command output goes to stdout; JSON errors go to stderr with `code`, `message`, `hint`, and `details` when available.
- `vivero version --json --no-input` and `vivero --version` expose version, commit, and build date for release provenance.

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

`make certify` expands to the deterministic release ladder: audit, canonical example E2E, Docker integration fixtures, nasty integration checks, dogfood config validation, deploy/release fixtures, and snapshot release smoke. CI runs the same surfaces as split jobs. A scheduled/manual live smoke covers Docker + Cloudflare quick tunnels + Playwright evidence.

After a tag publishes, run the install trust postflight against the exact release:

```sh
GH_CLI=gh VERSION=v0.1.1 make release-postflight
```

That verifies release metadata, required assets, checksums, GitHub artifact attestations, the checksum-verifying installer, and the Homebrew tap formula.

## Limits

- Local-first: Vivero stores state locally and defaults to loopback preview URLs.
- Docker-compatible runtime required for Docker preview fixtures.
- Public URLs require configured tunnel/provider support.
- Vivero records deploys and runs app-owned commands; it does not provision production infrastructure by itself.
- Secrets and credentials belong outside `vivero.yml`.
- Remote control is local-only unless `VIVERO_ALLOW_REMOTE_CONTROL=1` is set deliberately.
- Windows is intentionally out of current release scope.

## License

[MIT](LICENSE)
