#!/usr/bin/env bash
set -euo pipefail

snapshot=1
dist_dir="${DIST_DIR:-dist}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --skip-snapshot)
      snapshot=0
      shift
      ;;
    --dist)
      if [ "$#" -lt 2 ]; then
        echo "--dist requires a directory" >&2
        exit 2
      fi
      dist_dir="$2"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required for release smoke JSON assertions" >&2
  exit 127
fi

if [ "$snapshot" -eq 1 ]; then
  if ! command -v goreleaser >/dev/null 2>&1; then
    echo "goreleaser is required for release smoke; install GoReleaser or run with --skip-snapshot after creating dist/" >&2
    exit 127
  fi
  goreleaser release --snapshot --clean
fi

if [ ! -d "$dist_dir" ]; then
  echo "dist directory not found: $dist_dir" >&2
  exit 1
fi

shopt -s nullglob
archives=("$dist_dir"/*.tar.gz)
if [ "${#archives[@]}" -eq 0 ]; then
  echo "no release archives found in $dist_dir" >&2
  exit 1
fi

checksum_file="$dist_dir/checksums.txt"
if [ ! -f "$checksum_file" ]; then
  echo "missing release checksum file: $checksum_file" >&2
  exit 1
fi
python3 - "$checksum_file" "${archives[@]}" <<'PY'
import hashlib
import pathlib
import sys
checksum_file = pathlib.Path(sys.argv[1])
archives = [pathlib.Path(p) for p in sys.argv[2:]]
expected = {}
for line in checksum_file.read_text().splitlines():
    parts = line.split()
    if len(parts) != 2:
        raise SystemExit(f"invalid checksum line: {line!r}")
    expected[parts[1]] = parts[0]
for archive in archives:
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    want = expected.get(archive.name)
    if not want:
        raise SystemExit(f"archive missing from checksums.txt: {archive.name}")
    if digest != want:
        raise SystemExit(f"checksum mismatch for {archive.name}: got {digest}, want {want}")
PY

case "$(uname -s)" in
  Linux) host_goos="linux" ;;
  Darwin) host_goos="darwin" ;;
  *) host_goos="" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) host_goarch="amd64" ;;
  arm64|aarch64) host_goarch="arm64" ;;
  *) host_goarch="" ;;
esac

cleanup() {
  if [ "${tmp:-}" != "" ]; then
    rm -rf "$tmp"
  fi
}

tmp=""
smoked=0
for archive in "${archives[@]}"; do
  name="$(basename "$archive")"
  tmp="$(mktemp -d)"
  trap cleanup EXIT
  tar -xzf "$archive" -C "$tmp"

  test -f "$tmp/LICENSE"
  test -f "$tmp/README.md"
  test -f "$tmp/vivero"

  if [ -n "$host_goos" ] && [ -n "$host_goarch" ] && [[ "$name" == *"_${host_goos}_${host_goarch}.tar.gz" ]]; then
    chmod +x "$tmp/vivero"
    export VIVERO_HOME="$tmp/home"
    "$tmp/vivero" version --json --no-input > "$tmp/version.json"
    "$tmp/vivero" capabilities --json --no-input > "$tmp/capabilities.json"
    "$tmp/vivero" doctor --json --no-input > "$tmp/doctor.json"
    "$tmp/vivero" commands --json --no-input > "$tmp/commands.json"
    "$tmp/vivero" schema qa --json --no-input > "$tmp/schema-qa.json"
    "$tmp/vivero" doctor config examples/gumroad --json --no-input > "$tmp/gumroad-config.json"
    "$tmp/vivero" doctor config examples/helper-host-products --json --no-input > "$tmp/helper-config.json"
    python3 - "$tmp" <<'PY'
import json
import pathlib
import sys
root = pathlib.Path(sys.argv[1])
version = json.loads((root / "version.json").read_text())
capabilities = json.loads((root / "capabilities.json").read_text())
doctor = json.loads((root / "doctor.json").read_text())
commands = json.loads((root / "commands.json").read_text())
schema = json.loads((root / "schema-qa.json").read_text())
gumroad = json.loads((root / "gumroad-config.json").read_text())
helper = json.loads((root / "helper-config.json").read_text())

def require(condition, message, payload):
    if not condition:
        raise SystemExit(f"{message}: {payload}")

require(version.get("version"), "version missing", version)
require(version.get("commit") and version.get("commit") != "unknown", "commit provenance missing", version)
require(version.get("date") and version.get("date") != "unknown", "build date provenance missing", version)
require(capabilities.get("version") == version.get("version"), "capability version mismatch", capabilities)
require(capabilities.get("build", {}).get("commit") == version.get("commit"), "capability commit mismatch", capabilities)
require("release-checksums" in capabilities.get("features", []), "release checksum capability missing", capabilities)
require(doctor.get("ok") is True, "doctor failed", doctor)
names = {cmd.get("name") for cmd in commands.get("commands", [])}
require("qa final" in names, "qa final command missing", sorted(names))
require(schema.get("schema", {}).get("jsonStability") == "stable", "qa schema is not stable", schema)
for payload in (gumroad, helper):
    report = payload.get("configDoctor", {})
    require(report.get("ok") is True, "example config doctor failed", report)
PY
    smoked=1
  fi
  rm -rf "$tmp"
  trap - EXIT
done

if [ "$smoked" -ne 1 ]; then
  echo "validated archive contents but found no host-compatible archive for ${host_goos:-unknown}/${host_goarch:-unknown}" >&2
  exit 1
fi

echo "release smoke passed for ${host_goos}/${host_goarch} using $dist_dir"
