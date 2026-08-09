#!/usr/bin/env bash
# Panmail development runner.
#
#   scripts/dev.sh              start everything
#   scripts/dev.sh --help       every command and option
#
# Everything a development run creates lives in .dev/ at the repository root:
# the config file, the database, the Pebble stores and the compiled binary. A
# real installation keeps its signing key and database password in
# ~/.panmail/db_config.yaml, and nothing here reads or writes that.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
LOG_TAG="dev"
# shellcheck source=dev/lib.sh
. "$SCRIPT_DIR/dev/lib.sh"

usage() {
	cat <<'EOF'
Panmail development runner

USAGE
  scripts/dev.sh [command] [options]

COMMANDS
  up            (default) start the database, set up on first run, then run the
                gateway and the Vite dev server together
  backend       run only the Go gateway
  frontend      run only the Vite dev server
  bootstrap     perform first-run setup (database, admin user, config)
  generate      buf lint + buf generate, for both Go and TypeScript
  db <sub>      up | down | reset | status
  preflight     check the toolchain, the ports and the workspace
  status        show ports, database, and the admin credentials
  stop          stop the dev database container
  reset         delete .dev/ and the dev database, back to a clean slate
  help          this text

OPTIONS
  --db <engine>       sqlite (default) or postgres. postgres runs under Apple's
                      `container` (docker/podman accepted as fallbacks)
  --port <n>          gateway port (default 8080)
  --web-port <n>      Vite port (default 5173)
  --no-watch          do not rebuild and restart the gateway on file changes
  --no-frontend       run the gateway alone
  --single-port       serve the built UI from the gateway instead of Vite.
                      One port, no HMR; matches how production serves the app.
  --fresh             reset first, then start from scratch
  --skip-preflight    skip the environment checks
  --yes               do not ask for confirmation on destructive commands

ENVIRONMENT
  Values are read from .dev/dev.env, and anything already exported wins:

    PANMAIL_DEV_DB              sqlite | postgres
    PANMAIL_DEV_PORT            gateway port
    PANMAIL_DEV_WEB_PORT        Vite port
    PANMAIL_DEV_ADMIN_EMAIL     admin account created at bootstrap
    PANMAIL_DEV_ADMIN_PASSWORD  pin one instead of having it generated
    PANMAIL_SECRET_KEY          64 hex chars; encrypts stored provider
                                credentials. Generated once into .dev/dev.env —
                                replacing it makes existing ones unreadable.
    PANMAIL_DEV_PG_EXTERNAL=1   use your own PostgreSQL rather than a container
                                (with PANMAIL_DEV_PG_HOST/PORT/USER/PASSWORD/DB)

EXAMPLES
  scripts/dev.sh                        sqlite, gateway + Vite, hot reload
  scripts/dev.sh --db postgres          against a PostgreSQL container
  scripts/dev.sh --fresh                wipe and start over
  scripts/dev.sh backend --no-watch     just the API, no watcher
  scripts/dev.sh --single-port          production-style single-origin serving
EOF
}

COMMAND=""
FRESH=0
SKIP_PREFLIGHT=0
ASSUME_YES=0
WATCH_ARGS=()
RUN_FRONTEND=1
SINGLE_PORT=0
DB_SUBCOMMAND=""

while [ $# -gt 0 ]; do
	case "$1" in
	up | backend | frontend | bootstrap | generate | preflight | status | stop | reset | help)
		[ -z "$COMMAND" ] && COMMAND="$1" || die "unexpected argument: $1"
		;;
	db)
		[ -z "$COMMAND" ] && COMMAND="db" || die "unexpected argument: $1"
		if [ $# -gt 1 ]; then
			case "$2" in
			up | down | reset | status)
				DB_SUBCOMMAND="$2"
				shift
				;;
			esac
		fi
		;;
	--db)
		[ $# -ge 2 ] || die "--db needs a value"
		export PANMAIL_DEV_DB="$2"
		shift
		;;
	--db=*) export PANMAIL_DEV_DB="${1#*=}" ;;
	--port)
		[ $# -ge 2 ] || die "--port needs a value"
		export PANMAIL_DEV_PORT="$2"
		shift
		;;
	--port=*) export PANMAIL_DEV_PORT="${1#*=}" ;;
	--web-port)
		[ $# -ge 2 ] || die "--web-port needs a value"
		export PANMAIL_DEV_WEB_PORT="$2"
		shift
		;;
	--web-port=*) export PANMAIL_DEV_WEB_PORT="${1#*=}" ;;
	--no-watch) WATCH_ARGS+=(--no-watch) ;;
	--no-frontend) RUN_FRONTEND=0 ;;
	--single-port) SINGLE_PORT=1 ;;
	--fresh) FRESH=1 ;;
	--skip-preflight) SKIP_PREFLIGHT=1 ;;
	--yes | -y) ASSUME_YES=1 ;;
	-h | --help) COMMAND="help" ;;
	*) die "unknown option: $1 (try --help)" ;;
	esac
	shift
done

COMMAND="${COMMAND:-up}"
[ "$COMMAND" = "help" ] && {
	usage
	exit 0
}

load_dev_env

# --single-port makes the gateway serve the UI itself, which leaves nothing for
# Vite to do.
#
# The watcher is also turned off. cmd/api runs a full bun build on every start
# when --built-ui is passed to a binary without the UI embedded, so leaving the
# watcher on would mean a complete UI rebuild for every Go edit. This mode is
# for checking how production serves the app, not for iterating.
if [ "$SINGLE_PORT" -eq 1 ]; then
	RUN_FRONTEND=0
	WATCH_ARGS+=(--built-ui)
	case " ${WATCH_ARGS[*]} " in
	*" --no-watch "*) ;;
	*) WATCH_ARGS+=(--no-watch) ;;
	esac
fi

confirm() {
	[ "$ASSUME_YES" -eq 1 ] && return 0
	printf '%s%s%s [y/N] ' "$C_YELLOW" "$1" "$C_RESET"
	local reply
	read -r reply || true
	case "$reply" in y | Y | yes | YES) return 0 ;; *) return 1 ;; esac
}

cmd_reset() {
	if ! confirm "Delete .dev/ and the development database?"; then
		log "cancelled"
		return 0
	fi
	"$SCRIPT_DIR/dev/database.sh" reset || true
	# Guarded: an empty or unexpected DEV_DIR must never reach rm -rf.
	case "$DEV_DIR" in
	"$ROOT"/.dev) rm -rf "$DEV_DIR" ;;
	*) die "refusing to remove '$DEV_DIR': not \$ROOT/.dev" ;;
	esac

	# load_dev_env has already sourced and exported whatever was in the file
	# just deleted. Left in place, those values would be inherited by the
	# bootstrap that follows a --fresh, which would then reuse the old password
	# and the old encryption key without recording either — they would vanish
	# when this shell exits, taking access to the new database with them.
	unset PANMAIL_SECRET_KEY PANMAIL_DEV_ADMIN_PASSWORD

	log_ok "clean — the next run will set up again"
}

cmd_status() {
	printf '%sPanmail development%s\n' "$C_BOLD" "$C_RESET"
	printf '  %-12s %s\n' "root" "$ROOT"
	printf '  %-12s %s\n' "engine" "$PANMAIL_DEV_DB"
	printf '  %-12s %s\n' "gateway" "$(backend_url)"
	printf '  %-12s %s\n' "frontend" "$(frontend_url)"
	echo

	if is_bootstrapped; then
		printf '  %s✓%s set up  %s%s%s\n' "$C_GREEN" "$C_RESET" "$C_DIM" "$DEV_CONFIG" "$C_RESET"
		printf '  %-12s %s\n' "email" "${PANMAIL_DEV_ADMIN_EMAIL}"
		if [ -n "${PANMAIL_DEV_ADMIN_PASSWORD:-}" ]; then
			printf '  %-12s %s\n' "password" "${PANMAIL_DEV_ADMIN_PASSWORD}"
		else
			printf '  %-12s %s(not recorded — set one and re-run with --fresh)%s\n' \
				"password" "$C_DIM" "$C_RESET"
		fi
	else
		printf '  %s•%s not set up — run: scripts/dev.sh bootstrap\n' "$C_YELLOW" "$C_RESET"
	fi
	echo

	"$SCRIPT_DIR/dev/database.sh" status

	if port_in_use "$PANMAIL_DEV_PORT"; then
		log "gateway is running (pid $(port_pid "$PANMAIL_DEV_PORT"))"
	else
		log_dim "gateway is not running"
	fi
	if port_in_use "$PANMAIL_DEV_WEB_PORT"; then
		log "vite is running (pid $(port_pid "$PANMAIL_DEV_WEB_PORT"))"
	else
		log_dim "vite is not running"
	fi
}

cmd_up() {
	if [ "$SKIP_PREFLIGHT" -eq 0 ]; then
		# In --single-port mode there is no Vite server, so its port being taken
		# is not a problem worth failing on.
		PANMAIL_DEV_CHECK_WEB_PORT=$([ "$RUN_FRONTEND" -eq 1 ] && echo 1 || echo 0) \
			"$SCRIPT_DIR/dev/preflight.sh"
		echo
	fi

	"$SCRIPT_DIR/dev/database.sh" up
	"$SCRIPT_DIR/dev/bootstrap.sh"

	PIDS=()
	NAMES=()
	SHUTTING_DOWN=0

	shutdown() {
		[ "$SHUTTING_DOWN" -eq 1 ] && return 0
		SHUTTING_DOWN=1
		echo
		log "shutting down"
		local i=0
		while [ "$i" -lt "${#PIDS[@]}" ]; do
			# The component scripts install their own traps, so signalling them
			# is what stops the gateway and Vite cleanly.
			kill -TERM "${PIDS[$i]}" 2>/dev/null || true
			i=$((i + 1))
		done
		local waited=0
		while [ "$waited" -lt 15 ]; do
			local alive=0
			for p in "${PIDS[@]}"; do kill -0 "$p" 2>/dev/null && alive=1; done
			[ "$alive" -eq 0 ] && break
			sleep 1
			waited=$((waited + 1))
		done
		for p in "${PIDS[@]}"; do kill -KILL "$p" 2>/dev/null || true; done
		log_ok "stopped"
	}
	trap 'shutdown; exit 0' INT TERM

	"$SCRIPT_DIR/dev/backend.sh" "${WATCH_ARGS[@]+"${WATCH_ARGS[@]}"}" &
	PIDS+=($!)
	NAMES+=("backend")

	if [ "$RUN_FRONTEND" -eq 1 ]; then
		"$SCRIPT_DIR/dev/frontend.sh" &
		PIDS+=($!)
		NAMES+=("frontend")
	fi

	if [ "$RUN_FRONTEND" -eq 1 ]; then
		OPEN_URL="$(frontend_url)"
	else
		OPEN_URL="$(backend_url)"
	fi

	# Waiting for the gateway before printing the banner means the URL is
	# live by the time it appears, rather than a promise that fails on click.
	if wait_for_http "http://127.0.0.1:${PANMAIL_DEV_PORT}/healthz" 60; then
		echo
		printf '%s  Panmail is running%s\n' "$C_BOLD$C_GREEN" "$C_RESET"
		printf '    %sopen%s      %s%s%s\n' "$C_DIM" "$C_RESET" "$C_CYAN" "$OPEN_URL" "$C_RESET"
		printf '    %sapi%s       %s\n' "$C_DIM" "$C_RESET" "$(backend_url)"
		printf '    %ssign in%s   %s\n' "$C_DIM" "$C_RESET" "${PANMAIL_DEV_ADMIN_EMAIL}"
		[ -n "${PANMAIL_DEV_ADMIN_PASSWORD:-}" ] &&
			printf '    %spassword%s  %s\n' "$C_DIM" "$C_RESET" "${PANMAIL_DEV_ADMIN_PASSWORD}"
		printf '    %sctrl-c to stop%s\n' "$C_DIM" "$C_RESET"
		echo
	fi

	# Bash 3.2, which is what macOS ships, has no `wait -n`, so the exit of any
	# one component is detected by polling.
	while true; do
		sleep 1
		local i=0
		while [ "$i" -lt "${#PIDS[@]}" ]; do
			if ! kill -0 "${PIDS[$i]}" 2>/dev/null; then
				log_warn "${NAMES[$i]} exited"
				shutdown
				exit 1
			fi
			i=$((i + 1))
		done
	done
}

# Handled here rather than inside cmd_up so that --fresh means the same thing
# whichever command follows it, instead of being silently ignored by all but one.
if [ "$FRESH" -eq 1 ]; then
	case "$COMMAND" in
	reset) ;; # already what reset does
	*) cmd_reset ;;
	esac
fi

case "$COMMAND" in
up) cmd_up ;;
backend) exec "$SCRIPT_DIR/dev/backend.sh" "${WATCH_ARGS[@]+"${WATCH_ARGS[@]}"}" ;;
frontend) exec "$SCRIPT_DIR/dev/frontend.sh" ;;
bootstrap) exec "$SCRIPT_DIR/dev/bootstrap.sh" ;;
generate) exec "$SCRIPT_DIR/dev/generate.sh" ;;
preflight) exec "$SCRIPT_DIR/dev/preflight.sh" ;;
db) exec "$SCRIPT_DIR/dev/database.sh" "${DB_SUBCOMMAND:-status}" ;;
stop) exec "$SCRIPT_DIR/dev/database.sh" down ;;
status) cmd_status ;;
reset) cmd_reset ;;
*) die "unknown command: $COMMAND" ;;
esac
