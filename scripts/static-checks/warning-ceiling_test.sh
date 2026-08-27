#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
emit_one() { printf 'warning: retained debt\n'; }
emit_two() { printf 'warning: retained debt\nwarning: new debt\n'; }
export -f emit_one emit_two

"$SCRIPT_DIR/warning-ceiling.sh" 1 bash -c emit_one >/dev/null
if "$SCRIPT_DIR/warning-ceiling.sh" 1 bash -c emit_two >/dev/null 2>&1; then
  printf 'warning ceiling accepted a new warning\n' >&2
  exit 1
fi
printf 'warning ceiling tests passed\n'
