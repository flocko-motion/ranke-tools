#!/usr/bin/env bash
# Run the ranke-db dev server: install.sh catches the binary up to what
# server/.rankedb-version pins, then this runs it against config.json — an
# ephemeral, in-memory instance, the same shape as ranke-db's own `make dev`: a
# throwaway signing key, nothing persisted between runs.
#
# The pin only moves via `make upgrade`, not on every run: a dev server that
# silently followed GitHub's latest on each launch could change under you
# mid-session, and would need the network to start at all. `make upgrade` is the
# deliberate "take the new latest" step — and the point of testing against it is
# that ranke-tools' own tests should fail if they're not compatible with it.
#
# Usage: server/run.sh
#
# Refuses to start a second instance while one is already running (pid file) —
# server/stop.sh ends it.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$DIR/rankedb"
PID_FILE="$DIR/.rankedb.pid"
CONFIG="$DIR/config.json"

command -v openssl >/dev/null 2>&1 || { echo "run.sh needs openssl to mint a throwaway signing key" >&2; exit 1; }

# --- refuse a second instance ------------------------------------------------
if [ -f "$PID_FILE" ]; then
	pid="$(cat "$PID_FILE" 2>/dev/null || true)"
	if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
		echo "run.sh: already running (pid $pid) — server/stop.sh to stop it, or remove $PID_FILE if it's stale" >&2
		exit 1
	fi
	rm -f "$PID_FILE"
fi

"$DIR/install.sh"

# --- run ------------------------------------------------------------------
addr="$(grep -o '"addr"[[:space:]]*:[[:space:]]*"[^"]*"' "$CONFIG" | head -1 | sed -E 's/.*"([^"]*)"$/\1/')"
url="http://localhost${addr:-:8080}"
echo ">> $CONFIG — ephemeral signing key, in-memory storage, nothing persisted between runs"
echo ">> serving on  $url"
echo ">> try:  curl $url/health  ·  curl $url/branches  ·  curl $url/branches/main/head"
echo ">> ctrl-c (or server/stop.sh from another shell) to stop"

RANKE_SIGNER_KEY="$(openssl genpkey -algorithm ed25519)" "$BIN" run --dev "$CONFIG" &
child=$!
echo "$child" >"$PID_FILE"
trap 'kill "$child" 2>/dev/null || true; rm -f "$PID_FILE"' EXIT INT TERM
wait "$child"
