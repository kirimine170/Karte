#!/bin/bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
if [ "$(uname -s)" != Darwin ]; then
    echo 'This verification bundle is for macOS．' >&2
    exit 1
fi
export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/karte-accounting-go-cache}"

# Generate normal Karte bindings on a fresh checkout．Accounting calls also use the native bridge directly．
if [ ! -f frontend/wailsjs/wailsjs/go/main/App.js ]; then
    wails generate module -tags universal -nocolour
fi
npm --prefix frontend run build

output_bundle="$repo_dir/build/bin/KarteAccountingPoC.app"
staging_dir="$(mktemp -d /private/tmp/karte-accounting-build.XXXXXX)"
trap 'rm -rf "$staging_dir"' EXIT
bundle="$staging_dir/KarteAccountingPoC.app"
mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources"
# The validation bundle omits recording/ASR dependencies using Karte's existing universal tag．
CGO_LDFLAGS="${CGO_LDFLAGS:-} -framework UniformTypeIdentifiers" \
MACOSX_DEPLOYMENT_TARGET=11.0 \
go build -buildvcs=false -tags desktop,production,universal -ldflags '-w -s' \
    -o "$bundle/Contents/MacOS/karte-accounting" .
cat > "$bundle/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleName</key><string>Karte Accounting PoC</string>
<key>CFBundleDisplayName</key><string>Karte Accounting PoC</string>
<key>CFBundleExecutable</key><string>karte-accounting</string>
<key>CFBundleIdentifier</key><string>com.kirilab.karte.accounting-poc</string>
<key>CFBundleVersion</key><string>1</string>
<key>CFBundleShortVersionString</key><string>0.1.0</string>
<key>LSMinimumSystemVersion</key><string>11.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSScreenCaptureUsageDescription</key><string>画面キャプチャを保存するために使用します．</string>
</dict></plist>
PLIST
plutil -lint "$bundle/Contents/Info.plist"
# Finder/File Provider can add these to a freshly created local bundle．
xattr -dr com.apple.FinderInfo "$bundle" 2>/dev/null || true
xattr -dr com.apple.ResourceFork "$bundle" 2>/dev/null || true
codesign --force --sign - "$bundle"
xattr -dr com.apple.FinderInfo "$bundle" 2>/dev/null || true
codesign --verify --deep --strict "$bundle"
mkdir -p "$(dirname "$output_bundle")"
ditto --noextattr --norsrc "$bundle" "$output_bundle"
xattr -dr com.apple.FinderInfo "$output_bundle" 2>/dev/null || true
echo "Built: $output_bundle"
echo 'Run: ./scripts/run-accounting-poc.sh'
