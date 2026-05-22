# Agent demo fixture

This is Vivero's canonical small app for proving the full local-preview loop without depending on a private product repo.

It is intentionally boring:

- one Node HTTP server,
- one Docker runtime image,
- deterministic health and smoke routes,
- one public QA scope,
- one authenticated QA scope using a committed non-secret Playwright storage-state fixture.

Run it manually:

```sh
vivero projects sync examples/agent-demo --json --no-input
vivero preview up agent-demo --id agent-demo-local --wait --timeout 3m --json --no-input
vivero preview qa final preview:agent-demo-local --scope smoke --no-record --no-screenshots --json --no-input
vivero preview down agent-demo-local --discard --json --no-input
```

Run the repository smoke script:

```sh
make example-e2e
```

For browser screenshots/video, opt in explicitly because it needs host Chrome plus npm Playwright:

```sh
VIVERO_EXAMPLE_BROWSER_QA=1 make example-e2e
```

The storage-state file is a fixture cookie for this demo server only. Real projects should keep authenticated storage-state outside git and should never put credentials in `vivero.yml`.
