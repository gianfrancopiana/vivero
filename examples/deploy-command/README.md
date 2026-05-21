# Deploy command example

This certified example shows a production deploy that delegates to app-owned scripts and still exposes a fast deploy lane.

Vivero provides:

- `deploy plan` and `deploy apply` orchestration,
- a `prepareCommand` phase before apply,
- `VIVERO_CACHE_DIR`, `VIVERO_BUILD_CACHE_FROM`, and `VIVERO_BUILD_CACHE_TO` hints for app-owned build/push scripts,
- release state and idempotence,
- app-owned command output artifacts,
- `release status`, `release events`, `release logs`, `release smoke`, and `release rollback` evidence.

The app provides `scripts/deploy-command.sh`, which is deliberately tiny here but has the same contract as a real deploy script. The prepare phase writes the cache env proof; a real app would use those variables to warm or reuse build artifacts before applying the release.
