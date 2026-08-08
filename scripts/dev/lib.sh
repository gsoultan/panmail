#!/usr/bin/env bash
# Shared helpers for scripts/dev/*. Sourced, never executed directly.
#
# Every development run is confined to .dev/ at the repository root: the config
# file, the database, the Pebble stores and the compiled binary all live there.
# A real installation keeps its PASETO signing key and its database password in
# ~/.panmail/db_config.yaml, and a development run must never read, overwrite or
# lock that file. The --config flag on cmd/api is what makes this possible.

# shellcheck shell=bash

if [ -n "${_PANMAIL_DEV_LIB:-}" ]; then return 0; fi
_PANMAIL_DEV_LIB=1

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
DEV_DIR="$ROOT/.dev"
DEV_ENV_FILE="$DEV_DIR/dev.env"
DEV_CONFIG_DIR="$DEV_DIR/config"
DEV_CONFIG="$DEV_CONFIG_DIR/db_config.yaml"
DEV_STATE_DIR="$DEV_DIR/state"
DEV_BIN="$DEV_DIR/bin/panmail-dev"
DEV_SQLITE="$DEV_STATE_DIR/panmail.sqlite"

# ---------------------------------------------------------------------------
# Output
# ---------------------------------------------------------------------------

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	C_RESET=$'\033[0m'
	C_BOLD=$'\033[1m'
	C_DIM=$'\033[2m'
	C_RED=$'\033[31m'
	C_GREEN=$'\033[32m'
	C_YELLOW=$'\033[33m'
	C_BLUE=$'\033[34m'
	C_CYAN=$'\033[36m'
else
	C_RESET='' C_BOLD='' C_DIM='' C_RED='' C_GREEN='' C_YELLOW='' C_BLUE='' C_CYAN=''
fi

LOG_TAG="${LOG_TAG:-dev}"

log() { printf '%s[%s]%s %s\n' "$C_BLUE" "$LOG_TAG" "$C_RESET" "$*"; }
log_ok() { printf '%s[%s]%s %s%s%s\n' "$C_BLUE" "$LOG_TAG" "$C_RESET" "$C_GREEN" "$*" "$C_RESET"; }
log_warn() { printf '%s[%s]%s %swarning:%s %s\n' "$C_BLUE" "$LOG_TAG" "$C_RESET" "$C_YELLOW" "$C_RESET" "$*" >&2; }
log_err() { printf '%s[%s]%s %serror:%s %s\n' "$C_BLUE" "$LOG_TAG" "$C_RESET" "$C_RED" "$C_RESET" "$*" >&2; }
log_dim() { printf '%s[%s]%s %s%s%s\n' "$C_BLUE" "$LOG_TAG" "$C_RESET" "$C_DIM" "$*" "$C_RESET"; }
die() {
	log_err "$*"
	exit 1
}

# ---------------------------------------------------------------------------
# Tools
# ---------------------------------------------------------------------------

have() { command -v "$1" >/dev/null 2>&1; }

# resolve_go honours the note in .junie/agents.md: on macOS the Homebrew Go is
# often absent from a non-login shell's PATH.
resolve_go() {
	if have go; then
		command -v go
	elif [ -x /opt/homebrew/bin/go ]; then
		echo /opt/homebrew/bin/go
	elif [ -x /usr/local/go/bin/go ]; then
		echo /usr/local/go/bin/go
	else
		return 1
	fi
}

# Apple's `container` first: it is the native runtime on macOS 26+ and needs no
# Docker Desktop. docker and podman remain accepted so the same scripts work on
# Linux, but nothing here requires them.
resolve_container_runtime() {
	if have container; then
		echo container
	elif have docker; then
		echo docker
	elif have podman; then
		echo podman
	else
		return 1
	fi
}

# ---------------------------------------------------------------------------
# Randomness
# ---------------------------------------------------------------------------

# rand_hex32 produces the 64-character hex string that PANMAIL_SECRET_KEY and
# the PASETO key both expect (32 raw bytes, see pkg/secrets/keyring.go).
rand_hex32() {
	if have openssl; then
		openssl rand -hex 32
	elif have xxd; then
		head -c 32 /dev/urandom | xxd -p -c 64
	else
		od -An -tx1 -N32 /dev/urandom | tr -d ' \n'
		echo
	fi
}

# rand_password returns 20 alphanumeric characters, comfortably over the
# 12-character minimum enforced in internal/auth/usecases/auth_usecase.go, and
# free of any character that would need escaping inside a JSON payload.
rand_password() {
	LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom 2>/dev/null | head -c 20 || true
	echo
}

# ---------------------------------------------------------------------------
# Ports and readiness
# ---------------------------------------------------------------------------

port_pid() {
	local port="$1"
	if have lsof; then
		lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -n 1
	fi
}

port_in_use() {
	local port="$1"
	if have lsof; then
		[ -n "$(port_pid "$port")" ]
	elif have nc; then
		nc -z 127.0.0.1 "$port" >/dev/null 2>&1
	else
		return 1
	fi
}

# wait_for_http polls until the URL answers or the deadline passes. Used instead
# of a fixed sleep because the first boot also runs database migrations.
wait_for_http() {
	local url="$1" timeout="${2:-45}" waited=0
	while [ "$waited" -lt "$timeout" ]; do
		if curl -fsS -o /dev/null --max-time 2 "$url" 2>/dev/null; then
			return 0
		fi
		sleep 1
		waited=$((waited + 1))
	done
	return 1
}

# stop_pid asks a process to shut down and escalates only if it will not. The
# gateway drains its outbox and webhook workers on SIGINT, so it is given time
# to finish rather than being killed outright.
stop_pid() {
	local pid="$1" grace="${2:-10}" waited=0
	[ -n "$pid" ] || return 0
	kill -0 "$pid" 2>/dev/null || return 0

	kill -INT "$pid" 2>/dev/null || true
	while [ "$waited" -lt "$grace" ]; do
		kill -0 "$pid" 2>/dev/null || return 0
		sleep 1
		waited=$((waited + 1))
	done
	kill -KILL "$pid" 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Development environment
# ---------------------------------------------------------------------------

ensure_dev_dirs() {
	mkdir -p "$DEV_DIR" "$DEV_CONFIG_DIR" "$DEV_STATE_DIR" "$DEV_DIR/bin"
	# The config directory ends up holding the PASETO signing key and, for
	# PostgreSQL, the encrypted database password.
	chmod 700 "$DEV_CONFIG_DIR" 2>/dev/null || true
}

# load_dev_env reads .dev/dev.env, then fills in any value it did not define.
# Values already exported in the caller's environment always win, so a one-off
# `PANMAIL_DEV_PORT=9000 scripts/dev.sh up` works without editing the file.
load_dev_env() {
	if [ -f "$DEV_ENV_FILE" ]; then
		set -a
		# shellcheck disable=SC1090
		. "$DEV_ENV_FILE"
		set +a
	fi

	: "${PANMAIL_DEV_DB:=sqlite}"
	: "${PANMAIL_DEV_PORT:=8080}"
	: "${PANMAIL_DEV_WEB_PORT:=5173}"
	: "${PANMAIL_DEV_BOOTSTRAP_PORT:=$((PANMAIL_DEV_PORT + 1000))}"
	: "${PANMAIL_DEV_ADMIN_EMAIL:=admin@panmail.local}"
	: "${PANMAIL_DEV_ADMIN_NAME:=Dev Admin}"

	: "${PANMAIL_DEV_PG_CONTAINER:=panmail-dev-postgres}"
	# Fully qualified: Apple's container CLI does not assume a default registry
	# the way docker does, and docker accepts the long form too.
	: "${PANMAIL_DEV_PG_IMAGE:=docker.io/library/postgres:17-alpine}"
	: "${PANMAIL_DEV_PG_HOST:=127.0.0.1}"
	: "${PANMAIL_DEV_PG_PORT:=5433}"
	: "${PANMAIL_DEV_PG_USER:=panmail}"
	: "${PANMAIL_DEV_PG_PASSWORD:=panmail_dev}"
	: "${PANMAIL_DEV_PG_DB:=panmail}"
	# Set to 1 to point at a PostgreSQL server you manage yourself instead of
	# letting these scripts run a container.
	: "${PANMAIL_DEV_PG_EXTERNAL:=0}"

	export PANMAIL_DEV_DB PANMAIL_DEV_PORT PANMAIL_DEV_WEB_PORT \
		PANMAIL_DEV_BOOTSTRAP_PORT PANMAIL_DEV_ADMIN_EMAIL PANMAIL_DEV_ADMIN_NAME \
		PANMAIL_DEV_PG_CONTAINER PANMAIL_DEV_PG_IMAGE PANMAIL_DEV_PG_HOST \
		PANMAIL_DEV_PG_PORT PANMAIL_DEV_PG_USER PANMAIL_DEV_PG_PASSWORD \
		PANMAIL_DEV_PG_DB PANMAIL_DEV_PG_EXTERNAL

	case "$PANMAIL_DEV_DB" in
	sqlite | postgres) ;;
	*) die "PANMAIL_DEV_DB must be 'sqlite' or 'postgres', got '$PANMAIL_DEV_DB'" ;;
	esac
}

# persist_dev_env records a generated value so the next run reuses it. The
# secret key in particular must survive restarts: provider credentials in the
# database are encrypted with it, and a new key makes them unreadable.
persist_dev_env() {
	local key="$1" value="$2"
	ensure_dev_dirs
	if [ ! -f "$DEV_ENV_FILE" ]; then
		{
			echo "# Generated by scripts/dev.sh. Safe to delete; values are regenerated."
			echo "# This file is gitignored and holds development-only secrets."
		} >"$DEV_ENV_FILE"
		chmod 600 "$DEV_ENV_FILE"
	fi

	if grep -q "^${key}=" "$DEV_ENV_FILE" 2>/dev/null; then
		local tmp="$DEV_ENV_FILE.tmp.$$"
		grep -v "^${key}=" "$DEV_ENV_FILE" >"$tmp"
		mv "$tmp" "$DEV_ENV_FILE"
	fi
	printf '%s=%s\n' "$key" "$value" >>"$DEV_ENV_FILE"
	chmod 600 "$DEV_ENV_FILE"
}

# ensure_secret_key keeps the credential encryption key in the environment
# rather than in the config file. That is the arrangement README.md recommends
# for production, so development exercises the same code path
# (config.ResolveDataKey prefers the environment variable).
ensure_secret_key() {
	if [ -n "${PANMAIL_SECRET_KEY:-}" ]; then
		export PANMAIL_SECRET_KEY
		# The key has to outlive this shell. If it reached us through the
		# environment but is not recorded on disk, the next run would generate a
		# different one and every provider credential already encrypted with
		# this one would be unreadable. Record it, and say so, because writing
		# an inherited key to a file is not something to do silently.
		if ! grep -q '^PANMAIL_SECRET_KEY=' "$DEV_ENV_FILE" 2>/dev/null; then
			persist_dev_env PANMAIL_SECRET_KEY "$PANMAIL_SECRET_KEY"
			log_dim "recorded PANMAIL_SECRET_KEY in .dev/dev.env so it survives this shell"
		fi
		return 0
	fi
	local key
	key="$(rand_hex32)"
	persist_dev_env PANMAIL_SECRET_KEY "$key"
	export PANMAIL_SECRET_KEY="$key"
	log_dim "generated a development credential encryption key in .dev/dev.env"
}

is_bootstrapped() { [ -f "$DEV_CONFIG" ]; }

backend_url() { echo "http://localhost:${PANMAIL_DEV_PORT}"; }
frontend_url() { echo "http://localhost:${PANMAIL_DEV_WEB_PORT}"; }

# set_backend_flags fills the BACKEND_FLAGS array with the arguments every
# development invocation of the gateway needs: an isolated config file and
# isolated Pebble stores, so nothing is written to the repository root.
#
# An array rather than a string, because these are absolute paths and a
# repository checked out under a directory with a space in its name would
# otherwise be split into the wrong arguments.
set_backend_flags() {
	BACKEND_FLAGS=(
		--config "$DEV_CONFIG"
		--log-dir "$DEV_STATE_DIR/logs.db"
		--event-dir "$DEV_STATE_DIR/events.db"
		--inbound-dir "$DEV_STATE_DIR/inbound.db"
	)
}

# build_backend compiles cmd/api into .dev/bin. The builtui tag is deliberately
# omitted: without it web.IsBuiltUI is false, the UI is not embedded, and the
# gateway serves only its API — which is what the Vite dev server proxies to.
build_backend() {
	local go_bin
	go_bin="$(resolve_go)" || die "go not found in PATH or /opt/homebrew/bin"
	ensure_dev_dirs
	(cd "$ROOT" && "$go_bin" build -o "$DEV_BIN" ./cmd/api)
}
