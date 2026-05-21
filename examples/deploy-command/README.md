# Deploy command example

This certified example shows a production deploy that delegates to app-owned scripts.

Vivero provides:

- `deploy plan` and `deploy apply` orchestration,
- release state and idempotence,
- app-owned command output artifacts,
- `release status`, `release events`, `release logs`, `release smoke`, and `release rollback` evidence.

The app provides `scripts/deploy-command.sh`, which is deliberately tiny here but has the same contract as a real deploy script.
