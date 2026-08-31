#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

files=$(mktemp)
trap 'rm -f "$files"' EXIT
if (($#)); then
  printf '%s\n' "$@" >"$files"
else
  changed_files >"$files"
fi

matches() { grep -Eq "$1" "$files"; }
all=false go=false web=false python_rust=false repo_docs=false infra_contracts=false security=false fast_unit=false

if matches '^(Makefile|\.editorconfig|\.gitignore|\.golangci\.yml|\.oxlintrc\.json|ruff\.toml|\.pre-commit-config\.yaml|scripts/static-checks/|tools/static/|\.github/workflows/static-checks\.yml)'; then
  all=true
fi

$all || ! matches '(^|/)(go\.mod|go\.sum|.*\.go)$' || go=true
$all || ! matches '(^|/)(package(-lock)?\.json|tsconfig[^/]*\.json|.*\.(js|jsx|mjs|cjs|ts|tsx))$' || web=true
$all || ! matches '(^|/)(.*\.py|pyproject\.toml|uv\.lock|Cargo\.(toml|lock)|rust-toolchain\.toml|.*\.rs)$' || python_rust=true
$all || ! matches '^(docs/|\.github/workflows/|.*\.(md|mdx|ya?ml|json|toml|sh)$)' || repo_docs=true
$all || ! matches '^(terraform/|helm-charts/|examples/k8s/|transports/Dockerfile|transports/config\.schema\.json|docs/openapi/|\.github/workflows/scripts/(validate-schema-sync|validate-helm-schema)\.sh)' || infra_contracts=true
$all || [[ ! -s "$files" ]] || security=true
$all || ! matches '(^|/)(.*\.go|.*\.(ts|tsx)|Cargo\.(toml|lock)|.*\.rs)$|^scripts/static-checks/check-unit\.sh' || fast_unit=true

$all && go=true && web=true && python_rust=true && repo_docs=true && infra_contracts=true && security=true && fast_unit=true

for name in go web python_rust repo_docs infra_contracts security fast_unit; do
  printf '%s=%s\n' "$name" "${!name}"
  [[ -z "${GITHUB_OUTPUT:-}" ]] || printf '%s=%s\n' "$name" "${!name}" >>"$GITHUB_OUTPUT"
done
