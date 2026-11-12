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

`.mdsys/decision-log.json` に事業IDと表示名，採用者の表示名，公開物で併記を避ける語句の組み合わせを設定できる．事業IDと表示名は日次サマリの見出しに使える．初期値には `overall` と `karte` が入る．

```json
{
  "businesses": {"workshop": "工作室", "events": "催事班"},
  "decider": "local-human",
  "private_together": [["工作室", "催事班"]]
}
```

## Context Protocol v1

既存のsearch／readで `kind=decision`，事業IDは `project`，状態は `tags` と本文，出典URLとハッシュは `provenance` に入る．決定ログの応答SHA-256は保存データと表示内容から算出するため，置換や表示可能なリンクの変更を反映する．

- `未決 工作室` は工作室の未決を探す．
- `履歴 <検索語>`，`撤回 <検索語>`，`置換 <検索語>` は履歴を検索する．機械クライアントは `decision:history`，`decision:withdrawn`，`decision:superseded` をタグに指定できる．
- 履歴のIDを明示したreadでは現在の状態を返す．両記録が参照可能なら置換リンクを表示する．
- 非公開の決定はローカルの `human/local-human` のみに適用される．EphyとMCPからの検索・読取では公開区分と既存ポリシーをともに検証する．

## 受け入れテスト

通常のGoテストは合成サマリで，取り込み，重複集約，人のレビュー，receipt再試行，CPv1の検索・読取，置換，撤回，再起動後の読取を確認する．WindowsはリポジトリのGoビルド環境を準備してPowerShellで実行する．

```powershell
go test ./internal/decisionlog ./internal/contextcore/...
go test . -run 'TestDecisionLogAcceptance|TestPublicDecisionContext' -count=1
```

実際の日次サマリを使う場合は，ローカルの作業用ディレクトリに元ファイルと検証条件のJSONを置く．検証条件は `settings`（ワークスペース設定），`decided_query`（決定の検索語），`open_business`（未決の事業ID），`open_match`（未決の本文の一部）を持つ．テストは一時ワークスペースで動く．

```powershell
$env:KARTE_DECISION_ACCEPTANCE_SUMMARY = 'C:\work\2026-11-12_日次.txt'
$env:KARTE_DECISION_ACCEPTANCE_SOURCE = 'https://drive.google.com/file/d/<FILE_ID>/view'
$env:KARTE_DECISION_ACCEPTANCE_CASE = 'C:\work\decision-acceptance.json'
go test . -run 'TestDecisionLogAcceptance/meeting-room$' -v -count=1
```
