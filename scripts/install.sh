#!/usr/bin/env bash
set -euo pipefail

repo="${VIVERO_REPO:-gianfrancopiana/vivero}"
version="${VIVERO_VERSION:-latest}"
bin_dir="${VIVERO_BIN_DIR:-$HOME/.local/bin}"
base_url="${VIVERO_RELEASE_BASE_URL:-}"

usage() {
  cat <<'EOF'
Install Vivero from GitHub release artifacts.

Usage: scripts/install.sh [--version vX.Y.Z|latest] [--bin-dir DIR] [--repo OWNER/REPO] [--base-url URL]

Environment overrides:
  VIVERO_VERSION           Release tag, or latest. Default: latest
  VIVERO_BIN_DIR           Install directory. Default: ~/.local/bin
  VIVERO_REPO              GitHub repo. Default: gianfrancopiana/vivero
  VIVERO_RELEASE_BASE_URL  Override artifact base URL, useful for local smoke tests
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      version="${2:?--version requires a value}"
      shift 2
      ;;
    --bin-dir)
      bin_dir="${2:?--bin-dir requires a value}"
      shift 2
      ;;
    --repo)
      repo="${2:?--repo requires a value}"
      shift 2
      ;;
    --base-url)
      base_url="${2:?--base-url requires a value}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ ! "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "--repo must be OWNER/REPO with GitHub-safe characters, got: $repo" >&2
  exit 2
fi
if [ "$version" != "latest" ] && [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+][A-Za-z0-9._-]+)?$ ]]; then
  echo "--version must be latest or a semver tag like v1.2.3, got: $version" >&2
  exit 2
fi

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "$cmd is required" >&2
    exit 127
  fi
}

require_cmd curl
require_cmd tar

case "$(uname -s)" in
  Linux) goos="linux" ;;
  Darwin) goos="darwin" ;;
  *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) goarch="amd64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

asset="vivero_${goos}_${goarch}.tar.gz"
if [ -z "$base_url" ]; then
  if [ "$version" = "latest" ]; then
    base_url="https://github.com/${repo}/releases/latest/download"
  else
    base_url="https://github.com/${repo}/releases/download/${version}"
  fi
fi
base_url="${base_url%/}"

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

curl -fsSL "${base_url}/checksums.txt" -o "$tmp/checksums.txt"
curl -fsSL "${base_url}/${asset}" -o "$tmp/${asset}"

expected="$(awk -v asset="$asset" '$2 == asset {print $1}' "$tmp/checksums.txt")"
if [ -z "$expected" ]; then
  echo "missing checksum for ${asset}" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/${asset}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp/${asset}" | awk '{print $1}')"
else
  echo "sha256sum or shasum is required" >&2
  exit 127
fi

if [ "$actual" != "$expected" ]; then
  echo "checksum mismatch for ${asset}: got ${actual}, want ${expected}" >&2
  exit 1
fi

tar -xzf "$tmp/${asset}" -C "$tmp"
if [ ! -f "$tmp/vivero" ]; then
  echo "archive did not contain vivero binary" >&2
  exit 1
fi
chmod +x "$tmp/vivero"
mkdir -p "$bin_dir"
install -m 0755 "$tmp/vivero" "$bin_dir/vivero"

"$bin_dir/vivero" version --json --no-input >/dev/null
printf 'Installed vivero to %s\n' "$bin_dir/vivero"
