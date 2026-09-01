#!/usr/bin/env bash
set -euo pipefail

KARTE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "${KARTE_ROOT}/frontend"
npm run build

cd "${KARTE_ROOT}"
mkdir -p build/bin
go build \
  -buildvcs=false \
  -tags "desktop,wv2runtime.download,production" \
  -ldflags "-w -s" \
  -o build/bin/karte \
  .

echo "Built ${KARTE_ROOT}/build/bin/karte"
