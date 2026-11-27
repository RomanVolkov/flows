#!/bin/sh
set -eu

bench_only=0
if [ "${1:-}" = "--bench" ]; then
    bench_only=1
    shift
fi

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

wait_ready() {
    ready_file=$1
    attempt=0
    until [ -s "$ready_file" ]; do
        if ! kill -0 "$host" 2>/dev/null || [ "$attempt" -ge 80 ]; then
            printf 'Terminal fixture did not become ready (%s)\n' "$ready_file" >&2
            exit 1
        fi
        attempt=$((attempt + 1))
        sleep 0.1
    done
}

stop_host() {
    if [ -n "$host" ]; then
        kill "$host" 2>/dev/null || true
        wait "$host" 2>/dev/null || true
        host=
    fi
}

if [ "$bench_only" -eq 1 ]; then
    "$probe" host "$work/bench.sock" python3 "$fixture" --bench "$work/bench.ready" &
    host=$!
    wait_ready "$work/bench.ready"
    sleep 0.3
    for run in 1 2 3; do
        printf 'bench_run=%d ' "$run"
        FLOWS_TERMINAL_MODE=bench "$probe" window "$work/bench.sock"
    done
    stop_host
    exit 0
fi

# 1. Interactive production host attach, IME, control, paste, mouse, and resize across phases 1 and 2.
"$probe" host "$work/pty.sock" python3 "$fixture" "$work/ready" &
host=$!
wait_ready "$work/ready"
marker="child=$(cat "$work/ready")"
for phase in 1 2; do
    FLOWS_TERMINAL_PHASE="$phase" FLOWS_TERMINAL_EXPECT="$marker" "$probe" window "$work/pty.sock"
done
stop_host

# 2. Late attach to a live production host already scrolled away from the live viewport.
"$probe" host "$work/scrolled.sock" python3 "$fixture" --scrollback "$work/scrolled.ready" &
host=$!
wait_ready "$work/scrolled.ready"
sleep 0.2
"$probe" scroll "$work/scrolled.sock" -80
FLOWS_TERMINAL_MODE=scrolled FLOWS_EXPECT_HISTORY=ROW-0200 FLOWS_FORBID_BOTTOM=LIVE-BOTTOM \
    "$probe" window "$work/scrolled.sock"
stop_host

# 3. Exclusive input ownership, non-owner blocking, detach/claim_input transfer, and reconnect.
"$probe" host "$work/own.sock" python3 "$fixture" --ownership "$work/own.ready" &
host=$!
wait_ready "$work/own.ready"
FLOWS_TERMINAL_MODE=ownership "$probe" window "$work/own.sock"
FLOWS_WORKSPACE_PROBE_EXE="$probe" FLOWS_WORKSPACE_SOCKET="$work/own.sock" FLOWS_WORKSPACE_SESSION="probe" \
    "$(dirname "$probe")/flows-workspace-tests"
stop_host
