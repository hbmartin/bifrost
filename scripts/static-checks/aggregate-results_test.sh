#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
aggregate="$SCRIPT_DIR/aggregate-results.sh"

"$aggregate" success skipped >/dev/null
for state in failure cancelled timed_out; do
  if "$aggregate" success "$state" >/dev/null 2>&1; then
    printf 'aggregate accepted %s\n' "$state" >&2
    exit 1
  fi
done
printf 'aggregate result tests passed\n'
