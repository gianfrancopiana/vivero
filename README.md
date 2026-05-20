# Vivero

Vivero is Spanish for “nursery”: a place to grow app changes until they are ready.

For coding agents, Vivero is a nursery for app changes. It starts an app from a thin `vivero.yml`, waits until the app is healthy, returns a URL, and saves QA evidence.

Vivero is local-first. The boundary is simple: **the app owns how it runs; Vivero owns the preview.** Keep Dockerfiles, scripts, migrations, env contracts, and deploy logic in the app repo. Use `vivero.yml` to point at them and describe the preview.

## What Vivero gives agents

- Isolated previews for local repos, branches, or worktrees.
- Unique ports, networks, and state per preview.
- Warm dependency volumes, with baseline refs like `main` feeding branch-local copies.
- Multi-service previews and profiles for coupled apps.
- Health checks and smoke tests before a URL is returned.
- JSON output, logs, screenshots, QA reports, and recordings.
- Local URLs by default; public URLs only when `public:` is configured.
- Local-only remote control unless `VIVERO_ALLOW_REMOTE_CONTROL=1`.

## Basic workflow

Try the self-contained fixture first:

```sh
make example-e2e
```

It syncs `examples/agent-demo`, starts a Dockerized local preview, runs `vivero qa final` in lightweight mode, verifies artifact paths, diagnoses startup, tears the preview down, and checks that the example app files stayed clean. To opt into browser screenshots/video for that fixture, run `VIVERO_EXAMPLE_BROWSER_QA=1 make example-e2e`.

For a fuller Docker lifecycle check, run:

```sh
make integration-fixtures
```

That fixture covers a Docker app plus backing service, container networking, smart warm baseline/derived volumes, setup skip policy, final QA proof paths, and cleanup. Browser recording is optional with `VIVERO_INTEGRATION_BROWSER_QA=1`.

A normal project flow looks like this:

```sh
vivero projects sync /path/to/project --json --no-input
vivero up webapp --id webapp-local --source app.path=/path/to/webapp --wait --timeout 5m --json --no-input --quiet
vivero inspect webapp-local --json --no-input
vivero qa run webapp-local --scope public --json --no-input --quiet
vivero down webapp-local --archive-patch --json --no-input --quiet
```

`vivero up` reloads the project's current `vivero.yml` before starting. Use `--discard` on teardown only when preview changes do not need saving.

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

Add:

- `profiles:` when one project has small and full preview modes.
- `warm:` when expensive dependency volumes should be reused safely.
- `agent.qa:` for pages, flows, auth state, screenshots, recordings, and reports.
- `public:` only when a preview should expose a non-local URL.

Routes, selectors, QA flows, and restart commands belong in project config, not in the generic skill or Vivero core.

## Bundled skill

```sh
vivero skill print
vivero skill install --target ~/.agents/skills/vivero --json --no-input
vivero skill doctor --json --no-input
```

The bundled skill tells coding agents how to use Vivero. It stays generic; project-specific behavior belongs in `vivero.yml`.

## Production and release

Vivero stays preview-first, but it now has an explicit production/release command surface for app-owned deploy logic. `vivero doctor production` is the read-only gate: it blocks mutable preview inputs, quick tunnels, and other risky config before a deploy plan can be applied.

Configure deploy commands in `vivero.yml` and keep the implementation in the app repo:

```yaml
deploy:
  environments:
    production:
      applyCommand: ./script/deploy production
      statusCommand: ./script/deploy-status production
      rollbackCommand: ./script/deploy-rollback production
```

Run the flow explicitly:

```sh
vivero doctor production --project <path> --json --no-input
vivero deploy plan <path> --environment production --json --no-input
vivero deploy apply <plan-id> --json --no-input
vivero release status <project> --environment production --json --no-input
vivero release rollback <project> <release-id> --environment production --json --no-input
```

Before release-facing changes, run the local confidence ladder:

```sh
make verify
make cover
make example-e2e
make integration-fixtures
make release-smoke
```

`make example-e2e` is the fast canonical preview proof. `make integration-fixtures` is the Docker lifecycle proof. `make release-smoke` builds snapshot archives with GoReleaser, extracts the host-compatible tarball, runs `vivero doctor`, checks command/schema JSON, and validates the example configs from the packaged binary path.

CI runs quality gates, Linux/macOS builds, canonical example E2E, Docker integration fixtures, and a snapshot release smoke. Tag releases publish darwin/linux archives for amd64 and arm64 after the same archive smoke. Windows and Homebrew are intentionally out of scope for now.

## License

[MIT](LICENSE)
