#!/usr/bin/env bash
# Builds the release binaries of the fgoths CLI into dist/ with SHA256SUMS.
#
# Builds are static (CGO_ENABLED=0), path-independent (-trimpath) and stamped
# with the commit date instead of the wall clock, so the same commit and Go
# toolchain always produce byte-identical binaries that anyone can verify.
#
# Usage: VERSION=1.4.0 ./scripts/release.sh
set -euo pipefail

cd "$(dirname "$0")/.."

: "${VERSION:?set VERSION, e.g. VERSION=1.4.0}"
MODULE=github.com/WhoseBiasDoYallSeek/fgoths-framework
COMMIT=$(git rev-parse --short HEAD)
DATE=$(TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ)
PLATFORMS=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64)

LDFLAGS="-s -w -buildid= \
  -X ${MODULE}/internal/cli.Version=${VERSION} \
  -X ${MODULE}/internal/cli.Commit=${COMMIT} \
  -X ${MODULE}/internal/cli.BuildDate=${DATE}"

if [ -n "$(git status --porcelain)" ]; then
  echo "warning: working tree has uncommitted changes; binaries won't match ${COMMIT}" >&2
fi

rm -rf dist
mkdir -p dist

for platform in "${PLATFORMS[@]}"; do
  goos=${platform%/*}
  goarch=${platform#*/}
  name="fgoths_${VERSION}_${goos}_${goarch}"
  [ "$goos" = windows ] && name="${name}.exe"
  echo "building ${name}"
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch \
    go build -trimpath -ldflags="${LDFLAGS}" -o "dist/${name}" ./cmd/fgoths
done

(cd dist && shasum -a 256 fgoths_* > SHA256SUMS)
echo
echo "dist/SHA256SUMS (go $(go env GOVERSION), commit ${COMMIT}):"
cat dist/SHA256SUMS
