#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

mkdir -p "$TOOLS_BIN" "$STATIC_CACHE/downloads" "$TF_PLUGIN_CACHE_DIR"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

download_verified() {
  local url=$1
  local destination=$2
  local expected=$3
  local archive="$STATIC_CACHE/downloads/${url##*/}"

  if [[ ! -f "$archive" ]] || [[ "$(sha256_file "$archive")" != "$expected" ]]; then
    log "downloading ${url##*/}"
    curl --fail --location --silent --show-error "$url" --output "$archive"
  fi
  [[ "$(sha256_file "$archive")" == "$expected" ]] || die "checksum mismatch for ${url##*/}"
  cp "$archive" "$destination"
}

platform_tuple() {
  local os arch
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported operating system: $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
  printf '%s %s\n' "$os" "$arch"
}

install_go_tool() {
  local name=$1
  local module=$2
  local version=$3
  if [[ ! -x "$TOOLS_BIN/$name" ]]; then
    require_command go
    log "installing $name $version"
    GOBIN="$TOOLS_BIN" GOWORK=off go install "$module@v$version"
  fi
}

install_golangci_lint() {
  local marker="$STATIC_CACHE/golangci-lint-$GOLANGCI_LINT_VERSION-x-tools-$GOLANGCI_X_TOOLS_VERSION"
  [[ -x "$TOOLS_BIN/golangci-lint" && -f "$marker" ]] && return

  require_command go
  local source_dir build_dir
  source_dir=$(GOWORK=off go mod download -json "github.com/golangci/golangci-lint/v2@v$GOLANGCI_LINT_VERSION" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["Dir"])')
  build_dir=$(mktemp -d)
  cp -R "$source_dir/." "$build_dir/"
  chmod -R u+w "$build_dir"
  (
    cd "$build_dir"
    GOWORK=off go mod edit -require="golang.org/x/tools@v$GOLANGCI_X_TOOLS_VERSION"
    GOWORK=off go mod tidy
    GOWORK=off go build \
      -ldflags "-X main.version=$GOLANGCI_LINT_VERSION -X main.commit=x-tools-v$GOLANGCI_X_TOOLS_VERSION -X main.date=reproducible" \
      -o "$TOOLS_BIN/golangci-lint" ./cmd/golangci-lint
  )
  rm -rf "$build_dir"
  touch "$marker"
}

install_shellcheck() {
  [[ -x "$TOOLS_BIN/shellcheck" ]] && return
  local os arch checksum_var checksum archive extract_dir
  read -r os arch < <(platform_tuple)
  checksum_var="SHELLCHECK_${os^^}_${arch^^}_SHA256"
  checksum=${!checksum_var}
  archive="$STATIC_CACHE/downloads/shellcheck-v$SHELLCHECK_VERSION.$os.$([[ $arch == amd64 ]] && printf x86_64 || printf aarch64).tar.xz"
  download_verified "https://github.com/koalaman/shellcheck/releases/download/v$SHELLCHECK_VERSION/${archive##*/}" "$archive.tmp" "$checksum"
  extract_dir=$(mktemp -d)
  tar -xJf "$archive.tmp" -C "$extract_dir"
  cp "$extract_dir/shellcheck-v$SHELLCHECK_VERSION/shellcheck" "$TOOLS_BIN/shellcheck"
  chmod +x "$TOOLS_BIN/shellcheck"
}

install_hadolint() {
  [[ -x "$TOOLS_BIN/hadolint" ]] && return
  local os arch checksum_var checksum asset
  read -r os arch < <(platform_tuple)
  checksum_var="HADOLINT_${os^^}_${arch^^}_SHA256"
  checksum=${!checksum_var}
  asset="hadolint-$([[ $os == darwin ]] && printf macos || printf linux)-$([[ $arch == amd64 ]] && printf x86_64 || printf arm64)"
  download_verified "https://github.com/hadolint/hadolint/releases/download/v$HADOLINT_VERSION/$asset" "$TOOLS_BIN/hadolint" "$checksum"
  chmod +x "$TOOLS_BIN/hadolint"
}

install_terraform() {
  [[ -x "$TOOLS_BIN/terraform" ]] && return
  local os arch checksum_var checksum asset archive extract_dir
  read -r os arch < <(platform_tuple)
  checksum_var="TERRAFORM_${os^^}_${arch^^}_SHA256"
  checksum=${!checksum_var}
  asset="terraform_${TERRAFORM_VERSION}_${os}_${arch}.zip"
  archive="$STATIC_CACHE/downloads/$asset"
  download_verified "https://releases.hashicorp.com/terraform/$TERRAFORM_VERSION/$asset" "$archive.tmp" "$checksum"
  extract_dir=$(mktemp -d)
  unzip -q "$archive.tmp" -d "$extract_dir"
  cp "$extract_dir/terraform" "$TOOLS_BIN/terraform"
  chmod +x "$TOOLS_BIN/terraform"
}

install_helm() {
  [[ -x "$TOOLS_BIN/helm" ]] && return
  local os arch checksum_var checksum asset archive extract_dir
  read -r os arch < <(platform_tuple)
  checksum_var="HELM_${os^^}_${arch^^}_SHA256"
  checksum=${!checksum_var}
  asset="helm-v${HELM_VERSION}-${os}-${arch}.tar.gz"
  archive="$STATIC_CACHE/downloads/$asset"
  download_verified "https://get.helm.sh/$asset" "$archive.tmp" "$checksum"
  extract_dir=$(mktemp -d)
  tar -xzf "$archive.tmp" -C "$extract_dir"
  cp "$extract_dir/$os-$arch/helm" "$TOOLS_BIN/helm"
  chmod +x "$TOOLS_BIN/helm"
}

install_rust() {
  if ! command -v rustup >/dev/null 2>&1; then
    local os arch checksum_var checksum target
    read -r os arch < <(platform_tuple)
    checksum_var="RUSTUP_${os^^}_${arch^^}_SHA256"
    checksum=${!checksum_var}
    if [[ "$os" == linux ]]; then
      target="$([[ $arch == amd64 ]] && printf x86_64 || printf aarch64)-unknown-linux-gnu"
    else
      target="$([[ $arch == amd64 ]] && printf x86_64 || printf aarch64)-apple-darwin"
    fi
    download_verified "https://static.rust-lang.org/rustup/archive/$RUSTUP_VERSION/$target/rustup-init" "$TOOLS_BIN/rustup-init" "$checksum"
    chmod +x "$TOOLS_BIN/rustup-init"
    "$TOOLS_BIN/rustup-init" -y --no-modify-path --profile minimal --default-toolchain none
  fi
  if ! rustup run "$RUST_VERSION" rustc --version >/dev/null 2>&1; then
    log "installing Rust $RUST_VERSION with rustfmt, clippy, and wasm32 target"
    rustup toolchain install "$RUST_VERSION" --profile minimal --component rustfmt,clippy --target wasm32-unknown-unknown
  fi
}

requested() {
  local candidate=$1 item
  (($# > 1)) && shift
  (($# == 0)) && return 0
  for item in "$@"; do
    [[ "$item" == all || "$item" == "$candidate" ]] && return 0
  done
  return 1
}

tools=("$@")
requested golangci-lint "${tools[@]}" && install_golangci_lint
requested actionlint "${tools[@]}" && install_go_tool actionlint github.com/rhysd/actionlint/cmd/actionlint "$ACTIONLINT_VERSION"
requested gitleaks "${tools[@]}" && install_go_tool gitleaks github.com/zricethezav/gitleaks/v8 "$GITLEAKS_VERSION"
requested gosec "${tools[@]}" && install_go_tool gosec github.com/securego/gosec/v2/cmd/gosec "$GOSEC_VERSION"
requested govulncheck "${tools[@]}" && install_go_tool govulncheck golang.org/x/vuln/cmd/govulncheck "$GOVULNCHECK_VERSION"
requested shellcheck "${tools[@]}" && install_shellcheck
requested hadolint "${tools[@]}" && install_hadolint
requested terraform "${tools[@]}" && install_terraform
requested helm "${tools[@]}" && install_helm

if requested node "${tools[@]}"; then
  require_command npm
  if [[ ! -x "$NODE_TOOLS/oxlint" ]]; then
    log "installing locked Node static tools"
    npm --prefix "$REPO_ROOT/tools/static" ci --ignore-scripts
  fi
fi

if requested python "${tools[@]}"; then
  require_command uv
  log "syncing locked Python static tools"
  uv sync --project "$PYTHON_PROJECT" --extra dev --frozen
fi

requested rust "${tools[@]}" && install_rust

log "toolchain ready in $STATIC_CACHE"
