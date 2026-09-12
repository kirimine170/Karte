# Runtime会話・要約・日記の保存契約 v2

2026-09-12．Step 0で採用した設計であり，v2の実装・有効化・受入完了を表さない．この文書をKarte所有のprotocol／policy／共有fixture方針の正本とする．判断は[ADR-0005](adr/ADR-0005-scoped-runtime-diary-adoption.md)，段階と検証状況はRuntimeの`docs/runtime-diary/IMPLEMENTATION_PLAN.md`／`STATUS.md`に置く．

## 1．適用範囲と版

対象は，利用者が開始し，記録を有効にしたローカル会話，その要約，Ephy視点の日記，それらの訂正・削除・アクセス制限である．公開調査の取込み，学習，外部送信，多Worker，全既存文書のdoc_id移行は対象外である．

| 契約 | 現行 | 新設する契約とowner |
|---|---|---|
| mutation | `schemas/karte-ephy/v1`，`schema_version=1.1`，human review | `schemas/karte-ephy/v2`，`schema_version=2.0`．Karte |
| search／read | `schemas/karte-context/v1`，`protocol_version=1.0` | `schemas/karte-context/v2`，`protocol_version=2.0`．Karte |
| 会話本文と出典 | 任意frontmatter＋Markdown | v2内の`record.schema.json`／`source-reference.schema.json`．Karte |
| policy | v1のactor type単位のprivacy policy | v2の明示的なactor ID・保存領域・操作・記録種別を加えたpolicy．Karte |
| Runtimeの未配送・Job状態 | 新契約なし | `schemas/runtime-diary/v1`の予定．Runtime内だけの配送・実行状態 |

Step 3で上記Karte schemaと同じ意味のGo型を実装する．Step 0では稼働中schemaを変更しない．v1の`additionalProperties:false`，Goの`DisallowUnknownFields`，Runtimeの型検証を維持する．optional追加でもv1へ混ぜない．frontmatterがcustom fieldを保持できることは，旧clientが新しい認可や失効を理解することを意味しない．

v2は`.mdsys/ephy/outbox/v2/{pending,accepted,rejected,receipts,transactions}`と`.mdsys/context/v2/{requests,responses,processed}`を使用する．現行v1のdirectoryは移動しない．Karteが読取可能なcapabilities応答へprotocol，record schema，利用可能操作，policy revisionを返す．Runtimeはv2応答を確認してから配送する．v1専用serverは新directoryを処理しないため，client側で`unsupported_protocol`を表示し保全する．v1への自動変換・自動review代行をしない．新serverに不明版を送った場合も明示的に拒否する．

新しいrecordはKarteのMarkdown正本で閲覧・編集できる．v1 contextのAI検索・readからは新recordを除外し，新しい失効・scope契約を通るv2 clientへだけ返す．既存文書のv1検索・reviewは継続する．旧Karte binaryへ戻すと新recordの失効を理解できないため，rollback時はv2記録領域への旧Ephy actorのアクセスを停止する．

Runtimeの旧direct filesystem adapter／generic RAG indexも新recordを取込み対象から除外する．v2失敗時にその経路で本文を読むfallbackを作らない．Karte内の通常UI閲覧も現在のscope・失効状態に従う．

Step 3の検証はsynthetic専用scopeで行う．実会話のv2自動保存は，Step 4で対応Runtime reader・旧direct index除外・記録設定が揃ってから有効にする．新Karteだけを先に配布した状態で既存の実データへ自動採用を開始しない．

## 2．安定IDと人間可読な正本

### IDの役割

| 名前 | 生成・意味・変更規則 |
|---|---|
| `conversation_id` | Runtimeが会話作成時にランダムUUIDを1回発行する．記録ON後は永続化する．再起動・pause・model変更で変えない．明示的な新会話で変える |
| `session_id` | マイクを開始してから終了する1回の音声session．再起動後は新しいID．会話IDと区別する |
| `turn_id` | ユーザー発言を受け付ける単位．音声とtextで同じ規則．回答の再試行は元turnを参照する |
| `event_id` | 確定入力，assistant結果，再生状態更新，訂正，終了の各eventにUUIDを発行する．永続保全前に確定し，再送で変えない |
| `event_seq` | 同一会話の単調増加整数．1つのRuntime writerが割り当てる．欠番を正常な会話完了として扱わない |
| `event_revision` | 初版1．訂正は新event IDから元event IDと対象revisionを参照し，対象の有効revisionを増やす．原文の上書きはしない |
| `operation_id`／各revision | 回答試行，ASR segment，generation，playbackのcallback排除用．event IDやcanonical版の代わりにしない |
| `doc_id` | Karteが`scope_id＋producer_instance_id＋logical_record_key`から決定的に発行する．candidate IDやfilenameから会話identityを作らない |
| `revision`＋`sha256` | Karteの文書版と保存後のcanonical UTF-8全bytesのSHA-256．版は単調増加し，hashは同一内容でも版の代用にしない |
| `candidate_id` | 1つの変更試行のID．再送は同一bytes・同一ID．base変更時は新candidateにし，元event IDを引き継ぐ |

`scope_id`はKarte側で登録した利用者・案件・保存領域の組を指すopaque IDである．actor IDやproject名だけを他ユーザーの記録との分離に使わない．同じ会話の別scopeへの無断移動を禁止する．

### 記録単位

原記録は`kind=note`，`record_type=conversation`．要約は`kind=note`，`record_type=summary`．日記は既存の`kind=journal`，`record_type=ephy_diary`を使う．`ephy:conversation`等のtagは表示・検索補助であり認可の根拠にしない．利用者のjournalとEphy日記は`authorship=ephy`とAI作成表示で区別する．既存journalを一括変更しない．

正本は既存配置規則に従い`content/projects/<configured-project>/<kind>/<YYYY-MM>/<safe-name>.md`へ置く．保存先はKarteで設定したscopeから解決する．LLMがproject・kind・sensitivityを決めて自動採用する経路にはしない．分類未設定は`configuration_required`である．

会話は1文書へのevent単位appendを基本とし，日付変更または256 events／canonical 1 MiBのいずれかに達する前に新segmentへ分ける．1つのeventは最大64 KiBのUTF-8 textとし，受信側で正規化後bytesも検査する．境界で文を黙って切らない．収まらなければ明示的な上限エラーにする．`logical_record_key=conversation:<conversation_id>:<segment_no>`で複数文書を同じ会話へ結び，日付境界を越えるturnは確定入力のlocal日付へ帰属させる．assistant結果は同じturnに結び，日付だけで別会話にしない．

Markdownはfrontmatterに識別・版・scope・record type，本文にevent anchorと話者・原文・状態を持つ．eventの構造はreserved metadata blockとして機械可読に保持し，本文を二重に埋め込まない．任意発言内のfrontmatter，HTML comment，コードフェンスやanchorはデータとしてescapeし，偽eventとして解釈しない．Karteがrender／parseのround-tripを所有する．既存`internal/frontmatter.FrontMatter.Raw`のcustom field保持を再利用し，新情報の欠落・二重化をfixtureで検査する．

以下は実装前のsynthetic例である．短いIDやhash記号は説明用であり，有効schema fixtureでは実UUID・実計算hashに置換する．

```yaml
doc_id: <karte-derived-uuid>
project: synthetic-diary
kind: note
sensitivity: internal
tags: [ephy:conversation]
runtime_record:
  schema_version: '2.0'
  record_type: conversation
  conversation_id: <conversation-uuid>
  scope_id: <scope-uuid>
  producer_instance_id: <instance-uuid>
  segment_no: 1
  revision: 3
  timezone: Asia/Tokyo
  local_date: '2026-09-12'
```

```text
event e1／turn t1／seq 1／revision 1／user／input_kind=asr_final
利用者：次回は冬の観測を相談したい．

event e2／turn t1／seq 2／revision 1／assistant
確定した生成本文：夏と冬の観測について説明します．
generation=canceled／display=confirmed_prefix
playback=interrupted／unit 1 complete／unit 2 started，completion unknown

event e3／turn t2／seq 3／revision 1／user／input_kind=asr_final
利用者：いや，冬の話だけ聞きたい．
```

原記録にはASR finalであること，provider／model revision，final revisionを付ける．認識結果を本人の正確な発言だと保証しない．訂正は`corrects={event_id,event_revision}`，訂正者，訂正理由区分，訂正後textを保持する．本人の内容訂正とASR誤認識の訂正を区別する．UIからの自由編集はhuman revisionとして記録し，reserved metadataが破損した場合は検索・自動追記を止める．

assistant記録は生成完了・中断・失敗，確定本文，表示した確定範囲，SpeechUnitごとの再生開始／自然終了の観測を区別する．生tokenのpreview，reasoning，未確定断片を確定回答へ昇格しない．音響的な単語位置は記録しない．終了callbackがなければ`unknown`であり完了と推定しない．TTS用の読み正規化・辞書・voiceは原記録を変更しない．

## 3．出典と派生物

`source_refs`の構造化要素は`doc_id`，`revision`，`sha256`，`conversation_id`，対象`turn_ids`／`event_ids`と各有効event revisionを必須とする．pathは表示用locatorに限る．複数segmentならすべて列挙する．summary／diaryの`logical_record_key`はそれぞれ`summary:<conversation_id>:<scope_id>`，`diary:<producer_instance_id>:<scope_id>:<timezone>:<local_date>`とする．再生成は同じlogical keyの新revisionであり，新しい日記を重複createしない．

派生物は`derivation={kind,input_refs,model_id,model_revision,template_id,template_revision,generated_at,job_id}`を持つ．本文中の根拠範囲を`observed_utterance`，`user_report`，`ephy_interpretation`へ分類する．「疲れたと言っていた」と「無理をしているように感じた」を同じ確定事実にしない．本人の申告を外部検証済みにしない．要約・日記を自己入力した回数を独立根拠・検証回数として数えない．

Karteが会話の過去revisionを自分のprivate revision storeへ保持し，v2 readは`doc_id＋revision＋expected_sha256`でその版を取得できる．current MarkdownとKarte所有revision storeが正本を構成し，Runtimeに独立した版DBを持たせない．最初の対象は新recordだけであり，既存全文書の履歴移行を要求しない．過去版にも現在の閲覧policyと削除状態を適用する．sourceが変更済みなら旧版を読めても新派生物の自動採用には使えない．

v2 search／readは既存Context serviceを拡張し，`record_type`，scope，timezoneに基づく日付範囲，revision，source refs，`active/stale/superseded/deleted`を扱う．Karteで依存を検査し，stale派生物は既定の回答候補から除外する．v1の返却値にfieldを追加しない．検索時とread時で許可を再検査し，denied／missingの非開示を維持する．

## 4．policyによる採用

Karteが管理するpolicyには次を必須とする．初期値はdisableであり，既存Developer Modeのauto-submit設定を自動採用の同意へ読み替えない．設定済みの利用者同意と既存保存先を引き継げる場合も，この狭いscopeとの対応を確認して使う．

| 要素 | 採用する制約 |
|---|---|
| identity | `policy_id`，単調増加`policy_revision`，`enabled`，利用者ID，actor type=`ephy`，登録したactor IDとproducer instance ID |
| resource | 明示した`scope_id`，project，保存領域ID，`record_type` allow-list，kind，sensitivity，必要／禁止tag，source provenance type |
| operations | `create_record`，`append_events`，`revise_derivation`だけを自動許可できる．削除・scope変更・他文書のreplaceは含めない |
| freshness | `valid_from`，任意の`expires_at`，`consent_epoch`，scope generation．設定変更ごとにrevision／epochを更新 |
| producer boundary | human設定から発行するlocal producer credentialと登録IDを結び付ける．LLMやJSON中のactor／policy IDだけでは認可しない |
| independent permissions | `storage=true`と`training=false`，`external_transfer=false`を分ける．保存先のGit remote，公開site build，exportも自動許可しない |

ローカルtransportのcredentialはGit外の0700 directory／0600 fileへ置き，proposal本文へ含めない．descriptorのcanonical JSON表現と本文hashを含む署名対象を固定し，登録鍵によるMACをKarteで検査する．ローカル同一OSユーザー全体を強い隔離境界と主張せず，LLMが利用できるtoolからpolicy／鍵の作成・更新・参照を隔離する．exact bytes，配列順，UTF-8，改行，数値，重複JSON key拒否をfixtureで固定する．secret・MAC鍵は共有fixtureに入れず，テスト専用鍵だけを使う．

既存privacy policyは引き続き`Policy.Authorize`の単一判定へ通す．v2の自動採用grantはその上に追加する制限であり，v1のproject／sensitivity／tag拒否を緩和しない．両protocol向けに独立した編集可能privacy policyを複製しない．v2専用grantはKarteのprivate設定に置き，現在privacy判定との積集合で評価する．

受付時と確定直前に，同じKarte認可serviceで現在policy，credential，scope，consent epoch，sourceのcurrent revision／hashを検査する．受付後に取り消された許可でcommitしない．policy更新とcanonical mutationは同じ直列化境界で順序付ける．既にcommitした後の取消は，そのcommitをなかったことにせず，現在権限を反映したreceipt応答と削除／制限処理へ接続する．

human reviewは既存経路を保つ．採用来歴は`adoption.mode=human_review`＋reviewer，または`adoption.mode=policy`＋登録actor＋policy ID／実際の判定revision＋decision codeで区別する．policy採用で`local-human`を偽装しない．payload自己申告の来歴をKarteが再利用せず，確定時に付与する．分類外は自動的に範囲を拡げずdeny／review_requiredとする．

明示訂正・削除・アクセス制限はhumanの操作intent IDと対象ID／版を持つ別の認可済みcontrol要求とする．毎turnの採用クリックを復活させない．会話中の「保存しないで」は今後の記録停止として実行し，曖昧な範囲の削除まで拡張しない．

## 5．永続化，冪等性，競合

Karteの正本変更は同じ保存serviceを通す．Step 3の前提として，現行`App.SaveFile`の競合検出前writeと単純`os.WriteFile`を解消する必要がある．Karte #231／未統合PR #268のatomic-save実装を再照合し，採用可能な最小部分とそのfault testを利用する．PR stack全体を無条件にmergeする依存にはしない．UI保存と自動採用が共通のlock／CASを使い，canonicalの最終bytesを確定してから同一filesystemのtemp，file fsync，atomic replace，directory fsyncを行う．一般の編集・VCS競合も失敗時に旧本文を破壊しないことをgateにする．

1．署名・schema・payload hash・現在policyを検査する．同じcandidate IDに異なるpayloadは，receiptが既に存在しても`id_reuse`として拒否する．
2．同じscope／event IDが同じ内容で既に適用済みなら既存効果へ結び，同じeventをappendしない．同じevent IDで異なる内容は訂正扱いにせず拒否する．
3．Karte writer lock内でbase revision／hash，doc ID，scope，source依存，policyを再検査する．任意の人の編集を自動上書きしない．createは別doc IDの同名fileを既存collision suffix規則で回避する．
4．正規化後のexact canonical bytes，base／result hash，event IDs，採用来歴を`prepared` transactionへdurable保存する．正本へ書く前に復旧情報を確定する．
5．正本，Karte所有revision／依存・event ledgerをtransactionから再構築可能な形で確定する．index未反映なら新しいreadを安全側で止め，旧cacheを新しい版として返さない．
6．`saved`を記録し，receiptをatomicに確定する．receiptにはcandidate ID，proposal hash，applied event IDs，doc ID／revision／result hash，採用来歴，statusを含める．本文・音声を入れない．
7．pendingをarchiveしtransactionを終了する．このcleanupが失敗しても同じcandidateの効果を再実行しない．

現行のprepared／saved transaction復旧を拡張し，起動時と再送時に自動走査する．preparedで正本がbaseのままなら現在policyを再検査して実行できる．expected resultと一致すれば再writeせずledger・receiptの復旧だけを行う．baseにもresultにも一致しなければ`conflict`として原本を保ち，人の解決を待つ．saved後に人が編集した場合も過去commitのledgerを消さず，そのreceiptと現在版を区別する．削除後のreceiptは本文やpathを漏らさない最小の適用済み／削除済み結果を返す．

| 障害位置 | Runtimeの表示・復旧 | Karteの確定条件 |
|---|---|---|
| Runtime保全前のdisk failure | `save_failed`．保存済みと表示しない．記録をpauseして既存データを保護 | proposal未送信 |
| Runtime保全後，publish前に停止 | `locally_preserved`から次回起動時再開 | 未確定 |
| Karte停止／publish後receiptなし | `delivery_pending`．同一candidateを有界backoffで再送 | pendingは保存済みではない |
| prepared後，正本write前に停止 | 保全継続 | 起動時にbase／policyを再確認 |
| 正本replace直後，saved／receipt前に停止 | `delivery_pending`のまま | exact resultを照合し再writeせず復旧 |
| receipt取得後，Runtimeのack保存前に停止 | receiptを再取得し同じeventをack | 追加writeなし |
| stale base／human edit／不一致hash | `conflict`．自動retryでは変更しない | 原文を保持．再計画には新candidate |
| policy取消／削除 | `permission_blocked`または`deleted`．旧epochの再送・Jobを停止 | 取消後の未commit分を不採用 |

## 6．保持・訂正・削除

Runtime保全物は配送のためのものだけであり，独立した検索DB・編集可能な記憶正本にしない．Karte受領前はTTLで本文を捨てない．上限到達時は新しい記録受付をpauseし，書込み失敗を明示する．既存会話を黙って削除しない．

既定の保持は次のとおりとする．明示済み設定があればその適用可能性を確認して優先する．

| 対象 | 既定 |
|---|---|
| raw audio／ASR partial／reasoning | diskへ記録しない．captureとASRの有限メモリのみ |
| Runtime未配送本文 | 最大256 MiBまたは10,000 events．自動期限削除なし．80%で予告，満杯で記録pause |
| Runtime受領済み本文cache | receiptのdurable ackと読戻し照合後，最大24時間／64 MiBの早い方で削除．権限変更・明示削除は即失効 |
| Runtime Job queue | 最大128 jobs／16 MiBのdescriptor．入力本文はKarteから必要時取得．満杯なら生成延期を表示 |
| Karte正本・許可された過去revision | 明示削除まで．容量不足は保存失敗として返す．原文の自動忘却を既定にしない |
| Karte accepted/rejected proposal本文 | 終了後24時間以内にpurge可能．未解決transaction本文は復旧または明示削除まで保持 |
| receipt詳細 | 30日．以後もKarteの最小idempotency ledgerから重複判定・適用結果を再取得できる |

記録OFFは以後の新規event保存を止める．OFF前に保全したeventは当時の同意が現在も有効なら配送できる．OFF中のassistant結果・音声・textを後からまとめて保存しない．記録済みturnに対しては本文なしの区間終了状態だけで整合を閉じる．再ON時に記録空白を明示し，OFF区間はbackfillしない．pause／session endと記録OFFは別の状態である．

訂正は元eventを保持した新revision，削除は対象会話／日付／文書と派生物へのtombstoneとする．Karteが依存関係に基づきsummary／diaryをstaleまたはdeletedへ変更し，次のsearch／readから除外する．Runtimeは未配送・cache・生成中・生成待ち・生成済み未配送へ同じ変更を適用する．拒否後にIDを変えて再送しない．

削除はKarte管理のcurrent本文・過去版・proposal本文・派生本文・indexを消し，Runtimeの対応本文も消す．冪等性に必要な本文なしID／scope generation／削除epochを残し，古い再送・index再構築で復活させない．Karteは管理対象のbackup／VCSへの書込み・同期を自動保存policyと分離し，新領域の本文を未許可のGit履歴へ複製しない．既存の利用者管理backup等を消去できない場合は削除範囲を明示し，全複製消去を保証しない．

## 7．共有fixtureの実装計画

Step 3でKarteのv2 schema directoryへsynthetic JSONと期待結果を置き，Runtimeの`scripts/check_karte_contract.py`を拡張してbyte-for-byte照合する．各schemaはunknown field・trailing JSON・重複key・不明版を拒否する．旧15 JSON fixturesは変更せず併走する．schema検査だけでなく，Go／Pythonで同じsemantic outcomeを要求する．

| fixture名の予定 | 入力／期待結果 |
|---|---|
| `conversation-user-final` | e1を回答前にcreate．ASR finalの来歴と原文が残る |
| `conversation-8-messages` | 4 user＋4 assistantを別eventで追記．再送後も各1件で全件read可能 |
| `interrupted-answer` | unit 1完了，unit 2開始後取消，訂正発言e3．旧全文を聴取済みにしない |
| `correction-revision` | e1 rev1のASR訂正と本人内容訂正を区別．参照中の日記はstale |
| `derived-summary-diary` | 同じ原記録に対する要約と一人称日記．source ID／版／turnと推測区分を保持 |
| `policy-allowed-denied` | 登録actor／scopeのみ採用．偽actor，tagだけ，機密区分外，期限切れ，学習・外部送信はdeny |
| `policy-revoked-before-commit` | 受付後のepoch更新でwriteゼロ．saved後取消は過去commitを再実行しない |
| `duplicate-candidate-event` | 同payloadの再送は1効果．別payloadのcandidate／event ID再利用はreceipt既存時も拒否 |
| `crash-at-each-boundary` | prepared／replace／ledger／saved／receipt／archiveの各直後停止．再起動後のcanonical bytesと件数が一致 |
| `human-edit-collision` | 同名別doc IDはsuffix．base変更や日記のhuman editはconflictで旧本文保持 |
| `raw-text-escaping` | 発言にYAML，HTML comment，偽anchor，コードフェンス，Unicodeを含めても1eventとしてround-trip |
| `scope-delete-restart` | 削除・権限制限後に旧Job，旧proposal，旧検索cache，過去revision，reindexから復活しない |
| `version-compatibility` | v1 human review継続．新client＋旧serverは非対応，新recordをv1 AI readへ流さない |
| `retention-and-capacity` | 満杯・disk failure・24h cache purge・receipt詳細purge後の冪等性が明示した結果になる |

同じevent集合・hashで複数言語のrenderが食い違う問題を避けるため，canonical Markdownの生成はKarteだけが行う．Runtimeはtyped eventと本文を送る．receipt検証はKarteが返す実際のcanonical hashで行い，Runtime側renderの予測hashで置換しない．

## 8．最初のfixtureの具体例

以下は`conversation-user-final`のevent payloadに使うsyntheticデータである．実データではない．Step 3でschema fixtureへ移すまで，本節をフィールドと期待値の例とする．v1へ送信可能という意味ではない．

```json
{
  "conversation_id": "11111111-1111-4111-8111-111111111111",
  "scope_id": "22222222-2222-4222-8222-222222222222",
  "producer_instance_id": "33333333-3333-4333-8333-333333333333",
  "event_id": "44444444-4444-4444-8444-444444444444",
  "event_seq": 1,
  "event_revision": 1,
  "turn_id": "55555555-5555-4555-8555-555555555555",
  "event_type": "user_final",
  "input_kind": "asr_final",
  "text": "次回は冬の観測を相談したい．",
  "occurred_at": "2026-09-12T00:00:00Z",
  "timezone": "Asia/Tokyo",
  "local_date": "2026-09-12",
  "asr": {
    "provider": "synthetic",
    "model_revision": "fixture-1",
    "final_revision": 3
  },
  "consent_epoch": 1
}
```

event payloadのhashは，各objectのASCII schema keyを昇順に並べ，配列順を維持し，余分な空白・末尾改行なし，UTF-8，非ASCIIの不要なescapeなしのJSON bytesで計算する．数値は整数のみ，重複key・NaN・不正UTF-8を拒否する．textのtrim・Unicode正規化・読み変換はしない．この例のSHA-256は`01065eadd62df6a0af52210def3c81f633017119375175799a5a29e56f491f46`である．Pythonの`json.dumps(event, ensure_ascii=False, separators=(',', ':'), sort_keys=True).encode('utf-8')`で再計算した．GoのHTML escape既定や末尾改行を混ぜないbyte fixtureを追加する．

v2 proposal envelopeには`schema_version`，`candidate_id`，`operation`，`logical_record_key`，`scope_id`，`actor`，`policy_id`／`policy_revision`／`consent_epoch`，`target={doc_id,revision,sha256}`，`events`または`derivation`，`created_at`，`auth={key_id,mac}`を持たせる．createのtargetはnull，append／派生更新は現行targetを必須とする．1proposalは1eventまたは1派生revisionとし，並べ替えと一部成功を避ける．`auth.mac`を除くenvelopeを上の規則でencodeしたbytesがproposal hashとHMAC-SHA-256の入力になる．receiptのproposal hashはこの値を参照する．event hash・proposal hash・canonical hashは別物である．

このeventを許可内で2回送るとcanonical event数は1のまま，同じ適用結果を返す．textだけ「夏」に変えて同じevent IDを再利用した場合は`id_reuse`となる．正式な訂正は新event ID，`event_type=correction`，`corrects.event_id=44444444-4444-4444-8444-444444444444`，`corrects.event_revision=1`を指定し，元eventを残して有効revisionを更新する．そのsource版を参照する旧summary／diaryはstaleとなる．
