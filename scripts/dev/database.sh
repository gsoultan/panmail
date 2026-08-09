#!/usr/bin/env bash
# Manages the development database.
#
# SQLite is the default because it needs nothing installed and, per
# .serena/memories/multi-db-claim-is-partial.md, is the only other engine whose
# DML actually works. PostgreSQL is available for the cases SQLite cannot cover
# — provider name search uses ILIKE — and for anything worth verifying against
# the production engine before shipping.
#
# PostgreSQL runs under Apple's `container` (macOS 26+). docker and podman are
# accepted as fallbacks so the scripts still work on Linux, but nothing here
# requires them.
#
# Usage: database.sh <up|down|reset|status|wait>
set -euo pipefail

LOG_TAG="db"
# shellcheck source=lib.sh
. "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
load_dev_env

RUNTIME=""
if [ "$PANMAIL_DEV_DB" = "postgres" ] && [ "$PANMAIL_DEV_PG_EXTERNAL" != "1" ]; then
	RUNTIME="$(resolve_container_runtime || true)"
	[ -n "$RUNTIME" ] ||
		die "PANMAIL_DEV_DB=postgres needs Apple 'container' (or docker/podman), or set PANMAIL_DEV_PG_EXTERNAL=1"
fi

# --- runtime differences ------------------------------------------------------
# Apple's container CLI is close to docker's but not identical, and the three
# places it diverges are all load-bearing here.

# 1. `container inspect` has no --format, so state comes from `ls --format json`.
container_state() {
	case "$RUNTIME" in
	container)
		local state
		state="$("$RUNTIME" ls -a --format json 2>/dev/null |
			jq -r --arg n "$PANMAIL_DEV_PG_CONTAINER" \
				'.[] | select(.configuration.id == $n) | .status.state' 2>/dev/null || true)"
		echo "${state:-absent}"
		;;
	*)
		"$RUNTIME" inspect -f '{{.State.Status}}' "$PANMAIL_DEV_PG_CONTAINER" 2>/dev/null || echo "absent"
		;;
	esac
}

# 2. Apple container volumes are formatted block devices, so a fresh one is not
#    empty — it has a lost+found. initdb refuses to initialise into it:
#      initdb: error: directory "/var/lib/postgresql/data" exists but is not empty
#    Pointing PGDATA at a subdirectory of the mount is the fix, and it is
#    harmless on docker, so it is done unconditionally rather than per runtime.
PG_VOLUME="${PANMAIL_DEV_PG_CONTAINER}-data"
PG_MOUNT=/var/lib/postgresql/data
PG_DATA_DIR="$PG_MOUNT/pgdata"

# 3. docker creates a named volume on first use; container wants it to exist.
ensure_volume() {
	case "$RUNTIME" in
	container) "$RUNTIME" volume create "$PG_VOLUME" >/dev/null 2>&1 || true ;;
	*) : ;;
	esac
}

remove_volume() {
	case "$RUNTIME" in
	container) "$RUNTIME" volume delete "$PG_VOLUME" >/dev/null 2>&1 || true ;;
	*) "$RUNTIME" volume rm "$PG_VOLUME" >/dev/null 2>&1 || true ;;
	esac
}

pg_ready() {
	if [ -n "$RUNTIME" ]; then
		"$RUNTIME" exec "$PANMAIL_DEV_PG_CONTAINER" \
			pg_isready -q -U "$PANMAIL_DEV_PG_USER" -d "$PANMAIL_DEV_PG_DB" >/dev/null 2>&1
	elif have pg_isready; then
		pg_isready -q -h "$PANMAIL_DEV_PG_HOST" -p "$PANMAIL_DEV_PG_PORT" \
			-U "$PANMAIL_DEV_PG_USER" -d "$PANMAIL_DEV_PG_DB" >/dev/null 2>&1
	else
		# No client tools: fall back to "is something listening".
		port_in_use "$PANMAIL_DEV_PG_PORT"
	fi
}

# The published port is what the gateway connects through, so readiness has to
# mean reachable from the host, not just up inside the container.
pg_reachable() {
	pg_ready || return 1
	[ -n "$RUNTIME" ] || return 0
	port_in_use "$PANMAIL_DEV_PG_PORT"
}

wait_for_pg() {
	local timeout="${1:-90}" waited=0
	while [ "$waited" -lt "$timeout" ]; do
		if pg_reachable; then return 0; fi
		sleep 1
		waited=$((waited + 1))
	done
	return 1
}

cmd_up() {
	ensure_dev_dirs

	if [ "$PANMAIL_DEV_DB" = "sqlite" ]; then
		log_ok "sqlite at $DEV_SQLITE"
		return 0
	fi

	if [ "$PANMAIL_DEV_PG_EXTERNAL" = "1" ]; then
		log "using the PostgreSQL at $PANMAIL_DEV_PG_HOST:$PANMAIL_DEV_PG_PORT (external)"
		wait_for_pg 20 || die "that PostgreSQL is not accepting connections"
		log_ok "postgres reachable"
		return 0
	fi

	case "$(container_state)" in
	running)
		log "container $PANMAIL_DEV_PG_CONTAINER already running ($RUNTIME)"
		;;
	stopped | exited | created | paused)
		log "starting container $PANMAIL_DEV_PG_CONTAINER ($RUNTIME)"
		"$RUNTIME" start "$PANMAIL_DEV_PG_CONTAINER" >/dev/null
		;;
	*)
		log "creating container $PANMAIL_DEV_PG_CONTAINER ($RUNTIME, $PANMAIL_DEV_PG_IMAGE)"
		ensure_volume
		"$RUNTIME" run -d \
			--name "$PANMAIL_DEV_PG_CONTAINER" \
			-e POSTGRES_USER="$PANMAIL_DEV_PG_USER" \
			-e POSTGRES_PASSWORD="$PANMAIL_DEV_PG_PASSWORD" \
			-e POSTGRES_DB="$PANMAIL_DEV_PG_DB" \
			-e PGDATA="$PG_DATA_DIR" \
			-p "$PANMAIL_DEV_PG_HOST:$PANMAIL_DEV_PG_PORT:5432" \
			-v "$PG_VOLUME:$PG_MOUNT" \
			"$PANMAIL_DEV_PG_IMAGE" >/dev/null
		;;
	esac

	log "waiting for postgres to accept connections"
	if ! wait_for_pg 90; then
		log_err "postgres did not become ready"
		"$RUNTIME" logs "$PANMAIL_DEV_PG_CONTAINER" 2>&1 | tail -15 >&2 || true
		exit 1
	fi
	log_ok "postgres ready on $PANMAIL_DEV_PG_HOST:$PANMAIL_DEV_PG_PORT"
}

cmd_down() {
	if [ "$PANMAIL_DEV_DB" = "sqlite" ] || [ "$PANMAIL_DEV_PG_EXTERNAL" = "1" ]; then
		return 0
	fi
	if [ "$(container_state)" = "running" ]; then
		log "stopping container $PANMAIL_DEV_PG_CONTAINER"
		"$RUNTIME" stop "$PANMAIL_DEV_PG_CONTAINER" >/dev/null
	fi
	log_ok "stopped"
}

cmd_reset() {
	if [ "$PANMAIL_DEV_DB" = "sqlite" ]; then
		rm -f "$DEV_SQLITE" "$DEV_SQLITE-wal" "$DEV_SQLITE-shm"
		log_ok "removed the sqlite database"
		return 0
	fi

	if [ "$PANMAIL_DEV_PG_EXTERNAL" = "1" ]; then
		log_warn "PANMAIL_DEV_PG_EXTERNAL=1: refusing to drop a database these scripts do not own"
		log_warn "drop and recreate '$PANMAIL_DEV_PG_DB' yourself, then run: scripts/dev.sh bootstrap"
		return 0
	fi

	if [ "$(container_state)" != "absent" ]; then
		log "removing container $PANMAIL_DEV_PG_CONTAINER and its volume"
		"$RUNTIME" rm -f "$PANMAIL_DEV_PG_CONTAINER" >/dev/null 2>&1 || true
	fi
	remove_volume
	log_ok "removed"
}

cmd_status() {
	if [ "$PANMAIL_DEV_DB" = "sqlite" ]; then
		if [ -f "$DEV_SQLITE" ]; then
			log "sqlite  $DEV_SQLITE ($(wc -c <"$DEV_SQLITE" | tr -d ' ') bytes)"
		else
			log "sqlite  $DEV_SQLITE (not created yet)"
		fi
		return 0
	fi

	if [ "$PANMAIL_DEV_PG_EXTERNAL" = "1" ]; then
		if pg_ready; then
			log "postgres  $PANMAIL_DEV_PG_HOST:$PANMAIL_DEV_PG_PORT (external, reachable)"
		else
			log "postgres  $PANMAIL_DEV_PG_HOST:$PANMAIL_DEV_PG_PORT (external, unreachable)"
		fi
		return 0
	fi

	log "postgres  $PANMAIL_DEV_PG_CONTAINER is $(container_state) (via $RUNTIME)"
}

case "${1:-status}" in
up) cmd_up ;;
down) cmd_down ;;
reset) cmd_reset ;;
status) cmd_status ;;
wait) wait_for_pg 90 || die "postgres did not become ready" ;;
*) die "usage: database.sh <up|down|reset|status|wait>" ;;
esac
