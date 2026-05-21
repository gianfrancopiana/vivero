#!/usr/bin/env bash
set -euo pipefail

# High-signal cleanup markers only. Avoid broad words like "temporary" so
# legitimate product/docs language does not make the audit noisy.
marker_regex='TODO|FIXME|XXX|HACK|DEPRECATED|deprecated|remove me|commented-out|dead code'
commented_go_regex='^\s*//\s*(if|for|func|return|var|const|go |docker|curl|npm|pnpm|make)\b'

status=0

marker_hits="$(git grep -nE "$marker_regex" -- . \
  ':(exclude)dist/**' \
  ':(exclude)bin/**' \
  ':(exclude)scripts/audit-stale-markers.sh' || true)"
if [ -n "$marker_hits" ]; then
  echo "stale cleanup markers found:" >&2
  echo "$marker_hits" >&2
  status=1
fi

commented_hits="$(git grep -nE "$commented_go_regex" -- '*.go' || true)"
if [ -n "$commented_hits" ]; then
  echo "possible commented-out Go code found:" >&2
  echo "$commented_hits" >&2
  status=1
fi

if [ "$status" -eq 0 ]; then
  echo "stale marker audit passed"
fi

exit "$status"
