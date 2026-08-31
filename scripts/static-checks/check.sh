#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
area=${1:-all}

case "$area" in
  all)
    "$SCRIPT_DIR/check-go.sh"
    "$SCRIPT_DIR/check-web.sh"
    "$SCRIPT_DIR/check-python-rust.sh"
    "$SCRIPT_DIR/check-repo.sh"
    "$SCRIPT_DIR/check-infra.sh"
    "$SCRIPT_DIR/check-security.sh"
    "$SCRIPT_DIR/check-unit.sh"
    ;;
  go) "$SCRIPT_DIR/check-go.sh" ;;
  web) "$SCRIPT_DIR/check-web.sh" ;;
  python-rust) "$SCRIPT_DIR/check-python-rust.sh" ;;
  repo) "$SCRIPT_DIR/check-repo.sh" ;;
  infra) "$SCRIPT_DIR/check-infra.sh" ;;
  security) "$SCRIPT_DIR/check-security.sh" ;;
  unit) "$SCRIPT_DIR/check-unit.sh" ;;
  *) printf 'unknown check area: %s\n' "$area" >&2; exit 2 ;;
esac
