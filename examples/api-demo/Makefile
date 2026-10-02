.PHONY: run dev build test clean deps generate generate-assets docker-check docker-build docker-run

APP_IMAGE ?= $(shell basename "$$(pwd)" | tr '[:upper:]' '[:lower:]'):latest
APP_VOLUME ?= api-demo-data

# Main entry point (architecture-dependent)
# Both layouts compile from the module root: flat keeps main.go at the root and
# MVC's entrypoint is main.go too (views/models/handlers are packages).
MAIN:=.

# Generate optional generated assets (FlatBuffers data + Templ). This target is
# intentionally dependency-only and fast: dev HMR calls it on every .templ/.fbs
# change, so it must not run go mod tidy.
generate-assets:
	@echo "🔐 Generating static asset fingerprints..."
	go run ./cmd/assetmanifest
	@echo "🧩 Preparing FlatBuffers data files..."
	@if ! ls schemas/*.fbs >/dev/null 2>&1; then \
		echo "   No FlatBuffers data files found; skipping."; \
	fi

	@echo "✅ Assets generated!"

# Refresh module dependencies. Run by the full generate/build/docker flow, but
# intentionally skipped by the hot HMR path (`generate-assets`) after startup.
deps:
	@echo "🔧 Preparing Go module dependencies..."
	go mod tidy

# Generate optional generated assets and refresh module dependencies.
generate: deps generate-assets
	@echo "✅ Generation complete!"

# Run the app (no hot reload)
run: generate
	@echo "🚀 Starting api-demo..."
	go run $(MAIN)

# Dev mode: hot reload using pure Go (fsnotify) — no external binaries
dev: generate
	@echo "🚀 Starting api-demo in dev mode (pure Go watcher)..."
	FGOTHS_DEV=1 go run ./cmd/dev
build: generate
	@echo "📦 Building static binary..."
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o bin/app $(MAIN)
	@echo "✅ Binary ready at bin/app ($$(ls -lh bin/app | awk '{print $$5}'))"

# Build the production scratch container image
docker-check:
	@command -v docker >/dev/null 2>&1 || { \
		echo "❌ Docker CLI not found."; \
		echo "   Install: https://docs.docker.com/get-docker/"; \
		exit 1; \
	}
	@docker info >/dev/null 2>&1 || { \
		echo "❌ Docker daemon is not running or Docker socket is unavailable."; \
		echo "   Start the Docker daemon (or your container runtime)."; \
		echo "   Then retry: make docker-build"; \
		exit 1; \
	}
	@docker buildx version >/dev/null 2>&1 || { \
		echo "❌ Docker BuildKit/buildx is required by this Dockerfile."; \
		echo "   Install the docker-buildx plugin: https://docs.docker.com/build/install-buildx/"; \
		echo "   Then retry: make docker-build"; \
		exit 1; \
	}

# Build the production scratch container image
docker-build: docker-check generate
	@echo "🐳 Building scratch image $(APP_IMAGE)..."
	DOCKER_BUILDKIT=1 docker build -t $(APP_IMAGE) .
	@echo "✅ Docker image ready: $(APP_IMAGE)"

# Run the production container locally
docker-run: docker-check
	@echo "🚀 Running $(APP_IMAGE) on http://localhost:8080"

	docker run --rm --name api-demo -p 8080:8080 $(APP_IMAGE)


# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -rf bin/
	rm -rf tmp/
	rm -rf schemas/generated/
	rm -f build-errors.log
	rm -f *_templ.go
	rm -f views/*_templ.go
