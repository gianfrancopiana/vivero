#!/usr/bin/env bash
set -euo pipefail

# Every tracked shell script should be discoverable from Makefile, CI, docs,
# another script, or another product-facing file. This catches scripts that are
# left behind after release/runtime flows change.
status=0

while IFS= read -r script; do
  [ -n "$script" ] || continue
  case "$script" in
    scripts/audit-script-refs.sh) continue ;;
  esac
  base="$(basename "$script")"
  refs="$(git grep -nE "${script}|${base}" -- . \
    ':(exclude)dist/**' \
    ':(exclude)bin/**' \
    ":(exclude)$script" \
    ':(exclude)scripts/audit-script-refs.sh' || true)"
  if [ -z "$refs" ]; then
    echo "unreferenced tracked script: $script" >&2
    status=1
  fi
done <<EOF
$(git ls-files 'scripts/*.sh')
EOF

if [ "$status" -eq 0 ]; then
  echo "script reference audit passed"
fi

exit "$status"
