#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "$script_dir/lib/common.sh"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required for integration fixtures" >&2
  exit 127
fi
if ! docker version >/dev/null 2>&1; then
  echo "docker daemon is not reachable" >&2
  exit 1
fi
require_cmd python3

go build -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=integration" -o bin/vivero ./cmd/vivero

workdir="$(mktemp_workdir)"
baseline_id="integration-stack-main-$$"
derived_id="integration-stack-feature-$$"
fixture="examples/integration-stack"
known_volumes_file="$workdir/out/warm-volumes.txt"

cleanup_preview() {
  local id="$1"
  if HOME="$workdir/home" VIVERO_HOME="$workdir/vivero-home" bin/vivero preview inspect "$id" --json --no-input >/dev/null 2>&1; then
    HOME="$workdir/home" VIVERO_HOME="$workdir/vivero-home" bin/vivero preview down "$id" --discard --json --no-input >/dev/null 2>&1 || true
  fi
}

cleanup_known_warm_volumes() {
  if [ ! -f "$known_volumes_file" ]; then
    return 0
  fi
  sort -u "$known_volumes_file" | while IFS= read -r volume; do
    if [ -n "$volume" ]; then
      docker volume rm "$volume" >/dev/null 2>&1 || true
    fi
  done
}

cleanup_docker_fallback() {
  local containers
  containers="$(
    {
      docker ps -aq --filter "label=vivero.preview=$derived_id"
      docker ps -aq --filter "label=vivero.preview=$baseline_id"
    } | sort -u
  )"
  if [ -n "$containers" ]; then
    docker rm -f $containers >/dev/null 2>&1 || true
  fi
  docker network ls --format '{{.Name}}' \
    | grep -E "^vivero-(${derived_id}|${baseline_id})-network-" \
    | xargs -r docker network rm >/dev/null 2>&1 || true
}

cleanup() {
  set +e
  cleanup_preview "$derived_id"
  cleanup_preview "$baseline_id"
  cleanup_docker_fallback
  cleanup_known_warm_volumes
  rm -rf "$workdir"
}
trap cleanup EXIT

mkdir -p "$workdir/home" "$workdir/out"
export HOME="$workdir/home"
export VIVERO_HOME="$workdir/vivero-home"

bin/vivero doctor config "$fixture" --json --no-input > "$workdir/out/config-doctor.json"
bin/vivero projects sync "$fixture" --json --no-input > "$workdir/out/sync.json"

bin/vivero preview up integration-stack \
  --id "$baseline_id" \
  --metadata branch=main \
  --wait \
  --timeout 4m \
  --json \
  --no-input > "$workdir/out/baseline-up.json"
bin/vivero preview qa final "$baseline_id" --scope smoke --no-record --no-screenshots --json --no-input > "$workdir/out/baseline-final.json"
bin/vivero preview events "$baseline_id" --json --no-input > "$workdir/out/baseline-events.json"
bin/vivero preview down "$baseline_id" --json --no-input > "$workdir/out/baseline-down.json"

bin/vivero preview up integration-stack \
  --id "$derived_id" \
  --metadata branch=feature/integration-fixture \
  --wait \
  --timeout 4m \
  --json \
  --no-input > "$workdir/out/derived-up.json"
bin/vivero preview qa final "$derived_id" --scope smoke --no-record --no-screenshots --json --no-input > "$workdir/out/derived-final.json"
bin/vivero preview diagnose startup "$derived_id" --json --no-input > "$workdir/out/derived-diagnose.json"
bin/vivero preview events "$derived_id" --json --no-input > "$workdir/out/derived-events.json"

python3 - "$workdir/out" "$repo_root" "$fixture" <<'PY'
import json
import pathlib
import subprocess
import sys
import urllib.request

out = pathlib.Path(sys.argv[1])
repo = pathlib.Path(sys.argv[2])
fixture = sys.argv[3]

def load(name):
    return json.loads((out / name).read_text())

def service(payload, name):
    return payload["preview"]["services"][name]

config = load("config-doctor.json").get("configDoctor", {})
assert config.get("ok") is True, config
assert config.get("project") == "integration-stack", config

baseline = load("baseline-up.json")
derived = load("derived-up.json")
for payload, label in [(baseline, "baseline"), (derived, "derived")]:
    preview = payload.get("preview", {})
    assert preview.get("status") == "running", (label, preview)
    services = preview.get("services") or {}
    assert services.get("api", {}).get("status") == "healthy", (label, services.get("api"))
    assert services.get("web", {}).get("status") == "healthy", (label, services.get("web"))
    assert services.get("web", {}).get("originUrl", "").startswith("http://127.0.0.1:"), (label, services.get("web"))

for name in ["baseline-final.json", "derived-final.json"]:
    final = load(name)
    assert final.get("ok") is True, final
    proof = final.get("proof") or {}
    assert proof.get("runPath"), proof
    assert proof.get("reportPath"), proof
    assert proof.get("finalPath"), proof
    assert final.get("recordSkipped") is True, final

origin = service(derived, "web")["originUrl"]
with urllib.request.urlopen(origin + "/api/status", timeout=10) as response:
    body = response.read().decode()
status = json.loads(body)
assert status == {"app": "integration-stack", "seed": "seed-from-main", "backing": "backing-ok"}, status

events = load("derived-events.json").get("events", [])
baseline_events = load("baseline-events.json").get("events", [])
types = [event.get("type") for event in events]
baseline_types = [event.get("type") for event in baseline_events]
assert "warm.baseline.updated" in baseline_types, baseline_types
assert "setup.afterSeeds" in baseline_types, baseline_types
assert "warm.derived" in types, types
assert "setup.afterSeeds.skipped" in types, types
warm_derived = [event for event in events if event.get("type") == "warm.derived"]
assert any((event.get("metadata") or {}).get("baselineReady") == "true" for event in warm_derived), warm_derived
setup_skipped = [event for event in events if event.get("type") == "setup.afterSeeds.skipped"]
assert any((event.get("metadata") or {}).get("reason") == "warm-baseline-match" for event in setup_skipped), setup_skipped
healthy_services = {event.get("service") for event in events if event.get("type") == "service.healthy"}
assert {"api", "web"} <= healthy_services, healthy_services
warm_volumes = set()
for event in baseline_events + events:
    metadata = event.get("metadata") or {}
    for key in ["baseline", "volume"]:
        value = metadata.get(key)
        if value:
            warm_volumes.add(value)
(out / "warm-volumes.txt").write_text("\n".join(sorted(warm_volumes)) + "\n")

diag = load("derived-diagnose.json").get("diagnosis", {})
assert diag.get("previewId"), diag
assert diag.get("phases"), diag

status = subprocess.check_output(["git", "status", "--short", "--", fixture], cwd=repo, text=True).strip()
allowed = {f"?? {fixture}/"}
assert status == "" or status in allowed, status
PY

if [ "${VIVERO_INTEGRATION_BROWSER_QA:-0}" = "1" ]; then
  if ! command -v npm >/dev/null 2>&1; then
    echo "VIVERO_INTEGRATION_BROWSER_QA=1 requires npm" >&2
    exit 127
  fi
  bin/vivero preview qa final "$derived_id" --scope smoke --format webm --json --no-input > "$workdir/out/derived-browser-final.json"
  python3 - "$workdir/out/derived-browser-final.json" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
assert payload.get("ok") is True, payload
proof = payload.get("proof") or {}
assert proof.get("recordPath"), proof
assert proof.get("videos"), proof
PY
fi

bin/vivero preview down "$derived_id" --discard --json --no-input > "$workdir/out/derived-down.json"
bin/vivero preview down "$baseline_id" --discard --json --no-input > "$workdir/out/baseline-discard.json"
cleanup_known_warm_volumes

leftover_containers="$(
  {
    docker ps -aq --filter "label=vivero.preview=$derived_id"
    docker ps -aq --filter "label=vivero.preview=$baseline_id"
  } | sort -u
)"
if [ -n "$leftover_containers" ]; then
  echo "integration fixture left preview-labeled containers behind" >&2
  docker ps -a --filter "label=vivero.preview=$derived_id" >&2
  docker ps -a --filter "label=vivero.preview=$baseline_id" >&2
  exit 1
fi

leftover_networks="$(
  docker network ls --format '{{.Name}}' \
    | grep -E "^vivero-(${derived_id}|${baseline_id})-network-" || true
)"
if [ -n "$leftover_networks" ]; then
  echo "integration fixture left preview networks behind" >&2
  echo "$leftover_networks" >&2
  exit 1
fi

leftover_volumes=""
if [ -f "$known_volumes_file" ]; then
  while IFS= read -r volume; do
    if [ -n "$volume" ] && docker volume inspect "$volume" >/dev/null 2>&1; then
      leftover_volumes="${leftover_volumes}${volume}
"
    fi
  done < "$known_volumes_file"
fi
if [ -n "$leftover_volumes" ]; then
  echo "integration fixture left warm volumes behind" >&2
  echo "$leftover_volumes" >&2
  exit 1
fi

if [ -n "$(git status --short -- "$fixture" | grep -v '^?? examples/integration-stack/' || true)" ]; then
  echo "integration fixture dirtied tracked example files" >&2
  git status --short -- "$fixture" >&2
  exit 1
fi

echo "integration fixtures passed"
