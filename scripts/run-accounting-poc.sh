#!/bin/bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
binary="$repo_dir/build/bin/KarteAccountingPoC.app/Contents/MacOS/karte-accounting"
if [ ! -x "$binary" ]; then
    echo '先に ./scripts/build-accounting-poc.sh を実行してください．' >&2
    exit 1
fi

export KARTE_DATA_DIR="${KARTE_DATA_DIR:-$repo_dir/karte_data}"
# Reuse the user's existing bundled Receipt Lab runtime when available．
receipt_runtime="$repo_dir/../Accounting_KE/build/bin/KarteReceiptPoC.app/Contents/Resources/karte-runtime"
if [ -z "${KARTE_ACCOUNTING_OCR_RUNTIME:-}" ] && [ -x "$receipt_runtime/python/bin/python3.12" ]; then
    export KARTE_ACCOUNTING_OCR_RUNTIME="$receipt_runtime"
fi
cd "$repo_dir"
exec "$binary"
