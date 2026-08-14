# The image matches what .goreleaser.yaml builds, deliberately: CGO_ENABLED=0,
# -tags=builtui so the UI is embedded rather than served from disk, and the
# same Version ldflag. An image that differs from the released binary is a
# second artifact to reason about, and the one people actually run.

FROM oven/bun:1 AS ui
WORKDIR /src/web
# Lockfile first, so a change to application code does not reinstall.
COPY web/package.json web/bun.lock* ./
RUN bun install --frozen-lockfile
COPY web/ ./
ARG VERSION=development
ENV VITE_APP_VERSION=${VERSION}
RUN bun run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# web/dist is what the builtui tag embeds; without it the tag compiles and the
# binary serves nothing, which is the failure release-build exists to catch.
COPY --from=ui /src/web/dist ./web/dist
ARG VERSION=development

# -p=2 caps how many packages compile at once.
#
# modernc.org/sqlite is a pure-Go translation of SQLite and one of its packages
# needs on the order of a gigabyte to compile on its own. Go defaults this to
# the CPU count, so a builder with plenty of cores and modest memory — 7 CPUs
# and 2 GB is an ordinary CI runner, and is what this failed on first — starts
# seven of those and the kernel kills the compiler:
#
#   modernc.org/sqlite/lib: compile: signal: killed
#
# Capping it trades build time for a build that finishes on hardware people
# actually have. Raise it if your builder has memory to spare.
RUN CGO_ENABLED=0 go build -p=2 -tags=builtui \
    -ldflags "-s -w -X main.Version=${VERSION}" \
    -o /out/panmail ./cmd/api

FROM alpine:3.20
# ca-certificates because every provider is reached over TLS, and tzdata
# because retry schedules and event timestamps are otherwise all UTC-or-guess.
RUN apk add --no-cache ca-certificates tzdata

# Unprivileged. Nothing here needs root: the ports are above 1024 and the only
# writes are to the data directory.
RUN adduser -D -u 10001 -h /var/lib/panmail panmail
WORKDIR /var/lib/panmail

COPY --from=build /out/panmail /usr/local/bin/panmail

# The Pebble stores. Each one takes an exclusive directory lock, so this must be
# a per-instance volume — two replicas sharing it will not both start. See
# docs/scaling.md.
RUN mkdir -p /var/lib/panmail/data && chown -R panmail:panmail /var/lib/panmail
VOLUME /var/lib/panmail/data

USER panmail
EXPOSE 8080

# Metrics stay on loopback. The backup and profiling endpoints are served
# beside them and are withheld when this is not a loopback address, because
# neither takes credentials — see docs/backup.md. To scrape from another host,
# use a sidecar rather than widening this.
ENV PORT=8080
ENTRYPOINT ["/usr/local/bin/panmail"]
CMD ["--built-ui", \
     "--log-dir", "/var/lib/panmail/data/logs.db", \
     "--event-dir", "/var/lib/panmail/data/events.db", \
     "--inbound-dir", "/var/lib/panmail/data/inbound.db", \
     "--metrics-addr", "127.0.0.1:9090"]
