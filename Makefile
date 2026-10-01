# Framework version — single source of truth for release builds.
# Bump this (or override via ldflags) for maintenance releases.
VERSION ?= 1.4.0
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS = -X github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/cli.Version=$(VERSION) \
          -X github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/cli.Commit=$(COMMIT) \
          -X github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/cli.BuildDate=$(DATE)

# Expose ldflags for CI release builds (single source of truth).
print-ldflags:
	@echo "$(LDFLAGS)"

.PHONY: build test cover benchmark benchmark-quick benchmark-saturation benchmark-real-app benchmark-surface version check-templates check-perf print-ldflags clean

# Compile the CLI locally with version metadata stamped in
build:
	go build -ldflags="$(LDFLAGS)" -o bin/fgoths cmd/fgoths/main.go

# Print the stamped version without building into bin/
version:
	@go run -ldflags="$(LDFLAGS)" ./cmd/fgoths version

# Run the full suite with the race detector.
test:
	go test -race ./...

# Coverage report (per-package + total) with HTML output. Short mode avoids
# collecting coverage from the CLI's long-lived subprocess test helpers.
cover:
	go test -short -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | awk '/^total:/ { found=1; print; if ($$NF != "100.0%") { print "coverage is below the 100% statement-coverage target"; exit 1 } } END { if (!found) { print "coverage total was not reported"; exit 1 } }'
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage.html generated"

# Full reproducible benchmark suite (comparison + percentiles + vegeta + edge cases)
benchmark:
	./benchmarks/run-benchmarks.sh

# Quick benchmark pass (shorter durations)
benchmark-quick:
	./benchmarks/run-benchmarks.sh --quick

# Repeated clean saturation runs plus a separate pprof diagnostics run.
benchmark-saturation:
	./benchmarks/run-saturation.sh

# Compare generated CRUD traffic on the FGOTHS router and stdlib ServeMux.
benchmark-real-app:
	./benchmarks/run-real-app.sh

# Supply-chain surface of a generated project vs other Go frameworks.
benchmark-surface:
	./benchmarks/run-dep-surface.sh

# Dispatch allocation regression gate (fails on alloc increase vs baseline)
check-perf:
	./benchmarks/check-perf-regression.sh

# Verify runtime templates are in sync with pkg/runtime
check-templates:
	go run ./cmd/fgoths sync-templates --check

# Cleans the test binaries
clean:
	rm -rf bin/ test-app test-micro
