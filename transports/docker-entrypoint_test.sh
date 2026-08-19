#!/bin/sh
set -eu

SCRIPT_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
ENTRYPOINT="$SCRIPT_DIR/docker-entrypoint.sh"

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

TEST_ROOT=$(mktemp -d)
trap 'rm -rf "$TEST_ROOT"' EXIT

CONFIG_DIR="$TEST_ROOT/config"
mkdir -p "$CONFIG_DIR"

set +e
APP_DIR="$CONFIG_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
BIFROST_CONFIG='{"source_of_truth":"split"}' \
    sh "$ENTRYPOINT" >"$TEST_ROOT/materialize.out" 2>&1
set -e

[ -f "$CONFIG_DIR/config.json" ] || fail "BIFROST_CONFIG was not materialized"
[ "$(cat "$CONFIG_DIR/config.json")" = '{"source_of_truth":"split"}' ] || fail "materialized config content changed"
[ "$(stat -c '%a' "$CONFIG_DIR/config.json" 2>/dev/null || stat -f '%Lp' "$CONFIG_DIR/config.json")" = "600" ] || fail "materialized config mode is not 0600"

PAIR_DIR="$TEST_ROOT/pair"
mkdir -p "$PAIR_DIR"
set +e
APP_DIR="$PAIR_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
BIFROST_RUN_AS_UID=1000 \
    sh "$ENTRYPOINT" >"$TEST_ROOT/pair.out" 2>&1
PAIR_EXIT=$?
set -e

[ "$PAIR_EXIT" -ne 0 ] || fail "unpaired run-as setting was accepted"
grep -q "BIFROST_RUN_AS_UID and BIFROST_RUN_AS_GID must be set together" "$TEST_ROOT/pair.out" || fail "unpaired run-as setting did not fail clearly"

INVALID_DIR="$TEST_ROOT/invalid"
mkdir -p "$INVALID_DIR"
set +e
APP_DIR="$INVALID_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
BIFROST_RUN_AS_UID='1000:0' \
BIFROST_RUN_AS_GID=0 \
    sh "$ENTRYPOINT" >"$TEST_ROOT/invalid.out" 2>&1
INVALID_EXIT=$?
set -e

[ "$INVALID_EXIT" -ne 0 ] || fail "invalid run-as setting was accepted"
grep -q "must be non-negative integers" "$TEST_ROOT/invalid.out" || fail "invalid run-as setting did not fail clearly"

ROOT_UID_DIR="$TEST_ROOT/root-uid"
mkdir -p "$ROOT_UID_DIR"
set +e
APP_DIR="$ROOT_UID_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
BIFROST_RUN_AS_UID=0 \
BIFROST_RUN_AS_GID=0 \
    sh "$ENTRYPOINT" >"$TEST_ROOT/root-uid.out" 2>&1
ROOT_UID_EXIT=$?
set -e

[ "$ROOT_UID_EXIT" -ne 0 ] || fail "root run-as UID was accepted"
grep -q "BIFROST_RUN_AS_UID must be a non-zero UID" "$TEST_ROOT/root-uid.out" || fail "root run-as UID did not fail clearly"

PATH_CONFIG_DIR="$TEST_ROOT/path-config"
mkdir -p "$PATH_CONFIG_DIR"
set +e
APP_DIR="$PATH_CONFIG_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
BIFROST_CONFIG=/etc/bifrost/config.json \
    sh "$ENTRYPOINT" >"$TEST_ROOT/path-config.out" 2>&1
PATH_CONFIG_EXIT=$?
set -e

[ "$PATH_CONFIG_EXIT" -ne 0 ] || fail "a path-valued BIFROST_CONFIG was accepted"
[ -f "$PATH_CONFIG_DIR/config.json" ] && fail "a path-valued BIFROST_CONFIG was written to config.json"
grep -q "must hold a complete inline config.json document" "$TEST_ROOT/path-config.out" || fail "path-valued BIFROST_CONFIG did not fail clearly"
grep -q "looks like a filesystem path" "$TEST_ROOT/path-config.out" || fail "path-valued BIFROST_CONFIG did not name the mistake"

GARBAGE_CONFIG_DIR="$TEST_ROOT/garbage-config"
mkdir -p "$GARBAGE_CONFIG_DIR"
set +e
APP_DIR="$GARBAGE_CONFIG_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
BIFROST_CONFIG='not json at all' \
    sh "$ENTRYPOINT" >"$TEST_ROOT/garbage-config.out" 2>&1
GARBAGE_CONFIG_EXIT=$?
set -e

[ "$GARBAGE_CONFIG_EXIT" -ne 0 ] || fail "a non-JSON BIFROST_CONFIG was accepted"
grep -q "must hold a complete inline config.json document" "$TEST_ROOT/garbage-config.out" || fail "non-JSON BIFROST_CONFIG did not fail clearly"

# A JSON document may be indented or start on a later line; only the first
# non-blank character decides.
INDENTED_CONFIG_DIR="$TEST_ROOT/indented-config"
mkdir -p "$INDENTED_CONFIG_DIR"
set +e
APP_DIR="$INDENTED_CONFIG_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
BIFROST_CONFIG='
  {"source_of_truth":"split"}
' \
    sh "$ENTRYPOINT" >"$TEST_ROOT/indented-config.out" 2>&1
set -e

[ -f "$INDENTED_CONFIG_DIR/config.json" ] || fail "an indented BIFROST_CONFIG was rejected"

# A database left behind by an earlier root-owned run sits inside an APP_DIR
# whose own ownership is already correct, so the create-directory probe passes
# and only a per-file check can catch it.
UNUSABLE_DIR="$TEST_ROOT/unusable-db"
mkdir -p "$UNUSABLE_DIR"
: >"$UNUSABLE_DIR/config.db"
chmod 000 "$UNUSABLE_DIR/config.db"
set +e
APP_DIR="$UNUSABLE_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
    sh "$ENTRYPOINT" >"$TEST_ROOT/unusable-db.out" 2>&1
UNUSABLE_EXIT=$?
set -e
chmod 600 "$UNUSABLE_DIR/config.db"

[ "$UNUSABLE_EXIT" -ne 0 ] || fail "an unusable config.db was accepted"
grep -q "$UNUSABLE_DIR/config.db is not usable" "$TEST_ROOT/unusable-db.out" || fail "unusable config.db was not named"

# config.json is only read, and mounting it read-only is supported: it must not
# be treated as an unusable path.
READONLY_CONFIG_DIR="$TEST_ROOT/readonly-config"
mkdir -p "$READONLY_CONFIG_DIR"
printf '%s' '{"source_of_truth":"split"}' >"$READONLY_CONFIG_DIR/config.json"
chmod 444 "$READONLY_CONFIG_DIR/config.json"
set +e
APP_DIR="$READONLY_CONFIG_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
    sh "$ENTRYPOINT" >"$TEST_ROOT/readonly-config.out" 2>&1
set -e
chmod 644 "$READONLY_CONFIG_DIR/config.json"

! grep -q "is not usable" "$TEST_ROOT/readonly-config.out" || fail "a read-only config.json was rejected"

# Read and write permission is not enough for a directory: without search
# permission Bifrost cannot create the log database inside logs/, and the probe
# cannot even stat what the directory already holds.
NO_TRAVERSE_DIR="$TEST_ROOT/no-traverse-logs"
mkdir -p "$NO_TRAVERSE_DIR/logs"
chmod 600 "$NO_TRAVERSE_DIR/logs"
set +e
APP_DIR="$NO_TRAVERSE_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
    sh "$ENTRYPOINT" >"$TEST_ROOT/no-traverse-logs.out" 2>&1
NO_TRAVERSE_EXIT=$?
set -e
chmod 700 "$NO_TRAVERSE_DIR/logs"

[ "$NO_TRAVERSE_EXIT" -ne 0 ] || fail "a logs directory without search permission was accepted"
grep -q "$NO_TRAVERSE_DIR/logs is not usable" "$TEST_ROOT/no-traverse-logs.out" || fail "logs directory without search permission was not named"

# SQLite opens its databases in WAL mode, so it leaves -wal and -shm sidecars
# beside them. A sidecar the target identity cannot open stops Bifrost exactly
# like an unusable database does, and it can outlive a correctly owned database:
# a root-owned run killed mid-flight leaves the sidecar behind, and repairing
# only the paths with fixed names would never look at it.
#
# These cases exercise the probe. The ownership repair reads the same glob list,
# but only runs as root with CAP_CHOWN, which this suite does not assume.
SIDECAR_DIR="$TEST_ROOT/unusable-sidecar"
mkdir -p "$SIDECAR_DIR"
: >"$SIDECAR_DIR/config.db"
: >"$SIDECAR_DIR/config.db-wal"
chmod 000 "$SIDECAR_DIR/config.db-wal"
set +e
APP_DIR="$SIDECAR_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
    sh "$ENTRYPOINT" >"$TEST_ROOT/unusable-sidecar.out" 2>&1
SIDECAR_EXIT=$?
set -e
chmod 600 "$SIDECAR_DIR/config.db-wal"

[ "$SIDECAR_EXIT" -ne 0 ] || fail "an unusable config.db-wal was accepted"
grep -q "$SIDECAR_DIR/config.db-wal is not usable" "$TEST_ROOT/unusable-sidecar.out" || fail "unusable config.db-wal was not named"

LOG_SIDECAR_DIR="$TEST_ROOT/unusable-log-sidecar"
mkdir -p "$LOG_SIDECAR_DIR"
: >"$LOG_SIDECAR_DIR/logs.db"
: >"$LOG_SIDECAR_DIR/logs.db-shm"
chmod 000 "$LOG_SIDECAR_DIR/logs.db-shm"
set +e
APP_DIR="$LOG_SIDECAR_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
    sh "$ENTRYPOINT" >"$TEST_ROOT/unusable-log-sidecar.out" 2>&1
LOG_SIDECAR_EXIT=$?
set -e
chmod 600 "$LOG_SIDECAR_DIR/logs.db-shm"

[ "$LOG_SIDECAR_EXIT" -ne 0 ] || fail "an unusable logs.db-shm was accepted"
grep -q "$LOG_SIDECAR_DIR/logs.db-shm is not usable" "$TEST_ROOT/unusable-log-sidecar.out" || fail "unusable logs.db-shm was not named"

# The counterpart to the glob: a volume may carry entries Bifrost never opens.
# ext4-backed volumes mount a root-owned lost+found, and requiring it to be
# usable would refuse a perfectly good disk on every platform that provides one.
UNRELATED_DIR="$TEST_ROOT/unrelated-entry"
mkdir -p "$UNRELATED_DIR/lost+found"
chmod 000 "$UNRELATED_DIR/lost+found"
set +e
APP_DIR="$UNRELATED_DIR" \
APP_PORT=8080 \
APP_HOST=127.0.0.1 \
LOG_LEVEL=info \
LOG_STYLE=json \
    sh "$ENTRYPOINT" >"$TEST_ROOT/unrelated-entry.out" 2>&1
set -e
chmod 700 "$UNRELATED_DIR/lost+found"

! grep -q "is not usable" "$TEST_ROOT/unrelated-entry.out" || fail "an unrelated unusable entry was rejected"

echo "docker-entrypoint tests passed"
