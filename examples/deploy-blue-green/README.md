# Blue/green deploy example

This certified example shows a production deploy where Vivero plans an inactive slot, runs app-owned prepare and smoke commands, then promotes traffic.

Vivero provides:

- active/target/previous slot environment variables,
- release state and phase artifacts,
- smoke-before-promote enforcement,
- rollback targeting the previous slot.

The app provides `scripts/blue-green.sh`, which stands in for provider-specific slot operations.
