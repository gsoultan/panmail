#!/usr/bin/env bash
# First-run setup for a development instance.
#
# The gateway decides it has been configured purely by the existence of its
# config file (internal/setup/usecases/setup_usecase.go: IsSetup stats the path
# and returns true). Writing a config file by hand would therefore satisfy
# IsSetup, make Setup() refuse to run, and leave the instance with no admin
# user and no way to create one. So this script does not write the config: it
# starts the gateway, calls the SetupService.Setup RPC — which policy.go marks
# public precisely so it can be reached before any credential exists — and lets
# the application write its own config, generate its own PASETO key and create
# the admin user through the same path the browser wizard uses.
#
# The gateway is then stopped. It has to be: the credential keyring is built
# once at start-up and only when a config was already present, so the process
# that performed the setup is running without one.
#
# Usage: bootstrap.sh [--force]
set -euo pipefail

LOG_TAG="bootstrap"
# shellcheck source=lib.sh
. "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
load_dev_env

FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

if is_bootstrapped && [ "$FORCE" -eq 0 ]; then
	log_dim "already set up ($DEV_CONFIG) — nothing to do"
	exit 0
fi

if is_bootstrapped && [ "$FORCE" -eq 1 ]; then
	log_warn "--force: removing $DEV_CONFIG so setup can run again"
	rm -f "$DEV_CONFIG"
fi

ensure_dev_dirs
ensure_secret_key

# A password is generated rather than defaulted to something well known. The
# gateway binds every interface, so on a shared network a predictable dev
# password is a real account on a real admin API. Pin one with
# PANMAIL_DEV_ADMIN_PASSWORD if you would rather have a memorable one.
if [ -z "${PANMAIL_DEV_ADMIN_PASSWORD:-}" ]; then
	PANMAIL_DEV_ADMIN_PASSWORD="$(rand_password)"
	persist_dev_env PANMAIL_DEV_ADMIN_PASSWORD "$PANMAIL_DEV_ADMIN_PASSWORD"
fi
# internal/auth/usecases/auth_usecase.go rejects anything shorter.
if [ "${#PANMAIL_DEV_ADMIN_PASSWORD}" -lt 12 ]; then
	die "PANMAIL_DEV_ADMIN_PASSWORD must be at least 12 characters"
fi

"$(dirname -- "${BASH_SOURCE[0]}")/database.sh" up

# The setup server runs on its own port so that bootstrapping never collides
# with a development server someone already has running.
PORT_FOR_SETUP="$PANMAIL_DEV_BOOTSTRAP_PORT"
if port_in_use "$PORT_FOR_SETUP"; then
	die "port $PORT_FOR_SETUP is busy; set PANMAIL_DEV_BOOTSTRAP_PORT to a free one"
fi

log "building the gateway"
build_backend

SETUP_PID=""
cleanup() {
	if [ -n "$SETUP_PID" ]; then
		stop_pid "$SETUP_PID" 15
		SETUP_PID=""
	fi
}
trap cleanup EXIT INT TERM

BOOT_LOG="$DEV_DIR/bootstrap.log"
log "starting a temporary gateway on port $PORT_FOR_SETUP"

set_backend_flags
(
	cd "$ROOT" && PORT="$PORT_FOR_SETUP" exec "$DEV_BIN" "${BACKEND_FLAGS[@]}"
) >"$BOOT_LOG" 2>&1 &
SETUP_PID=$!

if ! wait_for_http "http://127.0.0.1:$PORT_FOR_SETUP/healthz" 45; then
	log_err "the gateway did not come up; last lines of $BOOT_LOG:"
	tail -n 20 "$BOOT_LOG" >&2 || true
	exit 1
fi

# Build the DatabaseConfig half of the request. Field names are protojson's
# lowerCamelCase form of api/panmail/v1/setup.proto.
if [ "$PANMAIL_DEV_DB" = "sqlite" ]; then
	# An absolute path: the gateway resolves a relative one against its working
	# directory, which is not guaranteed to be the repo root.
	DB_JSON=$(printf '{"type":"sqlite","filePath":"%s"}' "$DEV_SQLITE")
	DB_DESC="sqlite ($DEV_SQLITE)"
else
	DB_JSON=$(printf '{"type":"postgres","host":"%s","port":%s,"user":"%s","password":"%s","dbname":"%s"}' \
		"$PANMAIL_DEV_PG_HOST" "$PANMAIL_DEV_PG_PORT" "$PANMAIL_DEV_PG_USER" \
		"$PANMAIL_DEV_PG_PASSWORD" "$PANMAIL_DEV_PG_DB")
	DB_DESC="postgres ($PANMAIL_DEV_PG_HOST:$PANMAIL_DEV_PG_PORT/$PANMAIL_DEV_PG_DB)"
fi

# baseUrl is the address the gateway itself will put into tracking links, so it
# is the backend's real port, not the port this temporary instance is using and
# not the Vite dev server.
PAYLOAD=$(printf '{"dbConfig":%s,"adminConfig":{"email":"%s","password":"%s","name":"%s"},"baseUrl":"%s"}' \
	"$DB_JSON" "$PANMAIL_DEV_ADMIN_EMAIL" "$PANMAIL_DEV_ADMIN_PASSWORD" \
	"$PANMAIL_DEV_ADMIN_NAME" "$(backend_url)")

log "running setup against $DB_DESC"

RESPONSE_BODY="$DEV_DIR/bootstrap-response.json"
HTTP_CODE="$(curl -sS -o "$RESPONSE_BODY" -w '%{http_code}' \
	-X POST "http://127.0.0.1:$PORT_FOR_SETUP/panmail.v1.SetupService/Setup" \
	-H 'Content-Type: application/json' \
	--data-binary "$PAYLOAD" || echo "000")"

if [ "$HTTP_CODE" != "200" ]; then
	log_err "setup failed (HTTP $HTTP_CODE)"
	cat "$RESPONSE_BODY" >&2 2>/dev/null || true
	echo >&2
	log_err "gateway log: $BOOT_LOG"
	tail -n 20 "$BOOT_LOG" >&2 || true
	exit 1
fi

cleanup
trap - EXIT INT TERM
rm -f "$RESPONSE_BODY"

is_bootstrapped || die "setup reported success but $DEV_CONFIG was not written"

echo
log_ok "development instance ready"
printf '  %-10s %s\n' "config" "$DEV_CONFIG"
printf '  %-10s %s\n' "database" "$DB_DESC"
printf '  %-10s %s\n' "email" "$PANMAIL_DEV_ADMIN_EMAIL"
printf '  %-10s %s%s%s\n' "password" "$C_BOLD" "$PANMAIL_DEV_ADMIN_PASSWORD" "$C_RESET"
printf '  %s(also in .dev/dev.env; run scripts/dev.sh status to see it again)%s\n' "$C_DIM" "$C_RESET"
echo
