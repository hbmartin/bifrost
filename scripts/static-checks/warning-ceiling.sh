#!/usr/bin/env bash

set -euo pipefail

ceiling=${1:?usage: warning-ceiling.sh CEILING COMMAND...}
shift
output=$(mktemp)
trap 'rm -f "$output"' EXIT

set +e
"$@" 2>&1 | tee "$output"
status=${PIPESTATUS[0]}
set -e
((status == 0)) || exit "$status"

warnings=$(grep -Ec '(^|[[:space:]])warning([[:space:]]|:)' "$output" || true)
if ((warnings > ceiling)); then
  printf 'warning ceiling exceeded: %d found, %d allowed\n' "$warnings" "$ceiling" >&2
  exit 1
fi
printf 'warning ceiling: %d/%d\n' "$warnings" "$ceiling"
