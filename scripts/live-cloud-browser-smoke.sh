#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "$cmd is required for live cloud/browser smoke" >&2
    exit 127
  fi
}

require_cmd docker
require_cmd cloudflared
require_cmd npm
require_cmd npx
require_cmd python3
if ! docker version >/dev/null 2>&1; then
  echo "docker daemon is not reachable" >&2
  exit 1
fi

version="live-smoke"
go build -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=$version" -o bin/vivero ./cmd/vivero

workdir="$(mktemp -d)"
preview_id="agent-demo-live-${GITHUB_RUN_ID:-$$}-${GITHUB_RUN_ATTEMPT:-0}"
artifact_root="${VIVERO_LIVE_ARTIFACT_DIR:-}"
artifact_dir=""
cleanup_needed=0

if [ -n "$artifact_root" ]; then
  case "$artifact_root" in
    /*) ;;
    *) artifact_root="$repo_root/$artifact_root" ;;
  esac
  artifact_dir="$artifact_root/$preview_id"
fi

copy_artifacts() {
  if [ -z "$artifact_dir" ]; then
    return 0
  fi
  case "$(basename "$artifact_dir")" in
    agent-demo-live-*) ;;
    *) echo "refusing unsafe artifact dir: $artifact_dir" >&2; return 1 ;;
  esac
  rm -rf "$artifact_dir"
  mkdir -p "$artifact_dir"
  if [ -d "$workdir/out" ]; then
    cp -R "$workdir/out" "$artifact_dir/out" || true
  fi
  if [ -d "$workdir/vivero-home/runs/$preview_id" ]; then
    cp -R "$workdir/vivero-home/runs/$preview_id" "$artifact_dir/run" || true
  fi
  if [ -d "$workdir/vivero-home/logs/$preview_id" ]; then
    cp -R "$workdir/vivero-home/logs/$preview_id" "$artifact_dir/logs" || true
  fi
  if [ -d "$workdir/home/.cache/vivero/agent-demo" ]; then
    cp -R "$workdir/home/.cache/vivero/agent-demo" "$artifact_dir/qa" || true
  fi
}

run_down() {
  if command -v timeout >/dev/null 2>&1; then
    HOME="$workdir/home" VIVERO_HOME="$workdir/vivero-home" timeout 90s bin/vivero preview down "$preview_id" --discard --json --no-input > "$workdir/out/down.json" 2> "$workdir/out/down.stderr"
  else
    HOME="$workdir/home" VIVERO_HOME="$workdir/vivero-home" bin/vivero preview down "$preview_id" --discard --json --no-input > "$workdir/out/down.json" 2> "$workdir/out/down.stderr"
  fi
}

cleanup() {
  local status=$?
  set +e
  copy_artifacts
  if [ "$cleanup_needed" -eq 1 ]; then
    run_down || true
    copy_artifacts
  fi
  rm -rf "$workdir"
  exit "$status"
}
trap cleanup EXIT

mkdir -p "$workdir/home" "$workdir/out"
export HOME="$workdir/home"
export VIVERO_HOME="$workdir/vivero-home"
export PLAYWRIGHT_BROWSERS_PATH="$workdir/playwright-browsers"
export VIVERO_PLAYWRIGHT_PACKAGE="${VIVERO_PLAYWRIGHT_PACKAGE:-playwright@${PLAYWRIGHT_VERSION:-1.60.0}}"
case "$VIVERO_PLAYWRIGHT_PACKAGE" in
  playwright@[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "VIVERO_PLAYWRIGHT_PACKAGE must be pinned as playwright@<version>, got: $VIVERO_PLAYWRIGHT_PACKAGE" >&2; exit 1 ;;
esac

# QA recording uses Playwright's bundled ffmpeg even when Chrome is system-provided.
npx --yes "$VIVERO_PLAYWRIGHT_PACKAGE" --version >/dev/null
npx --yes "$VIVERO_PLAYWRIGHT_PACKAGE" install ffmpeg >/dev/null

bin/vivero doctor config examples/agent-demo --json --no-input > "$workdir/out/config-doctor.json"
bin/vivero projects sync examples/agent-demo --json --no-input > "$workdir/out/sync.json"
cleanup_needed=1
bin/vivero preview up agent-demo \
  --id "$preview_id" \
  --public \
  --wait \
  --timeout 6m \
  --json \
  --no-input > "$workdir/out/up-public.json"

bin/vivero preview events "$preview_id" --json --no-input > "$workdir/out/events-before-qa.json"
bin/vivero preview qa final "$preview_id" \
  --scope smoke \
  --public \
  --format webm \
  --json \
  --no-input > "$workdir/out/final-public-browser.json"
bin/vivero preview events "$preview_id" --json --no-input > "$workdir/out/events-after-qa.json"

python3 - "$workdir/out" <<'PY'
import json
import pathlib
import sys
import time
import urllib.parse
import urllib.request

out = pathlib.Path(sys.argv[1])

def load(name):
    return json.loads((out / name).read_text())

def check(condition, detail):
    if not condition:
        raise AssertionError(detail)

config = load("config-doctor.json").get("configDoctor", {})
check(config.get("ok") is True, config)

up = load("up-public.json").get("preview", {})
check(up.get("status") == "running", up)
web = (up.get("services") or {}).get("web") or {}
public_url = web.get("url") or ""
origin_url = web.get("originUrl") or ""
parsed = urllib.parse.urlparse(public_url)
check(parsed.scheme == "https", public_url)
check(parsed.hostname and (parsed.hostname == "trycloudflare.com" or parsed.hostname.endswith(".trycloudflare.com")), public_url)
check(origin_url.startswith("http://127.0.0.1:"), web)
check(web.get("tunnelPid", 0) > 0, web)
check(web.get("tunnelLogPath"), web)

body = None
last_error = None
for _ in range(12):
    try:
        with urllib.request.urlopen(public_url.rstrip("/") + "/api/status", timeout=10) as response:
            body = response.read().decode()
        break
    except Exception as exc:  # external quick tunnels can need a moment after startup
        last_error = exc
        time.sleep(5)
if body is None:
    raise AssertionError(f"public URL did not serve /api/status: {last_error}")
status = json.loads(body)
check(status.get("app") == "agent-demo", status)

before_events = load("events-before-qa.json").get("events", [])
after_events = load("events-after-qa.json").get("events", [])
types = [event.get("type") for event in before_events + after_events]
check("tunnel.started" in types, types)
check("tunnel.ready" in types, types)
check("preview.running" in types, types)

final = load("final-public-browser.json")
check(final.get("ok") is True, final)
check(final.get("target") == "public", final)
proof = final.get("proof") or {}
check(proof.get("target") == "public", proof)
check(proof.get("url") == public_url, (proof, public_url))
check(proof.get("smoke") is True, proof)
check(proof.get("screenshots"), proof)
check(proof.get("videos"), proof)
check(proof.get("recordPath"), proof)
check(proof.get("finalPath"), proof)

for key in ["screenshots", "videos"]:
    for artifact in proof.get(key) or []:
        if not pathlib.Path(artifact).exists():
            raise AssertionError(f"missing {key} artifact: {artifact}")
PY

run_down
cleanup_needed=0
copy_artifacts

echo "live cloud/browser smoke passed"
