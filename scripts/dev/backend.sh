#!/usr/bin/env bash
# Runs the Go gateway for development, rebuilding and restarting when a source
# file changes.
#
# The binary is compiled to .dev/bin rather than run with `go run`, because
# `go run` puts a wrapper process between this script and the server: killing
# the wrapper leaves the server holding the port and the Pebble locks.
#
# Usage: backend.sh [--no-watch] [--built-ui]
set -euo pipefail

LOG_TAG="backend"
# shellcheck source=lib.sh
. "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
load_dev_env
ensure_secret_key

WATCH=1
BUILT_UI=0
while [ $# -gt 0 ]; do
	case "$1" in
	--no-watch) WATCH=0 ;;
	--watch) WATCH=1 ;;
	# Serves web/dist from the Go process instead of relying on the Vite dev
	# server. Single port, no HMR — useful for testing the embedded-UI routing
	# and the /setup redirect exactly as production serves them.
	--built-ui) BUILT_UI=1 ;;
	*) die "unknown option: $1" ;;
	esac
	shift
done

is_bootstrapped || die "not set up yet — run: scripts/dev.sh bootstrap"

set_backend_flags
[ "$BUILT_UI" -eq 1 ] && BACKEND_FLAGS+=(--built-ui)

SERVER_PID=""
SHUTTING_DOWN=0

shutdown() {
	[ "$SHUTTING_DOWN" -eq 1 ] && return 0
	SHUTTING_DOWN=1
	if [ -n "$SERVER_PID" ]; then
		log "stopping"
		stop_pid "$SERVER_PID" 12
		SERVER_PID=""
	fi
}
trap 'shutdown; exit 0' INT TERM
trap shutdown EXIT

start_server() {
	(cd "$ROOT" && PORT="$PANMAIL_DEV_PORT" exec "$DEV_BIN" "${BACKEND_FLAGS[@]}") &
	SERVER_PID=$!
}

rebuild() {
	local out
	if ! out="$(build_backend 2>&1)"; then
		log_err "build failed"
		printf '%s\n' "$out" >&2
		return 1
	fi
	return 0
}

# --- air, if the developer has it -------------------------------------------
# air handles debouncing and incremental rebuilds better than a poll loop, so
# it wins when present. Its config is generated here rather than committed, to
# keep the paths consistent with the rest of these scripts.
if [ "$WATCH" -eq 1 ] && have air; then
	AIR_CONFIG="$DEV_DIR/air.toml"
	ensure_dev_dirs
	{
		printf 'root = "%s"\n' "$ROOT"
		printf 'tmp_dir = "%s"\n\n' "$DEV_DIR/tmp"
		printf '[build]\n'
		printf '  cmd = "%s build -o %s ./cmd/api"\n' "$(resolve_go)" "$DEV_BIN"
		printf '  bin = "%s"\n' "$DEV_BIN"
		printf '  full_bin = "PORT=%s %s' "$PANMAIL_DEV_PORT" "$DEV_BIN"
		for flag in "${BACKEND_FLAGS[@]}"; do printf ' %s' "$flag"; done
		printf '"\n'
		printf '  include_ext = ["go", "sql"]\n'
		printf '  exclude_dir = ["web", ".dev", ".git", "graphify-out", "archives", "docs"]\n'
		printf '  exclude_regex = ["_test\\\\.go"]\n'
		printf '  delay = 300\n'
		printf '  stop_on_error = false\n'
	} >"$AIR_CONFIG"

	log "watching with air on port $PANMAIL_DEV_PORT"
	cd "$ROOT"
	exec air -c "$AIR_CONFIG"
fi

# --- built-in watcher --------------------------------------------------------

log "building"
rebuild || exit 1

log_ok "listening on $(backend_url)"
start_server

if [ "$WATCH" -eq 0 ]; then
	wait "$SERVER_PID" 2>/dev/null || true
	exit 0
fi

WATCH_DIRS=("$ROOT/cmd" "$ROOT/internal" "$ROOT/pkg" "$ROOT/api")
STAMP="$DEV_DIR/.watch-stamp"
: >"$STAMP"

log_dim "watching cmd/ internal/ pkg/ api/ for changes"

# A poll loop rather than fsevents/inotify: it needs nothing installed, and at
# one second the latency is below the time a Go rebuild takes anyway. Test files
# are skipped so that editing a test does not bounce the server.
while true; do
	sleep 1

	# The server exiting on its own (a panic, a port conflict, a failed
	# migration) should end the script rather than leave a watcher spinning
	# against nothing.
	if [ -n "$SERVER_PID" ] && ! kill -0 "$SERVER_PID" 2>/dev/null; then
		wait "$SERVER_PID" 2>/dev/null || true
		log_err "the gateway exited"
		exit 1
	fi

	changed="$(find "${WATCH_DIRS[@]}" -type f \( -name '*.go' -o -name '*.sql' \) \
		! -name '*_test.go' -newer "$STAMP" -print 2>/dev/null | head -n 1)"
	[ -n "$changed" ] || continue

	: >"$STAMP"
	log "changed: ${changed#"$ROOT"/}"

	if rebuild; then
		stop_pid "$SERVER_PID" 12
		SERVER_PID=""
		start_server
		log_ok "restarted on $(backend_url)"
	else
		# The previous binary keeps serving. Losing a working server because of
		# a typo mid-edit is worse than running one revision behind.
		log_warn "keeping the previous build running"
	fi
done
