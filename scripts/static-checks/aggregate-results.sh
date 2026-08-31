#!/usr/bin/env bash

set -euo pipefail

[[ $# -gt 0 ]] || { printf 'usage: aggregate-results.sh RESULT...\n' >&2; exit 2; }
for result in "$@"; do
  case "$result" in
    success|skipped) ;;
    failure|cancelled|timed_out|action_required|stale|startup_failure)
      printf 'required static-check dependency ended as %s\n' "$result" >&2
      exit 1
      ;;
    *)
      printf 'unknown static-check result: %s\n' "$result" >&2
      exit 1
      ;;
  esac
done
printf 'all required static-check dependencies passed or were legitimately skipped\n'
