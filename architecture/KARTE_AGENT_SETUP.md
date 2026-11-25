# AIエージェントからKarteを使う

`karte-mcp` は、エージェントがローカルのKarte保管庫にあるMarkdownを検索して読むためのSTDIOサーバーです。[Karteスキル](../skills/karte/SKILL.md)には、検索から文書の引用までの使い方を記載しています。Google Driveの関係検索は[デスクトップアプリのDrive連携](GOOGLE_DRIVE_OAUTH_PROTOTYPE.md#drive関係検索)で使えます。

## 合成データで接続を試す

1. Karteの配布物から `karte-mcp`（Windowsでは `karte-mcp.exe`）を取り出します。ソースからビルドする場合は、リポジトリのルートで `go build -o karte-mcp.exe ./cmd/karte-mcp` を実行します。macOSとLinuxでは出力名を `karte-mcp` にします。
2. [サンプルの保管庫](../templates/karte_mcp_example/)を、新しい書き込み可能なディレクトリへコピーします。隠しディレクトリ `.mdsys` と `content` を含めます。サンプルには接続確認用の文書と、`demo` プロジェクトの検索・読取を許可する設定が入っています。
3. `KARTE_DATA_DIR` にコピー先の絶対パスを指定し、`karte-mcp --check` で起動条件を確認します。Windows PowerShellの例です。

   ```powershell
   $env:KARTE_DATA_DIR = 'C:\Karte\agent-demo'
   & 'C:\Karte\karte-mcp.exe' --check
   ```

4. エージェントにMCPを登録します。クライアントは登録した実行ファイルを起動するので、下記の設定中の `command` と `KARTE_DATA_DIR` をそれぞれの絶対パスに置き換えます。

## MCPクライアントの設定

### Codex

既存の `config.toml` に `[mcp_servers.karte]` を追加します。同名の設定がある場合はその項目を編集します。

```toml
[mcp_servers.karte]
command = 'C:\Karte\karte-mcp.exe'
env = { KARTE_DATA_DIR = 'C:\Karte\agent-demo' }
```

### Claude Desktop

開発者設定から設定ファイルを開いて、既存の `mcpServers` に `karte` を追加します。保存後にClaude Desktopを再起動します。[ローカルMCPの接続手順](https://modelcontextprotocol.io/docs/develop/connect-local-servers)

```json
{
  "mcpServers": {
    "karte": {
      "command": "C:\\Karte\\karte-mcp.exe",
      "env": { "KARTE_DATA_DIR": "C:\\Karte\\agent-demo" }
    }
  }
}
```

ほかのクライアントでも、ローカルのSTDIOサーバーを登録できれば同じ実行ファイルと環境変数が使えます。Skillを読み込めるクライアントには、[Karteスキルのフォルダ](../skills/karte/)をそのクライアントのスキル置き場へコピーします。Codexのプロジェクト内で共有する場合は `.agents/skills/karte/` に置きます。

## 検索と読取を確認する

接続後、エージェントに「Karteで `demo` プロジェクトの『接続確認』を検索して、見つけた文書の本文を読んで」と依頼します。ツールを個別に試すときは、次の値を使います。

| ツール | 引数の例 | 確認する結果 |
| --- | --- | --- |
| `karte_search` | `{"query":"接続確認","projects":["demo"],"tags":["quickstart"],"top_k":3}` | `doc:karte-agent-quickstart` が見つかる |
| `karte_read` | `{"doc_id":"doc:karte-agent-quickstart"}` | 合成文書の本文、相対パス、SHA-256が返る |

## 実データに接続する

エージェントに共有するMarkdownを専用のKarte保管庫に用意します。`.mdsys/context/v1/policy.json` で対象プロジェクトと機密度の上限を指定し、`mcp-scope.json` で接続に使うactorを指定します。クライアントごとに異なる公開範囲が必要なら、専用保管庫とactorを分けます。実データへ切り替える前に `--check` を実行し、そのクライアントから想定どおり検索できるか確認します。[権限設定とプロトコル](KARTE_CONTEXT_PROTOCOL_V1.md#mcp-facaderead-only)
