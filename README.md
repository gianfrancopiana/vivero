# Vivero

Vivero is Spanish for “nursery”: a place where young plants are grown until they are ready to move.

For coding agents, Vivero is a nursery for app changes. It starts a preview from `vivero.yml`, waits until it works, gives back a URL, and collects QA evidence.

## What Vivero handles

- Reads project setup from `vivero.yml`.
- Selects optional profiles, so one config can run the default app or a coupled multi-app preview.
- Starts app and support services through a Docker-compatible engine, such as Docker Desktop or OrbStack.
- Keeps expensive dependency volumes warm without sharing branch writes back into the main baseline.
- Waits for the app to be healthy before returning a preview URL.
- Provides local preview URLs, with optional public URLs from `public:` config.
- Gives agents JSON output, logs, screenshots, QA plans, recordings, and reports.
- Tears previews down safely; remote API access stays off unless `VIVERO_ALLOW_REMOTE_CONTROL=1` is set.

## Bundled skill

```sh
vivero skill print
vivero skill install --target ~/.agents/skills/vivero --json --no-input
vivero skill doctor --json --no-input
```

The bundled skill stays generic. Project routes, selectors, QA flows, and restart commands belong in `vivero.yml`.

## Basic use

```sh
vivero projects sync /path/to/project --json --no-input
vivero up webapp --id webapp-local --source app.path=/path/to/webapp --wait --timeout 5m --json --no-input --quiet
vivero inspect webapp-local --json --no-input
vivero qa run webapp-local --scope public --json --no-input --quiet
vivero down webapp-local --archive-patch --json --no-input --quiet
```

Use `--discard` only when preview changes do not need saving.

For multi-service previews, put each app under `services`. Containers can reach each other by service name, and Vivero reports a URL for each app service. Use `profiles:` when a project should normally start a small default service set but sometimes needs a coupled preview:

```sh
vivero up helper-host-products --id helper-gumroad --profile gumroad --wait --json --no-input --quiet
```

See `examples/helper-host-products/vivero.yml`: its default profile runs Helper alone, while explicit `gumroad` and `flexile` profiles add one host product and use `serviceEnv` to point Helper at that service.

## `vivero.yml`

Keep app-specific setup, routes, checks, and QA flows in project config.

```yaml
project:
  name: webapp

sources:
  app:
    mode: external
    path: ~/src/webapp
    defaultRef: main

warm:
  baselineRefs: [main]
  fingerprint:
    paths:
      - package-lock.json
      - db/migrate

services:
  web:
    source: app
    image: node:22-alpine
    workingDir: .
    dependencyVolumes:
      - name: node_modules
        target: /app/node_modules
        lifetime: smart
    command: npm run dev -- --host 127.0.0.1 --port 3000
    port: 3000
    health:
      path: /
      expectStatus: 200
      timeout: 2m
      interval: 2s

agent:
  defaultPreviewService: web
  commonPages:
    home:
      service: web
      path: /
  smokeTests:
    - name: homepage
      service: web
      path: /
      expectStatus: 200
  qa:
    defaultScope: public
    artifactRoot: .vivero/qa
    scopes:
      - name: public
        pages: [home]
        checks:
          - name: smoke-tests-pass
            category: health
            severity: critical
            method: vivero-smoke
        flows:
          - name: homepage-renders
            start: home
            steps:
              - visit: home
              - screenshot: homepage

profiles:
  default:
    services: [web]
    smokeTests: [homepage]
  full:
    services: [web]
    smokeTests: [homepage]
    serviceEnv:
      web:
        FEATURE_MODE: full
```

Profiles are optional. If `profiles.default` exists, `vivero up` uses it when `--profile` is omitted. A profile may select app `services`, `backingServices`, and `smokeTests`, and may add or override per-service env with `serviceEnv`. Setup steps, sources, QA pages, and QA flows are filtered to the selected services.

Warm dependency volumes:
- `preview` (default): removed by `vivero down --discard`.
- `project`: one project-wide Docker volume, shared by all previews.
- `smart`: baseline refs such as `main` update a canonical warm volume; branch previews start from a preview-local copy, so branch migrations or installs do not poison the baseline. `warm.fingerprint.paths` invalidates setup markers when lockfiles, migrations, schemas, or seeds change.

## License

[MIT](LICENSE)
