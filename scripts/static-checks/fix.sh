#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
area=${1:-all}

fix_go() {
  "$STATIC_CHECKS_DIR/bootstrap.sh" golangci-lint
  local workspace workspace_dir
  workspace=$(make_temporary_workspace)
  workspace_dir=${workspace%/go.work}
  while IFS= read -r mod_file; do
    module_dir=${mod_file%/go.mod}
    module_dir=${module_dir:-.}
    (cd "$REPO_ROOT/$module_dir" && GOWORK="$workspace" golangci-lint fmt --config "$REPO_ROOT/.golangci.yml")
  done < <(go_modules)
  rm -rf "$workspace_dir"
}

fix_web() {
  "$STATIC_CHECKS_DIR/bootstrap.sh" node
  local stale=
  mapfile -t files < <(repository_files | grep -E '\.(c|m)?jsx?$|\.tsx?$' | grep -Ev '(^ui/|(^|/)(routeTree\.gen\.ts|node_modules/|dist/|build/|transports/bifrost-http/ui/))')
  ((${#files[@]} == 0)) || (cd "$REPO_ROOT" && oxfmt --write "${files[@]}")
  if [[ -d "$REPO_ROOT/ui/node_modules" ]]; then
    stale=$(mktemp -d "${TMPDIR:-/tmp}/bifrost-npm-stale.XXXXXX")
    mv "$REPO_ROOT/ui/node_modules" "$stale/node_modules"
  fi
  npm --prefix "$REPO_ROOT/ui" ci --ignore-scripts
  if [[ -n "$stale" ]] && ! rm -rf "$stale"; then
    sleep 1
    rm -rf "$stale"
  fi
  (cd "$REPO_ROOT/ui" && ./node_modules/.bin/oxfmt --write .)
}

fix_python_rust() {
  "$STATIC_CHECKS_DIR/bootstrap.sh" python rust
  mapfile -t files < <(repository_files | grep -E '\.py$')
  ruff check --fix --exit-zero --config "$REPO_ROOT/ruff.toml" "${files[@]/#/$REPO_ROOT/}"
  ruff format --config "$REPO_ROOT/ruff.toml" "${files[@]/#/$REPO_ROOT/}"
  (cd "$REPO_ROOT/examples/plugins/hello-world-wasm-rust" && rustup run "$RUST_VERSION" cargo fmt)
}

fix_infra() {
  "$STATIC_CHECKS_DIR/bootstrap.sh" terraform
  (cd "$REPO_ROOT" && terraform fmt -recursive terraform)
}

case "$area" in
  all) fix_go; fix_web; fix_python_rust; fix_infra ;;
  go) fix_go ;;
  web) fix_web ;;
  python-rust) fix_python_rust ;;
  infra) fix_infra ;;
  repo|security|unit) log "$area has no safe automatic fixes" ;;
  *) die "unknown fix area: $area" ;;
esac
