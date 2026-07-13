#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "$script_dir/lib/common.sh"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required for compose integration fixtures" >&2
  exit 127
fi
if ! docker version >/dev/null 2>&1; then
  echo "docker daemon is not reachable" >&2
  exit 1
fi
require_cmd python3

real_home="$HOME"
export DOCKER_CONFIG="${DOCKER_CONFIG:-$real_home/.docker}"

go build -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=compose-integration" -o bin/vivero ./cmd/vivero

workdir="$(mktemp_workdir)"
fixture="examples/compose-integration"
first_id="compose-first-$$"
second_id="compose-second-$$"
known_volumes="$workdir/volumes"

cleanup_preview() {
  local id="$1"
  HOME="$workdir/home" VIVERO_HOME="$workdir/vivero-home" bin/vivero preview down "$id" --discard --json --no-input >/dev/null 2>&1 || true
}

cleanup() {
  set +e
  cleanup_preview "$first_id"
  cleanup_preview "$second_id"
  {
    docker ps -aq --filter "label=vivero.preview=$first_id"
    docker ps -aq --filter "label=vivero.preview=$second_id"
  } | sort -u | xargs -r docker rm -f >/dev/null 2>&1 || true
  if [ -f "$known_volumes" ]; then
    sort -u "$known_volumes" | xargs -r docker volume rm >/dev/null 2>&1 || true
  fi
  rm -rf "$workdir"
}
trap cleanup EXIT

mkdir -p "$workdir/home" "$workdir/out"
export HOME="$workdir/home"
export VIVERO_HOME="$workdir/vivero-home"

bin/vivero doctor config "$fixture" --json --no-input > "$workdir/out/doctor.json"
bin/vivero projects sync "$fixture" --json --no-input > "$workdir/out/sync.json"

up_preview() {
  local id="$1"
  local output="$2"
  bin/vivero preview up compose-integration --id "$id" --wait --timeout 2m --json --no-input > "$output"
}

up_preview "$first_id" "$workdir/out/first-up.json"
up_preview "$second_id" "$workdir/out/second-up.json"

first_container="$(docker ps -q --filter "label=vivero.preview=$first_id" --filter "label=vivero.service=web" | head -n1)"
if [ -z "$first_container" ]; then
  echo "first Compose target container was not found" >&2
  exit 1
fi
first_project="$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project" }}' "$first_container")"
app_volume="$(docker volume ls -q --filter "label=com.docker.compose.project=$first_project" | head -n1)"
scratch_volume="$(docker inspect --format '{{ range .Mounts }}{{ if eq .Destination "/scratch" }}{{ .Name }}{{ end }}{{ end }}' "$first_container")"
project_cache="$(docker inspect --format '{{ range .Mounts }}{{ if eq .Destination "/cache" }}{{ .Name }}{{ end }}{{ end }}' "$first_container")"
if [ -z "$app_volume" ] || [ -z "$scratch_volume" ] || [ -z "$project_cache" ]; then
  echo "expected Compose and injected dependency volumes" >&2
  docker inspect "$first_container" >&2
  exit 1
fi
if ! docker exec "$first_container" sh -c 'test "$(cat /cache/seed.txt)" = seeded'; then
  echo "Compose setup.afterSeeds did not populate the project cache" >&2
  exit 1
fi
printf '%s\n%s\n%s\n' "$app_volume" "$scratch_volume" "$project_cache" >> "$known_volumes"

bin/vivero preview down "$first_id" --json --no-input > "$workdir/out/first-down.json"
docker volume inspect "$app_volume" "$scratch_volume" "$project_cache" >/dev/null

up_preview "$first_id" "$workdir/out/first-retry.json"
retry_container="$(docker ps -q --filter "label=vivero.preview=$first_id" --filter "label=vivero.service=web" | head -n1)"
retry_app_volume="$(docker inspect --format '{{ range .Mounts }}{{ if eq .Destination "/app-data" }}{{ .Name }}{{ end }}{{ end }}' "$retry_container")"
retry_scratch_volume="$(docker inspect --format '{{ range .Mounts }}{{ if eq .Destination "/scratch" }}{{ .Name }}{{ end }}{{ end }}' "$retry_container")"
if [ "$retry_app_volume" != "$app_volume" ] || [ "$retry_scratch_volume" != "$scratch_volume" ]; then
  echo "same-ID retry did not retain Compose/dependency volumes" >&2
  exit 1
fi

bin/vivero preview down "$first_id" --discard --json --no-input > "$workdir/out/first-discard.json"
if docker volume inspect "$app_volume" >/dev/null 2>&1 || docker volume inspect "$scratch_volume" >/dev/null 2>&1; then
  echo "explicit discard retained preview-local Compose/dependency volumes" >&2
  exit 1
fi
docker volume inspect "$project_cache" >/dev/null

bin/vivero preview down "$second_id" --discard --json --no-input > "$workdir/out/second-discard.json"

python3 - "$workdir/out" <<'PY'
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
doctor = json.loads((out / "doctor.json").read_text())["configDoctor"]
assert doctor["ok"] is True, doctor
codes = {finding["code"] for finding in doctor["findings"]}
assert "compose-host-ports-stripped" in codes, doctor
assert "compose-services-omitted" in codes, doctor

for name in ["first-up.json", "second-up.json", "first-retry.json"]:
    preview = json.loads((out / name).read_text())["preview"]
    assert preview["status"] == "running", (name, preview)
    assert preview["services"]["web"]["status"] == "healthy", (name, preview)
PY

if find "$VIVERO_HOME/run/compose" -type f -print -quit 2>/dev/null | grep -q .; then
  echo "Compose runtime left generated override/env artifacts behind" >&2
  find "$VIVERO_HOME/run/compose" -type f -print >&2
  exit 1
fi

docker volume rm "$project_cache" >/dev/null
echo "compose integration fixtures passed"
