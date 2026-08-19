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
#
# su-exec is run, not merely located: a binary that is present but cannot execute
# in the published image — stale linkage against a bumped musl, a runtime stage
# that lost a library — satisfies `command -v` and still leaves every deployment
# that drops privileges refusing to start. Dropping privileges needs root, and
# the image declares USER 1000:0, so this check asks for it explicitly.
if ! docker run --rm --user 0:0 --entrypoint sh "$IMAGE_REF" -c '
  set -e
  command -v su-exec >/dev/null 2>&1 || { echo "su-exec is not installed"; exit 1; }
  dropped=$(su-exec 1000:0 id -u) || { echo "su-exec is installed but could not execute"; exit 1; }
  [ "$dropped" = "1000" ] || { echo "su-exec dropped to UID $dropped, expected 1000"; exit 1; }
  grep -q "materialize_inline_config" /app/docker-entrypoint.sh || { echo "entrypoint does not materialize BIFROST_CONFIG"; exit 1; }
  grep -q "BIFROST_RUN_AS_UID" /app/docker-entrypoint.sh || { echo "entrypoint does not support privilege dropping"; exit 1; }
'; then
  echo "ERROR: $IMAGE_REF does not carry the deployment runtime contract" >&2
  exit 1
fi

# Start the container the way the Railway SQLite template does: as root
# (RAILWAY_RUN_UID=0), handing the entrypoint the unprivileged identity to drop
# to. That is the only configuration in which the entrypoint hands what it
# materializes to the target identity, so the ownership asserted below is
# evidence the privilege-drop path ran, not a property of the image.
docker volume create "$VOLUME" >/dev/null
docker run -d --name "$CONTAINER" \
  --user 0:0 \
  -v "$VOLUME:/app/data" \
  -e APP_HOST=127.0.0.1 \
  -e APP_PORT=8080 \
  -e BIFROST_CONFIG="$INLINE_CONFIG" \
  -e BIFROST_RUN_AS_UID=1000 \
  -e BIFROST_RUN_AS_GID=0 \
  "$IMAGE_REF" >/dev/null

# Read the result out of the volume rather than the container: the entrypoint
# writes config.json before handing off, so this holds whether Bifrost then
# starts, exits, or is still booting.
materialized=""
for _ in $(seq 1 30); do
  if materialized=$(docker run --rm --user 0:0 -v "$VOLUME:/app/data" --entrypoint sh "$IMAGE_REF" -c '
    [ -f /app/data/config.json ] || exit 1
    printf "%s %s %s" "$(stat -c "%u:%g" /app/data/config.json)" \
      "$(stat -c "%a" /app/data/config.json)" "$(cat /app/data/config.json)"
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

expected="1000:0 600 $INLINE_CONFIG"
if [ "$materialized" != "$expected" ]; then
  echo "ERROR: $IMAGE_REF materialized '$materialized', expected '$expected'" >&2
  exit 1
fi

echo "$IMAGE_REF materializes BIFROST_CONFIG as 1000:0 at mode 0600 and drops privileges through su-exec"
