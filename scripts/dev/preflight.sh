#!/usr/bin/env bash
# Checks that the machine can actually run a Panmail development stack, and
# reports every problem it finds rather than stopping at the first one. A
# half-diagnosed environment costs more time than a slightly longer report.
set -euo pipefail

LOG_TAG="preflight"
# shellcheck source=lib.sh
. "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
load_dev_env

FAILURES=0
WARNINGS=0

require() {
	local name="$1" path="$2" detail="${3:-}"
	if [ -n "$path" ]; then
		printf '  %s✓%s %-8s %s%s%s\n' "$C_GREEN" "$C_RESET" "$name" "$C_DIM" "${detail:-$path}" "$C_RESET"
	else
		printf '  %s✗%s %-8s %smissing%s\n' "$C_RED" "$C_RESET" "$name" "$C_RED" "$C_RESET"
		FAILURES=$((FAILURES + 1))
	fi
}

optional() {
	local name="$1" path="$2" why="$3"
	if [ -n "$path" ]; then
		printf '  %s✓%s %-8s %s%s%s\n' "$C_GREEN" "$C_RESET" "$name" "$C_DIM" "$path" "$C_RESET"
	else
		printf '  %s•%s %-8s %snot installed — %s%s\n' "$C_YELLOW" "$C_RESET" "$name" "$C_DIM" "$why" "$C_RESET"
		WARNINGS=$((WARNINGS + 1))
	fi
}

log "checking the toolchain"

GO_BIN="$(resolve_go || true)"
require go "$GO_BIN" "${GO_BIN:+$("$GO_BIN" version 2>/dev/null | cut -d' ' -f3-4)}"
require bun "$(command -v bun 2>/dev/null || true)"
require curl "$(command -v curl 2>/dev/null || true)"
optional buf "$(command -v buf 2>/dev/null || true)" "needed only for 'dev.sh generate'"
optional air "$(command -v air 2>/dev/null || true)" "a built-in file watcher is used instead"

if [ "$PANMAIL_DEV_DB" = "postgres" ] && [ "$PANMAIL_DEV_PG_EXTERNAL" != "1" ]; then
	RUNTIME="$(resolve_container_runtime || true)"
	require runtime "$RUNTIME" "${RUNTIME:+$(command -v "$RUNTIME")}"
	if [ "$RUNTIME" = "container" ]; then
		# Apple's container CLI has no `inspect --format`, so container state is
		# read out of `ls --format json`. jq ships with macOS 26, which is also
		# the minimum for the container CLI, so this is belt and braces.
		require jq "$(command -v jq 2>/dev/null || true)"
		# Every other command fails in a confusing way if the service is down.
		if container ls >/dev/null 2>&1; then
			printf '  %s✓%s %-8s %sservice running%s\n' "$C_GREEN" "$C_RESET" "service" "$C_DIM" "$C_RESET"
		else
			printf '  %s✗%s %-8s container service is not responding — try: container system start\n' \
				"$C_RED" "$C_RESET" "service"
			FAILURES=$((FAILURES + 1))
		fi
	fi
fi

log "checking ports"

check_port() {
	local port="$1" what="$2" pid owner
	if port_in_use "$port"; then
		pid="$(port_pid "$port")"
		owner="$(ps -o comm= -p "$pid" 2>/dev/null | tr -d ' ' || true)"
		printf '  %s✗%s %-8s %s is busy (pid %s%s)\n' \
			"$C_RED" "$C_RESET" "$port" "$what" "$pid" "${owner:+, $owner}"
		FAILURES=$((FAILURES + 1))
	else
		printf '  %s✓%s %-8s %s%s free%s\n' "$C_GREEN" "$C_RESET" "$port" "$C_DIM" "$what" "$C_RESET"
	fi
}

check_port "$PANMAIL_DEV_PORT" "backend"
if [ "${PANMAIL_DEV_CHECK_WEB_PORT:-1}" = "1" ]; then
	check_port "$PANMAIL_DEV_WEB_PORT" "frontend"
fi

log "checking the workspace"

if [ -f "$ROOT/go.mod" ]; then
	printf '  %s✓%s %-8s %s%s%s\n' "$C_GREEN" "$C_RESET" "root" "$C_DIM" "$ROOT" "$C_RESET"
else
	printf '  %s✗%s %-8s could not find go.mod at %s\n' "$C_RED" "$C_RESET" "root" "$ROOT"
	FAILURES=$((FAILURES + 1))
fi

if is_bootstrapped; then
	printf '  %s✓%s %-8s %s%s%s\n' "$C_GREEN" "$C_RESET" "config" "$C_DIM" "$DEV_CONFIG" "$C_RESET"
else
	printf '  %s•%s %-8s %snot yet created — dev.sh bootstrap will make it%s\n' \
		"$C_YELLOW" "$C_RESET" "config" "$C_DIM" "$C_RESET"
fi

if [ -d "$ROOT/web/node_modules" ]; then
	printf '  %s✓%s %-8s %s%s%s\n' "$C_GREEN" "$C_RESET" "web deps" "$C_DIM" "web/node_modules" "$C_RESET"
else
	printf '  %s•%s %-8s %snot installed — dev.sh will run bun install%s\n' \
		"$C_YELLOW" "$C_RESET" "web deps" "$C_DIM" "$C_RESET"
fi

# cmd/api binds ":$PORT", not "127.0.0.1:$PORT". On an untrusted network the
# development gateway and its wizard are reachable by anyone who can route to
# this machine, so say so rather than letting it be discovered.
log_dim "note: the gateway listens on all interfaces (cmd/api/main.go binds \":\$PORT\")"

if [ "$PANMAIL_DEV_DB" = "sqlite" ]; then
	# See .serena/memories/multi-db-claim-is-partial.md: the provider search
	# query is built with ILIKE, which SQLite does not implement.
	log_dim "note: on SQLite, email provider name search fails (ILIKE). Use --db postgres to exercise it."
fi

echo
if [ "$FAILURES" -gt 0 ]; then
	die "$FAILURES check(s) failed"
fi
if [ "$WARNINGS" -gt 0 ]; then
	log_ok "ready ($WARNINGS optional tool(s) missing)"
else
	log_ok "ready"
fi
