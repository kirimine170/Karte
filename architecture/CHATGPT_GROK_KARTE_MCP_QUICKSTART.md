# Karte MCP：ChatGPT／Grok Bot接続ガイド（限定試用）

## 目的と現在の実装範囲

KarteにすでにあるContext Core（ローカルMarkdown／根拠），決定ログ，MCPとAI別トークンを再利用する．
Workspace Sync／Resource Graphの完成や外部RAGの導入を待たず，AIから次の操作を行えるようにする．

| MCP tool | 動作 | 正本への変更 |
| --- | --- | --- |
| `karte_search` | actorのpolicyで許可されたMarkdownと可視な決定を検索 | なし |
| `karte_read` | 許可された`doc_id`・決定IDを読取 | なし |
| `karte_propose_decision` | 機密の決定**候補**をレビュー待ちoutboxへ投入 | 採用しない |

`--shared-context` を**明示した場合だけ**通常のMarkdownも対象とする．
従来の `karte-mcp --agent-http 127.0.0.1:8791` と，
`KARTE_DECISION_AGENT_TOKEN` を指定して `--shared-context` を省略したSTDIOは
これまでどおり**決定ログ専用**である．
権限を与えられていないAIに既存のMarkdownを配信しない．

この段階ではPDF原本・画像・CSV・音声のblobをMCP経由で転送しない．
MCPが返す出典IDとSHA-256をもとにKarte側で参照する．
検索・読取はローカルKarteが起動していなくても，明示した`KARTE_DATA_DIR`から実行可能．
アクセス可能な内容はサービス側のContext Policyにより決まり，AIからの引数では拡大できない．

## 0．Windowsでの前提確認

- 現行`go.mod`のGo，Wails，ネイティブ依存については`windows.md`を参照．
- `karte-mcp.exe`はDesktop Buildの成果物にも含まれる．
- 機密情報を含まない**検証専用**`KARTE_DATA_DIR`から始めること．
- 他サービスへの情報送信はChatGPT／Grok Botに許可したデータに限り実施．
- 管理対象のKarteファイルをそのままHTTPサーバーの公開ディレクトリにしない．

PowerShellでの例（`C:\Karte-Test\karte_data` は実際の絶対パスに置換）：

```powershell
$env:KARTE_DATA_DIR = 'C:\Karte-Test\karte_data'
.\karte-mcp.exe --help
```

## 1．AIごとに最小権限のpolicyを設定する【利用者による設定】

`$env:KARTE_DATA_DIR\.mdsys\context\v1\policy.json`の既存設定を**上書きせず**，
`actors`へ使うAIだけを追加する．このファイルが存在しない場合，認証付き
agent MCPは起動しない（fail closed）．

次は，Karte案件にだけ両者の検索と読取を許可する**参考断片**．
既存policyの`protocol_version`や他actorを維持し，対象プロジェクト・sensitivityを調整する．

```json
{
  "agent:chatgpt": {
    "projects": ["karte"],
    "sensitivity_ceiling": "internal",
    "provenance_types": ["canonical", "meeting-summary"],
    "capabilities": ["search", "read"]
  },
  "agent:grok": {
    "projects": ["karte"],
    "sensitivity_ceiling": "internal",
    "provenance_types": ["canonical", "meeting-summary"],
    "capabilities": ["search", "read", "propose"]
  }
}
```

このJSONは`actors`内部に置く**断片**であり，独立したpolicy.jsonではない．
プロポーザルには`propose` capabilityと事業ID設定が必要．
提案内容は非公開で，必ずKarte上で人間が採否を決める．

**重要**：AI別設定を同一actorにまとめないこと．
`agent:chatgpt`と`agent:grok`へ別々のtokenを発行・失効する．

```powershell
.\karte-mcp.exe --issue-decision-token agent:chatgpt
.\karte-mcp.exe --issue-decision-token agent:grok
```

このコマンドはトークンを標準出力に**一度だけ**表示する．
端末履歴・Issues・PR・ログ・チャットに貼らず，OS資格情報保管庫やSecrets管理へ保存する．
再発行すると旧トークンは無効になる．

## 2．ローカル検証（Windows）

### ChatGPT用：STDIOとSecure MCP Tunnel

ChatGPTから直接127.0.0.1へ接続することはできない．
プライベートな試用にはOpenAIの**Secure MCP Tunnel**でSTDIOの
`karte-mcp --shared-context`を接続することを推奨する．
Tunnel登録・API Key・Pluginの追加は利用者のアカウントで行う．

プロセスの環境には以下を設定する（値をsource controlへ入れない）：

```text
KARTE_DATA_DIR=<Karte検証データルートの絶対パス>
KARTE_DECISION_AGENT_TOKEN=<agent:chatgptのtoken>
```

起動コマンド（`--check`は設定と権限のローカル検査のみ）：

```powershell
.\karte-mcp.exe --shared-context --check
.\karte-mcp.exe --shared-context
```

Tunnelクライアントではサーバー実行コマンドに
`karte-mcp.exe --shared-context`を指定し，Secretを環境参照する．
実際の設定キーは導入したTunnel Clientのバージョンに従い，
`tunnel-client help quickstart`と`tunnel-client doctor`で確認する．
その後，ChatGPT Plugins > Add custom MCP server > Tunnelで接続する．

公式：
- https://developers.openai.com/api/docs/guides/secure-mcp-tunnels
- https://developers.openai.com/plugins/deploy/connect-chatgpt

**注**：ChatGPTのプラン・ワークスペースによりMCPツールのread/write利用範囲は異なる．
読み取りのみ利用可能な環境では`karte_propose_decision`は接続試験の必須条件から外す．
ChatGPT API・Grokから接続する場合もサービスごとの課金・利用制限を確認する．

### Grok Bot用：認証付きループバックHTTP

既存のGrok Bot環境がローカルSTDIOからMCPを起動できる場合は
上の方式を`agent:grok`のtokenで使う．

GrokのCustom MCP ConnectorがリモートURLを必要とする場合は，
Karte側は**ループバック専用**で起動し，HTTPSのトンネル／リバースプロキシで公開する．
接続サービスが静的Bearer認証に対応するかはGrok Bot側の設定画面で確認する．

```powershell
.\karte-mcp.exe --agent-http 127.0.0.1:8791 --shared-context
```

リモートMCPの接続先はHTTPSの`/mcp`．
ループバックの`http://127.0.0.1:8791/mcp`を直接パブリックURLとして入力しない．
クライアントは各リクエストで`Authorization: Bearer <agent:grok token>`を送信する．
TLSの設定，接続元認証，リバースプロキシのログ抑止，token秘密管理を有効にする．
未認証の`/mcp`アクセスはHTTP 401で拒否する．

公式：
- https://docs.x.ai/grok/connectors
- https://docs.x.ai/build/features/mcp-servers

## 3．エージェント同士で利用する操作と評価

どちらの接続も，同じ`KARTE_DATA_DIR`をKarte Coreの正本として参照する．
両者のポリシーが一致していれば同じ資料を検索でき，違う場合は同じ資料でもアクセスできない．
最低限の合成データで以下を検証する．

| 試験 | 期待 |
| --- | --- |
| `initialize`／`tools/list` | `karte_search`／`karte_read`，許可された場合`karte_propose_decision`を検出 |
| `karte_search` | 許可された資料のみ，出典・doc_id・SHA-256つきで返す |
| `karte_read` | 検索結果のdoc_idを指定し本文と版を確認 |
| GrokへChatGPT限定プロジェクトを問う | タイトル・本文・件数を漏らさない |
| 既存の機密文書へのread | `denied`かそれに準じた非開示結果 |
| policy変更後の同じread | 直後に新しい権限を適用 |
| tokenローテーション | 古いtokenではHTTP 401／STDIOでは失敗 |
| `karte_propose_decision` | private pendingとなり，人間採用までは正本に存在しない |
| 複数OS | 同じ合成Workspaceを使ったGo/CI自動テストに成功 |

### 動作確認用JSON-RPC（トークンの実値は記録しない）

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"karte-smoke","version":"0.1.0"},"capabilities":{}}}
```

```json
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"karte_search","arguments":{"query":"Karte","top_k":5}}}
```

`karte_read`の引数は`{"doc_id":"<検索結果のdoc_id>"}`．
CLIの`--shared-context`を省略した場合，通常Markdownの検索結果が返らないことも確認する．

### 接続診断スクリプト

WindowsでGrok向けのHTTPを起動した後，トークンを環境変数に**安全に読み込ませた**状態で
以下を実行できる．資格情報や取得した本文は出力せず，MCP初期化・ツール発見・検索・任意の読取とdenyを確認する．

```powershell
python scripts/smoke_agent_mcp.py --url http://127.0.0.1:8791/mcp --query Karte
```

合成資料のIDを指定すれば読取・非開示の確認にも使用できる．

```powershell
python scripts/smoke_agent_mcp.py --expect-doc-id <許可済み合成doc_id> --expect-denied-doc-id <非許可の合成doc_id>
```

認証tokenの設定は利用者が行い，`--token-env`で参照する環境変数名を変更できる．
接続が失敗してもトークンや本文をGitHub Issues／チャットへ貼らない．

## 4．セキュリティと現在の限界

- Shared Contextの利用はコマンドラインの**明示フラグ**とactor policyの両方を必要とする．
- 接続設定と認可判定は別々．Bearer tokenを知っていても，Policyで許可されていない資料は利用できない．
- 欠損・無効`policy.json`はfail closed，tokenは再発行で失効する．
- 検索・読取で診断集計（非可視ファイル数など）を外部AIへ送らない．
- 入力資料の内容は信頼しない．文書の指示でツール権限や承認フローを変更できない．
- `--agent-http`は127.0.0.1または::1にのみbind可能．公開HTTPSは別のプロキシ／トンネルが必要．
- Web APIのMCP実装は**2025-06-18互換のStreamable HTTP（JSON応答）**を対象にする．より新しい2026年版のMCPクライアントの互換性は実接続で検証する．
- 現時点ではGrok BotおよびChatGPTの**アカウント内での接続完了は未検証**．接続権限やSecretは利用者が設定する．
- 正式な組織外公開にはOAuth 2.1，長期運用監視，レート制限など追加対策が必要．この段階では個人の限定試用を想定する．

## 5．リリース・ロールバック

この変更は新しい`--shared-context`オプトインのみを追加し，既存の決定ログ専用MCPを維持する．
問題が起きたら引数から`--shared-context`を除去して従来モードへ戻す．
トークンが漏洩した場合は，対応するagentのtokenを再発行し，公開経路を停止する．
実Workspaceのデータ移行・権限変更・mainマージは別途承認が必要．

関連：Issue #351／#348，ADR-0004，`architecture/KARTE_CONTEXT_PROTOCOL_V1.md`，
`architecture/KARTE_DECISION_LOG_V0_1.md`．
