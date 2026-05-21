#!/usr/bin/env bash
set -euo pipefail

formula="dist/vivero.rb"
repo="git@github.com:gianfrancopiana/homebrew-tap.git"
branch="main"
message=""

usage() {
  cat <<'EOF'
Publish a generated Vivero Homebrew formula to a tap repo.

Usage: scripts/publish-homebrew-tap.sh [--formula dist/vivero.rb] [--repo git@github.com:gianfrancopiana/homebrew-tap.git] [--branch main] [--message "vivero vX.Y.Z"]

For GitHub SSH repos, set HOMEBREW_TAP_SSH_KEY to a write-enabled deploy key for the tap repo.
Local filesystem repos are supported for release-smoke tests and do not require a key.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --formula)
      formula="${2:?--formula requires a file}"
      shift 2
      ;;
    --repo)
      repo="${2:?--repo requires a git remote or path}"
      shift 2
      ;;
    --branch)
      branch="${2:?--branch requires a name}"
      shift 2
      ;;
    --message)
      message="${2:?--message requires a value}"
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

if [ ! -f "$formula" ]; then
  echo "missing formula: $formula" >&2
  exit 1
fi
if ! git check-ref-format --branch "$branch" >/dev/null 2>&1 || [[ "$branch" == -* ]]; then
  echo "unsafe branch name: $branch" >&2
  exit 2
fi
if [ -z "$message" ]; then
  message="Update Vivero formula"
fi

formula_abs="$(cd "$(dirname "$formula")" && pwd)/$(basename "$formula")"
ruby -c "$formula_abs" >/dev/null

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

clone() {
  git clone --quiet -- "$repo" "$tmp/tap"
}
push() {
  git push --quiet origin "HEAD:$branch"
}

case "$repo" in
  git@github.com:*|ssh://git@github.com/*)
    if [ -z "${HOMEBREW_TAP_SSH_KEY:-}" ]; then
      echo "HOMEBREW_TAP_SSH_KEY is required to publish to $repo" >&2
      exit 1
    fi
    ssh_dir="$tmp/ssh"
    mkdir -p "$ssh_dir"
    chmod 700 "$ssh_dir"
    key_file="$ssh_dir/homebrew_tap_key"
    known_hosts="$ssh_dir/known_hosts"
    printf '%s\n' "$HOMEBREW_TAP_SSH_KEY" > "$key_file"
    unset HOMEBREW_TAP_SSH_KEY
    chmod 600 "$key_file"
    curl --retry 3 --retry-delay 2 --connect-timeout 10 --max-time 30 -fsSL https://api.github.com/meta -o "$tmp/github-meta.json"
    python3 - "$tmp/github-meta.json" "$known_hosts" <<'PY'
import json
import sys
meta = json.loads(open(sys.argv[1], encoding="utf-8").read())
keys = meta.get("ssh_keys") or []
if not keys:
    raise SystemExit("GitHub API metadata did not include ssh_keys")
with open(sys.argv[2], "w", encoding="utf-8") as out:
    for key in keys:
        out.write(f"github.com {key}\n")
PY
    chmod 600 "$known_hosts"
    export GIT_SSH_COMMAND="ssh -i $key_file -o IdentitiesOnly=yes -o UserKnownHostsFile=$known_hosts -o StrictHostKeyChecking=yes"
    ;;
esac

clone
cd "$tmp/tap"
git checkout -B "$branch" >/dev/null 2>&1
mkdir -p Formula
cp "$formula_abs" Formula/vivero.rb
ruby -c Formula/vivero.rb >/dev/null

if git diff --quiet -- Formula/vivero.rb && [ -z "$(git status --short -- Formula/vivero.rb)" ]; then
  echo "Homebrew tap formula already up to date"
  exit 0
fi

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git add Formula/vivero.rb
git commit -m "$message" >/dev/null
push

echo "published Formula/vivero.rb to $repo#$branch"
