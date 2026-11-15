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

ソースから試す場合はリポジトリのルートで `go build -o karte-mcp.exe ./cmd/karte-mcp` を実行する．
macOS／Linux では `go build -o karte-mcp ./cmd/karte-mcp` として `./karte-mcp` を使う．

## 1．AIごとに最小権限のpolicyを設定する【利用者による設定】

`$env:KARTE_DATA_DIR\.mdsys\context\v1\policy.json`の既存設定を**上書きせず**，
`actors`へ使うAIだけを追加する．このファイルが存在しない場合，認証付き
agent MCPは起動しない（fail closed）．

次は，Karte案件にだけ両者の検索と読取を許可する**参考断片**．
既存policyの`protocol_version`や他actorを維持し，対象プロジェクト・sensitivityを調整する．
同じプロジェクト内でも，actorごとの `allowed_tags`／`denied_tags` で対象を絞れる．
Workspaceを分ける場合は `KARTE_DATA_DIR` とそのルートで発行するトークンも分ける．

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

利用者がPlatformでTunnelを作成し，対象ChatGPT Workspaceを関連付ける．
作成・変更にはTunnels Read + Manage，ランタイムキーとアプリでの利用にはTunnels Read + Useが必要．
ChatGPTのWorkspaceでプライベートなカスタムMCPを追加できることも確認する．
Tunnelのランタイムキーは `CONTROL_PLANE_API_KEY` に，Karteの `agent:chatgpt` トークンは子プロセスの
`KARTE_DECISION_AGENT_TOKEN` に，それぞれ利用者のSecret管理から渡す．Karte側の認可はこの子プロセス内で行う．
ChatGPTの標準認証設定はOAuth等を扱うため，静的なKarteトークンをChatGPTのUIから直接送る構成には依存しない．

```powershell
tunnel-client help quickstart
tunnel-client init --sample sample_mcp_stdio_local --profile karte-stdio --tunnel-id '<利用者のTunnel ID>' --mcp-command "'C:/Karte-Test/karte-mcp.exe' --shared-context"
tunnel-client doctor --profile karte-stdio --explain
tunnel-client run --profile karte-stdio
```

トンネルの準備完了を確認してから，ChatGPTの Plugins → Add custom MCP server → Tunnel で
同じTunnelを選ぶ．この専用STDIO構成では，Workspaceが許可している場合にアプリの認証を
**No authentication** として作成し，インストールする．認証はTunnelの利用制限とKarte子プロセスで行う．
Workspaceがアプリ側のOAuthを必須とする場合は，別途認証が必要になる．
この構成ではプラグイン利用者が同じ `agent:chatgpt` としてKarteを参照するため，
合成試験用のプライベートアプリに限定する．アカウント単位のKarte認可が必要な共有構成では別途認証を設計する．
終了時は `tunnel-client run` を実行中の端末で Ctrl+C．STDIOを単独で起動した場合は標準入力を閉じる．

公式：
- https://developers.openai.com/api/docs/guides/secure-mcp-tunnels
- https://developers.openai.com/plugins/deploy/connect-chatgpt

**注**：ChatGPTのプラン・ワークスペースによりMCPツールのread/write利用範囲は異なる．
読み取りのみ利用可能な環境では`karte_propose_decision`は接続試験の必須条件から外す．
ChatGPT API・Grokから接続する場合もサービスごとの課金・利用制限を確認する．

### Grok Bot用：認証付きループバックHTTP

Grok Botデスクトップの Connect apps → Add a custom connector → Remote HTTPS で，
利用者が承認済みのHTTPS `/mcp` と `Authorization: Bearer <agent:grok token>` を登録する．
Commandの実行先はBotの環境なので，手元のWindowsの `KARTE_DATA_DIR` に届く検証では Remote HTTPS を使う．
組織のMCPポリシーや許可先リストの適用状況と，コネクターを利用できるBotの範囲を確認する．
Karte自体はループバックだけで待ち受ける．承認済みのHTTPSトンネル／リバースプロキシを経由し，
Karteでactor別Bearerを引き続き検証する．

```powershell
.\karte-mcp.exe --agent-http 127.0.0.1:8791 --shared-context
```

リモートMCPの接続先はHTTPSの`/mcp`．
ループバックの`http://127.0.0.1:8791/mcp`を直接パブリックURLとして入力しない．
クライアントは各リクエストで`Authorization: Bearer <agent:grok token>`を送信する．
TLSを使い，接続元の制限と，Authorization・要求本文・応答本文を保存しないプロキシ設定を確認する．
未認証の `POST /mcp` はHTTP 401で拒否する．停止時は承認済み公開経路を先に停止し，KarteのHTTPを端末のCtrl+Cで止める．
試験を繰り返す場合は固定HTTPS URLのトンネルを使うと，再起動のたびにGrok Botの登録先を入れ直す必要がなくなる．

公式：
- [Grok Botのカスタムコネクター設定](https://docs.x.ai/grok-bot/computer-and-apps#connect-an-app)
- [組織のMCPポリシー](https://docs.x.ai/grok-bot/teams-and-enterprises#connector-policy)

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

初期化応答の後に `{"jsonrpc":"2.0","method":"notifications/initialized"}` を送り，
HTTPの後続リクエストには合意した `MCP-Protocol-Version` を付与する．

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
python scripts/smoke_agent_mcp.py --query '<合成資料にだけ含まれる語>' --expect-doc-id '<許可済み合成doc_id>' --expect-sha256 '<合成Markdownファイル全体のSHA-256>' --expect-denied-doc-id '<非許可の合成doc_id>' --no-expect-propose
```

認証tokenの設定は利用者が行い，`--token-env`で参照する環境変数名を変更できる．
候補投稿を許可されたactorのツール検出を確認する場合は `--expect-propose` に置き換える．
SHA-256はMarkdownファイル全体のバイト列に対応し，読取に含まれる本文フィールドはフロントマターを除いた本文である．
検索と読取で資料ID・出典・版が一致するかを診断スクリプトが確認する．
この診断クライアントはBearer tokenの転送を避けるため，HTTP 301／302／307／308などの**リダイレクトを一切追跡しない**．接続先の変更はアカウント／トンネルの設定で明示的に行う．
環境変数やOSのプロキシ設定を使わず，指定した接続先へ直接接続する．
接続が失敗してもトークンや本文をGitHub Issues／チャットへ貼らない．

PowerShellでの切り分け

```powershell
Test-NetConnection 127.0.0.1 -Port 8791
Get-NetTCPConnection -LocalPort 8791 -State Listen | Select-Object LocalAddress, State
tunnel-client doctor --profile karte-stdio --explain
```

| 症状 | 確認箇所 |
| --- | --- |
| ポートへの接続失敗 | 起動中のプロセス，指定ポート，`KARTE_DATA_DIR` と必須ポリシーファイル |
| HTTP 401 | 対象Workspaceのactor grant，現在のトークン，プロキシでのBearer転送 |
| HTTP 3xx | 正しいHTTPS URLと末尾の `/mcp`。認証付きリダイレクトを停止 |
| HTTP 400／403 | 合意したMCPバージョンと `Origin`。ブラウザーからの直接リクエストは拒否される |
| ツールは見えるが資料が出ない | `--shared-context`，project／tag／sensitivity／provenanceと検索語 |
| トンネルは稼働しているがアプリに出ない | Platform組織とChatGPT Workspaceの関連付け，Tunnels Read + Use，Workspace権限 |

### アカウント設定後の実サービス試験

1. 同じ合成Workspaceに両者向け・ChatGPTのみ・Grok Botのみ・両者非公開のMarkdownと，人間が採用済みの決定を用意する．
2. ChatGPTとGrok Botそれぞれで対象コネクターを選び，両者向けの資料を検索して `karte_read` を呼ぶ．
   ツール出力で資料ID・SHA-256・出典が一致することを確認する．片方専用と非公開の合成IDを読取し，拒否されることも確認する．
3. `propose` が許可された方から合成の決定候補を一件送る．Karteでレビュー待ちを確認し，
   採用前にもう一方から検索・読取できないことを確認する．
4. 人間がKarte上で候補の内容と共有範囲を確認して採用する．もう一方に共有を許可した場合は，
   そのAIで決定を検索して同じ決定IDと出典を確認する．対応プランが読み取り専用の場合は，投稿できる側で実施する．
5. 別の合成資料で権限を撤回し，同じクライアントから再検索・再読取して結果を確認する．
   記録は実施日時・KarteのSHA・各チェックの成否に絞る．

### ローカル自動処理で決定を採用する

ローカル実行権限を持つ自動処理は，`KARTE_DATA_DIR` に対象Workspaceを設定し，保留中の候補IDを指定して採用できる．
候補IDは `.mdsys/ephy/outbox/pending/` のファイル名から取得できる．

```powershell
karte-mcp --accept-decision $CandidateId --share-decision-with agent:grok
```

コマンドは決定を正本に登録し，採用記録をJSONで返す．同じ候補IDを再実行すると元の採用記録を返す．
`--share-decision-with` を省略すると候補の共有設定を引き継ぐ．共有先を指定した場合はWorkspaceの読取権限を確認する．
MCPの `karte_propose_decision` は引き続き候補を登録する入口として使い，採用はローカルの自動処理から呼び出す．

## 4．セキュリティと現在の限界

- Shared Contextの利用はコマンドラインの**明示フラグ**とactor policyの両方を必要とする．
- 接続設定と認可判定は別々．Bearer tokenを知っていても，Policyで許可されていない資料は利用できない．
- 欠損・無効`policy.json`はfail closed，tokenは再発行で失効する．
- 検索・読取で診断集計（非可視ファイル数など）を外部AIへ送らない．
- 入力資料の内容は信頼しない．文書の指示でツール権限や承認フローを変更できない．
- `--agent-http`は127.0.0.1または::1にのみbind可能．公開HTTPSは別のプロキシ／トンネルが必要．
- MCPは `2025-06-18` と `2025-03-26` を交渉する．HTTPはステートレスなStreamable HTTPのJSON応答で，GETのSSEにはHTTP 405を返す．
- 共有モードのツール一覧は現在のcapabilityに従う．クライアントが一覧をキャッシュしていても，呼び出しごとに現在の権限を再評価する．
- 現時点ではGrok BotおよびChatGPTの**アカウント内での接続完了は未検証**．接続権限やSecretは利用者が設定する．
- 正式な組織外公開にはOAuth 2.1，長期運用監視，レート制限など追加対策が必要．この段階では個人の限定試用を想定する．

## 5．リリース・ロールバック

この変更は新しい`--shared-context`オプトインのみを追加し，既存の決定ログ専用MCPを維持する．
問題が起きたら引数から`--shared-context`を除去して従来モードへ戻す．
トークンが漏洩した場合は，対応するagentのtokenを再発行し，公開経路を停止する．
実Workspaceのデータ移行・権限変更・mainマージは別途承認が必要．

関連：Issue #351／#348，ADR-0004，`architecture/KARTE_CONTEXT_PROTOCOL_V1.md`，
`architecture/KARTE_DECISION_LOG_V0_1.md`．
