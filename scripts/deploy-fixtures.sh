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
blue_green_project="$workdir/blue-green-project"
blocked_project="$workdir/blocked-project"
out="$workdir/out"
mkdir -p "$home" "$ready_project" "$blue_green_project" "$blocked_project" "$out"

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
      applyCommand: 'count=0; test -f deploy-count.txt && count=$(cat deploy-count.txt); count=$((count+1)); printf "%s" "$count" > deploy-count.txt; printf "applied:%s:%s\n" "$VIVERO_DEPLOY_PLAN_ID" "$VIVERO_RELEASE_ID" > deploy-applied.txt; printf apply-output'
      smokeCommand: 'printf "smoked:%s\n" "$VIVERO_RELEASE_ID" > deploy-smoke.txt; printf smoke-output'
      statusCommand: 'printf "live-status:%s\n" "$VIVERO_RELEASE_ID" > deploy-status.txt; printf live-status'
      rollbackCommand: 'printf "rollback:%s\n" "$VIVERO_ROLLBACK_RELEASE_ID" > deploy-rollback.txt'
YAML

cat > "$blue_green_project/vivero.yml" <<'YAML'
project:
  name: deploy-blue-green
services:
  web:
    image: registry.example.com/deploy-blue-green@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
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
      strategy: blue-green
      blueGreen:
        slots: [blue, green]
        activeSlotCommand: 'test "$VIVERO_BLUE_GREEN_SLOTS" = "blue,green" && printf blue'
        prepareCommand: 'printf "prepare:%s:%s\n" "$VIVERO_BLUE_GREEN_ACTIVE_SLOT" "$VIVERO_BLUE_GREEN_TARGET_SLOT" >> blue-green.log'
        smokeCommand: 'printf "smoke:%s:%s\n" "$VIVERO_BLUE_GREEN_ACTIVE_SLOT" "$VIVERO_BLUE_GREEN_TARGET_SLOT" >> blue-green.log'
        promoteCommand: 'printf "promote:%s:%s\n" "$VIVERO_BLUE_GREEN_ACTIVE_SLOT" "$VIVERO_BLUE_GREEN_TARGET_SLOT" >> blue-green.log'
        statusCommand: 'printf "status:%s\n" "$VIVERO_BLUE_GREEN_ACTIVE_SLOT" > blue-green-status.txt; printf live-$VIVERO_BLUE_GREEN_ACTIVE_SLOT'
        rollbackCommand: 'printf "rollback:%s:%s\n" "$VIVERO_BLUE_GREEN_ACTIVE_SLOT" "$VIVERO_BLUE_GREEN_TARGET_SLOT" > blue-green-rollback.txt'
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
assert plan.get("stateVersion") == 1, plan
assert plan.get("verdict") == "ready", plan
assert plan.get("project") == "deploy-ready", plan
assert plan.get("environment") == "production", plan
assert plan.get("applyCommand"), plan
assert plan.get("smokeCommand"), plan
assert plan.get("rollbackCommand"), plan
changes = {change.get("kind") for change in plan.get("changes", [])}
assert {"service-image", "deploy-strategy"}.issubset(changes), (changes, plan)
services = plan.get("services") or []
assert len(services) == 1, services
assert "@sha256:" in services[0].get("image", ""), services
print(plan["id"])
PY
)"

run_json apply bin/vivero deploy apply "$plan_id" --json --no-input
release_id="$(python3 - "$out/apply.json" "$ready_project/deploy-applied.txt" "$ready_project/deploy-smoke.txt" "$plan_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
apply_proof = pathlib.Path(sys.argv[2]).read_text()
smoke_proof = pathlib.Path(sys.argv[3]).read_text()
plan_id = sys.argv[4]
assert release.get("stateVersion") == 1, release
assert release.get("status") == "applied", release
assert release.get("planId") == plan_id, release
assert any(event.get("action") == "apply" and event.get("status") == "succeeded" for event in release.get("audit", [])), release
assert any(event.get("action") == "smoke" and event.get("status") == "succeeded" for event in release.get("audit", [])), release
assert "smoke-output" in release.get("output", ""), release
assert plan_id in apply_proof, apply_proof
assert release.get("id") in apply_proof, (release, apply_proof)
assert release.get("id") in smoke_proof, (release, smoke_proof)
print(release["id"])
PY
)"

run_json release-events bin/vivero release events "release:$release_id" --json --no-input
python3 - "$out/release-events.json" "$release_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release_id = sys.argv[2]
assert payload.get("targetRef", {}).get("ref") == f"release:{release_id}", payload
events = payload.get("events") or []
actions = {(event.get("action"), event.get("status")) for event in events}
assert ("apply", "succeeded") in actions, actions
assert ("smoke", "succeeded") in actions, actions
PY

run_json release-logs bin/vivero release logs "release:$release_id" --json --no-input
python3 - "$out/release-logs.json" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
logs = payload.get("logs") or []
content = "\n".join(log.get("content", "") for log in logs)
assert "apply-output" in content, content
assert "smoke-output" in content, content
PY

run_json release-smoke bin/vivero release smoke deploy-ready --environment production --json --no-input
python3 - "$out/release-smoke.json" "$release_id" "$ready_project/deploy-smoke.txt" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release_id = sys.argv[2]
proof = pathlib.Path(sys.argv[3]).read_text()
assert payload.get("targetRef", {}).get("ref") == f"release:{release_id}", payload
assert payload.get("smoke", {}).get("ok") is True, payload
assert release_id in proof, proof
PY

run_json apply-repeat bin/vivero deploy apply "$plan_id" --json --no-input
python3 - "$out/apply-repeat.json" "$ready_project/deploy-count.txt" "$release_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
count = pathlib.Path(sys.argv[2]).read_text().strip()
assert release.get("id") == sys.argv[3], release
assert count == "1", count
PY

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
assert any(event.get("action") == "status" and event.get("status") == "succeeded" for event in release.get("audit", [])), release
assert release_id in proof, proof
PY

run_json apply-after-status bin/vivero deploy apply "$plan_id" --json --no-input
python3 - "$out/apply-after-status.json" "$ready_project/deploy-count.txt" "$release_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
count = pathlib.Path(sys.argv[2]).read_text().strip()
assert release.get("id") == sys.argv[3], release
assert release.get("status") == "live-status", release
assert count == "1", count
PY

run_json rollback bin/vivero release rollback deploy-ready "$release_id" --environment production --json --no-input
rollback_id="$(python3 - "$out/rollback.json" "$ready_project/deploy-rollback.txt" "$release_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
proof = pathlib.Path(sys.argv[2]).read_text()
assert release.get("status") == "rolled_back", release
assert release.get("rollbackOf") == sys.argv[3], release
assert any(event.get("action") == "rollback" and event.get("status") == "succeeded" for event in release.get("audit", [])), release
assert sys.argv[3] in proof, proof
print(release["id"])
PY
)"

run_json rollback-repeat bin/vivero release rollback deploy-ready "$release_id" --environment production --json --no-input
python3 - "$out/rollback-repeat.json" "$rollback_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
assert release.get("id") == sys.argv[2], release
assert release.get("status") == "rolled_back", release
PY

run_json plan-blue-green bin/vivero deploy plan "$blue_green_project" --environment production --json --no-input
blue_green_plan_id="$(python3 - "$out/plan-blue-green.json" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
plan = payload.get("plan") or {}
assert plan.get("ok") is True, plan
assert plan.get("verdict") == "ready", plan
assert plan.get("strategy") == "blue-green", plan
blue_green = plan.get("blueGreen") or {}
assert blue_green.get("activeSlot") == "blue", blue_green
assert blue_green.get("targetSlot") == "green", blue_green
phases = [phase.get("name") for phase in blue_green.get("phases", [])]
assert phases == ["prepare", "smoke", "promote"], phases
print(plan["id"])
PY
)"

run_json apply-blue-green bin/vivero deploy apply "$blue_green_plan_id" --json --no-input
blue_green_release_id="$(python3 - "$out/apply-blue-green.json" "$blue_green_project/blue-green.log" "$blue_green_plan_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
proof = pathlib.Path(sys.argv[2]).read_text()
plan_id = sys.argv[3]
assert release.get("planId") == plan_id, release
assert release.get("status") == "promoted", release
assert release.get("strategy") == "blue-green", release
assert release.get("activeSlot") == "green", release
assert release.get("previousSlot") == "blue", release
for expected in ["prepare:blue:green", "smoke:blue:green", "promote:blue:green"]:
    assert expected in proof, (expected, proof)
print(release["id"])
PY
)"

run_json status-blue-green bin/vivero release status deploy-blue-green --environment production --json --no-input
python3 - "$out/status-blue-green.json" "$blue_green_project/blue-green-status.txt" "$blue_green_release_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
proof = pathlib.Path(sys.argv[2]).read_text()
assert release.get("id") == sys.argv[3], release
assert payload.get("status") == "live-green", payload
assert release.get("activeSlot") == "green", release
assert "status:green" in proof, proof
PY

run_json rollback-blue-green bin/vivero release rollback deploy-blue-green "$blue_green_release_id" --environment production --json --no-input
python3 - "$out/rollback-blue-green.json" "$blue_green_project/blue-green-rollback.txt" "$blue_green_release_id" <<'PY'
import json
import pathlib
import sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
proof = pathlib.Path(sys.argv[2]).read_text()
assert release.get("status") == "rolled_back", release
assert release.get("rollbackOf") == sys.argv[3], release
assert release.get("activeSlot") == "blue", release
assert release.get("previousSlot") == "green", release
assert "rollback:green:blue" in proof, proof
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
