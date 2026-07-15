.PHONY: all build build-frontend build-backend generate clean test

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
