#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required for the agent demo e2e" >&2
  exit 127
fi
if ! docker version >/dev/null 2>&1; then
  echo "docker daemon is not reachable" >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required for JSON assertions" >&2
  exit 127
fi

go build -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=e2e" -o bin/vivero ./cmd/vivero

workdir="$(mktemp -d)"
preview_id="agent-demo-e2e-$$"
up_started=0

cleanup() {
  set +e
  if [ "$up_started" -eq 1 ]; then
    HOME="$workdir/home" VIVERO_HOME="$workdir/vivero-home" bin/vivero down "$preview_id" --discard --json --no-input >/dev/null 2>&1
  fi
  rm -rf "$workdir"
}
trap cleanup EXIT

mkdir -p "$workdir/home" "$workdir/out"
export HOME="$workdir/home"
export VIVERO_HOME="$workdir/vivero-home"

bin/vivero doctor config examples/agent-demo --json --no-input > "$workdir/out/config-doctor.json"
bin/vivero projects sync examples/agent-demo --json --no-input > "$workdir/out/sync.json"
bin/vivero up agent-demo --id "$preview_id" --wait --timeout 3m --json --no-input > "$workdir/out/up.json"
up_started=1
bin/vivero qa final "$preview_id" --scope smoke --no-record --no-screenshots --json --no-input > "$workdir/out/final-smoke.json"
bin/vivero diagnose startup "$preview_id" --json --no-input > "$workdir/out/diagnose.json"

if [ "${VIVERO_EXAMPLE_BROWSER_QA:-0}" = "1" ]; then
  if ! command -v npm >/dev/null 2>&1; then
    echo "VIVERO_EXAMPLE_BROWSER_QA=1 requires npm" >&2
    exit 127
  fi
  bin/vivero qa final "$preview_id" --scope smoke --format webm --json --no-input > "$workdir/out/final-browser.json"
fi

python3 - "$workdir/out" "$repo_root" <<'PY'
import json
import pathlib
import subprocess
import sys

out = pathlib.Path(sys.argv[1])
repo = pathlib.Path(sys.argv[2])

def load(name):
    return json.loads((out / name).read_text())

config = load("config-doctor.json").get("configDoctor", {})
assert config.get("ok") is True, config
assert config.get("project") == "agent-demo", config

up = load("up.json").get("preview", {})
assert up.get("status") == "running", up
services = up.get("services") or {}
web = services.get("web") or {}
assert web.get("url", "").startswith("http://127.0.0.1:"), web

final = load("final-smoke.json")
assert final.get("ok") is True, final
assert final.get("scope") == "smoke", final
assert final.get("recordSkipped") is True, final
proof = final.get("proof") or {}
primary_url = proof.get("primaryUrl") or proof.get("url")
assert primary_url.startswith("http://127.0.0.1:"), proof
assert proof.get("runPath"), proof
assert proof.get("reportPath"), proof
assert proof.get("finalPath"), proof

run = final.get("run") or {}
smoke = run.get("smoke") or {}
assert smoke.get("ok") is True, smoke

if (out / "final-browser.json").exists():
    browser = load("final-browser.json")
    assert browser.get("ok") is True, browser
    bproof = browser.get("proof") or {}
    assert bproof.get("recordPath"), bproof
    assert bproof.get("videos"), bproof

diag = load("diagnose.json").get("diagnosis", {})
assert diag.get("previewId"), diag
assert diag.get("phases"), diag

status = subprocess.check_output(["git", "status", "--short", "--", "examples/agent-demo"], cwd=repo, text=True).strip()
allowed = {"?? examples/agent-demo/"}
assert status == "" or status in allowed, status
PY

bin/vivero down "$preview_id" --discard --json --no-input > "$workdir/out/down.json"
up_started=0

if [ -n "$(git status --short -- examples/agent-demo | grep -v '^?? examples/agent-demo/' || true)" ]; then
  echo "agent demo e2e dirtied tracked example files" >&2
  git status --short -- examples/agent-demo >&2
  exit 1
fi

echo "agent demo e2e passed"
