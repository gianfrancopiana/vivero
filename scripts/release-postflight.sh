#!/usr/bin/env bash
set -euo pipefail

repo="gianfrancopiana/vivero"
gh_cli="${GH_CLI:-gh}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
bin_dir=""
skip_attestation=0
skip_homebrew=0
install_homebrew=0
example_e2e=0
version=""

usage() {
  cat <<'EOF'
Verify a published Vivero release from the same surfaces users install.

Usage:
  scripts/release-postflight.sh v0.1.0 [options]

Options:
  --repo OWNER/REPO        GitHub repository, default gianfrancopiana/vivero
  --gh-cli COMMAND         GitHub CLI command, default $GH_CLI or gh
  --bin-dir PATH           Temporary installer target. Defaults to a temp dir.
  --skip-attestation       Skip gh attestation verification
  --skip-homebrew          Skip Homebrew tap checks
  --install-homebrew       Install/reinstall gianfrancopiana/tap/vivero and check the binary
  --example-e2e            Run agent-demo preview E2E with the checksum-installed binary
  -h, --help               Show this help

The script checks release metadata, downloads all assets, verifies checksums,
validates the SPDX SBOM, optionally verifies GitHub attestations, runs the
checksum-verifying installer, and verifies the Homebrew tap formula. Homebrew
installation and Docker preview E2E are opt-in so routine postflight checks do
not install/reinstall Vivero or require Docker unless requested.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --repo)
      repo="${2:?missing repo}"
      shift 2
      ;;
    --gh-cli)
      gh_cli="${2:?missing gh cli command}"
      shift 2
      ;;
    --bin-dir)
      bin_dir="${2:?missing bin dir}"
      shift 2
      ;;
    --skip-attestation)
      skip_attestation=1
      shift
      ;;
    --skip-homebrew)
      skip_homebrew=1
      shift
      ;;
    --install-homebrew)
      install_homebrew=1
      shift
      ;;
    --example-e2e)
      example_e2e=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    v*)
      if [ -n "$version" ]; then
        echo "version already set: $version" >&2
        exit 2
      fi
      version="$1"
      shift
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$version" ]; then
  echo "missing release version, e.g. v0.1.0" >&2
  usage >&2
  exit 2
fi
if [ "$skip_homebrew" -eq 1 ] && [ "$install_homebrew" -eq 1 ]; then
  echo "--skip-homebrew and --install-homebrew cannot be combined" >&2
  exit 2
fi
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "version must look like vMAJOR.MINOR.PATCH, got: $version" >&2
  exit 2
fi

plain_version="${version#v}"
tmp="$(mktemp -d)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT
assets_dir="$tmp/assets"
mkdir -p "$assets_dir"
if [ -z "$bin_dir" ]; then
  bin_dir="$tmp/bin"
fi
mkdir -p "$bin_dir"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "$cmd is required" >&2
    exit 127
  fi
}

checksum_cmd() {
  if command -v sha256sum >/dev/null 2>&1; then
    echo sha256sum
  elif command -v shasum >/dev/null 2>&1; then
    echo "shasum -a 256"
  else
    echo "sha256sum or shasum is required" >&2
    exit 127
  fi
}

json_version_check() {
  local json_file="$1"
  python3 - "$json_file" "$plain_version" <<'PY'
import json, pathlib, sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
want = sys.argv[2]
errors = []
if payload.get("version") != want:
    errors.append(f"version={payload.get('version')!r}, want {want!r}")
if not payload.get("commit") or payload.get("commit") == "unknown":
    errors.append("commit missing or unknown")
if not payload.get("date") or payload.get("date") == "unknown":
    errors.append("date missing or unknown")
if errors:
    print("invalid vivero version payload: " + "; ".join(errors), file=sys.stderr)
    print(json.dumps(payload, indent=2, sort_keys=True), file=sys.stderr)
    sys.exit(1)
PY
}

require_cmd "$gh_cli"
require_cmd curl
require_cmd tar
require_cmd python3

printf 'release: '
release_json="$tmp/release.json"
"$gh_cli" release view "$version" --repo "$repo" --json tagName,isDraft,isPrerelease,url > "$release_json"
python3 - "$release_json" "$version" <<'PY'
import json, pathlib, sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
want = sys.argv[2]
errors = []
if payload.get("tagName") != want:
    errors.append(f"tagName={payload.get('tagName')!r}, want {want!r}")
if payload.get("isDraft") is not False:
    errors.append("release is a draft")
if payload.get("isPrerelease") is not False:
    errors.append("release is marked prerelease")
if errors:
    print("invalid release metadata: " + "; ".join(errors), file=sys.stderr)
    print(json.dumps(payload, indent=2, sort_keys=True), file=sys.stderr)
    sys.exit(1)
print(f"{payload['tagName']}\t{payload['url']}")
PY
"$gh_cli" release download "$version" --repo "$repo" --dir "$assets_dir"
for required_asset in checksums.txt vivero.rb vivero_sbom.spdx.json vivero_darwin_amd64.tar.gz vivero_darwin_arm64.tar.gz vivero_linux_amd64.tar.gz vivero_linux_arm64.tar.gz; do
  if [ ! -f "$assets_dir/$required_asset" ]; then
    echo "missing release asset: $required_asset" >&2
    exit 1
  fi
done
for archive in vivero_darwin_amd64.tar.gz vivero_darwin_arm64.tar.gz vivero_linux_amd64.tar.gz vivero_linux_arm64.tar.gz; do
  if ! grep -q "  ${archive}$" "$assets_dir/checksums.txt"; then
    echo "checksums.txt missing entry for $archive" >&2
    exit 1
  fi
done

(
  cd "$assets_dir"
  cmd="$(checksum_cmd)"
  # shellcheck disable=SC2086
  $cmd -c checksums.txt
)
echo "checksums: ok"

"$script_dir/verify-release-sbom.py" --version "$version" --sbom "$assets_dir/vivero_sbom.spdx.json" --dist "$assets_dir"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  darwin|linux) ;;
  *) echo "unsupported host OS for installer smoke: $os" >&2; exit 1 ;;
esac
arch="$(uname -m)"
case "$arch" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) echo "unsupported host arch for installer smoke: $arch" >&2; exit 1 ;;
esac
host_asset="$assets_dir/vivero_${os}_${arch}.tar.gz"
if [ ! -f "$host_asset" ]; then
  echo "missing host archive: $host_asset" >&2
  exit 1
fi

if [ "$skip_attestation" -eq 0 ]; then
  "$gh_cli" attestation verify "$host_asset" --repo "$repo" >/dev/null
  "$gh_cli" attestation verify "$assets_dir/checksums.txt" --repo "$repo" >/dev/null
  "$gh_cli" attestation verify "$assets_dir/vivero.rb" --repo "$repo" >/dev/null
  "$gh_cli" attestation verify "$assets_dir/vivero_sbom.spdx.json" --repo "$repo" >/dev/null
  echo "attestations: ok"
else
  echo "attestations: skipped"
fi

"$script_dir/install.sh" --version "$version" --bin-dir "$bin_dir" >/dev/null
"$bin_dir/vivero" version --json --no-input > "$tmp/installer-version.json"
json_version_check "$tmp/installer-version.json"
echo "installer: ok"

if [ "$example_e2e" -eq 1 ]; then
  VIVERO_BIN="$bin_dir/vivero" "$script_dir/example-e2e.sh"
  echo "installer example e2e: ok"
fi

if [ "$skip_homebrew" -eq 0 ]; then
  require_cmd brew
  brew_info_json="$tmp/brew-info.json"
  brew info --json=v2 gianfrancopiana/tap/vivero > "$brew_info_json"
  python3 - "$brew_info_json" "$plain_version" <<'PY'
import json, pathlib, sys
payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
want = sys.argv[2]
formulae = payload.get("formulae") or []
if not formulae:
    print("Homebrew JSON did not include formulae", file=sys.stderr)
    print(json.dumps(payload, indent=2, sort_keys=True), file=sys.stderr)
    sys.exit(1)
formula = formulae[0]
errors = []
if formula.get("full_name") != "gianfrancopiana/tap/vivero":
    errors.append(f"full_name={formula.get('full_name')!r}")
stable = (formula.get("versions") or {}).get("stable")
if stable != want:
    errors.append(f"stable={stable!r}, want {want!r}")
if errors:
    print("invalid Homebrew formula metadata: " + "; ".join(errors), file=sys.stderr)
    print(json.dumps(formula, indent=2, sort_keys=True), file=sys.stderr)
    sys.exit(1)
PY
  echo "homebrew formula: ok"
  if [ "$install_homebrew" -eq 1 ]; then
    if brew list --formula gianfrancopiana/tap/vivero >/dev/null 2>&1; then
      brew reinstall gianfrancopiana/tap/vivero >/dev/null
    else
      brew install gianfrancopiana/tap/vivero >/dev/null
    fi
    brew_bin="$(brew --prefix gianfrancopiana/tap/vivero)/bin/vivero"
    "$brew_bin" version --json --no-input > "$tmp/homebrew-version.json"
    json_version_check "$tmp/homebrew-version.json"
    echo "homebrew install: ok"
  fi
else
  echo "homebrew: skipped"
fi

echo "release postflight passed for $version"
