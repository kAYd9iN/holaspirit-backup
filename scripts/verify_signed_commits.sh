#!/usr/bin/env bash
set -euo pipefail

BASE="${1:-}"
HEAD="${2:-HEAD}"

if [ -z "$BASE" ] || [ "$BASE" = "0000000000000000000000000000000000000000" ]; then
  COMMITS=$(git rev-list "$HEAD")
else
  COMMITS=$(git rev-list "${BASE}..${HEAD}")
fi

if [ -z "$COMMITS" ]; then
  echo "No commits to verify."
  exit 0
fi

FAILED=0
for c in $COMMITS; do
  STATUS=$(git log --format="%G?" -1 "$c")
  case "$STATUS" in
    G|U)
      echo "OK: $c (signed)"
      ;;
    N)
      echo "FAIL: $c has no signature"
      FAILED=1
      ;;
    B)
      echo "FAIL: $c has a bad signature"
      FAILED=1
      ;;
    *)
      echo "WARN: $c signature status unknown ($STATUS)"
      ;;
  esac
done

if [ "$FAILED" -eq 1 ]; then
  echo ""
  echo "One or more commits are unsigned. All commits to main must be GPG-signed."
  exit 1
fi
