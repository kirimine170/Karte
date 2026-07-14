# Karte ビルドガイド

このプロジェクトは、Goで実装されたビルドマトリックスタイツール（`cmd/buildmatrix/main.go`）を使用してビルドします。

## ビルドマトリックスタイツール

`buildmatrix` は、`build/targets.json` に定義された複数のプラットフォームを一度にビルドできるGoプログラムです。

### ビルドマトリックスタイツールのビルド

```bash
go build -o buildmatrix ./cmd/buildmatrix
```

> **Windowsにおける補足**
> 出力ファイル名をbuildmatrix.exeにしないと
> 実行時にどのプログラムを使用して開くか聞かれ、実行ファイルのしての実行が妨げられます。

### 基本的な使用方法

```bash
# すべてのターゲットをビルド
./buildmatrix --all

# 特定のターゲットのみビルド
./buildmatrix --targets darwin
./buildmatrix --targets darwin-arm64
./buildmatrix --targets darwin,linux

# 依存関係を更新してからビルド
./buildmatrix --targets darwin --prep

# クリーンビルド（既存の成果物を削除）
./buildmatrix --targets darwin --clean

# クリーンビルド + 依存関係更新
./buildmatrix --targets darwin --clean --prep
```

### 利用可能なターゲット

`build/targets.json` で定義されています：

- `darwin` - macOS (Universal Binary) - Intel Mac / Apple Silicon の両方で動作（Mac向け配布の標準）
- `darwin-arm64` - macOS (Apple Silicon専用) - M1/M2/M3など
- `darwin-amd64` - macOS (Intel Mac専用)
- `windows` - Windows
- `linux` - Linux

### Apple Silicon (M1/M2/M3) の場合

Apple Silicon Macでビルドする場合、以下のいずれかを使用できます：

```bash
# Universal Binary（推奨: すべてのMacで動作）
./buildmatrix --targets darwin

# Apple Silicon専用（ファイルサイズが小さく、パフォーマンスが良い）
./buildmatrix --targets darwin-arm64
```

Universal Binaryはファイルサイズが大きくなりますが、Intel MacとApple Siliconの両方で動作します。Apple Silicon 上で `darwin-arm64` のみを配布すると Intel Mac（例: MacBook Pro/Intel Mac）では起動できないため、Mac向けに配布する成果物は `darwin` を使用してください。

#### macOS のアーキテクチャ依存について

現状の macOS 実装には Apple Silicon 専用の音声入力 / ASR 依存があります。`internal/audio/recorder.go` と `internal/asr/*` の実装は `darwin && arm64 && !universal` のときだけ `github.com/gordonklaus/portaudio` と `github.com/k2-fsa/sherpa-onnx-go-macos` を使い、`universal` または `amd64` ではスタブ実装に切り替わります。

そのため、録音 / ASR まで含めて正常に動作させる Apple Silicon 版と、Intel Mac で起動できる Intel 版は分けてビルド・配布します。`darwin-arm64` は Apple Silicon 専用の PortAudio / sherpa-onnx 依存を含むビルド、`darwin-amd64` は Intel Mac 用のスタブ実装ビルドです。`darwin` Universal Binary は互換性確認用ターゲットとして残していますが、`-tags universal` により録音 / ASR は無効化されるため、CI の配布成果物はアーキテクチャ別の `darwin-arm64` / `darwin-amd64` を優先します。


## CI と配布用成果物

`.github/workflows/ci.yml` の `Desktop Build` は、PRでは Apple Silicon macOS / Intel macOS / Linux amd64 / Windows amd64 のビルド確認を行います。`main` への push で同じビルドがすべて成功すると、成果物を ZIP 化して GitHub Releases の `latest-main-successful-build` にアップロードします。

- `Karte-macOS-apple-silicon.zip` - Apple Silicon macOS 版（録音 / ASR 依存を含む）
- `Karte-macOS-intel.zip` - Intel macOS 版（録音 / ASR はスタブ）
- `Karte-linux-amd64.zip` - Linux amd64 版
- `Karte-windows-amd64.zip` - Windows amd64 版

このリリースは「最後に成功した main ブランチのビルド」を指すローリングリリースです。新しい main ビルドが成功するたびに同じタグと添付ファイルが更新されます。

## ビルド成果物

ビルド成果物は、`build/targets.json` で指定された `artifactDir` に出力されます：

- `darwin` → `dist/darwin/`
- `darwin-arm64` → `dist/darwin-arm64/`
- `darwin-amd64` → `dist/darwin-amd64/`
- `windows` → `dist/windows/`
- `linux` → `dist/linux/`

## 方法2: Wailsコマンドを直接使用

最もシンプルな方法ですが、手動で依存関係の管理が必要です。

```bash
# フロントエンドのビルド
cd frontend
npm install
npm run build
cd ..

# Wailsアプリケーションのビルド
wails build -platform darwin/universal  # Universal Binary
wails build -platform darwin/arm64       # Apple Silicon専用
wails build -platform darwin/amd64       # Intel Mac専用
```

成果物は `build/bin/` に出力されます。

## トラブルシューティング

### package-lock.jsonのプラットフォーム間の不一致

MacとWindowsでpackage-lock.jsonの内容が異なる場合、以下の手順で統一できます：

```bash
cd frontend

# 既存のpackage-lock.jsonを削除
rm package-lock.json

# node_modulesも削除
rm -rf node_modules

# クリーンな状態から再インストール
npm install

# package-lock.jsonをコミット
git add package-lock.json
git commit -m "Update package-lock.json for cross-platform compatibility"
```

**注意**: `buildmatrix`ツールは自動的に`npm ci`を使用してpackage-lock.jsonを厳密に守ります。これにより、プラットフォーム間の一貫性が保たれます。

### フロントエンドのビルドエラー

```bash
cd frontend
rm -rf node_modules package-lock.json
npm install
npm run build
```

### Go依存関係のエラー

```bash
go mod tidy
go mod download
```

### Wailsが見つからない

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

### ビルドマトリックスタイツールのヘルプ

```bash
./buildmatrix --help
```

## アーキテクチャの選択

Mac向けビルドでは、以下のアーキテクチャを選択できます：

- **universal** (`darwin`): Intel MacとApple Siliconの両方で動作するUniversal Binary（推奨）
- **arm64** (`darwin-arm64`): Apple Silicon（M1/M2/M3など）専用
- **amd64** (`darwin-amd64`): Intel Mac専用

Universal Binaryはファイルサイズが大きくなりますが、すべてのMacで動作します。
