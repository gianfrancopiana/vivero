#!/usr/bin/env bash
set -euo pipefail

# Keep the repo's package boundaries intentional:
# - cmd/vivero is a thin CLI entrypoint.
# - internal/schema is data-only and cannot depend on runtime packages.
# - skills is only the embed boundary for the bundled agent skill.
module="$(go list -m)"
status=0

imports_for() {
  go list -f '{{range .Imports}}{{.}}{{"\n"}}{{end}}' "$1" | sort
}

check_exact_imports() {
  local pkg="$1"
  shift
  local imports allowed ok import
  imports="$(imports_for "$pkg")"
  while IFS= read -r import; do
    [ -n "$import" ] || continue
    ok=0
    for allowed in "$@"; do
      if [ "$import" = "$allowed" ]; then
        ok=1
        break
      fi
    done
    if [ "$ok" -ne 1 ]; then
      printf '%s imports %s; allowed imports are: %s\n' "$pkg" "$import" "$*" >&2
      status=1
    fi
  done <<< "$imports"
}

check_exact_imports "$module/cmd/vivero" \
  os \
  "$module/internal/vivero"

check_exact_imports "$module/internal/schema" \
  time

check_exact_imports "$module/skills" \
  embed

while IFS=' ' read -r pkg imports; do
  [ -n "$pkg" ] || continue
  if [ "$pkg" != "$module/internal/vivero" ] && [[ ",$imports," == *",$module/skills,"* ]]; then
    printf '%s imports top-level skills package; only internal/vivero may consume the embed boundary\n' "$pkg" >&2
    status=1
  fi
  if [ "$pkg" != "$module/cmd/vivero" ] && [ "$pkg" != "$module/internal/vivero" ] && [[ ",$imports," == *",$module/internal/vivero,"* ]]; then
    printf '%s imports internal/vivero; only cmd/vivero should depend on the runtime package\n' "$pkg" >&2
    status=1
  fi
done < <(go list -f '{{.ImportPath}} {{join .Imports ","}}' ./...)

if [ "$status" -eq 0 ]; then
  echo "package boundary audit passed"
fi

exit "$status"
