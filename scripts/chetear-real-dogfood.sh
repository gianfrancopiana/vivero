#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "$cmd is required" >&2
    exit 127
  fi
}

require_cmd git
require_cmd python3
require_cmd docker
require_cmd npm

if ! docker version >/dev/null 2>&1; then
  echo "docker daemon is not reachable" >&2
  exit 1
fi

if [ -n "${VIVERO_BIN:-}" ]; then
  if ! vivero_bin="$(command -v "$VIVERO_BIN" 2>/dev/null)"; then
    echo "VIVERO_BIN is not executable or on PATH: $VIVERO_BIN" >&2
    exit 127
  fi
else
  go build -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=chetear-dogfood" -o bin/vivero ./cmd/vivero
  vivero_bin="$repo_root/bin/vivero"
fi

workspace_root="${VIVERO_DOGFOOD_ROOT:-$HOME/.hermes/workspace}"
chetear_repo="${VIVERO_CHETEAR_REPO:-$workspace_root/gianfrancopiana/chetear.com}"
if [ ! -d "$chetear_repo/.git" ]; then
  if [ "${VIVERO_REAL_DOGFOOD_REQUIRE:-0}" = "1" ]; then
    echo "chetear repo not found: $chetear_repo" >&2
    exit 1
  fi
  echo "chetear repo not found: $chetear_repo; skipping real dogfood"
  exit 0
fi
if [ ! -f "$chetear_repo/site/package.json" ]; then
  echo "chetear site package.json not found under $chetear_repo" >&2
  exit 1
fi

workdir="$(mktemp -d)"
preview_id="chetear-real-dogfood-$$"
up_started=0
keep_artifacts="${VIVERO_REAL_DOGFOOD_KEEP_ARTIFACTS:-0}"
cleanup() {
  set +e
  if [ "$up_started" -eq 1 ]; then
    HOME="$workdir/home" VIVERO_HOME="$workdir/vivero-home" "$vivero_bin" preview down "$preview_id" --discard --json --no-input > "$workdir/out/down-cleanup.json" 2>/dev/null
  fi
  if [ "$keep_artifacts" != "1" ]; then
    rm -rf "$workdir"
  else
    echo "kept chetear dogfood artifacts at $workdir"
  fi
}
trap cleanup EXIT

mkdir -p "$workdir/home" "$workdir/out" "$workdir/proof" "$workdir/app" "$workdir/project" "$workdir/deploy-project"
export HOME="$workdir/home"
export VIVERO_HOME="$workdir/vivero-home"

git -C "$chetear_repo" archive --format=tar HEAD | tar -C "$workdir/app" -xf -
app_dir="$workdir/app"
proof_dir="$workdir/proof"
preview_config="$workdir/project/vivero.yml"
deploy_config="$workdir/deploy-project/vivero.yml"

python3 - "$preview_config" "$deploy_config" "$app_dir" "$proof_dir" <<'PY'
import pathlib
import shlex
import sys
import textwrap

preview_out = pathlib.Path(sys.argv[1])
deploy_out = pathlib.Path(sys.argv[2])
app = pathlib.Path(sys.argv[3])
proof = pathlib.Path(sys.argv[4])
app_q = shlex.quote(str(app))
proof_q = shlex.quote(str(proof))

def cmd(body: str) -> str:
    return "\n".join("          " + line for line in textwrap.dedent(body).strip().splitlines())

prepare = cmd(f"""
    set -euo pipefail
    mkdir -p {proof_q}
    cd {app_q}/site
    rm -rf node_modules package-lock.json
    npm install --no-audit --no-fund > {proof_q}/prepare.log 2>&1
    printf 'prepared %s\\n' "$VIVERO_RELEASE_ID" > {proof_q}/prepare.txt
""")
apply = cmd(f"""
    set -euo pipefail
    cd {app_q}/site
    npm run validate:data > {proof_q}/apply.log 2>&1
    test -f src/pages/index.astro
    printf 'applied %s plan %s\\n' "$VIVERO_RELEASE_ID" "$VIVERO_DEPLOY_PLAN_ID" > {proof_q}/apply.txt
""")
smoke = cmd(f"""
    set -euo pipefail
    test -f {app_q}/site/src/pages/index.astro
    test -f {proof_q}/apply.txt
    printf 'smoke %s action %s\\n' "$VIVERO_RELEASE_ID" "$VIVERO_RELEASE_ACTION" >> {proof_q}/smoke.txt
""")
status = cmd(f"""
    set -euo pipefail
    printf 'live-status %s\\n' "$VIVERO_RELEASE_ID" > {proof_q}/status.txt
    printf 'live-status\\n'
""")
rollback = cmd(f"""
    set -euo pipefail
    rm -rf {app_q}/site/dist
    printf 'rolled-back %s from %s\\n' "$VIVERO_RELEASE_ID" "$VIVERO_ROLLBACK_RELEASE_ID" > {proof_q}/rollback.txt
""")

preview_out.write_text(textwrap.dedent(f"""
project:
  name: dogfood-chetear-real
sources:
  site:
    mode: external
    path: {app}
services:
  web:
    source: site
    runtime: docker
    image: node:22-alpine
    workingDir: site
    dependencyVolumes:
      - name: node_modules
        target: /app/site/node_modules
        lifetime: smart
    command: >-
      sh -lc 'npm install --no-audit --no-fund && npm run dev -- --host 0.0.0.0 --port 4321'
    ports:
      http:
        container: 4321
    primaryPort: http
    health:
      path: /
      expectStatus: 200
      timeout: 4m
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
""").strip() + "\n")

deploy_out.write_text(textwrap.dedent(f"""
project:
  name: dogfood-chetear-real
services:
  web:
    runtime: docker
    image: registry.example.com/chetear@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    resources:
      cpus: "1"
      memory: 512m
    health:
      path: /
      expectStatus: 200
      timeout: 30s
deploy:
  environments:
    production:
      commandTimeout: 5m
      statusTimeout: 30s
      prepareCommand: |-
{prepare}
      applyCommand: |-
{apply}
      smokeCommand: |-
{smoke}
      statusCommand: |-
{status}
      rollbackCommand: |-
{rollback}
""").strip() + "\n")
PY

run_json() {
  local name="$1"
  shift
  "$@" > "$workdir/out/$name.json"
}

run_json config-doctor "$vivero_bin" doctor config "$workdir/project" --json --no-input
run_json sync "$vivero_bin" projects sync "$workdir/project" --json --no-input
run_json up "$vivero_bin" preview up dogfood-chetear-real --id "$preview_id" --wait --timeout 5m --json --no-input
up_started=1
run_json evidence-smoke "$vivero_bin" evidence smoke "preview:$preview_id" --json --no-input
run_json evidence-logs "$vivero_bin" evidence logs "preview:$preview_id" web --json --no-input
run_json evidence-events "$vivero_bin" evidence events "preview:$preview_id" --tail --json --no-input
run_json final-qa "$vivero_bin" preview qa final "preview:$preview_id" --scope smoke --no-record --no-screenshots --json --no-input
run_json down "$vivero_bin" preview down "$preview_id" --discard --json --no-input
up_started=0

run_json production-doctor "$vivero_bin" doctor production --project "$workdir/deploy-project" --json --no-input
run_json deploy-plan "$vivero_bin" deploy plan "$workdir/deploy-project" --environment production --json --no-input
plan_id="$(python3 - "$workdir/out/deploy-plan.json" <<'PY'
import json
import pathlib
import sys

payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
plan = payload.get("plan") or {}
errors = []
if plan.get("ok") is not True:
    errors.append("plan is not ok")
if plan.get("project") != "dogfood-chetear-real":
    errors.append(f"project={plan.get('project')!r}")
if not plan.get("applyCommand"):
    errors.append("applyCommand missing")
if not plan.get("id"):
    errors.append("plan id missing")
if errors:
    print("invalid deploy plan: " + "; ".join(errors), file=sys.stderr)
    print(json.dumps(plan, indent=2, sort_keys=True), file=sys.stderr)
    sys.exit(1)
print(plan["id"])
PY
)"
run_json deploy-apply "$vivero_bin" deploy apply "$plan_id" --json --no-input
release_id="$(python3 - "$workdir/out/deploy-apply.json" <<'PY'
import json
import pathlib
import sys

payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
release = payload.get("release") or {}
if release.get("status") != "applied" or not release.get("id"):
    print("invalid applied release", file=sys.stderr)
    print(json.dumps(release, indent=2, sort_keys=True), file=sys.stderr)
    sys.exit(1)
print(release["id"])
PY
)"
run_json release-events "$vivero_bin" release events "release:$release_id" --json --no-input
run_json release-logs "$vivero_bin" release logs "release:$release_id" --json --no-input
run_json release-smoke "$vivero_bin" release smoke dogfood-chetear-real --environment production --json --no-input
run_json release-status "$vivero_bin" release status dogfood-chetear-real --environment production --json --no-input
run_json release-rollback "$vivero_bin" release rollback dogfood-chetear-real "$release_id" --environment production --json --no-input

python3 - "$workdir/out" "$proof_dir" "$release_id" <<'PY'
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
proof = pathlib.Path(sys.argv[2])
release_id = sys.argv[3]

def load(name):
    return json.loads((out / f"{name}.json").read_text())


def require(condition, message, payload=None):
    if condition:
        return
    print(message, file=sys.stderr)
    if payload is not None:
        print(json.dumps(payload, indent=2, sort_keys=True), file=sys.stderr)
    sys.exit(1)

config = load("config-doctor").get("configDoctor", {})
require(config.get("ok") is True, "config doctor failed", config)
up = load("up").get("preview", {})
require(up.get("status") == "running", "preview did not report running", up)
web = (up.get("services") or {}).get("web") or {}
require(web.get("url", "").startswith("http://127.0.0.1:"), "preview web URL was not local", web)
smoke_payload = load("evidence-smoke")
require(smoke_payload.get("ok") is True, "preview smoke failed", smoke_payload)
require(smoke_payload.get("targetRef", {}).get("kind") == "preview", "preview smoke target ref mismatch", smoke_payload)
require(any(result.get("ok") is True for result in smoke_payload.get("results", [])), "preview smoke had no passing result", smoke_payload)
final_qa = load("final-qa")
require(final_qa.get("ok") is True, "final QA failed", final_qa)
require(final_qa.get("proof") or final_qa.get("run"), "final QA did not include proof/run metadata", final_qa)
logs = load("evidence-logs")
require(logs.get("targetRef", {}).get("kind") == "preview", "logs target ref mismatch", logs)
events = load("evidence-events")
require(events.get("targetRef", {}).get("kind") == "preview", "events target ref mismatch", events)
production = load("production-doctor").get("productionDoctor", {})
require(production.get("ok") is True, "production doctor failed", production)
apply = load("deploy-apply").get("release", {})
require(apply.get("id") == release_id, "deploy apply release id mismatch", apply)
require(any(phase.get("name") == "smoke" and phase.get("status") == "succeeded" for phase in apply.get("phases", [])), "deploy apply did not record a successful smoke phase", apply)
release_events = load("release-events")
require(release_events.get("targetRef", {}).get("ref") == f"release:{release_id}", "release events target ref mismatch", release_events)
release_logs = load("release-logs")
require(release_logs.get("targetRef", {}).get("ref") == f"release:{release_id}", "release logs target ref mismatch", release_logs)
release_smoke = load("release-smoke")
require(release_smoke.get("targetRef", {}).get("ref") == f"release:{release_id}", "release smoke target ref mismatch", release_smoke)
require(release_smoke.get("smoke", {}).get("ok") is True, "release smoke failed", release_smoke)
status = load("release-status")
require(status.get("status") == "live-status", "release status mismatch", status)
rollback = load("release-rollback").get("release", {})
require(rollback.get("status") == "rolled_back", "release rollback failed", rollback)
for name in ("prepare.txt", "apply.txt", "smoke.txt", "status.txt", "rollback.txt"):
    path = proof / name
    require(path.exists(), f"missing proof file: {path}")
apply_text = (proof / "apply.txt").read_text()
smoke_text = (proof / "smoke.txt").read_text()
require(release_id in apply_text, "apply proof missing release id", apply_text)
require(release_id in smoke_text, "smoke proof missing release id", smoke_text)
print(f"validated chetear real dogfood release {release_id}")
PY

echo "chetear real dogfood passed"
