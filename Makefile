.PHONY: all build build-frontend build-backend generate clean test \
	fmt check check-fmt check-backend check-frontend \
	dev dev-backend dev-frontend dev-bootstrap dev-generate dev-db dev-status \
	dev-stop dev-reset dev-check

all: build

generate:
	rtk buf generate
	rtk go generate ./...

build-frontend:
	cd web && rtk bun install && rtk bun run build

build-backend:
	rtk go build -tags builtui -o panmail ./cmd/api

build: build-frontend build-backend

test:
	rtk go test -v ./...

# --- Checks ------------------------------------------------------------------
# The gates CI enforces, runnable before a push, cheapest first so a formatting
# slip costs a second rather than a full -race run.
#
# gofmt is its own workflow step, which is why `go build && go vet && go test`
# can all pass locally while CI still turns red on the same commit. That has
# happened. `make fmt` fixes whatever `make check` reports.
#
# Not covered here: the second test pass against PostgreSQL, which needs a
# server (`scripts/ci/services.sh up` exports PANMAIL_TEST_POSTGRES for it), and
# govulncheck, which wants the network. Both still run in CI.

fmt:
	@files="$$(gofmt -l . | grep -v '^web/' || true)"; \
	if [ -n "$$files" ]; then gofmt -w $$files; echo "formatted:"; echo "$$files"; \
	else echo "already gofmt'd"; fi

check-fmt:
	@files="$$(gofmt -l . | grep -v '^web/' || true)"; \
	if [ -n "$$files" ]; then \
		echo "These files are not gofmt'd (run 'make fmt'):"; \
		echo "$$files"; \
		exit 1; \
	fi

check-backend: check-fmt
	rtk go build ./...
	rtk go vet ./...
	rtk go test -race ./...

check-frontend:
	cd web && rtk bun run lint && rtk bun run test && rtk bun run build

check: check-backend check-frontend

clean:
	rm -f panmail
	rm -rf web/dist

# --- Development -------------------------------------------------------------
# Thin wrappers over scripts/dev.sh, which is the real entry point and takes the
# options these targets do not expose (--db, --port, --single-port, ...).
# The scripts deliberately call go/bun/buf directly rather than through rtk, so
# a build error reaches you verbatim.

dev:
	./scripts/dev.sh up

dev-backend:
	./scripts/dev.sh backend

dev-frontend:
	./scripts/dev.sh frontend

dev-bootstrap:
	./scripts/dev.sh bootstrap

dev-generate:
	./scripts/dev.sh generate

dev-db:
	./scripts/dev.sh db status

dev-status:
	./scripts/dev.sh status

dev-check:
	./scripts/dev.sh preflight

dev-stop:
	./scripts/dev.sh stop

dev-reset:
	./scripts/dev.sh reset
