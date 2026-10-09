# 作業記録と日報 v1

Ephyが失敗を次の作業へ持ち越すための記録基盤．作業IDごとに目的，進捗，失敗，検証の申告，根拠への参照，次の作業案を追記する．日報はその原本から決定的に生成する．次の作業案の実行・コード変更の採用・モデルの学習はこの機能では行わない．

## 記録の意味

| 情報 | 扱い |
| --- | --- |
| `host_observation` | CLIを呼ぶ実行側で観測したイベント．Piの実行開始，ツール名・call ID・`isError`，応答停止，実行停止など |
| `model_report` | モデルが書いた目的，原因の候補，検証結果，次の作業案．MCPからの書込みは必ずこの出所になる |
| `recorded_at` | Karteが受信した時刻．この時刻と設定タイムゾーンで日報を集計する |
| `occurred_at` | 呼出側が申告した発生時刻．通信復旧後に届いたイベントも，発生時刻と受信時刻を区別する |
| `completed` | 作業の終了申告．テスト合格・要件充足を自動で証明する値ではない |
| `next_action` | 根拠と合わせて検索できる次の作業案．未実行の提案 |

モデルには，まず作業目的を書き，重要な失敗には「観測・原因の候補・根拠・次の案」を残すようPiの開始時に通知する．Piは本文の意味を推測せず，ホストで観測できるイベントを自動記録する．プロンプト本文・ツール引数・出力本文・秘密情報を自動収集しない．根拠が必要なときはモデルが`evidence`にrun IDやファイル・commitへの参照を追記する．参照先を自動で取得・実行する機能はない．

`--observer`はホストが選ぶCLIオプションで，MCPには公開しない．これはMCP利用者との出所の境界であり，同じOSユーザーがCLIや保存ファイルを操作することに対する認証・改ざん耐性ではない．

## 保存と権限

```text
.mdsys/worklog/v1/config.json       記録先プロジェクト・actor・タイムゾーン・有効化
.mdsys/worklog/v1/tasks/<id>.json   作業ごとの原本（APIは追記のみ）
.mdsys/worklog/v1/daily/<date>.json 日報の原本
.mdsys/worklog/v1/snapshots/<sha>.json 日報の対象日時点の作業スナップショット
.mdsys/worklog/pi-spool/<sha>.json  Piのセッションごとの未送信キュー
content/projects/<project>/note/worklog-<id>.md
content/projects/<project>/journal/worklog-daily-<date>.md
```

Markdownは閲覧・既存context検索用の生成物．原本のイベント列が正となる．人の追記もAPIの`note`で行う．生成物を直接編集すると，上書きせず`worklog_conflict`を返す．作業イベントは先に保存するので，`recorded=true, views_ready=false`なら原本は保存済み．同じイベントIDで再送すると重複追記せず生成を再試行する．編集したMarkdownを別の通常ノートに退避し，生成物だけを削除して再送すれば原本から再生成できる．

共有canonical writerのプロセス間ロック・原子的置換・CAS・Windows ACLを利用する．同じイベントIDと同じ内容の再送は成功，内容を変えた再送は競合．呼出側は失敗時に新しいIDを発行しない．原本JSONはOS上で編集可能であり，ハッシュ・日報スナップショットは監査の参照点であって署名ではない．

全呼出しで現在の`config.json`と`.mdsys/context/v1/policy.json`を読み直す．専用設定の`enabled=true`に加え，actorの`search/read/propose`を通常のKarte privacy policyで照合する．書込みは`propose`と専用記録設定の組合せに限定する．コード・skillの採用や`learn/export`を許可するものではない．`enabled=false`や権限の取消しは次の呼出しから有効．モデル用ツールに初期設定・policy編集・任意パスへの書込みは公開しない．

新しい空のデータ領域では`init`が指定project・actor・sensitivity・canonical provenanceだけのpolicyを作る．既存policyを上書きしない．既存データ領域で使う場合は，そのpolicyに記録actorの対象プロジェクトと`search/read/propose`が必要．普段のデータ領域の代わりに専用領域から始めることを推奨する．

今回のKarteソースでは，worklog生成物と`.mdsys/worklog/`を自動Gitコミットとサイト生成から除外する．既存context検索とローカル閲覧からは除外しない．この除外には更新後のKarteアプリが必要なので，旧版アプリで専用記録領域を公開サイトとしてビルドしない．

## ビルド・初期設定

リポジトリ内で実行する．Go CLIはWails・音声処理のビルドを必要としない．データ領域は既存のディレクトリを明示する．

```powershell
go build -o build/bin/karte-worklog.exe ./cmd/karte-worklog
New-Item -ItemType Directory -Path C:/path/to/ephy-worklog-data
./build/bin/karte-worklog.exe --root C:/path/to/ephy-worklog-data init
```

初期値はactor/project=`ephy-worklog`，timezone=`Asia/Tokyo`，sensitivity=`internal`．変更する場合は`init`の前に`--actor/--project/--timezone/--sensitivity`を指定する．モデルが設定を動かして別の領域に記録する運用は行わない．

CLIの全オプションはサブコマンドより前に置く．`start/append/read/search`はstdinのJSONを読む．

```powershell
'{"task_id":"research-001","event_id":"start-001","title":"検索の改善","objective":"検索失敗の再現条件を調べる"}' | ./build/bin/karte-worklog.exe --root C:/path/to/ephy-worklog-data start
'{"task_id":"research-001","event_id":"failure-001","kind":"failure","text":"429エラーを観測したと申告","next_action":"待機時間を変えて再検証する","evidence":[{"reference":"run:research-001"}]}' | ./build/bin/karte-worklog.exe --root C:/path/to/ephy-worklog-data append
'{"query":"429","limit":10}' | ./build/bin/karte-worklog.exe --root C:/path/to/ephy-worklog-data search
'{"task_id":"research-001","offset":0,"limit":10}' | ./build/bin/karte-worklog.exe --root C:/path/to/ephy-worklog-data read
./build/bin/karte-worklog.exe --root C:/path/to/ephy-worklog-data --date 2026-10-09 daily
```

出力はJSONでstdout，診断はstderr．CLIは生成失敗でも保存済みのreceiptを返す．`read`は10イベントまでのページ，`search`は20件までの概要．MCPの日報読取はUnicode文字単位でページ送りできる．

## Pi

Pi 0.86.1の拡張APIで確認した．同梱の`.ts`と`.mjs`を一緒に置く．グローバル設定を変えずに起動する例:

```powershell
./integrations/pi/start-worklog-pi.ps1 -PiBinary C:/path/to/pi.exe -DataRoot C:/path/to/ephy-worklog-data
```

手動設定なら`KARTE_WORKLOG_ROOT`と`KARTE_WORKLOG_BIN`を明示し，`pi -e ./integrations/pi/karte-worklog.ts`で読み込む．両方が未設定なら何も登録しない．`KARTE_WORKLOG_SPOOL`で未送信キューの場所を変更できる．

1回の`before_agent_start`を1作業として開始．`agent_start`，ツール開始・終了，応答のerror/aborted，`agent_settled`，session shutdownを記録する．モデルの自己申告は`karte_work_note`で別の出所として追記する．`karte_work_search/read`で過去の根拠を参照できる．実行停止だけでは成果を判定できないので，終了申告がない作業は`paused`として持ち越す．

キューは配送前に同期保存する．保存とビュー生成の両方のackを得てから削除する．通信失敗はPiに送信待ち件数を表示する．次のイベント・毎分のtick・`/karte-flush`・同じセッションの再開で再送する．同じセッションのスプールを複数のPiプロセスから同時に使わない．別の新規セッションを開始しただけでは，古いセッションの未送信キューは再送されないので，元のセッションを再開する．キューを消して復旧したことにはしない．

活動中は各追記で日報を更新し，Piが起動している間は毎分のtickで当日日報を生成する．日付をまたいで待機している場合も未完了作業を引き継ぐ．Piが停止している日のカレンダー上の日報が必要なら，別の運用でCLIの`daily`を1日1回実行する．この変更では常駐サービスやOSスケジュールを登録しない．

プロセスが強制終了した場合はshutdownを観測できない．最後に保存されたrunning/paused状態と最終観測時刻を残し，成功や失敗を後から作らない．

## Strata

`integrations/strata/karte-worklog.example.json`のパスを実際の絶対パスに置き換え，Strataの`mcp_servers`設定に追加する．既存の`serve/mcp.py`のSTDIO transportを利用し，モデルやMCPクライアントの改修は不要．

公開するツールは`karte_work_start/append/read/search/daily`．Strataからは`karte_worklog__karte_work_start`等の名前で利用できる．既存の`karte-mcp`は読取専用のまま維持する．

Strata自身のチャットだけで使う場合，MCPの開始・失敗・終了の書込みはモデルによる明示的なツール呼出しになる．Strata本体の全チャット・全ツールを自動観測するフックはこの変更に含まない．Piの推論プロバイダーとしてStrataを使う場合は，Piの実行側のイベントをPi拡張が自動観測する．Strata単体にも同じ観測保証が必要なら，次の変更で実行側にCLI observerを接続する．MCPの自己申告をホスト観測として扱わない．

## 日報と検索の性質

日報には当日の受信イベント，その日時点の最終状態，失敗，検証の記録，根拠への参照，最後の次の作業案を含める．新しいイベントがないrunning/paused作業も最終観測時刻とともに引き継ぐ．翌日の完了を過去の日報に混ぜない．対象日のイベント射影とSHA256をスナップショットとして保存し，同じ資料から同じ日報を作る．LLMを日報生成に使わないので追加の推論費用は発生しない．

作業検索は単語ごとのAND一致による初期実装．日報・作業記録のMarkdownはKarteの既存contextサービスでも検索・読取できることをテストする．これがRAGの保存・検索部分になる．学習効果を測るeval，ベクトル検索，次の作業の自動起票・実行は別の段階．記録が存在することと，モデルが改善したことを同一視しない．

初期上限: 原本1件2MiB，1作業4096イベント，4096作業，検索・集計する原本の合計32MiB，本文16KiB，次の案4KiB，根拠16件，Pi未送信キュー5MiB．上限に達した場合は明示的に失敗し，キュー・原本を消して継続しない．小規模の運用を想定し，全作業を読む検索・日報集計のため，蓄積が増えた段階で索引・アーカイブが必要．

## 検証

```powershell
go test ./internal/worklog ./cmd/karte-worklog ./internal/canonical ./internal/contextcore ./internal/contextcore/mcp ./internal/git
$env:KARTE_TEST_BIN = (Resolve-Path build/bin/karte-worklog.exe).Path
node --test integrations/pi/worklog.test.mjs
node integrations/pi/smoke-loader.mjs C:/path/to/pi.exe build/bin/karte-worklog.exe
python integrations/strata/smoke_worklog.py --strata-root E:/Strata --binary build/bin/karte-worklog.exe
```

重複・競合・並行追記・日付境界・持ち越し・過去日報の安定性・権限取消し・ルート分離・出所偽装の拒否・生成物の競合からの復旧・記録内容の無害化・既存context検索を確認する．Nodeテストは実際のCLIとの接続も含む．PiローダーとStrata MCP hubの確認にはモデル呼出しや稼働サービスへの変更を必要としない．アプリのサイト除外テストは`app_worklog_test.go`にあり，音声依存とfrontend assetsが整ったアプリのテスト環境で実行する．
