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
      - name: Generate code
        run: go run ./cmd/assetmanifest{{if .IsMVC}} && go tool templ generate{{end}}

      - name: Run tests
        run: go test -race ./...

      - name: Build release binaries
        env:
          # Space-separated GOOS/GOARCH targets. Add more as needed, e.g. windows/amd64.
          PLATFORMS: linux/amd64 linux/arm64
        run: |
          mkdir -p dist
          for platform in $PLATFORMS; do
            os=${platform%/*}
            arch=${platform#*/}
            ext=""
            [ "$os" = "windows" ] && ext=".exe"
            output="dist/{{.ProjectName}}-${os}-${arch}${ext}"
            CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build \
              -ldflags="-s -w" -trimpath -o "$output" .
          done

      - name: Generate SBOM
        run: go run github.com/mfridman/sbom@v0.6.0 -o dist/sbom.spdx.json ./...

      - name: Generate checksums
        run: cd dist && sha256sum * > SHA256SUMS

      - name: Create release
        uses: softprops/action-gh-release@v2
        with:
          files: |
            dist/*
          generate_release_notes: true
