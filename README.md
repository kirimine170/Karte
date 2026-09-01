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

- Go 1.25.0（`go.mod`を正本とする）
- Node.js 22.13.0（`.node-version`を正本とする）
- Wails CLI v2

### Wails CLIのインストール

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

### Windows 向けの設定

#### 埋め込みフォントの配置

埋め込みフォントとして`internal/pdf/fonts/NotoSansJP-Regular.ttf`を配置する。

PDF出力ではwkhtmltopdfを使用しません。Karte Rendererは明示設定、`PATH`、Windows標準の
インストール先の順にEdge／Chromeを探索します。通常のWindows 11では追加インストールは不要です。
詳細と診断方法は[Windowsガイド](windows.md)を参照してください。

### アプリケーションのビルド

マルチプラットフォーム用に`cmd/buildmatrix`を用意しています。`build/targets.json`に定義されたターゲット（デフォルト: Windows/macOS/Linux）をまとめて、あるいは個別にビルドできます。

```bash
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
cd karte-desktop
wails dev
```

### 本番ビルド

本番向けは上記の`buildmatrix`コマンドを使用してください。ビルド結果はターゲットごとに`dist/<target name>`配下へ整理され、`build/bin`は毎回クリーンアップされるため、異なるOS成果物が混在しません。

### アプリケーションの使用

1. **プロジェクトの初期化**
   - Windowsでは`%LOCALAPPDATA%\Karte\karte_data`、macOS／Linuxでは従来どおりアプリケーション隣接の`karte_data`を使用します
   - `KARTE_DATA_DIR`を設定すると保存先を明示的に上書きできます
   - Ephy同梱版ではアプリ隣接の`.karte-data-dir`を読み，環境変数がない手動再起動でも同じPersonal Context保管庫を再度開きます
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
   - main／manual buildとWindows releaseでは`./scripts/fetch-asr-models.sh`が固定したsherpa-onnx release assetを取得し，archiveと3つのONNX fileをSHA-256で検証して初期templateへ配置
   - ローカルで配布成果物を作る場合も，build前に同じscriptを一度実行
   - Encoder/Decoder/Joiner 形式の Transducer モデル、または Zipformer CTC モデルを `karte_data/data/asr/` などに配置

2. **設定ファイルの編集**
   - `karte_data/data/asr/config.json` に以下を設定
     - `enabled`: `true`
     - `model.tokens` および各モデルファイルパス（相対パス可）
     - `sampleRate` は 16000 を推奨

3. **依存コマンド**
   - Windowsの正式配布ZIPにはFFmpegが同梱されます。開発ビルドでは`KARTE_FFMPEG_BINARY`、
     `FFMPEG_PATH`、または`PATH`で任意のFFmpegを指定できます

4. **実行フロー**
   - 音声ファイルをドロップすると `audio-imported` イベント→ASR開始
   - 文字起こし完了時に `audio-transcribed` イベント経由で通知され、自動的に Markdown が開きます

## プロジェクト構成

```
karte-desktop/
├── app.go                 # Wailsアプリケーションのメインロジック
├── main.go               # アプリケーションエントリーポイント
├── frontend/             # フロントエンド（HTML/CSS/TypeScript）
│   ├── index.html       # メインUI
│   └── src/main.ts      # TypeScriptエントリーポイント
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

Ephy連携V1では`.mdsys/ephy/outbox`をreview済みfile exchangeに使用する．Ephyは`content`をread-onlyで参照し，Karteの`Ephy候補`画面で明示的に採用したproposalだけが既存`SaveFile`経路からcanonical Markdownへ反映される．contractと障害回復手順は[`Karte–Ephy reviewed outbox V1`](architecture/KARTE_EPHY_OUTBOX.md)を参照する．

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

Karte v1.0.0の公開可否は，[v1.0 release criteria](RELEASE_CRITERIA_V1.md)の
機能，安全，互換性，配布，rollback gateで判定する．Required gateが1件でも
`FAIL`または`BLOCKED`なら公開しない．

### アーキテクチャ

- **バックエンド**: Go + Wails v2
- **フロントエンド**: TypeScript + HTML/CSS
- **Markdown処理**: Goldmark
- **ビルドシステム**: Wails CLI

### テストの実行と推奨フロー

1. **Go**: バックエンドのユニットテスト。
   ```bash
   go test ./...
   ```
2. **Node (フロントエンド)**: Viteで提供されるユニット/コンポーネントテスト（追加する場合は `package.json` にテストスクリプトを用意）。
   ```bash
   cd frontend
   npm install
   npm run test
   ```
3. **Playwright**: E2Eテスト。事前に Playwright をセットアップし、ブラウザをインストールしておきます。
   ```bash
   cd frontend
   npm install
   npx playwright install --with-deps
   npx playwright test
   ```

推奨フローは「Go → Node → Playwright」の順で、ユニットテストから E2E まで段階的に確認します。バックエンドとフロントエンドでインターフェイスが変わった場合、該当レイヤーのテストを優先的に更新してから次の層へ進めます。

### テスト命名規約とモック指針

- **命名**: Goは `Test<対象>`、Node/Playwrightは `<機能名>.<シナリオ>.spec.(ts|js)` または `test.(ts|js)` として、対象機能と期待シナリオが分かる名前にします。
- **モック**: 外部I/O（ファイル、ネットワーク、ブラウザAPI、OSコール）はモックし、副作用を遮断します。ユニットテストではモックをデフォルトとし、E2Eは実サービスを模したテストデータを用意する形で最小限にとどめます。
- **共有ヘルパー**: モックやテストデータの共通化は各レイヤーの `internal`/`__tests__`/`tests` ディレクトリを利用し、テストコードからのみ参照されるようにします。

### 新規機能のチェックリスト

- [ ] Go・Node・Playwright の推奨フローに沿ってテストを追加／更新した
- [ ] テスト名が対象とシナリオを示す命名規約に従っている
- [ ] 外部依存をモックし、副作用を遮断した（必要なE2Eのみ実サービスに近いデータを利用）
- [ ] 追加したモック／テストヘルパーが共有ディレクトリに整理されている

### カスタマイズ

- テーマの追加: `frontend/index.html`のCSS変数を編集
- 機能の追加: `app.go`にWailsバインディングメソッドを追加
- UIの変更: `frontend/index.html`と`frontend/src/main.ts`を編集

## ライセンス

Copyright © 2024, kirimine170

## サポート

問題や機能要望がある場合は、GitHubのIssuesでお知らせください。
