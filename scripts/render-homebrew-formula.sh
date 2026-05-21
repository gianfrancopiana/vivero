#!/usr/bin/env bash
set -euo pipefail

repo="${VIVERO_REPO:-gianfrancopiana/vivero}"
version=""
dist_dir="dist"
output=""

usage() {
  cat <<'EOF'
Render a Homebrew formula for Vivero from GoReleaser archives and checksums.

Usage: scripts/render-homebrew-formula.sh --version vX.Y.Z [--dist dist] [--output dist/vivero.rb] [--repo OWNER/REPO]
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      version="${2:?--version requires a value}"
      shift 2
      ;;
    --dist)
      dist_dir="${2:?--dist requires a directory}"
      shift 2
      ;;
    --output)
      output="${2:?--output requires a path}"
      shift 2
      ;;
    --repo)
      repo="${2:?--repo requires OWNER/REPO}"
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

if [ -z "$version" ]; then
  echo "--version is required" >&2
  usage >&2
  exit 2
fi
if [[ ! "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "--repo must be OWNER/REPO with GitHub-safe characters, got: $repo" >&2
  exit 2
fi
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+][A-Za-z0-9._-]+)?$ ]]; then
  echo "--version must be a semver tag like v1.2.3, got: $version" >&2
  exit 2
fi
if [ -z "$output" ]; then
  output="$dist_dir/vivero.rb"
fi
checksum_file="$dist_dir/checksums.txt"
if [ ! -f "$checksum_file" ]; then
  echo "missing checksum file: $checksum_file" >&2
  exit 1
fi

sha_for() {
  local asset="$1"
  local sha
  sha="$(awk -v asset="$asset" '$2 == asset {print $1}' "$checksum_file")"
  if [ -z "$sha" ]; then
    echo "missing checksum for $asset" >&2
    exit 1
  fi
  printf '%s' "$sha"
}

tag="$version"
formula_version="${version#v}"
base="https://github.com/${repo}/releases/download/${tag}"
darwin_amd64_sha="$(sha_for vivero_darwin_amd64.tar.gz)"
darwin_arm64_sha="$(sha_for vivero_darwin_arm64.tar.gz)"
linux_amd64_sha="$(sha_for vivero_linux_amd64.tar.gz)"
linux_arm64_sha="$(sha_for vivero_linux_arm64.tar.gz)"

mkdir -p "$(dirname "$output")"
cat > "$output" <<EOF
# typed: false
# frozen_string_literal: true

class Vivero < Formula
  desc "Local-first preview and QA nursery for coding agents"
  homepage "https://github.com/${repo}"
  version "${formula_version}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "${base}/vivero_darwin_arm64.tar.gz"
      sha256 "${darwin_arm64_sha}"
    else
      url "${base}/vivero_darwin_amd64.tar.gz"
      sha256 "${darwin_amd64_sha}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "${base}/vivero_linux_arm64.tar.gz"
      sha256 "${linux_arm64_sha}"
    else
      url "${base}/vivero_linux_amd64.tar.gz"
      sha256 "${linux_amd64_sha}"
    end
  end

  def install
    bin.install "vivero"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/vivero version --json --no-input")
    system "#{bin}/vivero", "doctor", "--json", "--no-input"
  end
end
EOF

ruby -c "$output" >/dev/null
printf 'rendered Homebrew formula: %s\n' "$output"
