.PHONY: all build build-frontend build-backend generate clean test \
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
