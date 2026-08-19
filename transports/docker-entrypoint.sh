#!/bin/sh
set -e

APP_DIR=${APP_DIR:-/app/data}
RUN_AS_UID=${BIFROST_RUN_AS_UID:-}
RUN_AS_GID=${BIFROST_RUN_AS_GID:-}

# Paths under APP_DIR that Bifrost opens at startup, relative to it ("." is
# APP_DIR itself). A correctly owned APP_DIR says nothing about entries a
# previous root-owned run left inside it, and the create-directory probe below
# cannot see them: it creates a new directory and never touches the existing
# databases. Both the ownership repair and the probe walk these explicitly.
DATA_WRITE_ENTRIES=". config.db logs.db logs"
# config.json is only read. Deployments legitimately mount it read-only, so it
# is never required to be writable and never triggers an ownership repair.
DATA_READ_ENTRIES="config.json"

validate_run_as_config() {
    if { [ -n "$RUN_AS_UID" ] && [ -z "$RUN_AS_GID" ]; } || { [ -z "$RUN_AS_UID" ] && [ -n "$RUN_AS_GID" ]; }; then
        echo "Error: BIFROST_RUN_AS_UID and BIFROST_RUN_AS_GID must be set together"
        exit 1
    fi

    if [ -z "$RUN_AS_UID" ]; then
        return
    fi

    case "$RUN_AS_UID" in
        *[!0-9]*)
            echo "Error: BIFROST_RUN_AS_UID and BIFROST_RUN_AS_GID must be non-negative integers"
            exit 1
            ;;
    esac
    case "$RUN_AS_GID" in
        *[!0-9]*)
            echo "Error: BIFROST_RUN_AS_UID and BIFROST_RUN_AS_GID must be non-negative integers"
            exit 1
            ;;
    esac

    if [ "$RUN_AS_UID" = "0" ]; then
        echo "Error: BIFROST_RUN_AS_UID must be a non-zero UID"
        exit 1
    fi

    if [ "$(id -u)" != "0" ]; then
        echo "Error: BIFROST_RUN_AS_UID and BIFROST_RUN_AS_GID require the entrypoint to start as root"
        exit 1
    fi

    if ! command -v su-exec >/dev/null 2>&1; then
        echo "Error: su-exec is required when BIFROST_RUN_AS_UID and BIFROST_RUN_AS_GID are set"
        exit 1
    fi
}

materialize_inline_config() {
    if [ "${BIFROST_CONFIG+x}" != "x" ]; then
        return
    fi
    if [ -z "$BIFROST_CONFIG" ]; then
        echo "Error: BIFROST_CONFIG is set but empty"
        exit 1
    fi

    # BIFROST_CONFIG carries the config.json document itself, not a path to one;
    # deployments that mount a file leave it unset. Writing a path here would
    # produce a config.json whose contents are the literal path string and start
    # Bifrost on invalid JSON, so reject anything that is not a JSON object and
    # name the mistake. Only the first non-blank character is ever echoed.
    CONFIG_FIRST_CHAR=$(printf '%s' "$BIFROST_CONFIG" | tr -d '[:space:]' | cut -c1)
    if [ "$CONFIG_FIRST_CHAR" != "{" ]; then
        echo "Error: BIFROST_CONFIG must hold a complete inline config.json document"
        if [ "$CONFIG_FIRST_CHAR" = "/" ]; then
            echo "  The value looks like a filesystem path, which this variable does not accept."
            echo "  Mount that file at $APP_DIR/config.json and leave BIFROST_CONFIG unset,"
            echo "  or set BIFROST_CONFIG to the JSON document itself."
        else
            echo "  Expected a value beginning with '{'. Set it to the JSON document itself,"
            echo "  or leave it unset and mount a config.json at $APP_DIR/config.json."
        fi
        exit 1
    fi

    CONFIG_PATH="$APP_DIR/config.json"
    if ! CONFIG_TMP=$(umask 077 && mktemp "$APP_DIR/.config.json.tmp.XXXXXX"); then
        echo "Error: Could not create a temporary config file in $APP_DIR"
        exit 1
    fi
    if ! printf '%s' "$BIFROST_CONFIG" > "$CONFIG_TMP" || ! chmod 0600 "$CONFIG_TMP"; then
        rm -f "$CONFIG_TMP"
        echo "Error: Could not materialize BIFROST_CONFIG at $CONFIG_PATH"
        exit 1
    fi
    if [ "$(id -u)" = "0" ] && [ -n "$RUN_AS_UID" ]; then
        if ! chown "$RUN_AS_UID:$RUN_AS_GID" "$CONFIG_TMP"; then
            rm -f "$CONFIG_TMP"
            echo "Error: Could not set BIFROST_CONFIG ownership to $RUN_AS_UID:$RUN_AS_GID"
            exit 1
        fi
    fi
    if ! mv -f "$CONFIG_TMP" "$CONFIG_PATH"; then
        rm -f "$CONFIG_TMP"
        echo "Error: Could not materialize BIFROST_CONFIG at $CONFIG_PATH"
        exit 1
    fi
    unset BIFROST_CONFIG
}

# The probe runs under the identity Bifrost will actually use and prints the
# first path that identity cannot open, so the caller can name it. Creating a
# new directory proves nothing about a root-owned config.db already sitting in a
# correctly owned APP_DIR, so the existing entries are checked one by one.
# shellcheck disable=SC2016 # The inner shell expands its own positional parameters.
APP_DIR_PROBE='
    app_dir="$1"
    write_entries="$2"
    read_entries="$3"

    probe_dir="$app_dir/.bifrost-write-test.$$"
    if [ -e "$probe_dir" ]; then
        probe_dir="$probe_dir.$(date +%s)"
    fi
    if ! mkdir "$probe_dir" 2>/dev/null; then
        printf %s "$app_dir"
        exit 1
    fi
    rmdir "$probe_dir" 2>/dev/null || true

    # Read and write alone do not make a directory usable: without search
    # permission Bifrost can neither traverse it nor create the log database
    # inside it, and the glob below cannot even stat what it holds. Only
    # directories are asked for it — a data file is never executable.
    unusable_for_write() {
        [ -e "$1" ] || return 1
        if [ ! -r "$1" ] || [ ! -w "$1" ]; then
            return 0
        fi
        [ -d "$1" ] && [ ! -x "$1" ]
    }

    for entry in $write_entries; do
        path="$app_dir/$entry"
        if unusable_for_write "$path"; then
            printf %s "$path"
            exit 1
        fi
    done
    # Log databases roll over into logs/, so a root-owned file can sit inside a
    # correctly owned directory. Cover its immediate children, not a full walk.
    for path in "$app_dir"/logs/*; do
        if unusable_for_write "$path"; then
            printf %s "$path"
            exit 1
        fi
    done
    for entry in $read_entries; do
        path="$app_dir/$entry"
        if [ -e "$path" ] && [ ! -r "$path" ]; then
            printf %s "$path"
            exit 1
        fi
    done
    exit 0
'

# UNUSABLE_PATH names the path that failed the most recent probe.
UNUSABLE_PATH=""

app_dir_writable() {
    if [ -n "$RUN_AS_UID" ]; then
        UNUSABLE_PATH=$(su-exec "$RUN_AS_UID:$RUN_AS_GID" sh -c "$APP_DIR_PROBE" sh \
            "$APP_DIR" "$DATA_WRITE_ENTRIES" "$DATA_READ_ENTRIES")
        return $?
    fi

    UNUSABLE_PATH=$(sh -c "$APP_DIR_PROBE" sh \
        "$APP_DIR" "$DATA_WRITE_ENTRIES" "$DATA_READ_ENTRIES")
    return $?
}

# misowned_data_paths prints every data path whose ownership differs from the
# target identity, one per line, and nothing when they all match. config.json is
# excluded: it is only read, and a read-only mount of it must not force a chown
# on every start.
misowned_data_paths() {
    for _entry in $DATA_WRITE_ENTRIES; do
        report_if_misowned "$APP_DIR/$_entry"
    done
    for _path in "$APP_DIR"/logs/*; do
        report_if_misowned "$_path"
    done
}

report_if_misowned() {
    if [ ! -e "$1" ]; then
        return
    fi
    # `|| true`: a failing stat must not abort the caller under `set -e`, and an
    # ownership we cannot read is not evidence of a mismatch — the probe below
    # still refuses to start on a path the target identity cannot open.
    _uid=$(stat -c '%u' "$1" 2>/dev/null || true)
    _gid=$(stat -c '%g' "$1" 2>/dev/null || true)
    if [ -z "$_uid" ] || [ -z "$_gid" ]; then
        return
    fi
    if [ "$_uid:$_gid" != "$TARGET_UID:$TARGET_GID" ]; then
        printf '%s (%s:%s)\n' "$1" "$_uid" "$_gid"
    fi
}

# repair_data_paths hands APP_DIR and everything inside it to the target
# identity, with one exception: config.json. That exception is the whole point.
# config.json is only ever read, so deployments legitimately mount it read-only —
# and on a read-only mount chown fails, which would make a repair that fixed
# every path that actually needed fixing report itself as having failed.
#
# Nothing else is excluded. SQLite leaves -wal and -shm sidecars beside its
# databases, and a root-owned sidecar stops Bifrost just as dead as a root-owned
# database does, so the children are still repaired recursively.
repair_data_paths() {
    _repair_status=0

    if ! chown "$TARGET_UID:$TARGET_GID" "$APP_DIR" 2>/dev/null || ! chmod u+rwX "$APP_DIR" 2>/dev/null; then
        _repair_status=1
    fi

    for _path in "$APP_DIR"/*; do
        if [ ! -e "$_path" ]; then
            continue
        fi
        if [ "$_path" = "$APP_DIR/config.json" ]; then
            continue
        fi
        if ! chown -R "$TARGET_UID:$TARGET_GID" "$_path" 2>/dev/null || ! chmod -R u+rwX "$_path" 2>/dev/null; then
            _repair_status=1
        fi
    done

    return "$_repair_status"
}

# Ensure APP_DIR exists when possible, but do not require CAP_CHOWN at startup.
ensure_app_dir() {
    mkdir -p "$APP_DIR" 2>/dev/null || true

    if [ ! -d "$APP_DIR" ]; then
        echo "Error: Could not create APP_DIR at $APP_DIR"
        echo "  Ensure the path exists or the parent directory is writable by the container user."
        exit 1
    fi

    CURRENT_UID=$(id -u)
    CURRENT_GID=$(id -g)
    TARGET_UID=${RUN_AS_UID:-$CURRENT_UID}
    TARGET_GID=${RUN_AS_GID:-$CURRENT_GID}

    materialize_inline_config
    mkdir -p "$APP_DIR/logs" 2>/dev/null || true
    if [ "$(id -u)" = "0" ] && [ -n "$RUN_AS_UID" ]; then
        chown "$RUN_AS_UID:$RUN_AS_GID" "$APP_DIR/logs" 2>/dev/null || true
    fi

    # Ownership repair only works as root (needs CAP_CHOWN). Repair APP_DIR *and*
    # the entries inside it: a volume whose top level was fixed by an earlier
    # run can still hold root-owned databases, and repairing only on a top-level
    # mismatch leaves Bifrost to fail opening them later.
    if [ "$CURRENT_UID" = "0" ]; then
        MISOWNED_PATHS=$(misowned_data_paths)
        if [ -n "$MISOWNED_PATHS" ]; then
            echo "Fixing permissions on $APP_DIR (setting to $TARGET_UID:$TARGET_GID); currently misowned:"
            printf '%s\n' "$MISOWNED_PATHS" | sed 's/^/  /'
            if repair_data_paths; then
                echo "Successfully updated permissions on $APP_DIR"
            else
                echo "Warning: Could not update permissions on $APP_DIR"
            fi
        fi
    fi

    if ! app_dir_writable; then
        UNUSABLE_PATH=${UNUSABLE_PATH:-$APP_DIR}
        # `|| true`: under `set -e` a failing stat here would abort the script
        # before it printed the diagnostic this branch exists to print.
        DATA_UID=$(stat -c '%u' "$UNUSABLE_PATH" 2>/dev/null || true)
        DATA_GID=$(stat -c '%g' "$UNUSABLE_PATH" 2>/dev/null || true)
        DATA_UID=${DATA_UID:-unknown}
        DATA_GID=${DATA_GID:-unknown}
        if [ "$BIFROST_SKIP_WRITE_CHECK" = "1" ]; then
            echo "Warning: $UNUSABLE_PATH is not usable by UID:GID $TARGET_UID:$TARGET_GID (owned by $DATA_UID:$DATA_GID)"
            echo "  BIFROST_SKIP_WRITE_CHECK=1 set; continuing without a writable APP_DIR."
            echo "  Only safe for read-only deployments backed by external stores (e.g. Postgres)."
        else
            echo "Error: $UNUSABLE_PATH is not usable by UID:GID $TARGET_UID:$TARGET_GID (owned by $DATA_UID:$DATA_GID)"
            echo "  Bifrost needs a writable APP_DIR for config.db and logs.db before startup."
            echo "  On vanilla Kubernetes, set podSecurityContext.fsGroup (for example, 1000)."
            echo "  On OpenShift (restricted-v2), leave fsGroup unset/null so the SCC assigns an in-range GID."
            echo "  Or mount a volume writable by GID 0, matching the image's group-0 ownership."
            echo "  Set BIFROST_SKIP_WRITE_CHECK=1 to bypass for read-only deployments with external stores."
            exit 1
        fi
    fi

    chmod g+rwX "$APP_DIR/logs" 2>/dev/null || true
}

# Prepare the app directory before starting the application
validate_run_as_config
ensure_app_dir

# Parse command line arguments and set environment variables
parse_args() {
    while [ $# -gt 0 ]; do
        case $1 in
            --port|-port)
                if [ -n "$2" ]; then
                    export APP_PORT="$2"
                    shift 2
                else
                    echo "Error: --port requires a value"
                    exit 1
                fi
                ;;
            --host|-host)
                if [ -n "$2" ]; then
                    export APP_HOST="$2"
                    shift 2
                else
                    echo "Error: --host requires a value"
                    exit 1
                fi
                ;;
            *)
                # Keep other arguments for the main application
                set -- "$@" "$1"
                shift
                ;;
        esac
    done
}

# Parse arguments if any are provided
if [ $# -gt 1 ]; then
    parse_args "$@"
fi

# Build the command with environment variables and standard arguments.
if [ -n "$RUN_AS_UID" ]; then
    exec su-exec "$RUN_AS_UID:$RUN_AS_GID" /app/main -app-dir "$APP_DIR" -port "$APP_PORT" -host "$APP_HOST" -log-level "$LOG_LEVEL" -log-style "$LOG_STYLE"
fi

exec /app/main -app-dir "$APP_DIR" -port "$APP_PORT" -host "$APP_HOST" -log-level "$LOG_LEVEL" -log-style "$LOG_STYLE"
