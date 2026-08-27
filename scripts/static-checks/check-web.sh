#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
"$STATIC_CHECKS_DIR/bootstrap.sh" node

mapfile -t web_files < <(
  repository_files |
    grep -E '\.(c|m)?jsx?$|\.tsx?$' |
    grep -Ev '(^ui/|(^|/)(routeTree\.gen\.ts|node_modules/|dist/|build/|transports/bifrost-http/ui/))'
)

root_lint=$(mktemp)
ui_lint=$(mktemp)
trap 'rm -f "$root_lint" "$ui_lint"' EXIT

if ((${#web_files[@]})); then
  (cd "$REPO_ROOT" && oxfmt --check "${web_files[@]}")
fi
if ((${#web_files[@]})); then
  (cd "$REPO_ROOT" && oxlint --config .oxlintrc.json --format json "${web_files[@]}" >"$root_lint") || true
  python3 "$STATIC_CHECKS_DIR/check-oxlint-warnings.py" "$root_lint" "$STATIC_CHECKS_DIR/baselines/warning-ceilings.json"
fi

install_node_project() {
  local directory=$1
  local stale=
  [[ -f "$REPO_ROOT/$directory/package-lock.json" ]] || die "$directory has a TypeScript config but no lockfile"
  if [[ -d "$REPO_ROOT/$directory/node_modules" ]]; then
    stale=$(mktemp -d "${TMPDIR:-/tmp}/bifrost-npm-stale.XXXXXX")
    mv "$REPO_ROOT/$directory/node_modules" "$stale/node_modules"
  fi
  if ! npm --prefix "$REPO_ROOT/$directory" ci --ignore-scripts; then
    [[ -z "$stale" ]] || rm -rf "$stale" || true
    return 1
  fi
  if [[ -n "$stale" ]] && ! rm -rf "$stale"; then
    # APFS can briefly report ENOTEMPTY while npm's file handles drain.
    sleep 1
    rm -rf "$stale"
  fi
}

mapfile -t tsconfigs < <(repository_files | grep -E '(^|/)tsconfig[^/]*\.json$')
declare -A installed=()
for config in "${tsconfigs[@]}"; do
  directory=${config%/*}
  [[ "$directory" != "$config" ]] || directory=.
  [[ "$config" == */assembly/tsconfig.json ]] && continue
  if [[ -z "${installed[$directory]:-}" ]]; then
    install_node_project "$directory"
    installed[$directory]=1
  fi
  [[ -x "$REPO_ROOT/$directory/node_modules/.bin/tsc" ]] || die "$directory does not provide a pinned TypeScript compiler"
  "$REPO_ROOT/$directory/node_modules/.bin/tsc" --project "$REPO_ROOT/$config" --noEmit
done

ui_warning_ceiling=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["ui"]["typescript/no-explicit-any"])' "$STATIC_CHECKS_DIR/baselines/warning-ceilings.json")
(cd "$REPO_ROOT/ui" && ./node_modules/.bin/oxfmt --check .)
(cd "$REPO_ROOT/ui" && ./node_modules/.bin/oxlint --config .oxlintrc.json --max-warnings="$ui_warning_ceiling" --format json . >"$ui_lint") || true
python3 "$STATIC_CHECKS_DIR/check-oxlint-warnings.py" --package ui "$ui_lint" "$STATIC_CHECKS_DIR/baselines/warning-ceilings.json"

for directory in \
  examples/mcps/edge-case-server \
  examples/mcps/error-test-server \
  examples/mcps/parallel-test-server \
  examples/mcps/temperature \
  examples/mcps/test-tools-server \
  examples/plugins/hello-world-wasm-typescript; do
  [[ -n "${installed[$directory]:-}" ]] || install_node_project "$directory"
  npm --prefix "$REPO_ROOT/$directory" run build
done

log "web checks passed for ${#tsconfigs[@]} TypeScript configuration(s)"
