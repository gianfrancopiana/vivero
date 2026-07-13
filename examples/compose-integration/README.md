# Compose integration fixture

This fixture exercises the app-owned Compose path against a real Docker daemon:

- a target service plus its dependency and an intentionally omitted worker;
- fixed host ports in the app Compose file that Vivero must replace or strip;
- Compose-backed `setup.afterSeeds` and injected dependency volumes;
- two concurrent previews;
- normal teardown and same-ID retry retaining Compose volumes;
- explicit discard deleting preview-local Compose and dependency volumes.

Run it with `make compose-integration-fixtures`.
