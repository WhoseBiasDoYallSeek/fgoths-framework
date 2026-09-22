.PHONY: run dev build test clean deps generate generate-assets docker-check docker-build docker-run

APP_IMAGE ?= $(shell basename "$$(pwd)" | tr '[:upper:]' '[:lower:]'):latest
APP_VOLUME ?= {{.ProjectName}}-data

# Main entry point (architecture-dependent)
# Both layouts compile from the module root: flat keeps main.go at the root and
# MVC's entrypoint is main.go too (views/models/handlers are packages).
MAIN:=.

# Generate optional generated assets (FlatBuffers data + Templ). This target is
# intentionally dependency-only and fast: dev HMR calls it on every .templ/.fbs
# change, so it must not run go mod tidy.
generate-assets:
	@echo "🧩 Preparing FlatBuffers data files..."
	@if ! ls schemas/*.fbs >/dev/null 2>&1; then \
		echo "   No FlatBuffers data files found; skipping."; \
	fi
{{if .IsMVC}}
	@echo "🎨 Compiling Templ templates..."
	@if command -v templ >/dev/null 2>&1; then \
		templ generate; \
	else \
		go tool templ generate; \
	fi
{{end}}
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
	@echo "🚀 Starting {{.ProjectName}}..."
	go run $(MAIN)

# Dev mode: hot reload using pure Go (fsnotify) — no external binaries
dev: generate
	@echo "🚀 Starting {{.ProjectName}} in dev mode (pure Go watcher)..."
	FGOTHS_DEV=1 go run ./cmd/dev
build: generate
	@echo "📦 Building static binary..."
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o bin/app $(MAIN)
	@echo "✅ Binary ready at bin/app ($$(ls -lh bin/app | awk '{print $$5}'))"

# Build the production scratch container image
docker-check:
	@command -v docker >/dev/null 2>&1 || { \
		echo "❌ Docker CLI not found."; \
		echo "   macOS: brew install --cask docker  # or: brew install colima docker && colima start"; \
		echo "   Linux: curl -fsSL https://get.docker.com | sh"; \
		exit 1; \
	}
	@docker info >/dev/null 2>&1 || { \
		echo "❌ Docker daemon is not running or Docker socket is unavailable."; \
		echo "   Docker Desktop: open the app and wait until it says Docker is running."; \
		echo "   Colima: colima start"; \
		echo "   Linux: sudo systemctl start docker"; \
		echo "   Then retry: make docker-build"; \
		exit 1; \
	}
	@docker buildx version >/dev/null 2>&1 || { \
		echo "❌ Docker BuildKit/buildx is required by this Dockerfile."; \
		echo "   Docker Desktop: upgrade/reinstall Docker Desktop (buildx is bundled)."; \
		echo "   Colima/Homebrew: brew install docker-buildx && mkdir -p ~/.docker/cli-plugins && ln -sf $$(brew --prefix)/opt/docker-buildx/bin/docker-buildx ~/.docker/cli-plugins/docker-buildx"; \
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
{{if .UseSQLite}}
	@docker volume create $(APP_VOLUME) >/dev/null
	docker run --rm --name {{.ProjectName}} -p 8080:8080 -v $(APP_VOLUME):/data $(APP_IMAGE)
{{else}}
	docker run --rm --name {{.ProjectName}} -p 8080:8080 $(APP_IMAGE)
{{end}}

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
