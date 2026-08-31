#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
"$STATIC_CHECKS_DIR/bootstrap.sh" gitleaks gosec govulncheck

results=$(mktemp -d)
workspace=$(make_temporary_workspace)
workspace_dir=${workspace%/go.work}
trap 'rm -rf "$results" "$workspace_dir"' EXIT
baseline="$STATIC_CHECKS_DIR/baselines/security.json"

gitleaks_args=(git --redact --report-format json --report-path "$results/gitleaks.json")
if [[ "${FULL_CHECK:-0}" != 1 && -n "${BASE_REF:-}" ]]; then
  gitleaks_args+=(--log-opts "$BASE_REF..HEAD")
fi
set +e
(cd "$REPO_ROOT" && gitleaks "${gitleaks_args[@]}")
gitleaks_status=$?
set -e
((gitleaks_status == 0 || gitleaks_status == 1)) || exit "$gitleaks_status"

ordinal=0
while IFS= read -r mod_file; do
  module_dir=${mod_file%/go.mod}
  module_dir=${module_dir:-.}
  set +e
  (
    cd "$REPO_ROOT/$module_dir"
    GOWORK="$workspace" gosec -quiet -severity medium -confidence medium -fmt json -out "$results/gosec-$ordinal.json" ./...
  )
  status=$?
  set -e
  ((status == 0 || status == 1)) || exit "$status"
  set +e
  (
    cd "$REPO_ROOT/$module_dir"
    GOWORK="$workspace" govulncheck -json ./... >"$results/govulncheck-$ordinal.json"
  )
  status=$?
  set -e
  ((status == 0 || status == 3)) || exit "$status"
  ordinal=$((ordinal + 1))
done < <(go_modules)

comparison_status=0
for kind in gitleaks gosec govulncheck; do
  input=$results
  [[ "$kind" == gitleaks ]] && input="$results/gitleaks.json"
  args=(--kind "$kind" --input "$input" --baseline "$baseline")
  [[ "${UPDATE_SECURITY_BASELINE:-0}" == 1 ]] && args+=(--write)
  python3 "$STATIC_CHECKS_DIR/normalize-security.py" "${args[@]}" || comparison_status=1
done
"$STATIC_CHECKS_DIR/normalize-security_test.sh"
((comparison_status == 0)) || exit "$comparison_status"

log "security checks passed"
