#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
"$STATIC_CHECKS_DIR/bootstrap.sh" terraform helm hadolint python

(cd "$REPO_ROOT" && terraform fmt -check -recursive terraform)

terraform_copy=$(mktemp -d)
openapi_output=$(mktemp)
trap 'rm -rf "$terraform_copy"; rm -f "$openapi_output"' EXIT
rsync -a --exclude '.terraform/' "$REPO_ROOT/terraform/" "$terraform_copy/"

terraform_roots=(modules/bifrost examples/aws-ecs examples/azure-aks examples/gcp-gke examples/kubernetes)
for root in "${terraform_roots[@]}"; do
  log "Terraform init and validate: $root"
  terraform -chdir="$terraform_copy/$root" init -backend=false -lockfile=readonly -input=false
  terraform -chdir="$terraform_copy/$root" validate
done
terraform -chdir="$terraform_copy/modules/bifrost" test

chart="$REPO_ROOT/helm-charts/bifrost"
helm_required_values=(--set-string image.tag=static-checks)
helm lint --strict "$chart" "${helm_required_values[@]}"
helm template bifrost "$chart" "${helm_required_values[@]}" >/dev/null
while IFS= read -r values; do
  log "Helm values profile: $values"
  helm lint --strict "$chart" --values "$REPO_ROOT/$values" "${helm_required_values[@]}"
  helm template bifrost "$chart" --values "$REPO_ROOT/$values" "${helm_required_values[@]}" >/dev/null
done < <(
  repository_files |
    grep -E '^(helm-charts/bifrost/values-examples/.*\.ya?ml|examples/k8s/(enterprise|examples)/values[^/]*\.ya?ml)$'
)

while IFS= read -r dockerfile; do
  hadolint "$REPO_ROOT/$dockerfile"
done < <(repository_files | grep -E '(^|/)Dockerfile([^/]*)$')

(cd "$REPO_ROOT" && .github/workflows/scripts/validate-schema-sync.sh)
(cd "$REPO_ROOT" && .github/workflows/scripts/validate-helm-schema.sh)
"$PYTHON_BIN/python" "$REPO_ROOT/docs/openapi/bundle.py" --output "$openapi_output"
cmp "$REPO_ROOT/docs/openapi/openapi.json" "$openapi_output" || {
  diff -u "$REPO_ROOT/docs/openapi/openapi.json" "$openapi_output" || true
  die "docs/openapi/openapi.json is stale; regenerate it with docs/openapi/bundle.py"
}

log "infrastructure and contract checks passed"
