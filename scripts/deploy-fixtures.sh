#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required for deploy fixture JSON assertions" >&2
  exit 127
fi

go build -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=deploy-fixtures" -o bin/vivero ./cmd/vivero

workdir="$(mktemp -d)"
cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

home="$workdir/home"
ready_project="$workdir/ready-project"
blocked_project="$workdir/blocked-project"
out="$workdir/out"
mkdir -p "$home" "$ready_project" "$blocked_project" "$out"

run_json() {
  local name="$1"
  shift
  if ! VIVERO_HOME="$home/.vivero" HOME="$home" "$@" > "$out/$name.json" 2> "$out/$name.stderr"; then
    echo "$name failed" >&2
    cat "$out/$name.stderr" >&2
    exit 1
  fi
  if [ -s "$out/$name.stderr" ]; then
    echo "$name wrote stderr in JSON mode" >&2
    cat "$out/$name.stderr" >&2
    exit 1
  fi
}

cat > "$ready_project/vivero.yml" <<'YAML'
project:
  name: deploy-ready
services:
  web:
    image: registry.example.com/deploy-ready@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    port: 3000
    health:
      path: /
      timeout: 30s
    resources:
      cpus: '1'
      memory: 512m
deploy:
  environments:
    production:
      applyCommand: 'printf "applied:%s:%s\n" "$VIVERO_DEPLOY_PLAN_ID" "$VIVERO_RELEASE_ID" > deploy-applied.txt'
      statusCommand: 'printf "live-status:%s\n" "$VIVERO_RELEASE_ID" > deploy-status.txt; printf live-status'
      rollbackCommand: 'printf "rollback:%s\n" "$VIVERO_ROLLBACK_RELEASE_ID" > deploy-rollback.txt'
YAML

cat > "$blocked_project/vivero.yml" <<'YAML'
project:
  name: deploy-blocked
sources:
  app:
    mode: external
    path: .
public:
  provider: cloudflared
  mode: quick
services:
  web:
    source: app
    build:
      context: .
      dockerfile: Dockerfile
    port: 3000
    public: true
    health:
      path: /
      timeout: 30s
    resources:
      cpus: '1'
      memory: 512m
deploy:
  environments:
    production:
      applyCommand: 'printf should-not-run > deploy-applied.txt'
      statusCommand: 'printf should-not-run'
      rollbackCommand: 'printf should-not-run > deploy-rollback.txt'
YAML

run_json doctor-ready bin/vivero doctor production --project "$ready_project" --json --no-input
run_json plan-ready bin/vivero deploy plan "$ready_project" --environment production --json --no-input
plan_id="$(python3 - "$out/plan-ready.json" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
plan = payload.get("plan") or {}
assert plan.get("ok") is True, plan
assert plan.get("verdict") == "ready", plan
assert plan.get("project") == "deploy-ready", plan
assert plan.get("environment") == "production", plan
assert plan.get("applyCommand"), plan
assert plan.get("rollbackCommand"), plan
services = plan.get("services") or []
assert len(services) == 1, services
assert "@sha256:" in services[0].get("image", ""), services
print(plan["id"])
PY
)"

run_json apply bin/vivero deploy apply "$plan_id" --json --no-input
release_id="$(python3 - "$out/apply.json" "$ready_project/deploy-applied.txt" "$plan_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
proof = pathlib.Path(sys.argv[2]).read_text()
plan_id = sys.argv[3]
assert release.get("status") == "applied", release
assert release.get("planId") == plan_id, release
assert plan_id in proof, proof
assert release.get("id") in proof, (release, proof)
print(release["id"])
PY
)"

run_json status bin/vivero release status deploy-ready --environment production --json --no-input
python3 - "$out/status.json" "$ready_project/deploy-status.txt" "$release_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
proof = pathlib.Path(sys.argv[2]).read_text()
release_id = sys.argv[3]
assert release.get("id") == release_id, release
assert payload.get("status") == "live-status", payload
assert release.get("status") == "live-status", release
assert release_id in proof, proof
PY

run_json rollback bin/vivero release rollback deploy-ready "$release_id" --environment production --json --no-input
python3 - "$out/rollback.json" "$ready_project/deploy-rollback.txt" "$release_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
proof = pathlib.Path(sys.argv[2]).read_text()
assert release.get("status") == "rolled_back", release
assert release.get("rollbackOf") == sys.argv[3], release
assert sys.argv[3] in proof, proof
PY

set +e
VIVERO_HOME="$home/.vivero" HOME="$home" bin/vivero deploy plan "$blocked_project" --environment production --json --no-input > "$out/plan-blocked.json" 2> "$out/plan-blocked.stderr"
blocked_exit=$?
set -e
if [ "$blocked_exit" -eq 0 ]; then
  echo "blocked deploy fixture unexpectedly passed" >&2
  exit 1
fi
if [ -s "$out/plan-blocked.stderr" ]; then
  echo "blocked deploy fixture should keep JSON mode errors out of stderr for plan diagnostics" >&2
  cat "$out/plan-blocked.stderr" >&2
  exit 1
fi
blocked_plan_id="$(python3 - "$out/plan-blocked.json" "$blocked_project" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
plan = payload.get("plan") or {}
assert plan.get("ok") is False, plan
assert plan.get("verdict") == "blocked", plan
assert plan.get("id"), plan
codes = {diagnostic.get("code") for diagnostic in plan.get("diagnostics", [])}
for code in {"mutable-source", "mutable-build", "quick-tunnel-production"}:
    assert code in codes, (code, codes, plan)
for forbidden in ["deploy-applied.txt", "deploy-rollback.txt"]:
    assert not (pathlib.Path(sys.argv[2]) / forbidden).exists(), forbidden
print(plan["id"])
PY
)"

set +e
VIVERO_HOME="$home/.vivero" HOME="$home" bin/vivero deploy apply "$blocked_plan_id" --json --no-input > "$out/apply-blocked.json" 2> "$out/apply-blocked.stderr"
apply_blocked_exit=$?
set -e
if [ "$apply_blocked_exit" -eq 0 ]; then
  echo "blocked deploy plan unexpectedly applied" >&2
  exit 1
fi
python3 - "$out/apply-blocked.stderr" "$blocked_project" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
error = payload.get("error") or {}
assert error.get("code") == "error", payload
assert "blocked" in error.get("message", ""), payload
for forbidden in ["deploy-applied.txt", "deploy-rollback.txt"]:
    assert not (pathlib.Path(sys.argv[2]) / forbidden).exists(), forbidden
PY

echo "deploy fixtures passed"
