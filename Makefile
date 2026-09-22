# Framework version — single source of truth for release builds.
# Bump this (or override via ldflags) for maintenance releases.
VERSION ?= 1.0.0
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS = -X github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/cli.Version=$(VERSION) \
          -X github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/cli.Commit=$(COMMIT) \
          -X github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/cli.BuildDate=$(DATE)

.PHONY: build test cover benchmark version check-templates clean

# Compile the CLI locally with version metadata stamped in
build:
	go build -ldflags="$(LDFLAGS)" -o bin/fgoths cmd/fgoths/main.go

# Print the stamped version without building into bin/
version:
	@go run -ldflags="$(LDFLAGS)" ./cmd/fgoths version

# Run internal tests (race detector on)
test:
	go test -race -cover ./...

# Coverage report (per-package + total) with HTML output
cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage.html generated"

# Full reproducible benchmark suite (comparison + percentiles + vegeta + edge cases)
benchmark:
	./benchmarks/run-benchmarks.sh

# Quick benchmark pass (shorter durations)
benchmark-quick:
	./benchmarks/run-benchmarks.sh --quick

# Verify runtime templates are in sync with pkg/runtime
check-templates:
	go run ./cmd/fgoths sync-templates --check

# Cleans the test binaries
clean:
	rm -rf bin/ test-app test-micro
