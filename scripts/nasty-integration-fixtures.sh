#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "$script_dir/lib/common.sh"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"
original_gomodcache="$(go env GOMODCACHE)"
original_gocache="$(go env GOCACHE)"

require_cmd python3

go build -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=nasty-integration" -o bin/vivero ./cmd/vivero

workdir="$(mktemp_workdir)"
cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

mkdir -p "$workdir/home" "$workdir/out" "$workdir/cases" "$workdir/bin"
real_docker="$(command -v docker || true)"
export REAL_DOCKER="$real_docker"
cat > "$workdir/bin/docker" <<'SH'
#!/usr/bin/env sh
set -eu
if [ "${1:-}" = "buildx" ] && [ "${2:-}" = "version" ]; then
  printf 'github.com/docker/buildx v0.12.0\n'
  exit 0
fi
if [ -n "${REAL_DOCKER:-}" ] && [ "$REAL_DOCKER" != "$0" ]; then
  exec "$REAL_DOCKER" "$@"
fi
echo "fake docker only supports buildx version in nasty integration fixtures" >&2
exit 127
SH
chmod +x "$workdir/bin/docker"
export PATH="$workdir/bin:$PATH"
export HOME="$workdir/home"
export VIVERO_HOME="$workdir/vivero-home"
export GOMODCACHE="$original_gomodcache"
export GOCACHE="$original_gocache"

bin/vivero doctor config examples/nasty-integration --json --no-input > "$workdir/out/nasty-example.json"
bin/vivero projects sync examples/nasty-integration --json --no-input > "$workdir/out/sync.json"
bin/vivero project inspect nasty-integration --json --no-input > "$workdir/out/inspect.json"

python3 - "$workdir/cases" <<'PY'
import pathlib
import textwrap
import sys

cases = pathlib.Path(sys.argv[1])

(cases / "duplicate-public-hosts").mkdir(parents=True)
(cases / "duplicate-public-hosts" / "vivero.yml").write_text(textwrap.dedent("""
project:
  name: duplicate-public-hosts
public:
  provider: cloudflare
  mode: named-tunnel
  baseDomain: preview.example.com
  hostname: fixed.preview.example.com
services:
  api:
    image: node:22-alpine
    public: true
    ports:
      http:
        container: 3001
    primaryPort: http
  web:
    image: node:22-alpine
    public: true
    ports:
      http:
        container: 3000
    primaryPort: http
"""))

(cases / "bad-public-domain").mkdir(parents=True)
(cases / "bad-public-domain" / "vivero.yml").write_text(textwrap.dedent("""
project:
  name: bad-public-domain
public:
  provider: cloudflare
  mode: named-tunnel
  baseDomain: preview.example.com
  hostname: evil.example.net
services:
  web:
    image: node:22-alpine
    public: true
    ports:
      http:
        container: 3000
    primaryPort: http
"""))

(cases / "bad-fingerprint-path").mkdir(parents=True)
(cases / "bad-fingerprint-path" / "vivero.yml").write_text(textwrap.dedent("""
project:
  name: bad-fingerprint-path
warm:
  fingerprint:
    paths:
      - ../outside
services:
  web:
    image: node:22-alpine
    port: 3000
"""))
PY

for case in duplicate-public-hosts bad-public-domain; do
  if bin/vivero doctor production --project "$workdir/cases/$case" --json --no-input > "$workdir/out/$case.json" 2>"$workdir/out/$case.err"; then
    echo "expected production doctor to reject $case" >&2
    cat "$workdir/out/$case.json" >&2
    exit 1
  fi
done
for case in bad-fingerprint-path; do
  if bin/vivero doctor config "$workdir/cases/$case" --json --no-input > "$workdir/out/$case.json" 2>"$workdir/out/$case.err"; then
    echo "expected config doctor to reject $case" >&2
    cat "$workdir/out/$case.json" >&2
    exit 1
  fi
done

go test ./internal/vivero -run 'Test(ServicePortPlanSupportsNamedDynamicPortsAndLegacyFixedPort|DockerRunArgsPublishesNamedPortsWithDynamicHostPort|DockerBuildSpecForServiceResolvesAppOwnedDockerfile|DockerBuildSpecRejectsContextOutsideProjectRoot|DockerBuildSpecRejectsDockerfileOutsideContextRoot|NamedTunnelPublicURLUsesStableHostnameTemplate|ValidateNamedPublicRoutesRejectsDuplicateHosts|ValidateNamedPublicRouteConflictsRejectsExistingPreviewHost|UpValidatesNamedPublicRouteBeforeStartingDockerNetwork|UpRejectsInvalidNamedPublicRouteBeforeStartingDockerNetwork|QuickTunnelArgsIncludesHostHeader|QuickTunnelLogPollingIgnoresOldURLs|PublicServiceRejectsNonLoopbackOriginHost|PublicPreviewRouterRejectsNonLoopbackUpstream|CropScreenshotOuterWhitespaceRemovesBlankViewportPadding|ScreenshotValidationRejectsBadOptions|SmartWarmFingerprintChangesWhenMigrationChanges|PrepareSmartWarmVolumesUsesBaselineOnMainAndDerivedOnBranch|ContainerPreviewProfilesSelectServicesBackingSourcesAndSmoke)$'

python3 - "$workdir/out" <<'PY'
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
example = json.loads((out / "nasty-example.json").read_text()).get("configDoctor", {})
assert example.get("ok") is True, example
assert example.get("project") == "nasty-integration", example
inspect = json.loads((out / "inspect.json").read_text()).get("project", {})
config = inspect.get("config", {})
for profile in ["static-only", "app-with-db", "monorepo", "full"]:
    assert profile in (config.get("profiles") or {}), (profile, config.get("profiles"))
assert "db" in (config.get("backingServices") or {}), config
assert (config.get("public") or {}).get("mode") == "named-tunnel", config.get("public")
monorepo = (config.get("services") or {}).get("monorepo-web") or {}
build_cache = ((monorepo.get("build") or {}).get("cache") or {})
assert build_cache.get("enabled") is True, build_cache
assert build_cache.get("from") and build_cache.get("to"), build_cache
assert monorepo.get("dependencyVolumes"), monorepo
for case in ["duplicate-public-hosts", "bad-public-domain", "bad-fingerprint-path"]:
    payload = json.loads((out / f"{case}.json").read_text())
    doctor = payload.get("configDoctor") or payload.get("productionDoctor") or payload.get("doctor") or {}
    assert doctor.get("ok") is False, (case, payload)
    errors = doctor.get("errors", 0)
    diagnostics = doctor.get("diagnostics") or doctor.get("findings") or []
    assert errors >= 1 or any(item.get("level") == "error" or item.get("severity") == "error" for item in diagnostics), (case, payload)
PY

echo "nasty integration fixtures passed"
