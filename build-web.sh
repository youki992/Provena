#!/bin/bash
# Build the Web console build of Provena.
#
# The console is opt-in: a plain `go build ./cmd/provena` produces the CLI-only
# binary. This script adds the console back — the serve command and the whole
# HTTP route table — which is what run.sh and the deploy/ packages start.
#
# Usage: ./build-web.sh [output-name]

set -euo pipefail
cd "$(dirname "$0")"

OUT="${1:-provena-web}"
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) OUT="${OUT%.exe}.exe" ;;
  *) OUT="${OUT%.exe}" ;;
esac

echo "building web console binary -> $OUT"
go build -tags webconsole -o "$OUT" ./cmd/provena

echo
echo "done. this build has the web console:"
"./$OUT" version
