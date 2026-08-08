#!/usr/bin/env bash
# Runs the Vite dev server for the React UI.
#
# Vite serves the app and proxies the gateway's routes to the Go process (see
# the server.proxy block in web/vite.config.ts). That split is what makes HMR
# possible: the alternative, --built-ui, makes the Go process serve web/dist,
# which means a full bun build for every change.
#
# The ConnectRPC client in web/src/services/client.ts uses an empty baseUrl, so
# every RPC goes to the origin serving the page. The proxy is what turns that
# into a request against the gateway.
#
# Usage: frontend.sh
set -euo pipefail

LOG_TAG="frontend"
# shellcheck source=lib.sh
. "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
load_dev_env

have bun || die "bun is required for the frontend (https://bun.sh)"

cd "$ROOT/web"

# bun install is skipped when node_modules is already newer than the lockfile,
# because it is the slowest step in an otherwise instant start-up.
if [ ! -d node_modules ] || [ bun.lock -nt node_modules ]; then
	log "installing dependencies"
	bun install
	touch node_modules
fi

VITE_PID=""
SHUTTING_DOWN=0
shutdown() {
	[ "$SHUTTING_DOWN" -eq 1 ] && return 0
	SHUTTING_DOWN=1
	if [ -n "$VITE_PID" ]; then
		stop_pid "$VITE_PID" 5
		VITE_PID=""
	fi
}
trap 'shutdown; exit 0' INT TERM
trap shutdown EXIT

# Both are read by web/vite.config.ts. Passing them through the environment
# rather than the command line keeps a single source of truth for the ports.
export PANMAIL_DEV_WEB_PORT
export PANMAIL_API_TARGET="http://127.0.0.1:${PANMAIL_DEV_PORT}"

log "starting vite on $(frontend_url), proxying the API to $PANMAIL_API_TARGET"

bun run dev &
VITE_PID=$!
wait "$VITE_PID" 2>/dev/null || true
