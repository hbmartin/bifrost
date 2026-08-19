#!/usr/bin/env bash
set -euo pipefail

# Prove the *published* image carries the runtime contract the one-click
# templates depend on.
#
# The image the deployment smoke tests build comes from Dockerfile.local against
# the Go workspace; the image the release publishes comes from
# transports/Dockerfile with GOWORK=off against the published modules. Those are
# different builds of different module graphs, so "the smoke tests passed" says
# nothing about the artifact operators actually pull. Tag and digest equality
# does not close that gap either: an image built wrongly and then tagged as both
# the release and latest satisfies every check in the gate around this one.
#
# What is checked here is deliberately offline and deterministic — nothing
# depends on Bifrost reaching the network or staying up, because a release gate
# that flakes gets bypassed. The entrypoint materializes BIFROST_CONFIG before
# it execs Bifrost, so the volume tells us the whole story either way.
#
# Usage: smoke-test-published-image.sh <image-ref>

IMAGE_REF=${1:?usage: smoke-test-published-image.sh <image-ref>}

CONTAINER="bifrost-release-smoke-$$"
VOLUME="bifrost-release-smoke-volume-$$"
INLINE_CONFIG='{"source_of_truth":"split"}'

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  docker volume rm "$VOLUME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "Smoke-testing $IMAGE_REF"

# The Blueprints hand Bifrost its configuration through BIFROST_CONFIG and drop
# privileges on platform volumes through su-exec. A release without either
# cannot serve them, however it was tagged.
if ! docker run --rm --entrypoint sh "$IMAGE_REF" -c '
  set -e
  command -v su-exec >/dev/null 2>&1 || { echo "su-exec is not installed"; exit 1; }
  grep -q "materialize_inline_config" /app/docker-entrypoint.sh || { echo "entrypoint does not materialize BIFROST_CONFIG"; exit 1; }
  grep -q "BIFROST_RUN_AS_UID" /app/docker-entrypoint.sh || { echo "entrypoint does not support privilege dropping"; exit 1; }
'; then
  echo "ERROR: $IMAGE_REF does not carry the deployment runtime contract" >&2
  exit 1
fi

docker volume create "$VOLUME" >/dev/null
docker run -d --name "$CONTAINER" \
  -v "$VOLUME:/app/data" \
  -e APP_HOST=127.0.0.1 \
  -e APP_PORT=8080 \
  -e BIFROST_CONFIG="$INLINE_CONFIG" \
  "$IMAGE_REF" >/dev/null

# Read the result out of the volume rather than the container: the entrypoint
# writes config.json before handing off, so this holds whether Bifrost then
# starts, exits, or is still booting.
materialized=""
for _ in $(seq 1 30); do
  if materialized=$(docker run --rm -v "$VOLUME:/app/data" --entrypoint sh "$IMAGE_REF" -c '
    [ -f /app/data/config.json ] || exit 1
    printf "%s %s" "$(stat -c "%a" /app/data/config.json)" "$(cat /app/data/config.json)"
  ' 2>/dev/null); then
    break
  fi
  materialized=""
  sleep 1
done

if [ -z "$materialized" ]; then
  echo "ERROR: $IMAGE_REF did not materialize BIFROST_CONFIG at /app/data/config.json" >&2
  docker logs "$CONTAINER" 2>&1 | tail -50 >&2 || true
  exit 1
fi

expected="600 $INLINE_CONFIG"
if [ "$materialized" != "$expected" ]; then
  echo "ERROR: $IMAGE_REF materialized '$materialized', expected '$expected'" >&2
  exit 1
fi

echo "$IMAGE_REF materializes BIFROST_CONFIG at mode 0600 and supports privilege dropping"
