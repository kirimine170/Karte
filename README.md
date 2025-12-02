# Karte v1.0

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

## インストール

### 前提条件

- Go 1.23以上
- Node.js 16以上
- Wails CLI v2

### Wails CLIのインストール

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

### 埋め込みフォントのインストール(For Windows)

埋め込みフォントとして`internal/pdf/fonts/NotoSansJP-Regular.ttf`を配置する。

### アプリケーションのビルド

```bash
cd karte-desktop
go mod tidy
wails build
```

## 使い方

### 開発モード

開発中は以下のコマンドで開発モードを起動できます：

```bash
cd karte-desktop
wails dev
```

### 本番ビルド

#### macOS
```bash
wails build -platform darwin/universal
```

#### Windows
```bash
wails build -platform windows/amd64
```

#### Linux
```bash
wails build -platform linux/amd64
```

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

## プロジェクト構成

```
karte-desktop/
├── app.go                 # Wailsアプリケーションのメインロジック
├── main.go               # アプリケーションエントリーポイント
├── frontend/             # フロントエンド（HTML/CSS/JavaScript）
│   ├── index.html       # メインUI
│   └── src/main.js      # JavaScriptロジック
├── internal/            # 内部パッケージ
│   ├── site/           # Markdownレンダリング
│   └── sync/           # ファイル同期（将来のgit統合用）
├── build/              # ビルド出力
└── wails.json         # Wails設定ファイル
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

