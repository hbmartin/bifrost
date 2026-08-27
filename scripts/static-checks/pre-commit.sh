#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
"$STATIC_CHECKS_DIR/bootstrap.sh" gitleaks shellcheck node python

files=("$@")
((${#files[@]})) || exit 0

mapfile -t go_files < <(printf '%s\n' "${files[@]}" | grep -E '\.go$' || true)
if ((${#go_files[@]})); then
  gofmt_diff=$(gofmt -d "${go_files[@]}")
  [[ -z "$gofmt_diff" ]] || { printf '%s\n' "$gofmt_diff"; exit 1; }
fi

mapfile -t web_files < <(printf '%s\n' "${files[@]}" | grep -E '\.(js|jsx|mjs|cjs|ts|tsx)$' | grep -Ev 'routeTree\.gen\.ts$' || true)
if ((${#web_files[@]})); then
  oxfmt --check "${web_files[@]}"
  oxlint --config "$REPO_ROOT/.oxlintrc.json" "${web_files[@]}"
fi

mapfile -t python_files < <(printf '%s\n' "${files[@]}" | grep -E '\.py$' || true)
if ((${#python_files[@]})); then
  ruff check --config "$REPO_ROOT/ruff.toml" "${python_files[@]}"
  ruff format --check --config "$REPO_ROOT/ruff.toml" "${python_files[@]}"
fi

mapfile -t shell_files < <(printf '%s\n' "${files[@]}" | grep -E '\.sh$' || true)
((${#shell_files[@]} == 0)) || shellcheck -S error "${shell_files[@]}"

gitleaks git --staged --redact --no-banner
