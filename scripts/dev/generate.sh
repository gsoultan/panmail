#!/usr/bin/env bash
# Regenerates the protobuf artefacts.
#
# One `buf generate` produces both halves of the contract — the Go server code
# under api/ and the TypeScript client under web/src/api (see buf.gen.yaml) — so
# changing a .proto and skipping this leaves the two sides disagreeing in ways
# that only show up at runtime.
#
# `make generate` also runs `go generate ./...`, which triggers a full bun build
# of the UI. That is a release step, not a development one, so it is behind
# --all here.
#
# Usage: generate.sh [--all]
set -euo pipefail

LOG_TAG="generate"
# shellcheck source=lib.sh
. "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
load_dev_env

ALL=0
STRICT=0
while [ $# -gt 0 ]; do
	case "$1" in
	--all) ALL=1 ;;
	# Turn the advisory lint into a hard gate.
	--strict) STRICT=1 ;;
	*) die "unknown option: $1 (expected --all or --strict)" ;;
	esac
	shift
done

have buf || die "buf is required (https://buf.build/docs/installation)"

cd "$ROOT"

# Lint is advisory. The API as it stands does not satisfy buf's DEFAULT
# category — request and response types are shared between RPCs in
# email_provider_service.proto, and LogService.StreamLogs returns a bare
# LogEntry. Those are contract decisions to make deliberately, not reasons to
# refuse to regenerate. --strict makes them blocking.
log "buf lint (advisory)"
lint_rc=0
lint_out="$(buf lint 2>&1)" || lint_rc=$?
if [ "$lint_rc" -ne 0 ]; then
	printf '%s\n' "$lint_out" >&2
	[ "$STRICT" -eq 1 ] && die "lint failed and --strict was given"
	log_warn "the findings above are pre-existing; regenerating anyway"
else
	log_ok "clean"
fi

log "buf generate"
buf generate
log_ok "regenerated api/ and web/src/api"

if [ "$ALL" -eq 1 ]; then
	GO_BIN="$(resolve_go)" || die "go not found"
	log "go generate ./... (includes a full UI build)"
	"$GO_BIN" generate ./...
	log_ok "done"
fi

# Generated Go often needs new module requirements before it will compile.
GO_BIN="$(resolve_go || true)"
if [ -n "$GO_BIN" ]; then
	log "go build ./... (checking the generated code compiles)"
	if "$GO_BIN" build ./...; then
		log_ok "compiles"
	else
		log_warn "the generated code does not compile — you may need: go mod tidy"
		exit 1
	fi
fi
