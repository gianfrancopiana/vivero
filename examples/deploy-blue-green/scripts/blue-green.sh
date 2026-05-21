#!/usr/bin/env sh
set -eu

action="${1:-}"
case "$action" in
  active-slot)
    test "${VIVERO_BLUE_GREEN_SLOTS:-}" = "blue,green"
    printf 'blue'
    ;;
  prepare|smoke|promote)
    : "${VIVERO_BLUE_GREEN_ACTIVE_SLOT:?VIVERO_BLUE_GREEN_ACTIVE_SLOT is required}"
    : "${VIVERO_BLUE_GREEN_TARGET_SLOT:?VIVERO_BLUE_GREEN_TARGET_SLOT is required}"
    printf '%s:%s:%s\n' "$action" "$VIVERO_BLUE_GREEN_ACTIVE_SLOT" "$VIVERO_BLUE_GREEN_TARGET_SLOT" >> blue-green.log
    printf '%s-output' "$action"
    ;;
  status)
    : "${VIVERO_BLUE_GREEN_ACTIVE_SLOT:?VIVERO_BLUE_GREEN_ACTIVE_SLOT is required}"
    printf 'status:%s\n' "$VIVERO_BLUE_GREEN_ACTIVE_SLOT" > blue-green-status.txt
    printf 'live-%s' "$VIVERO_BLUE_GREEN_ACTIVE_SLOT"
    ;;
  rollback)
    : "${VIVERO_BLUE_GREEN_ACTIVE_SLOT:?VIVERO_BLUE_GREEN_ACTIVE_SLOT is required}"
    : "${VIVERO_BLUE_GREEN_TARGET_SLOT:?VIVERO_BLUE_GREEN_TARGET_SLOT is required}"
    printf 'rollback:%s:%s\n' "$VIVERO_BLUE_GREEN_ACTIVE_SLOT" "$VIVERO_BLUE_GREEN_TARGET_SLOT" > blue-green-rollback.txt
    ;;
  *)
    echo "usage: $0 active-slot|prepare|smoke|promote|status|rollback" >&2
    exit 64
    ;;
esac
