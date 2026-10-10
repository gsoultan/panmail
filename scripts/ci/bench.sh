#!/usr/bin/env bash
# Runs the hot-path benchmarks on two trees and fails on a real regression.
#
# CI's test job only compile-checks benchmarks (`go test ./...` skips them), so
# the paths in PRD §5.1 were guarded by ordinary tests alone: an allocation
# budget or a round-trip count catches the regression someone already had once,
# and nothing else. This compares the timings themselves, base against head.
#
#   scripts/ci/bench.sh run <base-tree> <head-tree> <out-dir>
#       Builds each tree's test binaries for the packages below and runs them
#       interleaved, base then head, BENCH_COUNT times. Interleaving matters on
#       a shared runner: a noisy minute then lands on both sides rather than
#       on one, which is what would turn it into a false regression.
#       Writes <out-dir>/base.txt and <out-dir>/head.txt.
#
#   scripts/ci/bench.sh compare <out-dir>
#       Runs benchstat over them, writes <out-dir>/benchstat.txt and .csv,
#       and exits non-zero if any benchmark got slower or allocated more by
#       more than BENCH_THRESHOLD percent with p below BENCH_ALPHA.
#
# A package or benchmark missing from the base tree (one added by the change
# under test) is skipped there; benchstat compares only what both sides ran.
set -euo pipefail

# Each covers one path in PRD §5.1. Add the package here when a new hot path
# gets a benchmark.
PACKAGES=(
	./internal/email/usecases                   # admission, suppression, link rewriting
	./internal/auth/usecases                    # API-key verification
	./internal/event/usecases                   # RecordEvent without a body read
	./internal/emailfilter/stores/postgres      # compiled filter rule cache
	./internal/event/repositories/stores/pebble # ranged GetMetrics
)

BENCH="${BENCH:-.}"
BENCH_COUNT="${BENCH_COUNT:-10}"
BENCH_TIME="${BENCH_TIME:-200ms}"
BENCH_THRESHOLD="${BENCH_THRESHOLD:-25}"
BENCH_ALPHA="${BENCH_ALPHA:-0.05}"
BENCHSTAT="${BENCHSTAT:-benchstat}"

build() { # tree bindir
	local tree=$1 bin=$2 pkg name
	mkdir -p "$bin"
	for pkg in "${PACKAGES[@]}"; do
		name=$(echo "${pkg#./}" | tr '/' '_')
		if [ ! -d "$tree/$pkg" ]; then
			echo "skip $pkg: not in $tree" >&2
			continue
		fi
		(cd "$tree" && go test -c -o "$bin/$name.test" "$pkg")
	done
}

run_once() { # tree bindir out
	local tree=$1 bin=$2 out=$3 pkg name
	for pkg in "${PACKAGES[@]}"; do
		name=$(echo "${pkg#./}" | tr '/' '_')
		[ -x "$bin/$name.test" ] || continue
		# From the package directory, as `go test` would, for any testdata.
		(cd "$tree/$pkg" && "$bin/$name.test" -test.run '^$' -test.bench "$BENCH" \
			-test.benchmem -test.benchtime "$BENCH_TIME" -test.count 1) >>"$out"
	done
}

cmd_run() {
	local base head out
	base=$(cd "$1" && pwd)
	head=$(cd "$2" && pwd)
	mkdir -p "$3"
	out=$(cd "$3" && pwd)
	: >"$out/base.txt"
	: >"$out/head.txt"

	build "$base" "$out/bin/base"
	build "$head" "$out/bin/head"
	for i in $(seq "$BENCH_COUNT"); do
		echo "round $i of $BENCH_COUNT" >&2
		run_once "$base" "$out/bin/base" "$out/base.txt"
		run_once "$head" "$out/bin/head" "$out/head.txt"
	done
	rm -rf "$out/bin"
}

cmd_compare() {
	local out=$1
	"$BENCHSTAT" -alpha "$BENCH_ALPHA" base="$out/base.txt" head="$out/head.txt" | tee "$out/benchstat.txt"
	"$BENCHSTAT" -alpha "$BENCH_ALPHA" -format csv base="$out/base.txt" head="$out/head.txt" >"$out/benchstat.csv" 2>/dev/null

	# The CSV has one table per unit, each starting with a header row naming
	# it ("...,sec/op,CI,...") and then a row per benchmark whose second to
	# last column is the change ("+31.20%", or "~" when not significant) and
	# whose last is "p=0.000 n=10". Only time and allocations gate: bytes
	# move with allocation sizes the runtime chooses, and custom metrics such
	# as db-round-trips/op are counts that the ordinary tests already pin.
	python3 - "$out/benchstat.csv" "$BENCH_THRESHOLD" "$BENCH_ALPHA" <<'PY'
import csv, re, sys

path, threshold, alpha = sys.argv[1], float(sys.argv[2]), float(sys.argv[3])
gated = {"sec/op", "allocs/op"}
unit, failures = None, []
with open(path, newline="") as f:
    for row in csv.reader(f):
        if len(row) < 3:
            continue
        if row[0] == "" or row[0].startswith("goos") or row[0].startswith("pkg"):
            # A table header names its unit in the second column.
            if len(row) > 1 and row[1].strip():
                unit = row[1].strip()
            continue
        if unit not in gated:
            continue
        change, p = row[-2].strip(), row[-1].strip()
        m = re.fullmatch(r"([+-]?\d+(?:\.\d+)?)%", change)
        pm = re.match(r"p=([0-9.]+)", p)
        if not m or not pm:
            continue
        delta, pval = float(m.group(1)), float(pm.group(1))
        if delta > threshold and pval < alpha:
            failures.append(f"{row[0]}: {unit} {change} ({p})")

if failures:
    print(f"\nRegressed by more than {threshold:g}% with p < {alpha:g}:")
    for line in failures:
        print("  " + line)
    sys.exit(1)
print(f"\nNo benchmark regressed by more than {threshold:g}% with p < {alpha:g}.")
PY
}

case "${1:-}" in
run)
	[ $# -eq 4 ] || { echo "usage: $0 run <base-tree> <head-tree> <out-dir>" >&2; exit 2; }
	cmd_run "$2" "$3" "$4"
	;;
compare)
	[ $# -eq 2 ] || { echo "usage: $0 compare <out-dir>" >&2; exit 2; }
	cmd_compare "$2"
	;;
*)
	echo "usage: $0 run <base-tree> <head-tree> <out-dir> | compare <out-dir>" >&2
	exit 2
	;;
esac
