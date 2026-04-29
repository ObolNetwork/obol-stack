#!/bin/bash
# Launch a flow script detached so it survives SSH disconnect / terminal close.
# Tries tmux → screen → setsid -f. The first that's available wins.
#
# Usage:
#   flows/run-detached.sh <flow-script.sh> [flow-args...]
# Output:
#   Prints the path to the log file. tail -F it for progress.

set -euo pipefail

SCRIPT="${1:?usage: $0 <flow-script> [args...]}"
shift || true

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
SCRIPT_PATH="$HERE/$SCRIPT"

if [ ! -f "$SCRIPT_PATH" ]; then
    echo "error: $SCRIPT_PATH not found" >&2
    exit 1
fi

LOG_DIR="$REPO/.tmp"
mkdir -p "$LOG_DIR"
BASENAME="${SCRIPT%.sh}"
LOG="$LOG_DIR/${BASENAME}-$(date +%Y%m%d-%H%M%S).log"
NAME="flow-${BASENAME}-$$"

CMD="bash '$SCRIPT_PATH' $* > '$LOG' 2>&1"

if command -v tmux >/dev/null 2>&1; then
    tmux new-session -d -s "$NAME" "$CMD"
    echo "tmux session: $NAME"
elif command -v screen >/dev/null 2>&1; then
    screen -dmS "$NAME" bash -c "$CMD"
    echo "screen session: $NAME"
else
    setsid -f bash -c "exec $CMD < /dev/null" </dev/null >/dev/null 2>&1
    echo "setsid (no tmux/screen available)"
fi

echo "$LOG"
