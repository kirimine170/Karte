# ADR-0005：Runtime会話・日記を限定policyで採用する

## Status

Accepted／Step 3 implemented，2026-09-12．Karte の限定採用・受信復旧・版付き read を実装し，隔離した合成データで検証する．実機受入，Runtime の Step 4，human control の Step 6 は別に管理する．

## Context

現行V1.1はEphyの`propose`とlocal humanの`review`を検査し，採用処理だけが`SaveFile`へ進む．RuntimeのDeveloper Modeによる自動送信もpendingへの公開までであり，自動採用ではない．記録有効の会話を毎turnのクリックなしで残すには，認可・採用来歴・復旧の契約を明示的に拡張する必要がある．

## Decision

[Runtime会話・要約・日記の保存契約 v2](../KARTE_RUNTIME_DIARY_V2.md)を採用する．Karteが正本，安定ID・版，scope，policy，依存失効，receiptを所有し，Runtimeは確定eventの一時保全と配送，ローカル要約・日記Job，読取利用を所有する．会話原記録とAI解釈を別文書にし，元doc ID・版・turnへ戻れるようにする．

ADR-0001とADR-0003の「常にhuman review」という前提を，新v2で登録したactor・scope・操作・記録種別に限って置換する．旧v1 human reviewは変更しない．ADR-0004の単一policy owner，非開示，保存と学習の分離は継続し，exact actor ID，producer credential，consent epoch，採用主体・policy revisionを加える．proposal自己申告やLLMの文言で権限を得られるようにしない．

初期の自動操作は新recordのcreate，会話event append，未編集派生物のrevision更新に限る．削除・訂正・scope変更は明示したhuman操作intentを別途検証する．既存文書への任意patch，分類外の資料，Web調査全般を自動採用へ拡げない．

v2の有効化前に，canonical writerの競合防止とatomic保存を整える．既存prepared／saved transactionを拡張し，正本保存後receipt前の停止で二重writeを起こさない．この前提はKarte #231／PR #268と重なるため，実装時の最新状態を確認して必要部分を再利用する．未統合stack全体をStep 0で取り込まない．

## Consequences

- source revision，訂正，削除，派生失効はKarte #301／#305／#306の会話記録への限定適用になる．#304のpolicy取込み基盤を共有できるが，公開調査結果の受入完了を主張しない．
- 新protocol／schema／fixtureの正本はKarteに置き，Karte側の契約とconsumerを先に統合する．Runtimeはその確定版をmirrorし，旧clientとの非互換を検出する．
- Runtimeの保全物を第二の記憶DBへ発展させない．Karteが履歴と依存を所有し，Runtime受領済みcacheに有限の保持規則を適用する．
- 自動保存領域からのVCS同期・export・学習は保存同意から推論しない．本文をmetadata-only auditや評価exportへ追加しない．
- rollbackは新producer停止→未配送保全→v2領域の旧client access遮断→旧binary復帰の順とし，新文書・取引・tombstoneを削除して戻さない．

## Verification

Step 3のgateはv2契約の共有fixture，許可・取消・偽actor，ID再利用，canonical保存各境界のcrash，human edit，旧review回帰である．詳細とfixture名は契約正本に集約する．Step 0は文書整合と現行v1基盤の検証のみを行う．

## Step 3 の具体化

`internal/canonical`の共通 writer に通常 editor と v2 採用を接続し，受付・確定直前の現在 policy 検査，event／candidate ledger，canonical 保存後の receipt 復旧を実装した．PR #268 の非破壊競合検出・fault test を限定再利用し，ASR／media／background job を含む stack 全体は統合していない．

scope 設定と鍵は data root・Git の外の human 管理領域に置く．privacy の正本は既存 v1 policy のままとし，v2 grant と積集合で判定する．設定 command も canonical と同じ lock を使う．新 record を v1 AI context，site build，自動 Git commit へ流さない．現在 policy による UI read と，人の編集による自動更新停止を追加した．

[実装・設定・検証手順](../RUNTIME_RECORDS_V2_SETUP.md)を追加した．公開 capabilities は create／append／derivation 更新／search／read のみであり，未実装の削除・期限 purge を広告しない．音声記録の開始は，C1 Step 2 の中断・再生状態の契約と Runtime Step 4 の reader／queue／設定が揃った後に限る．
