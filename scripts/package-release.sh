#!/usr/bin/env bash
#
# Build the Provena release archives.
#
#   scripts/package-release.sh [version]
#
# Produces, under dist/:
#
#   provena-<version>-windows-amd64.zip
#   provena-<version>-linux-amd64.tar.gz
#
# Each archive carries the binary plus the runtime assets Provena reads at
# startup: tools/, roles/, agents/, docs/ and config.example.yaml. No scanner
# binaries are included -- tools/bin/ is supplied by the user, as the README
# explains.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

OUT_DIR="${OUT_DIR:-$ROOT_DIR/dist}"

# The version defaults to the one compiled into the binary, so a release never
# disagrees with `provena version`.
VERSION="${1:-$(sed -n 's/^var version = "\(.*\)"$/\1/p' cmd/provena/main.go)}"
if [ -z "$VERSION" ]; then
  echo "error: cannot read the default version from cmd/provena/main.go" >&2
  exit 1
fi

ARCHIVE_ROOT_FILES=(
  README.md
  README_EN.md
  LICENSE
  SECURITY.md
  GLOBAL_SYSTEM_PROMPT.md
  config.example.yaml
  requirements.txt
)

ARCHIVE_ROOT_DIRS=(
  agents
  roles
  tools
  docs
)

make_zip() {
  local name="$1"
  if command -v zip >/dev/null 2>&1; then
    (cd "$OUT_DIR" && zip -qr "$name.zip" "$name")
    return
  fi
  local py
  py="$(command -v python3 || command -v python || true)"
  if [ -z "$py" ]; then
    echo "error: need either zip or python to build $name.zip" >&2
    exit 1
  fi
  (cd "$OUT_DIR" && "$py" -m zipfile -c "$name.zip" "$name")
}

build() {
  local goos="$1" goarch="$2" suffix="$3"
  local name="provena-$VERSION-$goos-$goarch"
  local stage="$OUT_DIR/$name"

  echo "==> $name"

  rm -rf "$stage"
  mkdir -p "$stage"

  # The project has no cgo dependencies, so both targets cross-compile from any
  # host.
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-X main.version=$VERSION" \
    -o "$stage/provena$suffix" \
    ./cmd/provena

  cp "${ARCHIVE_ROOT_FILES[@]}" "$stage/"
  for dir in "${ARCHIVE_ROOT_DIRS[@]}"; do
    cp -R "$dir" "$stage/"
  done

  # Populated by the user after unpacking; never shipped.
  rm -rf "$stage/tools/bin" "$stage/tools/vendor"
  find "$stage" -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null || true

  rm -f "$OUT_DIR/$name.zip" "$OUT_DIR/$name.tar.gz"
  if [ "$goos" = "windows" ]; then
    make_zip "$name"
  else
    tar -czf "$OUT_DIR/$name.tar.gz" -C "$OUT_DIR" "$name"
  fi
  rm -rf "$stage"

  echo "    ok"
}

mkdir -p "$OUT_DIR"
echo "Packaging Provena $VERSION into $OUT_DIR"
echo

build windows amd64 .exe
build linux amd64 ""

echo
echo "Done:"
ls -lh "$OUT_DIR"
