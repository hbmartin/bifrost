#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
"$STATIC_CHECKS_DIR/bootstrap.sh" python rust

mapfile -t python_files < <(repository_files | grep -E '\.py$')
ruff check --config "$REPO_ROOT/ruff.toml" "${python_files[@]/#/$REPO_ROOT/}"
ruff format --check --config "$REPO_ROOT/ruff.toml" "${python_files[@]/#/$REPO_ROOT/}"

(
  cd "$PYTHON_PROJECT"
  # Dynamic third-party SDK response objects intentionally remain outside the
  # typed boundary. The runner/config/model utilities are the stable integration
  # control plane where pragmatic static typing provides actionable signal.
  mypy run_all_tests.py run_integration_tests.py \
    tests/utils/config_loader.py tests/utils/models.py tests/utils/parametrize.py
)

rust_project="$REPO_ROOT/examples/plugins/hello-world-wasm-rust"
(
  cd "$rust_project"
  rustup run "$RUST_VERSION" cargo fmt --check
  rustup run "$RUST_VERSION" cargo clippy --locked --target wasm32-unknown-unknown -- -D warnings
  rustup run "$RUST_VERSION" cargo test --locked
  rustup run "$RUST_VERSION" cargo build --locked --release --target wasm32-unknown-unknown
)

log "Python and Rust checks passed"
