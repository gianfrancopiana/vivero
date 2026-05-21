#!/usr/bin/env bash
set -euo pipefail

# Builds legitimately create ignored output directories. This target fails only
# on unexpected ignored files so local release/build artifacts do not make the
# audit unusable.
status=0
ignored="$(git status --short --ignored --untracked-files=normal | awk '/^!! / {print substr($0, 4)}')"

if [ -n "$ignored" ]; then
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    case "$path" in
      bin/|dist/|.tmp/|coverage.out|*.test)
        ;;
      *)
        echo "unexpected ignored artifact: $path" >&2
        status=1
        ;;
    esac
  done <<< "$ignored"
fi

if [ "$status" -eq 0 ]; then
  echo "ignored artifact audit passed"
fi

exit "$status"
