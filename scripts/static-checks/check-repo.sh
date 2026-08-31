#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
"$STATIC_CHECKS_DIR/bootstrap.sh" actionlint shellcheck node python

mapfile -t workflows < <(repository_files | grep -E '^\.github/workflows/[^/]+\.ya?ml$' || true)
(cd "$REPO_ROOT" && actionlint -shellcheck "shellcheck -S error" "${workflows[@]}")

mapfile -t shell_files < <(repository_files | grep -E '\.sh$' || true)
if ((${#shell_files[@]})); then
  shellcheck -S error "${shell_files[@]/#/$REPO_ROOT/}"
fi

selection=$(mktemp)
markdown_selection=$(mktemp)
trap 'rm -f "$selection" "$markdown_selection"' EXIT
if [[ "${FULL_CHECK:-0}" == 1 || (-z "${BASE_REF:-}" && -z "${FILES_FROM:-}" && -z "$(git -C "$REPO_ROOT" diff --cached --name-only)") ]]; then
  repository_files >"$selection"
else
  changed_files >"$selection"
fi

if [[ -n "${BASE_REF:-}" || -n "${FILES_FROM:-}" || -n "$(git -C "$REPO_ROOT" diff --cached --name-only)" ]]; then
  changed_files >"$markdown_selection"
else
  : >"$markdown_selection"
fi

# Helm templates are programs, not YAML documents; check-infra renders them
# before validation. config.json is an intentionally empty local-runtime
# placeholder, while transports/config.schema.json is the tracked contract.
mapfile -t data_files < <(
  grep -E '\.(json|toml|ya?ml)$' "$selection" |
    grep -Ev '(^config\.json$|^helm-charts/[^/]+/templates/)' |
    sed "s#^#$REPO_ROOT/#" || true
)
((${#data_files[@]} == 0)) || "$PYTHON_BIN/python" "$STATIC_CHECKS_DIR/validate-data.py" "${data_files[@]}"

mapfile -t changed_shell < <(grep -E '\.sh$' "$selection" | sed "s#^#$REPO_ROOT/#" || true)
if ((${#changed_shell[@]})); then
  log "non-blocking ShellCheck diagnostics below warning severity"
  shellcheck -S style "${changed_shell[@]}" || true
fi

mapfile -t markdown_files < <(grep -E '\.(md|mdx)$' "$markdown_selection" | sed "s#^#$REPO_ROOT/#" || true)
if ((${#markdown_files[@]})); then
  remark --frail --use remark-preset-lint-recommended --use remark-mdx "${markdown_files[@]}"
fi

if [[ "${FULL_CHECK:-0}" == 1 ]] || grep -Eq '^docs/' "$selection"; then
  (cd "$REPO_ROOT/docs" && mint validate)
  if [[ -n "${BASE_REF:-}" ]]; then
    (cd "$REPO_ROOT" && .github/workflows/scripts/check-new-broken-links.sh "$BASE_REF")
  fi
fi

"$PYTHON_BIN/python" "$STATIC_CHECKS_DIR/repo-hygiene.py"
(cd "$REPO_ROOT" && .github/workflows/scripts/check-egress-allowlist.sh)
(cd "$REPO_ROOT" && .github/workflows/scripts/version-format.test.sh)
(cd "$REPO_ROOT" && .github/workflows/scripts/check-new-broken-links_test.sh)
"$STATIC_CHECKS_DIR/detect-paths_test.sh"
"$STATIC_CHECKS_DIR/aggregate-results_test.sh"
"$STATIC_CHECKS_DIR/warning-ceiling_test.sh"
"$STATIC_CHECKS_DIR/inventory.sh"

log "repository and documentation checks passed"
