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

# Run wails build
cd "$PROJECT_ROOT"
if ! command -v wails &> /dev/null; then
    echo "Error: wails command not found"
    echo "Please install Wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    exit 1
fi

wails build -platform darwin/universal -o karte-macos-universal

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
    LATEST_DIST=$(find "$BUILD_DIST" -maxdepth 1 -type d -name "Karte-*-macos-universal" | sort -r | head -1)
    if [ -n "$LATEST_DIST" ] && [ -d "$LATEST_DIST/Karte.app" ]; then
        APP_BUNDLE="$LATEST_DIST/Karte.app"
        TEMPLATE_DIR="$LATEST_DIST/karte_data_template"
        echo "Detected build output in $LATEST_DIST"
    fi
fi

if [ -z "$APP_BUNDLE" ] || [ -z "$TEMPLATE_DIR" ]; then
    echo "Error: Unable to locate build output (.app) or template directory"
    exit 1
fi

# Ensure template directory exists
mkdir -p "$TEMPLATE_DIR/data"

ASR_SOURCE="$PROJECT_ROOT/karte_data/data/asr"
ASR_TARGET="$TEMPLATE_DIR/data/asr"

echo "Packaging ASR models into $TEMPLATE_DIR..."

# Clean previous ASR target and recreate
rm -rf "$ASR_TARGET"
mkdir -p "$ASR_TARGET"

# Copy ASR config and models
if [ -d "$ASR_SOURCE" ]; then
    echo "Copying ASR models from $ASR_SOURCE..."
    cp -R "$ASR_SOURCE"/* "$ASR_TARGET/"
    echo "ASR models packaged successfully"
else
    echo "Warning: ASR source directory not found at $ASR_SOURCE"
    echo "Creating minimal ASR config..."
    mkdir -p "$ASR_TARGET"
    cat > "$ASR_TARGET/config.json" << 'EOF'
{
  "enabled": false,
  "sampleRate": 16000,
  "model": {
    "tokens": "sherpa-onnx-zipformer-ja-reazonspeech-2024-08-01/tokens.txt",
    "encoder": "sherpa-onnx-zipformer-ja-reazonspeech-2024-08-01/encoder-epoch-99-avg-1.onnx",
    "decoder": "sherpa-onnx-zipformer-ja-reazonspeech-2024-08-01/decoder-epoch-99-avg-1.onnx",
    "joiner": "sherpa-onnx-zipformer-ja-reazonspeech-2024-08-01/joiner-epoch-99-avg-1.onnx"
  },
  "decoding": {
    "method": "greedy_search",
    "maxActivePaths": 4
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

