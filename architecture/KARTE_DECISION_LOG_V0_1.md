# Karte 決定ログ v0.1

## 日次サマリからの取り込み

Karteの「候補レビュー」から「決定ログ」を開き，元のDriveファイルのHTTPS URLを入力してUTF-8のテキストを選ぶ．Google Drive for desktop等の同期フォルダや，Driveからダウンロードしたファイルを利用できる．ファイル名は `YYYY-MM-DD_bot名` または `YYYY-MM-DD_まとめ` で，`.txt` と `.md` の拡張子を付けられる．

決定と未決を明記した行，定例サマリの③の判断依頼と④の依頼から候補を作る．事業名や状態を明示する場合は `・【Karte】【決定】本文`，`・【工作室】【未決】本文` の形式を使う．日次サマリは非公開扱いで取り込む．候補は既存のoutboxに入り，レビュー担当者が「提案どおり採用」または「編集内容で採用」を選ぶと記録される．採用・破棄は同じoutboxにreceiptを残す．

同じ事業でUnicode正規化，空白，文末の句点を揃えて一致する本文は，同一候補に集約する．別URLから得た出典を同じ記録に追加する．同じURLの再取り込みは最初に観測したSHA-256を維持し，件数を増やさない．

## 記録と状態

[JSON Schema](../schemas/decision-log/v0.1/record.schema.json)がフィールドを定義する．採用時の状態は `decided`（決定）または `open`（未決）で，`withdrawn`（撤回）と `superseded`（置換）を履歴として保持する．出典のURLと元ファイルのSHA-256を記録する．参照先の存在，自己参照，循環，競合する置換はKarteが保存・読取時に検証する．

同じ事業で置き換える候補を取り込み，既存の候補レビューで「決定候補（JSON）」の `status` を `decided` にし，`supersedes` に現在有効な記録のIDを指定して「編集内容で採用」を選ぶ．記録IDは決定ログの各項目に表示される．新しい記録を1ファイルで確定すると，元の記録は置換済みになる．複数回の置換は履歴を順に辿れる．撤回は一覧の「撤回」から行う．置換先を撤回した後も元の決定の置換状態は維持される．

公開区分は `public` と `private` を取る．ワークスペースで指定した語句の組み合わせを含む候補には `publication_restriction: workspace-private` が付き，公開区分は `private` になる．決定ファイルは選択中のKarteワークスペースの `content/.decision-log/` に保存する．通常の文書一覧，グラフ，HTML・PDF・静的サイト出力と自動Gitコミットはこのディレクトリを除外する．

## ワークスペース設定

`.mdsys/decision-log.json` に事業IDと表示名，採用者の表示名，公開物で併記を避ける語句の組み合わせを設定できる．事業IDと表示名は日次サマリの見出しに使える．初回の取り込み時に `overall`、`karte`、`hacksick`、`essen`、`mirai-labo`、`hsap`、`ephy`、`far` と、HSAP・ESSENの同時公開を防ぐルールを初期設定に書き込む。既存の設定ファイルで採用や撤回を行う場合は、`decider` に `雫さん` を設定する。採用待ちの旧候補に `local-human` が入っている場合は、設定を更新してから元サマリを再取り込みする。

```json
{
  "businesses": {"workshop": "工作室", "events": "催事班"},
  "decider": "雫さん",
  "private_together": [["工作室", "催事班"]]
}
```

## Context Protocol v1

既存のsearch／readで `kind=decision`，事業IDは `project`，状態は `tags` と本文，出典URLとハッシュは `provenance` に入る．決定ログの応答SHA-256は保存データと表示内容から算出するため，置換や表示可能なリンクの変更を反映する．

- `未決 工作室` は工作室の未決を探す．
- `履歴 <検索語>`，`撤回 <検索語>`，`置換 <検索語>` は履歴を検索する．機械クライアントは `decision:history`，`decision:withdrawn`，`decision:superseded` をタグに指定できる．
- 履歴のIDを明示したreadでは現在の状態を返す．両記録が参照可能なら置換リンクを表示する．
- 新規候補の `visibility_tags` は `["private"]`。雫さんは「編集内容で採用」で `public`、`internal`、`business:<事業ID>`、`agent:grok`、`agent:muse`、`agent:chatgpt` を指定できる。従来の明示的な `visibility: public` も読み取れる。
- 決定ログの「書き出し先」で一般公開または個別のAIを選び、JSONをローカルに保存できる。一般公開と選択した受信者向けの記録が対象になる。HSAPとESSENが同時に公開対象になる場合は、両方に関する記録を共有から除外する。
- 非公開の決定はローカルの `human/local-human` が検索・読み取りできる。Ephyと既存のMCPは公開記録に対して既存ポリシーを適用する。

## Google Drive同期

1. Google CloudのプロジェクトでDrive APIを有効にし、デスクトップアプリ用のOAuthクライアントを作成する。Karteの起動環境に `KARTE_GOOGLE_CLIENT_ID` と `KARTE_GOOGLE_CLIENT_SECRET` を設定する。
2. MacとWindowsで同じGoogleアカウントでログインする。「候補レビュー」の決定ログで「Googleアカウント」を開き、両方に同じ同期IDを入力して同期を有効にする。最初の受信側には空の決定ログを用意する。
3. 片方で決定を採用し、もう片方の一覧への反映を確認する。逆方向も試す。オフラインで編集した場合は、再接続後の自動同期または「今すぐ同期」で反映を確認する。
4. 両端末をオフラインにして同じ決定に別々の編集を行う。再接続後、各端末に自分の版が残り、相手側の版が `.mdsys/decision-sync/conflicts/` に保存されることを確認する。

同期対象は採用済みの決定と決定ログ設定。同じOAuthクライアントとGoogleアカウントの `appDataFolder` を使う。トークンはmacOSのKeychainまたはWindowsの資格情報マネージャーに保存し、ログアウトで削除する。競合時は元の両版を残す。

## AI用MCP

ワークスペースの `.mdsys/context/v1/policy.json` の `actors` に `agent:grok`、`agent:muse`、`agent:chatgpt` のうち有効にする受信者を明示する。各受信者に対象事業、`sensitivity_ceiling: internal`、必要な権限 `search`、`read`、`propose` を付与する。

```powershell
$env:KARTE_DATA_DIR = 'C:\work\karte_data'
go run ./cmd/karte-mcp --issue-decision-token agent:grok
go run ./cmd/karte-mcp --agent-http 127.0.0.1:8791
```

トークンは受信者ごとに発行する。再発行すると旧トークンは無効になる。HTTPクライアントは `POST /mcp` に `Authorization: Bearer <token>` を付ける。遠隔クライアントから使う場合は、ループバックのMCPサーバーの前にHTTPSのリバースプロキシを置く。ローカルのSTDIOクライアントは `KARTE_DECISION_AGENT_TOKEN` にトークンを設定できる。

認証済みのAIは `karte_search`、`karte_read`、`karte_propose_decision` を使う。投稿は非公開の候補としてレビュー一覧に入り、採用、撤回、置換は雫さんがKarteで確定する。AIによる検索、読取、候補投稿はContext監査に受信者別で記録される。

## 受け入れテスト

通常のGoテストは合成サマリで，取り込み，重複集約，人のレビュー，receipt再試行，CPv1の検索・読取，置換，撤回，再起動後の読取を確認する．WindowsはリポジトリのGoビルド環境を準備してPowerShellで実行する．

```powershell
go test ./internal/decisionlog ./internal/contextcore/...
go test . -run 'TestDecisionLogAcceptance|TestPublicDecisionContext' -count=1
```

JSON Schemaとフィクスチャは `python scripts/verify_decision_log_fixtures.py` で検証する。自己参照はGoのフィクスチャテストでも確認する。

実際の日次サマリを使う場合は，ローカルの作業用ディレクトリに元ファイルと検証条件のJSONを置く．検証条件は `settings`（ワークスペース設定），`decided_query`（決定の検索語），`open_business`（未決の事業ID），`open_match`（未決の本文の一部）を持つ．テストは一時ワークスペースで動く．

```powershell
$env:KARTE_DECISION_ACCEPTANCE_SUMMARY = 'C:\work\YYYY-MM-DD_summary.txt'
$env:KARTE_DECISION_ACCEPTANCE_SOURCE = 'https://drive.google.com/file/d/<FILE_ID>/view'
$env:KARTE_DECISION_ACCEPTANCE_CASE = 'C:\work\decision-acceptance.json'
go test . -run 'TestDecisionLogAcceptance/meeting-room$' -v -count=1
```

### Windows 1050ノートでの手動確認

1. 一時ワークスペースでKarteを起動し、「候補レビュー」の決定ログから受け入れ用の日次サマリを取り込む。同じファイルをもう一度選び、候補が重複しないことを確認する。
2. 決定1件と未決1件を採用し、事業別一覧、再起動後の表示、通常の検索・書き出しでの非公開扱いを確認する。通常のメモから決定ファイルへリンクし、グラフと `links.json` に対象が出ないことを確認する。
3. JSON Schemaと実データのテストを実行し、端末と結果を記録する。

2026年11月13日、別のWindows端末で原文を使った `meeting-room` テストとグラフの回帰テストが通過した。1050ノートでの手動確認は未実施。
