#!/bin/bash
# build.sh - Wails v3用ビルドスクリプト（ログ表示・時間計測付き）

set -e

PLATFORM="${1:-darwin/universal}"
OUTPUT="${2:-karte-macos-universal}"

# 色付きログ
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}=========================================="
echo "Karte ビルド開始 (Wails v3)"
echo "==========================================${NC}"
echo "プラットフォーム: $PLATFORM"
echo "出力ファイル: $OUTPUT"
echo "開始時刻: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""

START_TIME=$(date +%s)

# wails3コマンドのパスを取得
WAILS3_CMD="$(go env GOPATH)/bin/wails3"
if [ ! -f "$WAILS3_CMD" ]; then
    echo -e "${YELLOW}警告: wails3コマンドが見つかりません${NC}"
    echo "  インストール中..."
    go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha.20
    WAILS3_CMD="$(go env GOPATH)/bin/wails3"
    if [ ! -f "$WAILS3_CMD" ]; then
        echo -e "${RED}エラー: wails3のインストールに失敗しました${NC}"
        exit 1
    fi
    echo -e "${GREEN}  ✓ wails3インストール完了${NC}"
fi

# ステップ1: フロントエンドビルド
echo -e "${YELLOW}[1/3] フロントエンドビルド...${NC}"
if [ -d "frontend/dist" ] && [ "frontend/dist" -nt "frontend/package.json" ] && [ "frontend/dist" -nt "frontend/src" ]; then
    echo -e "${GREEN}  ✓ フロントエンドは最新のためスキップ${NC}"
else
    echo "  フロントエンドをビルド中..."
    cd frontend
    npm run build 2>&1 | sed 's/^/    /'
    cd ..
    echo -e "${GREEN}  ✓ フロントエンドビルド完了${NC}"
fi
echo ""

# ステップ2: Go依存関係
echo -e "${YELLOW}[2/3] Go依存関係の確認...${NC}"
go mod download 2>&1 | grep -E "(go:|downloading|extracting)" | sed 's/^/    /' || true
echo -e "${GREEN}  ✓ 依存関係確認完了${NC}"
echo ""

# ステップ3: Wailsビルド
echo -e "${YELLOW}[3/3] Wailsアプリケーションビルド...${NC}"
echo "  これには時間がかかります（数分）..."
echo ""

# v3はTaskfileが必要だが、v2のCLIでもビルドできる可能性がある
# まずv2のCLIを試す
WAILS2_CMD="wails"
if command -v "$WAILS2_CMD" &> /dev/null; then
    echo "  v2 CLIを使用してビルドします..."
    echo "  プラットフォーム: $PLATFORM"
    echo ""
    
    # ビルド実行（ログを表示しながら）
    "$WAILS2_CMD" build -platform "$PLATFORM" -o "$OUTPUT" 2>&1 | while IFS= read -r line; do
        echo "    $line"
    done
    
    BUILD_EXIT_CODE=${PIPESTATUS[0]}
    
    if [ $BUILD_EXIT_CODE -eq 0 ]; then
        echo -e "${GREEN}  ✓ ビルド成功${NC}"
    else
        echo -e "${RED}  ✗ ビルド失敗 (終了コード: $BUILD_EXIT_CODE)${NC}"
        exit $BUILD_EXIT_CODE
    fi
else
    echo -e "${RED}  エラー: wailsコマンドが見つかりません${NC}"
    echo "    v2 CLIをインストール: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    exit 1
fi

# ビルド結果の確認
if [ -f "build/bin/karte" ] || [ -f "build/bin/karte.app" ] || [ -f "$OUTPUT" ] || [ -d "build/bin" ]; then
    echo -e "${GREEN}  ✓ ビルド出力を確認${NC}"
else
    echo -e "${YELLOW}  警告: ビルド出力が見つかりません（正常に完了した可能性もあります）${NC}"
fi

END_TIME=$(date +%s)
ELAPSED=$((END_TIME - START_TIME))
MIN=$((ELAPSED / 60))
SEC=$((ELAPSED % 60))

echo ""
echo -e "${BLUE}=========================================="
echo -e "${GREEN}ビルド完了${NC}"
echo -e "${BLUE}==========================================${NC}"
echo "終了時刻: $(date '+%Y-%m-%d %H:%M:%S')"
echo "ビルド時間: ${MIN}分${SEC}秒"
if [ -d "build/bin" ]; then
    echo "出力ディレクトリ: build/bin/"
    ls -lh build/bin/ 2>/dev/null | tail -n +2 | sed 's/^/  /' || true
fi
echo ""

