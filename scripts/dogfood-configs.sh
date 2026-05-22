#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "$script_dir/lib/common.sh"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

require_cmd python3

go build -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=dogfood" -o bin/vivero ./cmd/vivero

workdir="$(mktemp_workdir)"
cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

original_home="$HOME"
mkdir -p "$workdir/home" "$workdir/out" "$workdir/generated" "$workdir/bin"
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
echo "fake docker only supports buildx version in dogfood config fixtures" >&2
exit 127
SH
chmod +x "$workdir/bin/docker"
export PATH="$workdir/bin:$PATH"
export HOME="$workdir/home"
export VIVERO_HOME="$workdir/vivero-home"

# Committed examples must keep loading even without access to private dogfood repos.
for fixture in examples/gumroad examples/helper-host-products examples/nasty-integration; do
  bin/vivero doctor config "$fixture" --json --no-input > "$workdir/out/$(basename "$fixture")-doctor.json"
done

DOGFOOD_ROOT="${VIVERO_DOGFOOD_ROOT:-$original_home/.hermes/workspace}"

python3 - "$workdir/generated" "$DOGFOOD_ROOT" <<'PY'
import pathlib
import sys
import textwrap

out = pathlib.Path(sys.argv[1])
root = pathlib.Path(sys.argv[2]).expanduser()

def write_case(name, yaml):
    case = out / name
    case.mkdir(parents=True, exist_ok=True)
    (case / "vivero.yml").write_text(textwrap.dedent(yaml).strip() + "\n")
    return case

helper = root / "antiwork" / "helper"
flexile = root / "antiwork" / "flexile"
chetear = root / "gianfrancopiana" / "chetear.com"
self_site = root / "gianfrancopiana" / "self"

if helper.is_dir() and flexile.is_dir():
    write_case("helper-flexile", f"""
    project:
      name: dogfood-helper-flexile
    sources:
      helper:
        mode: external
        path: {helper}
      flexile:
        mode: external
        path: {flexile}
    backingServices:
      helper-db:
        image: postgres:16-alpine
        env:
          POSTGRES_PASSWORD: password
      flexile-db:
        image: postgres:16-alpine
        env:
          POSTGRES_PASSWORD: password
    services:
      helper-web:
        source: helper
        runtime: docker
        image: node:22-alpine
        command: pnpm dev:next -H 0.0.0.0 -p 3010
        env:
          HOST_PRODUCT: flexile
          FLEXILE_URL: http://flexile-web:3000
          DATABASE_URL: postgres://postgres:password@helper-db:5432/postgres
        ports:
          http:
            container: 3010
        primaryPort: http
        health:
          path: /
          expectStatus: 200
          timeout: 3m
          interval: 2s
      flexile-web:
        source: flexile
        runtime: docker
        image: node:22-alpine
        command: pnpm next dev frontend -H 0.0.0.0 -p 3000
        env:
          DATABASE_URL: postgres://postgres:password@flexile-db:5432/postgres
        ports:
          http:
            container: 3000
        primaryPort: http
        health:
          path: /
          expectStatus: 200
          timeout: 3m
          interval: 2s
    profiles:
      default:
        services:
          - helper-web
        backingServices:
          - helper-db
      flexile-hosted:
        services:
          - helper-web
          - flexile-web
        backingServices:
          - helper-db
          - flexile-db
    agent:
      defaultPreviewService: helper-web
      smokeTests:
        - name: helper-home
          service: helper-web
          path: /
          expectStatus: 200
        - name: flexile-home
          service: flexile-web
          path: /
          expectStatus: 200
    """)

if chetear.is_dir():
    write_case("chetear", f"""
    project:
      name: dogfood-chetear
    sources:
      site:
        mode: external
        path: {chetear}
    services:
      web:
        source: site
        runtime: docker
        image: node:22-alpine
        workingDir: site
        command: npm run dev -- --host 0.0.0.0 --port 4321
        ports:
          http:
            container: 4321
        primaryPort: http
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
        - name: home
          service: web
          path: /
          expectStatus: 200
    """)

if self_site.is_dir():
    write_case("self", f"""
    project:
      name: dogfood-self
    sources:
      site:
        mode: external
        path: {self_site}
    services:
      web:
        source: site
        runtime: docker
        image: node:22-alpine
        command: npm run dev -- --hostname 0.0.0.0 --port 3000
        ports:
          http:
            container: 3000
        primaryPort: http
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
        - name: home
          service: web
          path: /
          expectStatus: 200
    """)
PY

found=0
for case in "$workdir/generated"/*; do
  [ -d "$case" ] || continue
  found=$((found + 1))
  name="$(basename "$case")"
  bin/vivero doctor config "$case" --json --no-input > "$workdir/out/dogfood-$name.json"
  bin/vivero projects sync "$case" --json --no-input > "$workdir/out/dogfood-$name-sync.json"
done

if [ "$found" -eq 0 ]; then
  if [ "${VIVERO_DOGFOOD_REQUIRE:-0}" = "1" ]; then
    echo "no dogfood repositories found under $DOGFOOD_ROOT" >&2
    exit 1
  fi
  echo "no dogfood repositories found under $DOGFOOD_ROOT; committed examples validated"
  exit 0
fi

python3 - "$workdir/out" <<'PY'
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
for path in out.glob("*.json"):
    payload = json.loads(path.read_text())
    if "configDoctor" in payload:
        doctor = payload["configDoctor"]
        assert doctor.get("ok") is True, (path.name, doctor)
    if "project" in payload:
        assert payload["project"].get("name"), (path.name, payload)
print(f"validated {len(list(out.glob('*.json')))} dogfood artifacts")
PY

echo "dogfood configs passed ($found live repo fixture(s))"
