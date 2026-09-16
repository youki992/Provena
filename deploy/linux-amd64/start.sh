#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

# Archives created on Windows do not preserve Linux execute bits.
chmod +x provena
find tools/bin -type f \( -name amass -o -name ffuf -o -name gau -o -name subfinder -o -name dddd -o -name katana \) -exec chmod +x {} +

exec ./provena serve -config ./config.yaml --http
