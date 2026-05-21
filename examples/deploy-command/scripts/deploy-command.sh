#!/usr/bin/env sh
set -eu

action="${1:-}"
case "$action" in
  apply)
    count=0
    if [ -f deploy-count.txt ]; then
      count="$(cat deploy-count.txt)"
    fi
    count=$((count + 1))
    printf '%s' "$count" > deploy-count.txt
    printf 'applied:%s:%s\n' "$VIVERO_DEPLOY_PLAN_ID" "$VIVERO_RELEASE_ID" > deploy-applied.txt
    printf 'apply-output'
    ;;
  smoke)
    printf 'smoked:%s\n' "$VIVERO_RELEASE_ID" > deploy-smoke.txt
    printf 'smoke-output'
    ;;
  status)
    printf 'live-status:%s\n' "$VIVERO_RELEASE_ID" > deploy-status.txt
    printf 'live-status'
    ;;
  rollback)
    : "${VIVERO_ROLLBACK_RELEASE_ID:?VIVERO_ROLLBACK_RELEASE_ID is required}"
    printf 'rollback:%s\n' "$VIVERO_ROLLBACK_RELEASE_ID" > deploy-rollback.txt
    ;;
  *)
    echo "usage: $0 apply|smoke|status|rollback" >&2
    exit 64
    ;;
esac
