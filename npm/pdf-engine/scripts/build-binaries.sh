#!/bin/sh
# Cross-compiles the Go engine into bin/<node-platform>-<node-arch>/.
# Pure Go (PDFium runs as embedded wasm), so CGO_ENABLED=0 cross-builds work from any host.
#   sh scripts/build-binaries.sh         # all targets
#   sh scripts/build-binaries.sh local   # current machine only
# ponytail: one package carries every binary (~15 MB each); split into per-platform
# optionalDependencies (esbuild-style) once install size matters.
set -eu
cd "$(dirname "$0")/.."
ROOT=../..

if [ "${1:-}" = "local" ]; then
  TARGETS="$(go env GOOS)/$(go env GOARCH)"
else
  TARGETS="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64"
fi

for t in $TARGETS; do
  goos=${t%/*}; goarch=${t#*/}
  platform=$goos; [ "$goos" = windows ] && platform=win32
  arch=$goarch; [ "$goarch" = amd64 ] && arch=x64
  exe=pdf-engine; [ "$goos" = windows ] && exe=pdf-engine.exe
  out="bin/$platform-$arch/$exe"
  echo "building $out"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags="-s -w" -o "npm/pdf-engine/$out" ./cmd/pdf-engine)
done
