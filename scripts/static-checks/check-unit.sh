#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
"$STATIC_CHECKS_DIR/bootstrap.sh" node python rust

workspace=$(make_temporary_workspace)
workspace_dir=${workspace%/go.work}
stale_node_modules=
cleanup() {
  rm -rf "$workspace_dir"
  if [[ -n "$stale_node_modules" ]]; then
    rm -rf "$stale_node_modules" || {
      # APFS can briefly report ENOTEMPTY while npm's file handles drain.
      sleep 1
      rm -rf "$stale_node_modules"
    }
  fi
}
trap cleanup EXIT

(cd "$REPO_ROOT/core" && GOWORK="$workspace" go test -short ./schemas ./keyselectors ./network ./mcp/...)
(cd "$REPO_ROOT/framework" && GOWORK="$workspace" go test -short ./encrypt/... ./modelcatalog/... ./streaming/... ./tracing/...)

if [[ -d "$REPO_ROOT/ui/node_modules" ]]; then
  stale_node_modules=$(mktemp -d "${TMPDIR:-/tmp}/bifrost-npm-stale.XXXXXX")
  mv "$REPO_ROOT/ui/node_modules" "$stale_node_modules/node_modules"
fi
npm --prefix "$REPO_ROOT/ui" ci --ignore-scripts
(
  cd "$REPO_ROOT/ui"
  npm exec -- vitest run --config "$REPO_ROOT/ui/vitest.config.ts"
)

(
  cd "$REPO_ROOT/examples/plugins/hello-world-wasm-rust"
  rustup run "$RUST_VERSION" cargo test --locked
)

"$PYTHON_BIN/python" "$REPO_ROOT/.github/workflows/scripts/validate-deployment-templates_test.py"
"$PYTHON_BIN/python" "$REPO_ROOT/docs/api/procuring-api-keys_test.py"
"$PYTHON_BIN/python" "$REPO_ROOT/docs/openapi/spec_invariants_test.py"

log "credential-free unit checks passed"
