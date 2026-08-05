# Karte v1.0 release criteria

この文書は，Karte v1.0.0を公開してよいかを判定する唯一のrelease gateである．
対象はKarte本体，固定されたKarte Renderer依存，配布archive，既存workspace dataである．

## 判定規則

1. Release candidateはcommit SHAと`v1.0.0-rc.N` tagで固定する．途中でcode，依存，workflow，配布物が変わった場合は新しいcandidateとして全gateを再判定する．
2. Gateの状態は`PASS`，`FAIL`，`BLOCKED`のいずれかとする．`PASS`にはcandidate SHA，実行日時，実行者，commandまたはworkflow URL，生成物を含む証跡が必要である．
3. すべてのrequired gateが`PASS`の場合だけ`v1.0.0`を公開できる．`FAIL`または`BLOCKED`が1件でもあれば公開しない．例外承認や口頭でのwaiverは認めない．
4. 自動testが成功しても，配布archiveからのinstall／launch smokeとworkspace restoreは省略できない．
5. 証跡はrelease issueまたはrelease PRへ集約し，この文書のEvidence記録templateを使用する．

## 対応platformと配布物

| Platform | 最低条件 | Required artifact |
| --- | --- | --- |
| macOS Apple Silicon | macOS 11以降，arm64 | `Karte-macOS-apple-silicon.zip` |
| macOS Intel | macOS 11以降，amd64 | `Karte-macOS-intel.zip` |
| Windows | Windows amd64 | `Karte-windows-amd64.zip` |
| Linux | Ubuntu 22.04互換，amd64 | `Karte-linux-amd64.zip` |

`latest-main-successful-build`は開発用rolling releaseであり，v1.0.0の配布証跡には使用しない．v1.0.0はimmutable tagとreleaseを使用する．

## Gate checklist

### 機能

| ID | Required criterion | PASS evidence | FAIL condition |
| --- | --- | --- | --- |
| F-01 | 新規workspaceでMarkdownを作成，編集，保存，再起動後に再読込でき，CSV／TeX／画像を含むpreviewが表示される． | 4 platformのsmoke記録と使用fixture． | いずれかのplatformでdata loss，保存失敗，preview不能が再現する． |
| F-02 | HTML／PDF export，Marp preview，Web Clip取込が固定Renderer contractで成功する． | Renderer dependency test，Karte contract fixture，実生成物． | contract差分，export失敗，未解決import，生成物欠落がある． |
| F-03 | UIの主要経路であるsidebar，editor，preview，search，board，modalがkeyboardとpointerで操作できる． | Frontend CIとFrontend E2Eのcandidate SHA URL，manual smoke記録． | 主要経路が到達不能，操作不能，crashする． |

### 安全

| ID | Required criterion | PASS evidence | FAIL condition |
| --- | --- | --- | --- |
| S-01 | Candidateに未解決のP0／P1 security issue，既知のdata corruption，秘密情報の混入がない． | Security review結果，secret scan結果，対象issue query URL． | 重大issueがopen，credential／token／個人dataがarchiveへ含まれる． |
| S-02 | Markdown import，Renderer asset，Web Clip，archive展開のpath traversalとsymlink脱出が拒否される． | 境界testとmalicious fixtureの結果． | workspace root外のread／write，外部fileのarchive混入が可能である． |
| S-03 | `govulncheck`とfrontend dependency auditを実行し，runtime到達可能なHigh／Critical脆弱性がない． | command，tool version，結果artifact，例外0件． | High／Criticalが残る，またはscanを再現できない． |

### 互換性

| ID | Required criterion | PASS evidence | FAIL condition |
| --- | --- | --- | --- |
| C-01 | 既存workspace fixtureをbackup後にcandidateで開き，Markdown，board，graph，assetを変更せず読める． | fixture hash，open／save／reopen結果，backup path． | 読込不能，自動破壊migration，file／metadata欠落がある． |
| C-02 | Karte Rendererは`go.mod`のtested pseudo-versionへ固定され，Renderer全testとKarte contract testが同じSHAで成功する． | Backend CIとDesktop Buildのdependency／contract step URL． | local replace依存，未固定commit，contract failureがある． |
| C-03 | Candidateが書いたworkspaceを直前のrollback buildでreadonly確認できるか，非互換変更には復元手順とbackupがある． | round-trip fixtureまたはdocumented migration／restore結果． | rollbackでdataを失う，復元手順が未検証である． |

### 配布

| ID | Required criterion | PASS evidence | FAIL condition |
| --- | --- | --- | --- |
| D-01 | Backend CI，ASR Audio CI，Frontend CI，Frontend E2E，Desktop Buildのcandidate実行が成功する． | 5 workflow URLとcandidate SHA．Path filterでskipされたworkflowはmanual dispatchまたは同一SHA rerunが必要である． | failure，cancel，skip，別SHAの結果が混在する． |
| D-02 | 4つのrequired artifactが同一candidateから生成され，非空で，SHA-256 checksumが公開される． | Artifact URL，file size，checksum manifest． | 欠落，0 byte，SHA不一致，candidate不一致がある． |
| D-03 | 各archiveをclean環境へ展開／installし，launch，workspace open，save，preview，export，終了を確認する．macOS runtime dependencyも検証する． | Platform別smoke logとscreenshot． | 起動警告を回避できない，runtime欠落，基本操作失敗がある． |
| D-04 | `wails.json`，tag，release名，release noteが`1.0.0`で一致し，license，既知の制約，install／upgrade／rollback手順が公開される． | Release draft URLと内容review． | version不一致，license／手順欠落，rolling tagだけで配布する． |

### Rollback

| ID | Required criterion | PASS evidence | FAIL condition |
| --- | --- | --- | --- |
| R-01 | Release前にworkspace backupを作成し，別directoryへのrestoreとhash照合を実施する． | Backup／restore command，manifest，hash結果． | restore不能，backup対象漏れ，原本上書きがある． |
| R-02 | 直前のknown-good build，checksum，install手順を保持し，v1.0.0と同じplatformで再取得できる． | Immutable rollback release URLとdownload確認． | rollback artifactがrolling更新，欠落，checksum不明である． |
| R-03 | Rollback trigger，判断者，連絡先，手順がrelease issueに記録され，30分以内に配布停止，60分以内にknown-good案内へ切替できる． | 時刻入りdry-run記録． | 判断者不在，停止／切替手順が未検証，時間条件を満たさない． |

## リリース手順

1. Release issueを作り，対象SHA，Renderer pseudo-version，責任者，予定時刻を記録する．
2. 対象SHAへ`v1.0.0-rc.N`を付け，5つのrequired workflowを同じSHAで実行する．
3. 4 platform artifactを保存し，checksum manifestを生成する．
4. 機能，安全，互換性，配布，rollbackの順にgateを実行し，証跡をrelease issueへ追加する．
5. 全gateが`PASS`であることをrelease責任者が確認する．1件でも`FAIL`／`BLOCKED`なら修正後に新しいcandidateを作る．
6. `wails.json`の`productVersion`とrelease noteを確認し，対象SHAへimmutable `v1.0.0` tagを付ける．
7. 4 artifact，checksum，license，既知の制約，install／upgrade／rollback手順をGitHub Releaseへ公開する．
8. 公開artifactでplatform別post-release smokeを行う．Rollback triggerに該当した場合は即時にrollback手順へ移る．

## Rollback判定

次のいずれかを確認した時点で新規downloadを停止し，R-02のknown-good buildへ切り替える．

- Workspaceの消失，破損，意図しない書換え．
- 任意のrequired platformでinstall／launch／open／saveが不能．
- Credential漏洩，workspace root外access，remote code executionにつながるsecurity defect．
- HTML／PDF exportまたはRenderer contractの広範なfailure．
- Artifactとchecksum，tag，source SHAの不一致．

Rollback時はreleaseを削除して証跡を失わず，downloadを停止したうえで警告を明示する．Incident issueへ影響範囲，検知時刻，停止時刻，known-good切替時刻，data restore要否を記録する．

## Evidence記録template

```md
Candidate: <commit SHA>
RC tag: v1.0.0-rc.N
Renderer version: <go.mod pseudo-version>
Release owner: <name>

| Gate | State | Evidence URL／artifact | Executed at | Executor | Notes |
| --- | --- | --- | --- | --- | --- |
| <Gate ID> | BLOCKED | | | | |
```

Release issueにはF-01からR-03までの全行を作り，空欄のgateを`PASS`として扱わない．
