# Integration stack fixture

This fixture exercises the fuller preview lifecycle used by CI:

- a Docker app service plus a Docker backing service on the preview network;
- command and HTTP health checks;
- `setup.afterSeeds` running in the service container;
- smart warm dependency volumes copied from `main` to a feature preview;
- QA final proof artifacts without dirtying the example repo.

Run it with:

```sh
make integration-fixtures
```
