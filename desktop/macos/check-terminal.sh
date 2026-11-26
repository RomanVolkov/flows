#!/bin/sh
set -eu

probe=$1
fixture=$2
work=$(mktemp -d /tmp/flows-probe.XXXXXX)
host=
cleanup() {
    if [ -n "$host" ]; then
        kill "$host" 2>/dev/null || true
        wait "$host" 2>/dev/null || true
    fi
    rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

"$probe" host "$work/pty.sock" python3 "$fixture" "$work/ready" &
host=$!
attempt=0
until [ -s "$work/ready" ]; do
    if ! kill -0 "$host" 2>/dev/null || [ "$attempt" -ge 50 ]; then
        printf 'Terminal fixture did not acknowledge the detached cursor query\n' >&2
        exit 1
    fi
    attempt=$((attempt + 1))
    sleep 0.1
done
marker="child=$(cat "$work/ready")"
for phase in 1 2; do
    FLOWS_TERMINAL_PHASE="$phase" FLOWS_TERMINAL_EXPECT="$marker" "$probe" window "$work/pty.sock"
done
