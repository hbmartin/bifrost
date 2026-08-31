#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

assert_flags() {
  local expected=$1
  shift
  local actual
  actual=$($SCRIPT_DIR/detect-paths.sh "$@" | tr '\n' ' ')
  [[ "$actual" == *"$expected"* ]] || {
    printf 'expected %q in %q for %q\n' "$expected" "$actual" "$*" >&2
    return 1
  }
}

assert_flags 'go=true' core/bifrost.go
assert_flags 'web=false' core/bifrost.go
assert_flags 'repo_docs=true' docs/index.mdx
assert_flags 'go=false' docs/index.mdx
assert_flags 'infra_contracts=true' terraform/modules/bifrost/main.tf
assert_flags 'web=true' ui/removed.tsx
assert_flags 'go=true' .golangci.yml
assert_flags 'security=true' tools/static/package-lock.json
assert_flags 'repo_docs=false' README.txt

printf 'path detection tests passed\n'
