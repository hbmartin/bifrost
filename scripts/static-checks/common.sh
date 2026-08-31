#!/usr/bin/env bash

set -euo pipefail

STATIC_CHECKS_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$STATIC_CHECKS_DIR/../.." && pwd)
# shellcheck source=versions.env
source "$STATIC_CHECKS_DIR/versions.env"

STATIC_CACHE=${BIFROST_STATIC_CACHE:-$REPO_ROOT/.cache/bifrost-static}
TOOLS_BIN=$STATIC_CACHE/bin
NODE_TOOLS=$REPO_ROOT/tools/static/node_modules/.bin
PYTHON_PROJECT=$REPO_ROOT/tests/integrations/python
PYTHON_BIN=$PYTHON_PROJECT/.venv/bin

export CARGO_HOME=${CARGO_HOME:-$STATIC_CACHE/cargo}
export RUSTUP_HOME=${RUSTUP_HOME:-$STATIC_CACHE/rustup}
export TF_PLUGIN_CACHE_DIR=${TF_PLUGIN_CACHE_DIR:-$STATIC_CACHE/terraform-plugin-cache}
export PYTHONDONTWRITEBYTECODE=1
export PATH="$TOOLS_BIN:$NODE_TOOLS:$PYTHON_BIN:$CARGO_HOME/bin:$PATH"

log() {
  printf '[static-checks] %s\n' "$*"
}

die() {
  printf '[static-checks] error: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required; run 'make static-tools'"
}

repository_files() {
  git -C "$REPO_ROOT" ls-files --cached --others --exclude-standard -z |
    while IFS= read -r -d '' path; do
      if [[ -e "$REPO_ROOT/$path" || -L "$REPO_ROOT/$path" ]]; then
        printf '%s\n' "$path"
      fi
    done |
    LC_ALL=C sort
}

go_modules() {
  repository_files | grep -E '(^|/)go\.mod$'
}

module_selected() {
  local ordinal=$1
  local shard_index=${GO_SHARD_INDEX:-0}
  local shard_total=${GO_SHARD_TOTAL:-1}
  (( ordinal % shard_total == shard_index ))
}

make_temporary_workspace() {
  local workspace_dir
  workspace_dir=$(mktemp -d)
  (
    cd "$workspace_dir"
    GOWORK=off go work init
    while IFS= read -r mod_file; do
      go work use "$REPO_ROOT/${mod_file%/go.mod}"
    done < <(go_modules)
  )
  printf '%s/go.work\n' "$workspace_dir"
}

changed_files() {
  if [[ -n "${FILES_FROM:-}" ]]; then
    sed '/^[[:space:]]*$/d' "$FILES_FROM"
  elif [[ -n "${BASE_REF:-}" ]]; then
    git -C "$REPO_ROOT" diff --name-only --diff-filter=ACMRTUXBD "$BASE_REF...HEAD"
  elif ! git -C "$REPO_ROOT" diff --cached --quiet; then
    git -C "$REPO_ROOT" diff --cached --name-only --diff-filter=ACMRTUXBD
  fi
}

has_changed_path() {
  local pattern=$1
  changed_files | grep -Eq "$pattern"
}
