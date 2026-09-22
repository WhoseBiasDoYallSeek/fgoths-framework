name: Release

on:
  push:
    tags: ["v*"]

permissions:
  contents: write

jobs:
  release:
    name: Release {{"${{ github.ref_name }}"}}
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Run tests
        run: go test -race ./...

      - name: Build multi-platform binaries
        run: |
          mkdir -p dist
          for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
            os=${platform%/*}
            arch=${platform#*/}
            output="dist/{{.ProjectName}}-${os}-${arch}"
            CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build \
              -ldflags="-s -w" -trimpath -o "$output" ./cmd/app
          done

      - name: Generate SBOM
        run: go run github.com/mfridman/sbom@v0.6.0 -o dist/sbom.spdx.json ./...

      - name: Create release
        uses: softprops/action-gh-release@v2
        with:
          files: |
            dist/*
          generate_release_notes: true
