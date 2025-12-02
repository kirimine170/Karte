# Karte v1.0(未リリース)

Markdownを正本に、CSVを@importで取り込み、ライブプレビューできるデスクトップアプリケーション。

## 概要

Karteは、Wailsフレームワークを使用して開発されたクロスプラットフォームのMarkdownエディターです。デスクトップアプリケーションとして、快適なMarkdown編集環境を提供します。

## 機能

- **ライブプレビュー**: Markdownの編集と同時にリアルタイムでプレビューを表示
- **CSVインポート**: `@import`ディレクティブを使用してCSVファイルをMarkdownに取り込み
- **テーマ対応**: Light、Dark、High Contrastの3つのテーマをサポート
- **ファイル管理**: content/ディレクトリ内のMarkdownファイルをサイドバーで管理
- **ネイティブUI**: 各プラットフォームのネイティブメニューとダイアログ（将来実装予定）
- **クロスプラットフォーム**: Windows、macOS、Linuxで動作

## クイックスタート

```bash
git clone https://github.com/kirimine170/Karte.git
cd Karte
go mod download
npm install --prefix frontend
wails dev
```

Wails CLI がまだ導入されていない場合は `go install github.com/wailsapp/wails/v2/cmd/wails@latest` を実行してください。

## 開発環境

### 対応プラットフォーム
検証進捗状況
- macOS
  - [x] Apple Silicon
  - [ ] Intel
- Windows
  - [x] Windows 11
  - [ ] Windows 10
- Linux
  - [ ] Ubuntu 22.04 LTS
  - [ ] その他Linuxは要検証

### 必須ツール

| 種別          | バージョン / 備考               |
| ------------- | ------------------------------- |
| Go            | 1.24 以上（`go.mod`に合わせる） |
| Node.js / npm | Node 18 LTS 以上（npm同梱）     |
| Wails CLI     | v2 系列                         |
| Git           | 2.x                             |

追加機能向け:

- ffmpeg（ASR/音声取り込み用、macOSなら`brew install ffmpeg`）
- PortAudio（リアルタイム録音機能が必要な場合。macOS: `brew install portaudio`）

### セットアップ手順

1. **リポジトリをクローン**
   ```bash
   git clone https://github.com/kirimine170/Karte.git
   cd Karte
   ```
2. **Wails CLIをインストール**
   ```bash
   go install github.com/wailsapp/wails/v2/cmd/wails@latest
   ```
3. **環境チェック（推奨）**
   ```bash
   wails doctor
   ```
   Go / Node / npm / Wails CLI の組み合わせに問題がないかをここで確認します。
4. **バックエンド依存を取得**
   ```bash
   go mod download
   ```
5. **フロントエンド依存をインストール**
   ```bash
   cd frontend
   npm install
   cd ..
   ```
6. **ASRを使う場合の追加準備（任意）**
   - `ffmpeg`がPATHで利用可能であることを確認（`ffmpeg -version`）
   - PortAudioをOS標準手段で導入
7. **開発モードを起動**
   ```bash
   wails dev
   ```
   バックエンドとVite開発サーバ（フロントエンド）が同時起動し、ホットリロードできます。

### 本番ビルド
### 埋め込みフォントのインストール(For Windows)

埋め込みフォントとして`internal/pdf/fonts/NotoSansJP-Regular.ttf`を配置する。

### アプリケーションのビルド

マルチプラットフォーム用に`cmd/buildmatrix`を用意しています。`build/targets.json`に定義されたターゲット（デフォルト: Windows/macOS/Linux）をまとめて、あるいは個別にビルドできます。

```bash
wails build
```

ターゲットを限定する場合は `-platform` フラグを指定します（例: `wails build -platform darwin/universal`）。ビルド成果物は `build/bin` 以下に生成されます。
# 依存関係も整える場合
go run ./cmd/buildmatrix --all --prep

# 特定ターゲットのみ
go run ./cmd/buildmatrix --targets windows
```

主なオプション:

- `--targets windows,linux` … `build/targets.json`内の名前で絞り込み
- `--all` … 全ターゲットを順番にビルド
- `--prep` … `go mod tidy`と`npm install`をビルド前に実行
- `--clean` … 各ターゲットの出力ディレクトリ（`dist/<name>`）を削除してから再生成

`build/targets.json`を編集すると、新しいターゲットの追加や環境変数（`GOOS`/`GOARCH`等）、出力先ディレクトリ、追加の`wails build`フラグを柔軟に設定できます。

## 使い方

### 開発モード

開発中は以下のコマンドで開発モードを起動できます：

```bash
wails dev
```

### 本番ビルド

本番向けは上記の`buildmatrix`コマンドを使用してください。ビルド結果はターゲットごとに`dist/<target name>`配下へ整理され、`build/bin`は毎回クリーンアップされるため、異なるOS成果物が混在しません。

### アプリケーションの使用

1. **プロジェクトの初期化**
   - アプリケーションを起動すると、現在のディレクトリがプロジェクトルートとして使用されます
   - `content/`ディレクトリにMarkdownファイルを配置してください

2. **ファイルの編集**
   - サイドバーから編集したいMarkdownファイルを選択
   - 左側のエディターでMarkdownを編集
   - 右側にリアルタイムでプレビューが表示されます

3. **保存**
   - `Ctrl/Cmd+S`でファイルを保存
   - 保存後、自動的にサイトがビルドされます

4. **テーマの変更**
   - ツールバーの「Theme」セレクターからテーマを選択
   - Light、Dark、High Contrastが利用可能

5. **音声メモの取り込み**
   - アプリ画面に WAV / MP3 / M4A をドラッグ＆ドロップすると `karte_data/data/audio/` に自動コピー
   - 取り込み状況は右上ステータスに表示されます

6. **文字起こし（ASR）**
   - `karte_data/data/asr/config.json` を有効化してモデルパスを記入すると、取り込み直後に自動で文字起こし
   - 生成された Markdown は `karte_data/content/transcripts/` 以下に保存され、音声ファイルへのリンクを含みます

### ASR 設定手順

1. **モデルファイルの配置**
   - Encoder/Decoder/Joiner 形式の Transducer モデル、または Zipformer CTC モデルを `karte_data/data/asr/` などに配置

2. **設定ファイルの編集**
   - `karte_data/data/asr/config.json` に以下を設定
     - `enabled`: `true`
     - `model.tokens` および各モデルファイルパス（相対パス可）
     - `sampleRate` は 16000 を推奨

3. **依存コマンド**
   - ffmpeg が PATH に必要です（例: `brew install ffmpeg`）

4. **実行フロー**
   - 音声ファイルをドロップすると `audio-imported` イベント→ASR開始
   - 文字起こし完了時に `audio-transcribed` イベント経由で通知され、自動的に Markdown が開きます

## プロジェクト構成

```
Karte/
├── app.go                 # Wailsアプリケーションのメインロジック
├── main.go                # アプリケーションエントリーポイント
├── frontend/              # フロントエンド（Vite + Vanilla JS）
│   ├── index.html         # メインUI
│   ├── src/main.js        # UIロジック
│   └── graph-d3.js        # D3描画
├── internal/              # 内部パッケージ
│   ├── site/              # Markdownレンダリング
│   └── sync/              # ファイル同期（将来のgit統合用）
├── build/                 # ビルド出力
└── wails.json             # Wails設定ファイル
```

## ディレクトリ構造

- `content/` … Markdownファイル（文書の正本）
- `data/` … CSVファイル等のデータ
- `themes/default/` … レイアウトや共通CSS
- `public/` … ビルドされたHTMLファイル
- `.mdsys/` … システム領域（index、drafts等）

## キーボードショートカット

- `Ctrl/Cmd+S`: ファイル保存
- `Ctrl/Cmd+N`: 新規ファイル（将来実装予定）
- `Ctrl/Cmd+O`: ファイルを開く（将来実装予定）
- `Ctrl/Cmd+T`: テーマ切り替え（将来実装予定）
- `Ctrl/Cmd+P`: プレビュー切り替え（将来実装予定）

## 将来の予定

- **ネイティブメニュー**: 各プラットフォームのネイティブメニューとダイアログの実装
- **Git統合**: ファイル共有機能をgitベースで実装
- **検索API**: ファイル名検索→@import挿入機能
- **差分ビルド**: 依存グラフによる効率的なビルド
- **Marp連携**: 外部marp-cliとの連携
- **CSVフィルタ・集計**: より高度なCSV処理機能
- **ACLメタデータ**: アクセス制御の統合管理

## 開発者向け情報

### アーキテクチャ

- **バックエンド**: Go + Wails v2
- **フロントエンド**: Vanilla JavaScript + HTML/CSS
- **Markdown処理**: Goldmark
- **ビルドシステム**: Wails CLI

### カスタマイズ

- テーマの追加: `frontend/index.html`のCSS変数を編集
- 機能の追加: `app.go`にWailsバインディングメソッドを追加
- UIの変更: `frontend/index.html`と`frontend/src/main.js`を編集

## ライセンス

Copyright © 2024, kirimine170

## サポート

問題や機能要望がある場合は、GitHubのIssuesでお知らせください。

