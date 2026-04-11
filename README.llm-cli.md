# Karte LLM CLI Guide

LLM から Karte を操作するときの説明は、このドキュメントを正本とする。

## Purpose

`karte-cli` は、Karte workspace 配下の `karte_data` を JSON ベースで操作するための CLI です。

- 対象は Markdown ドキュメント操作とその周辺機能です
- 原則としてドキュメントは `karte_data/content` 配下のみを扱います
- CLI 利用者は `content/` を明示しなくて構いません

例:

- `test.md` -> 実際には `content/test.md`
- `notes/today.md` -> 実際には `content/notes/today.md`

## Workspace Model

Karte は「アプリケーションが置かれているディレクトリ」を workspace root として扱います。

- workspace root: `<workspace>`
- data directory: `<workspace>/karte_data`
- built-in util directory: `<workspace>/karte_data/karte_util`
- built-in CLI path:
  - macOS / Linux: `<workspace>/karte_data/karte_util/karte-cli`
  - Windows: `<workspace>/karte_data/karte_util/karte-cli.exe`

開発中は次のどちらかで実行します。

```bash
go run ./cmd/karte-cli <subcommand> --root <workspace> --json
```

```bash
./build/bin/karte-cli <subcommand> --root <workspace> --json
```

配布済みアプリでは、built-in CLI を絶対パスで実行します。

## Output Contract

`--json` を付けると、すべてのレスポンスは次の形式になります。

```json
{
  "ok": true,
  "result": {}
}
```

```json
{
  "ok": false,
  "error": {
    "code": "invalid_input",
    "message": "..."
  }
}
```

終了コード:

- `0`: success
- `2`: invalid input
- `3`: not found
- `4`: conflict
- `10`: internal error

## Supported Commands

### `init`

workspace 配下に `karte_data` を初期化します。

```bash
go run ./cmd/karte-cli init --root /tmp/karte-demo --json
```

### `list`

`content` 配下の Markdown 一覧を返します。

```bash
go run ./cmd/karte-cli list --root /tmp/karte-demo --json
```

### `read`

ドキュメントを読み出します。

```bash
go run ./cmd/karte-cli read --root /tmp/karte-demo --path test.md --json
```

### `create`

新しい Markdown を作成します。

```bash
go run ./cmd/karte-cli create \
  --root /tmp/karte-demo \
  --path notes/test.md \
  --title "Test" \
  --json
```

### `write`

ドキュメント内容を保存します。

現状は `--path` 必須、本文は `--content-file` 必須です。

```bash
go run ./cmd/karte-cli write \
  --root /tmp/karte-demo \
  --path notes/test.md \
  --content-file /tmp/body.md \
  --create \
  --json
```

`write` 実行時には次を行います。

- `doc_id` の自動付与
- frontmatter の正規化
- Git コミット
- サイト再ビルド

### `build`

`karte_data/public` に HTML を生成します。

```bash
go run ./cmd/karte-cli build --root /tmp/karte-demo --json
```

### `preview`

指定 Markdown を HTML にレンダリングして返します。

```bash
go run ./cmd/karte-cli preview --root /tmp/karte-demo --path test.md --json
```

### `graph`

ドキュメント間リンクのグラフを返します。

```bash
go run ./cmd/karte-cli graph --root /tmp/karte-demo --json
```

## Typical CRUD Flow

```bash
go run ./cmd/karte-cli init --root /tmp/karte-demo --json

go run ./cmd/karte-cli create \
  --root /tmp/karte-demo \
  --path test.md \
  --title "CLI Demo" \
  --json

go run ./cmd/karte-cli read \
  --root /tmp/karte-demo \
  --path test.md \
  --json

go run ./cmd/karte-cli write \
  --root /tmp/karte-demo \
  --path test.md \
  --content-file /tmp/body.md \
  --json
```

現状の CRUD 対応:

- Create: supported
- Read: supported
- Update: supported
- Delete: not implemented yet

## Current Constraints

- ドキュメント操作対象は `content` 配下の `.md` のみ
- `--path` は必須
- `write` 本文は現状 `--content-file` 経由のみ
- `delete` と `rename` は未実装

## Notes For LLM Integrations

- 長文 Markdown は CLI 引数に直接埋め込まず、一時ファイルを作って `--content-file` で渡してください
- パスは `test.md` のような短い形で渡してよいですが、レスポンスは `content/test.md` で返る場合があります
- `build` 後の生成物は `karte_data/public` を参照してください
- `graph` はリンク解決確認に使えます
