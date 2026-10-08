#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BUILD_DIR=/tmp/karte-receipt-ocr-poc
ITERATIONS=${ITERATIONS:-10}

mkdir -p "$BUILD_DIR" "$SCRIPT_DIR/fixtures"
export CLANG_MODULE_CACHE_PATH="$BUILD_DIR/module-cache"
export SWIFT_MODULECACHE_PATH="$BUILD_DIR/module-cache"
xcrun swiftc -O "$SCRIPT_DIR/ReceiptOCR.swift" -o "$BUILD_DIR/receipt-ocr"
xcrun swift "$SCRIPT_DIR/GenerateFixtures.swift" "$SCRIPT_DIR/fixtures"
python3 "$SCRIPT_DIR/benchmark.py" \
  --binary "$BUILD_DIR/receipt-ocr" \
  --fixtures "$SCRIPT_DIR/fixtures" \
  --iterations "$ITERATIONS" \
  --output "$SCRIPT_DIR/results.json"

echo "結果: $SCRIPT_DIR/results.json"
