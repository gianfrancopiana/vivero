#!/usr/bin/env bash

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "$cmd is required" >&2
    exit 127
  fi
}

validate_semver_tag() {
  local version="$1"
  local label="${2:-version}"
  if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "$label must look like vMAJOR.MINOR.PATCH, got: $version" >&2
    exit 2
  fi
}

validate_github_repo() {
  local repo="$1"
  local label="${2:---repo}"
  if [[ ! "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
    echo "$label must be OWNER/REPO with GitHub-safe characters, got: $repo" >&2
    exit 2
  fi
}

mktemp_workdir() {
  mktemp -d
}
