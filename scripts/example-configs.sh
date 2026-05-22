#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "$script_dir/lib/common.sh"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

require_cmd python3

go build -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=example-configs" -o bin/vivero ./cmd/vivero

workdir="$(mktemp_workdir)"
cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

mkdir -p "$workdir/home" "$workdir/out" "$workdir/bin"
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
echo "fake docker only supports buildx version in example config fixtures" >&2
exit 127
SH
chmod +x "$workdir/bin/docker"
export PATH="$workdir/bin:$PATH"
export HOME="$workdir/home"
export VIVERO_HOME="$workdir/vivero-home"

fixtures=(
  examples/gumroad
  examples/helper-host-products
  examples/nasty-integration
)

for fixture in "${fixtures[@]}"; do
  name="$(basename "$fixture")"
  bin/vivero doctor config "$fixture" --json --no-input > "$workdir/out/$name-doctor.json"
  bin/vivero projects sync "$fixture" --json --no-input > "$workdir/out/$name-sync.json"
done

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
print(f"validated {len(list(out.glob('*.json')))} example config artifacts")
PY

echo "example configs passed (${#fixtures[@]} fixture(s))"
