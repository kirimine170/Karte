#!/bin/bash
# Package script: runs wails build and bundles ASR models into karte_data_template

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIST="$PROJECT_ROOT/build/dist"
BUILD_BIN="$PROJECT_ROOT/build/bin"

echo "=========================================="
echo "Building Karte application..."
echo "=========================================="

# Clean Go build cache to ensure fresh embed
echo "Cleaning Go build cache..."
cd "$PROJECT_ROOT"
go clean -cache

# Clean and build frontend first to ensure dist/ is up to date
cd "$PROJECT_ROOT/frontend"
echo "Cleaning frontend dist directory..."
rm -rf dist
echo "Building frontend..."
if ! npm run build; then
    echo "Error: Frontend build failed"
    exit 1
fi

# Verify recording button is in dist/index.html
if ! grep -q "recordingBtn" dist/index.html; then
    echo "Warning: recordingBtn not found in dist/index.html"
    echo "This may indicate a build issue."
fi

# Run wails build
cd "$PROJECT_ROOT"
if ! command -v wails &> /dev/null; then
    echo "Error: wails command not found"
    echo "Please install Wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    exit 1
fi

wails build -platform darwin/arm64 -o karte-macos-arm64

# Verify recording button is still in dist/index.html after Wails build
echo ""
echo "Verifying recording button after Wails build..."
if ! grep -q "recordingBtn" "$PROJECT_ROOT/frontend/dist/index.html"; then
    echo "ERROR: recordingBtn not found in dist/index.html after Wails build!"
    echo "This indicates Wails may have overwritten the frontend build."
    exit 1
else
    echo "✓ recordingBtn confirmed in dist/index.html after Wails build"
fi

echo ""
echo "=========================================="
echo "Packaging ASR models into karte_data_template..."
echo "=========================================="

# Determine build output locations
APP_BUNDLE=""
TEMPLATE_DIR=""

if [ -d "$BUILD_BIN/Karte.app" ]; then
    APP_BUNDLE="$BUILD_BIN/Karte.app"
    TEMPLATE_DIR="$BUILD_BIN/karte_data_template"
    echo "Detected build output in $BUILD_BIN"
else
    LATEST_DIST=$(find "$BUILD_DIST" -maxdepth 1 -type d -name "Karte-*-macos-arm64" | sort -r | head -1)
    if [ -n "$LATEST_DIST" ] && [ -d "$LATEST_DIST/Karte.app" ]; then
        APP_BUNDLE="$LATEST_DIST/Karte.app"
        TEMPLATE_DIR="$LATEST_DIST/karte_data_template"
        echo "Detected build output in $LATEST_DIST"
    fi
fi

if [ -z "$APP_BUNDLE" ]; then
    echo "Error: Unable to locate build output (.app)"
    exit 1
fi

# Source template directory from repository
TEMPLATE_SOURCE="$PROJECT_ROOT/templates/karte_data_template"

# Ensure template directory exists
mkdir -p "$TEMPLATE_DIR"

# Copy template from repository if it exists
if [ -d "$TEMPLATE_SOURCE" ]; then
    echo "Copying template from $TEMPLATE_SOURCE to $TEMPLATE_DIR..."
    rm -rf "$TEMPLATE_DIR"
    cp -R "$TEMPLATE_SOURCE" "$TEMPLATE_DIR"
    echo "Template copied successfully"
else
    echo "Warning: Template source not found at $TEMPLATE_SOURCE"
    echo "Creating empty template directory..."
    mkdir -p "$TEMPLATE_DIR/data"
fi

ASR_SOURCE="$PROJECT_ROOT/karte_data/data/asr"
ASR_TARGET="$TEMPLATE_DIR/data/asr"

echo "Packaging ASR models into $TEMPLATE_DIR..."

# Ensure ASR target directory exists
mkdir -p "$ASR_TARGET"

# Copy ASR config and models from karte_data if it exists (overwrites template if present)
if [ -d "$ASR_SOURCE" ]; then
    echo "Copying ASR models from $ASR_SOURCE..."
    cp -R "$ASR_SOURCE"/* "$ASR_TARGET/"
    echo "ASR models packaged successfully"
elif [ ! -d "$ASR_TARGET" ] || [ ! -f "$ASR_TARGET/config.json" ]; then
    # Only create minimal config if template doesn't already have one
    echo "Warning: ASR source directory not found at $ASR_SOURCE"
    echo "Creating minimal ASR config..."
    mkdir -p "$ASR_TARGET"
    cat > "$ASR_TARGET/config.json" << 'EOF'
{
  "enabled": false,
  "sampleRate": 16000,
  "model": {
    "tokens": "sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10/tokens.txt",
    "encoder": "sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10/encoder-epoch-75-avg-11-chunk-16-left-128.int8.onnx",
    "decoder": "sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10/decoder-epoch-75-avg-11-chunk-16-left-128.onnx",
    "joiner": "sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10/joiner-epoch-75-avg-11-chunk-16-left-128.int8.onnx"
  },
  "decoding": {
    "method": "greedy_search"
  },
  "runtime": {
    "threads": 4,
    "provider": "cpu"
  }
}
EOF
    echo "Minimal ASR config created (disabled by default)"
fi

echo ""
echo "=========================================="
echo "Copying karte_data_template into .app bundle..."
echo "=========================================="

RESOURCES_DIR="$APP_BUNDLE/Contents/Resources"

if [ -d "$APP_BUNDLE" ]; then
    if [ -d "$TEMPLATE_DIR" ]; then
        echo "Copying $TEMPLATE_DIR to $RESOURCES_DIR/karte_data_template..."
        rm -rf "$RESOURCES_DIR/karte_data_template"
        cp -R "$TEMPLATE_DIR" "$RESOURCES_DIR/karte_data_template"
        echo "karte_data_template copied to .app bundle successfully"
    else
        echo "Warning: karte_data_template not found at $TEMPLATE_DIR"
    fi
else
    echo "Warning: .app bundle not found at $APP_BUNDLE"
fi

echo ""
echo "=========================================="
echo "Package script completed successfully!"
echo "Build output: $LATEST_BUILD"
echo "=========================================="

