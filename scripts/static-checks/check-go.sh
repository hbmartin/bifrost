#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
"$STATIC_CHECKS_DIR/bootstrap.sh" golangci-lint

workspace=$(make_temporary_workspace)
workspace_dir=${workspace%/go.work}
trap 'rm -rf "$workspace_dir"' EXIT

ordinal=0
selected=0
while IFS= read -r mod_file; do
  module_dir=${mod_file%/go.mod}
  module_dir=${module_dir:-.}
  if module_selected "$ordinal"; then
    selected=$((selected + 1))
    log "Go module $module_dir: isolated tidy diff"
    (cd "$REPO_ROOT/$module_dir" && GOWORK=off go mod tidy -diff)
    log "Go module $module_dir: format and correctness"
    (
      cd "$REPO_ROOT/$module_dir"
      GOWORK="$workspace" golangci-lint fmt --config "$REPO_ROOT/.golangci.yml" --diff
      lint_args=(run --allow-parallel-runners --config "$REPO_ROOT/.golangci.yml")
      if [[ "$module_dir" == examples/plugins/hello-world-wasm-go ]]; then
        # TinyGo's //export roots are invisible to the standard unused analyzer;
        # the dedicated WASM build below is authoritative for ABI reachability.
        lint_args+=(--disable unused)
      fi
      GOWORK="$workspace" golangci-lint "${lint_args[@]}" ./...
    )
  fi
  ordinal=$((ordinal + 1))
done < <(go_modules)

((selected > 0)) || die "Go shard selected no modules"

if [[ "${GO_SHARD_INDEX:-0}" == 0 && "${SKIP_GO_VARIANTS:-0}" != 1 ]]; then
  log "checking critical Go build variants"
  (cd "$REPO_ROOT/core" && GOWORK="$workspace" go build ./...)
  (cd "$REPO_ROOT/transports" && GOWORK="$workspace" go build ./...)
  (cd "$REPO_ROOT/transports" && GOWORK="$workspace" go build -tags dev ./bifrost-http/...)
  # -exec=true makes go test compile every package and hand the foreign binary
  # to the host `true` command instead of trying to execute a Windows PE file.
  (cd "$REPO_ROOT/core" && GOWORK="$workspace" GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -run '^$' -exec=true ./...)
  (cd "$REPO_ROOT/transports" && GOWORK="$workspace" GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -run '^$' -exec=true ./bifrost-http/...)
  (cd "$REPO_ROOT/examples/plugins/hello-world-wasm-go" && GOWORK="$workspace" GOOS=wasip1 GOARCH=wasm go build ./...)
fi

log "Go checks passed for $selected module(s)"
