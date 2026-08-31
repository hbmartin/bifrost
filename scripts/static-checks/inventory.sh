#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

assert_nonempty_inventory() {
  local label=$1 pattern=$2 count
  count=$(repository_files | grep -Ec "$pattern" || true)
  ((count > 0)) || die "$label inventory is empty"
  printf '%-24s %4d\n' "$label" "$count"
}

module_count=$(go_modules | wc -l | tr -d ' ')
((module_count == 48)) || die "Go module inventory changed: expected 48, found $module_count; update the documented invariant"
printf '%-24s %4d\n' 'Go modules' "$module_count"
assert_nonempty_inventory 'package manifests' '(^|/)package\.json$'
assert_nonempty_inventory 'TypeScript configs' '(^|/)tsconfig[^/]*\.json$'
assert_nonempty_inventory 'Dockerfiles' '(^|/)Dockerfile([^/]*)$'
assert_nonempty_inventory 'Terraform roots' '^terraform/.*/main\.tf$'

for contract in transports/config.schema.json helm-charts/bifrost/values.schema.json docs/openapi/openapi.json; do
  git -C "$REPO_ROOT" ls-files --error-unmatch "$contract" >/dev/null || die "generated contract is not tracked: $contract"
done

printf 'inventory coverage is complete\n'
