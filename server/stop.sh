#!/usr/bin/env bash
# Stop the dev server run.sh started in the background, via its pid file.
#
# Usage: server/stop.sh
#        PORT=8123 server/stop.sh   # stop the instance run.sh was given the same PORT for
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PID_FILE="$DIR/.rankedb${PORT:+.$PORT}.pid"

if [ ! -f "$PID_FILE" ]; then
	echo "stop.sh: not running (no $PID_FILE)"
	exit 0
fi

pid="$(cat "$PID_FILE" 2>/dev/null || true)"
if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
	echo "stop.sh: $PID_FILE is stale — removing it"
	rm -f "$PID_FILE"
	exit 0
fi

echo ">> stopping ranke-db (pid $pid)…"
kill "$pid"
for _ in $(seq 1 50); do
	kill -0 "$pid" 2>/dev/null || break
	sleep 0.1
done
if kill -0 "$pid" 2>/dev/null; then
	echo "stop.sh: pid $pid did not exit in 5s — sending SIGKILL" >&2
	kill -9 "$pid" 2>/dev/null || true
fi
rm -f "$PID_FILE"
echo ">> stopped"
