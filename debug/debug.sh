#!/usr/bin/env bash

if [ -z "$(command -v socat)" ]; then
    echo you must have "'socat'" installed
    exit 1
fi

if [ -z "$(command -v pgrep)" ]; then
    echo you must have "'pgrep'" installed
    exit 1
fi

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${PROJECT_DIR}"

# Build frontend with debugging setup Note the host path for the dalec source
# and the in-container build path must be the same
REF="local/dalec/frontend:tmp"
docker build \
    -f Dockerfile.debug \
    -t "${REF}" \
    --build-arg=HOSTDIR="${PROJECT_DIR}" \
    .

# Wait for frontend process to start, and forward the socket connection when the process has started
(
    pid=""
    while [ -z "$pid" ]; do
        sleep 0.5
        pid="$(pgrep frontend)"
    done

    socat_logfile="$(mktemp)"
    socat -v UNIX:"/proc/${pid}/root/dlv.sock" TCP-LISTEN:30157,reuseaddr,fork 2>"$socat_logfile" &
    scatpid="$!"
    trap "kill -9 ${scatpid}" EXIT

    # Detect when vs code has connected, then send the CONT signal to the frontend
    txt=""
    while [ -z "$txt" ]; do
        sleep 0.5
        if ! [ -f "$socat_logfile" ]; then
            continue
        fi
        txt="$(head -n1 "$socat_logfile")"
    done

    kill -s CONT "$pid"
    wait "$scatpid"
) &

# Run the build
exec docker build --build-arg=BUILDKIT_SYNTAX="${REF}" "$@"
