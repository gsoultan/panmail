#!/usr/bin/env bash
# The backend job's Postgres and IMAP servers, started explicitly.
#
# These used to be a `services:` block. That key is implemented by the runner
# shelling out to Docker and is only supported on Linux runners, which tied the
# whole job to a Linux box. Starting the same two images here instead costs a
# few lines and lets the job run wherever a container CLI exists -- including a
# self-hosted mac under Apple's `container`, which runs Linux images natively.
#
#   scripts/ci/services.sh up     start and wait until both actually answer
#   scripts/ci/services.sh down   remove them
set -euo pipefail

# Apple's container CLI first: on a mac it is the one that exists, and the repo
# already prefers it in scripts/dev.sh. Docker is the fallback for Linux.
CLI="${PANMAIL_CI_CONTAINER_CLI:-}"
if [ -z "$CLI" ]; then
	if command -v container >/dev/null 2>&1; then
		CLI=container
	elif command -v docker >/dev/null 2>&1; then
		CLI=docker
	else
		echo "no container CLI found: install Apple's container or docker" >&2
		exit 1
	fi
fi

# Fully qualified because Apple's CLI assumes no default registry. Docker
# accepts the long form too, so one name works for both.
PG_IMAGE="${PANMAIL_CI_PG_IMAGE:-docker.io/library/postgres:16-alpine}"
GM_IMAGE="${PANMAIL_CI_GM_IMAGE:-docker.io/greenmail/standalone:2.1.0}"
PG_NAME=panmail-ci-postgres
GM_NAME=panmail-ci-greenmail

# Host ports. The defaults are what the test lanes expect on a clean CI runner;
# override them on a developer machine already running something on 5432, which
# is common enough that hardcoding it makes the script useless locally.
PG_PORT="${PANMAIL_CI_PG_PORT:-5432}"
IMAP_PORT="${PANMAIL_CI_IMAP_PORT:-3143}"
SMTP_PORT="${PANMAIL_CI_SMTP_PORT:-3025}"

up() {
	down >/dev/null 2>&1 || true

	"$CLI" run -d --name "$PG_NAME" \
		-e POSTGRES_USER=panmail \
		-e POSTGRES_PASSWORD=panmail_test \
		-e POSTGRES_DB=panmail_test \
		-p "${PG_PORT}:5432" "$PG_IMAGE" >/dev/null

	# A real IMAP server, because IDLE cannot be judged from a fake. Whether
	# the server pushes an unsolicited EXISTS, whether the client notices, and
	# what happens when the connection dies are properties of the protocol and
	# the two implementations, not of our code.
	"$CLI" run -d --name "$GM_NAME" \
		-e GREENMAIL_OPTS="-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0 -Dgreenmail.auth.disabled" \
		-p "${IMAP_PORT}:3143" -p "${SMTP_PORT}:3025" "$GM_IMAGE" >/dev/null

	# Wait for the servers, not for their ports. The postgres entrypoint runs
	# initdb against a temporary server and then restarts, so the port answers
	# before the database does -- tests that start on the open port fail for a
	# reason that has nothing to do with them.
	for _ in $(seq 1 60); do
		if "$CLI" exec "$PG_NAME" pg_isready -U panmail -d panmail_test >/dev/null 2>&1; then
			break
		fi
		sleep 2
	done
	"$CLI" exec "$PG_NAME" pg_isready -U panmail -d panmail_test >/dev/null 2>&1 || {
		echo "postgres never became ready" >&2
		"$CLI" logs "$PG_NAME" 2>&1 | tail -20 >&2
		exit 1
	}

	for _ in $(seq 1 60); do
		if (exec 3<>/dev/tcp/127.0.0.1/"$IMAP_PORT") 2>/dev/null; then
			exec 3>&- 2>/dev/null || true
			echo "services ready via $CLI"
			echo "  postgres 127.0.0.1:${PG_PORT}  imap 127.0.0.1:${IMAP_PORT}  smtp 127.0.0.1:${SMTP_PORT}"
			return 0
		fi
		sleep 2
	done
	echo "greenmail never accepted a connection on $IMAP_PORT" >&2
	"$CLI" logs "$GM_NAME" 2>&1 | tail -20 >&2
	exit 1
}

down() {
	"$CLI" rm -f "$PG_NAME" >/dev/null 2>&1 || true
	"$CLI" rm -f "$GM_NAME" >/dev/null 2>&1 || true
}

case "${1:-}" in
	up) up ;;
	down) down ;;
	*) echo "usage: $0 {up|down}" >&2; exit 2 ;;
esac
