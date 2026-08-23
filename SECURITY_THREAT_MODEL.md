# Karte Renderer／App security threat model

## 1．目的と状態

この文書は，Karte desktop applicationにおけるRenderer，App，native runtime，filesystem，network，生成物のsecurity境界を定義します．対象は，画像／音声／PDF／CSV import，raw HTML，theme，Web Clip，preview iframe，site，PDF export，外部binary，native ASR model，Wails IPC，filesystem，symlink／TOCTOU，SSRF，配布artifactです．

機械可読な正本は[`security/threat-model.json`](security/threat-model.json)です．この文書は人間向けの判断根拠であり，status，owner，closure task，production／test evidence，App IPC method分類はJSONを正とします．[`security_threat_model_test.go`](security_threat_model_test.go)がschema，参照，evidence symbol，既知blocker，App exported methodの完全性を検査します．

このmodelは2026-08-23のT-007／T-018以後の共有treeを監査した結果です．`implemented`は列挙したcontrolとtestが現行riskを許容可能なlevelまで下げる状態，`partial`は一部経路だけがcontrolを利用する状態，`blocked`はreleaseまたはtrust判断に必要なcontrolが未実装の状態です．critical／highの残余riskは，具体的なclosure taskを持つ`blocked`として管理します．

## 2．security goalと非goal

security goalは次です．

- document，media，theme，network responseを，desktop App authorityと同一のtrustとして暗黙に扱わないこと．
- project root外のhost fileを，path traversal，symlink，ancestor swap，TOCTOU，browser local-file権限から読書きしないこと．
- 既存fileをcollisionやraceで置換せず，成功した永続化をdurableかつ決定的にすること．
- private network，loopback，link-local，metadata endpointへdocument由来のrequestを送らないこと．
- untrusted parser，browser，native modelによるCPU，memory，process，filesystem，network影響をboundedにすること．
- preview，site，PDFが同じactive-content／theme asset trust policyを共有すること．
- 配布treeの全file，native dependency，model，licenseを最終archive前に検証し，artifactの由来を追跡可能にすること．

非goalは次です．

- OS account自体を完全に掌握したattackerからsecretを守ること．ただし，同一userでpathをraceできるprocessによるApp固有のconfused-deputyは対象です．
- 任意binary format decoderのmemory safetyをKarte内のheader検査だけで証明すること．decoderは隔離とresource budgetで防御します．
- userが明示的に公開したstatic siteのcontent confidentialityを保証すること．ただし，意図しないhost file混入とstored active contentは対象です．
- `internal/themeasset.Discover`の成功だけをpreview／site／PDF runtimeの安全性とみなすこと．runtimeが同じvalidated snapshotを消費するまで保証しません．

## 3．asset

| ID | asset | 失敗時のimpact |
| --- | --- | --- |
| `ASSET-AUTHORITY` | Wails Appが持つfilesystem，process，network，screen，native API権限 | preview contentからhost authorityへ昇格する |
| `ASSET-HOST-FILES` | Karte data root外のhost file，credential，private image | 無断read，overwrite，PDF／log／siteへの混入 |
| `ASSET-PROJECT` | Markdown，board，media，CSV，metadata，Git history | 破損，置換，stale mapping，意図しない公開 |
| `ASSET-NETWORK` | loopback，private，link-local，metadata service | SSRF，credential取得，internal state変更 |
| `ASSET-OUTPUT` | preview，static site，HTML，PDF，transcript | stored script，非決定出力，秘密混入 |
| `ASSET-AVAILABILITY` | UI，import，renderer，ASR，recording | memory／CPU／process exhaustion，停止不能 |
| `ASSET-NATIVE` | ffmpeg，browser，sherpa／ONNX，native library | 同一user権限でcode executionまたはcrash |
| `ASSET-ARTIFACT` | 配布application，model，library，license，provenance | supply-chain改変，license欠落，rollback不能 |

## 4．attacker能力とtrust前提

| actor | 能力 | 代表入力 |
| --- | --- | --- |
| `ACTOR-DOCUMENT` | crafted inputを渡せるが，host filesystem権限は前提にしない | Markdown raw HTML，CSS，CSV，image，audio，PDF，model |
| `ACTOR-LOCAL-RACE` | 同一userとしてchecked pathやancestorをsymlink／別inodeへ差し替えられる | import source，project content，public tree，model path |
| `ACTOR-NETWORK` | DNS，redirect，HTML，image，CDN responseを制御できる | Web Clip，preview／PDF subresource |
| `ACTOR-ENVIRONMENT` | application launch environment，PATH，override variable，editable ASR configを変更できる | `FFMPEG_PATH`，`KARTE_PDF_BINARY`，model absolute path |
| `ACTOR-SUPPLY` | dependency，CI action，runner，build input，release stepの1つを改変できる | Go／npm／native dependency，workflow action，archive |

Project内のMarkdown，theme，custom CSS，CSV，Web Clip outputを自動的にtrustedとしません．OS dialogでuserが選択したpathは，選択の事実だけでrace-freeまたはsafe parser inputにはなりません．App自身が生成したassetも，publish後のeditable filesystem上ではidentityを再確認しない限りtrusted snapshotではありません．

## 5．trust boundary

| ID | boundary | crossing |
| --- | --- | --- |
| `TB-IPC` | webview JavaScript→Wails App | main frameとsame-origin child frameからexported method authorityへ移る |
| `TB-IMPORT` | OS path／encoded bytes→data root | 外部fileをdecode，stage，publishする |
| `TB-DATA-ROOT` | logical project path→host filesystem | lexical nameがopen，read，rename，replace対象になる |
| `TB-NETWORK` | public URL／DNS／redirect→Web Clip | remote byteとURLがMarkdown／assetへ変換される |
| `TB-PREVIEW` | Renderer HTML→iframe realm | raw HTML，theme，custom CSS，enhancerがDOMとして実行される |
| `TB-RENDERER-PROCESS` | HTML／local asset→browser process | local-file，network，process権限を持つ外部engineへ渡る |
| `TB-NATIVE` | audio／model→external／in-process native code | ffmpeg，sherpa，ONNX parserへuntrusted byteを渡す |
| `TB-ARTIFACT` | repository／dependency→release archive | build inputが利用者実行物へ変換される |

## 6．data flowと既存control

### 6.1 画像／音声／PDF importとserve

`Import*File`，legacy Base64，chunk sessionからのbyteは，[`app_media_import.go`](app_media_import.go)でkind別上限を受け，exclusive stageへstreamされます．native pathは`os.OpenRoot`，`Lstat`，open handle `Stat`，`os.SameFile`でsource identityを固定します．destinationはancestorを含めsymlinkを拒否し，O_EXCL tempをSyncしてhard-link no-replaceでpublishし，directoryをSyncします．imageはformat／extension一致，16384 dimension，40MP input，derived sizeを検査します．

[`app_media_serve.go`](app_media_serve.go)はprefixとextensionをallowlist化し，同じroot-confined identity検査，magic byte，固定MIME，`X-Content-Type-Options: nosniff`，`Cache-Control: private，no-store`を適用します．source／destination swap，ancestor symlink，collision，short write，Sync fault，image bomb，heap，range／headerの回帰testがあります．

残余blockerは，audio／PDFがpublish前に短いsignatureしか検査せず，最大512 MiBを後段のffmpeg／browser parserへ渡す点です．Karte processと同等のauthorityでparserを動かさず，CPU／memory／filesystem／networkを隔離する必要があります．

### 6.2 CSV

監査時点のlegacy flowは，`Stat→Open`，Base64全量decode，`csv.Reader.ReadAll`，caller pathの単純join，O_TRUNC saveで構成されていました．byte，row，cell上限，root-confined handle，identity pin，atomic durabilityがsecurity closure条件です．T-064がこの経路を移行中であるため，最終statusとevidenceは同taskのstable treeに合わせます．frontend previewのcell escapeはlocal XSS controlですが，filesystemとresource exhaustionを解決しません．

### 6.3 Web Clip

[`internal/clip/clip.go`](internal/clip/clip.go)のdefault HTTP clientはproxyを無効化し，dial時に全DNS addressを検査し，private／loopback／link-local／metadata rangeを拒否してから検査済IPへ直接接続します．redirectとasset URLも再検査し，decoded response byteを制限します．sanitizerはscript，iframe，object，form，SVG／MathML，event handler，unsafe scheme，private URLを除去します．生成pathはroot内へ限定し，exclusive write／moveとsymlink拒否を行います．

production Appはdefault clientを利用します．将来custom `clip.Service.Client`をproductionへ注入する場合は，validatorだけでなくdial-time IP pinningを同じcontractとして要求します．sanitized Markdownが後で手編集されraw HTMLを含む場合は，preview boundaryのriskへ再分類します．

### 6.4 raw HTML，theme，preview iframe

KarteRendererのcurrent pinはGoldmark unsafe rendererを使い，raw HTML保持をcaller contractとして明記します．`PreviewMarkdownForPath`の結果は[`frontend/src/utils/preview-frame.ts`](frontend/src/utils/preview-frame.ts)で`document.write`されます．`stripLegacyRemotePreviewAssets`は旧Mermaid／KaTeX fragmentだけを除去し，任意script，event handler，form，nested frame，remote URLをsanitizeしません．

`frontend/index.html`のpreview iframeはsandboxなしで，parentとsame-originです．同時に[`main.go`](main.go)は`App`全体をWailsへbindします．したがって，preview raw HTMLからparentのIPC authorityへ到達できることをcritical blockerとして扱います．現在のdrop，page session，timestamp，PDF snapshotはparentから`contentDocument`へ直接接続するため，sandbox attributeの追加だけではclosureになりません．opaque-origin sandbox，active-content sanitizer，狭いtyped message bridge，trusted top-frame IPC facadeを一体で移行します．

[`frontend/src/utils/custom-css.ts`](frontend/src/utils/custom-css.ts)はraw CSSをstyle文字列へ連結します．`</style>` breakout，CSS `url()` network load，resource exhaustionを拒否しません．legacy default themeもremote jsDelivrとactive scriptを含みます．[`internal/themeasset`](internal/themeasset)のportable discoveryはactive HTML，remote／file／data URL，unsafe CSS，symlink，sizeを厳格に拒否しますが，preview／site／PDF runtimeはそのsnapshotをまだ利用しません．詳細contractは[`THEME_ASSET_CONTRACT.md`](THEME_ASSET_CONTRACT.md)にあります．

MermaidはpreviewとPDFで`securityLevel: 'loose'`とHTML labelを有効化しています．untrusted diagram textを扱う間はstrict security levelとsanitized label policyが必要です．

### 6.5 static site

[`site_build.go`](site_build.go)はroot，content，metadata，public，backupのsymlinkを拒否し，checksum manifest，stage，atomic publish，rollback／recoveryを実装します．failureが既存publicとindexを保持するtestもあります．

一方，sourceはWalkDirのregular判定後にpathで`os.ReadFile`され，checksum後にRendererが同じpathを再openします．この間のfile／ancestor swapにsource identityを固定しません．file単位／build全体のbyte上限もありません．同じconfined byte snapshotをchecksumとrenderに利用し，validated theme snapshotをdeterministic prefixへcopy／rewriteするまでhigh blockerです．raw HTMLを公開するsite policyは，author-trusted modeまたはsanitized modeとして明示する必要があります．

### 6.6 PDF

`ExportPDF(html)`はIPCからraw HTMLを受け，remote Mermaidとmutable `katex@latest`をinjectします．`convertImageURLsToDataURIs`は`/image/`／`data/image/`文字列をdata rootへjoinし，通常の`Stat`／`ReadFile`でsymlinkとdot segmentを追います．有効画像であればroot外byteがPDFやdata URI logへ入る可能性があります．HTML sizeはwarningだけでhard failしません．

`exportHTMLToPDFWithRenderer`はtemporary HTMLをenvironment／PATHで選ばれたChromiumまたはwkhtmltopdfへ渡し，`AllowLocalFiles: true`を設定します．任意HTML，remote URL，local file，scriptを同時に許すため，local file disclosure，SSRF，exfiltration，resource exhaustion，mutable outputのcritical境界です．validated snapshotだけをnetwork-disabled engineへ渡し，hard input／output／time limitを設け，HTML／data URIのcontent logを削除する必要があります．

### 6.7 external binaryとnative model

[`internal/audio/decoder.go`](internal/audio/decoder.go)はffmpegを`FFMPEG_PATH`，PATH，common locationの順に選びます．command argument injectionは避け，PCM 256 MiB，stderr 64 KiB，short write，cancelを制御しますが，binaryのregular／non-symlink identity，version，hash／signature，sandboxを検査しません．PDF engineも`KARTE_PDF_BINARY`とPATHを信頼します．approved executable policyとprocess隔離がblockerです．

ASRはeditable `data/asr/config.json`を読み，absolute model pathを許可します．native service作成前の存在確認は通常の`Stat`で，symlink，swap，size，runtime hashを固定しません．bundled modelとnative libraryはbuild／artifact inventoryでhash化され，native smoke testもありますが，runtimeに外部modelを指定できる境界とは別です．trusted rootまたはexplicit hash authorization，identity pin，byte bound，isolated workerを要求します．thread，provider，idle，single-flight，lease，shutdown orderingのruntime budgetは実装済みです．

### 6.8 IPCとfilesystem

Wailsは`App`を1 objectとしてbindします．method単位capability，caller frame provenance，argument trust levelはありません．JSONの`ipcMethods`は全exported `*App` methodをsurfaceとcapabilityへ分類し，ASTでproduction method集合と完全一致させます．method追加時に分類を忘れるとtestが失敗します．これはinventory controlであり，whole-App binding自体を安全にするcontrolではありません．

media route以外のgeneral pathは同じconfinement primitiveを共有していません．`resolveContentPath`はlexical containmentだけで，existing symlinkまたはsymlink ancestorをcanonical handleとして拒否しません．Load，Save，Rename，Board，metadata，CSV，custom CSS，site，PDF imageなどを，operation別のconfined read，create-only，atomic replace contractへ移行します．T-062のactive transcript reservationは録音中のidentity競合を閉じますが，一般filesystem trust boundaryの代替ではありません．

### 6.9 artifact

[`internal/compliance`](internal/compliance)と[`cmd/licensegate`](cmd/licensegate)は，asset SHA-256，license evidence，native manifest，symlink，ancestor，untracked native file，platform exclusionをfail-closedでauditできます．[`cmd/buildmatrix`](cmd/buildmatrix)はcompliance evidenceをplatform treeへpackageし，CIはnative smokeとextracted artifact startup smokeを実行します．

監査時点では，final platform treeに対する`artifact-audit`がarchive／upload前のworkflowへ接続されていません．macOS packagingはad-hoc codesign検証を行いますが，notarization，Windows／Linux signature，archive checksum，provenance attestationはありません．GitHub Actionsはmajor tag，rolling releaseはforce-moved tagを利用します．T-069完了後のworkflowを再監査し，各platformでnative packaging後かつarchive前にartifact auditを実行します．CI actionはcommit SHAへpinし，immutable versioned releaseへsigned checksumとprovenanceを添付します．runtime-selected ffmpeg，PDF engine，external ASR modelはartifact inventory外であるため，runtime trust policyも別途必要です．

## 7．blocker register

| blocker | severity | closure condition |
| --- | --- | --- |
| `T-029-B1` CSV confinement | critical | bounded streaming，root-confined identity，atomic durable save，race／limit test．T-064 stable treeでstatusを再判定する |
| `T-029-B2` preview isolation | critical | sanitizer，opaque sandbox，typed message bridge，top-frame IPC facade，real-webview injection test |
| `T-029-B3` theme runtime | critical | legacy migration，validated snapshot接続，remote／file URL拒否，CSP，strict Mermaid |
| `T-029-B4` site source snapshot | high | checksumとrenderで同じbounded confined snapshotを利用し，assetをdeterministic publishする |
| `T-029-B5` PDF sandbox | critical | ambient local-file／network除去，validated asset embed，pinned engine，hard budget，SSRF／file test |
| `T-029-B6` external binary trust | high | executable identity／version／provenance検証とparser process isolation |
| `T-029-B7` native model trust | high | trusted rootまたはhash authorization，identity／size，isolated load |
| `T-029-B8` IPC capability | critical | whole-App bindをreviewed facadeへ縮小し，caller provenanceとpath contractを強制する |
| `T-029-B9` artifact publish | high | final-tree audit，commit-pinned actions，immutable signed release，checksum／provenance |
| `T-029-B10` parser isolation | high | malformed audio／PDF corpusに対するCPU／memory／filesystem／network isolationとtimeout |

blockerの一部だけを変更してstatusを`implemented`へ上げてはいけません．JSONに記録したresidual riskを`none`または`low`へ下げるproduction controlと決定的test evidenceが必要です．例えばpreview iframeへ`sandbox="allow-scripts allow-same-origin"`だけを追加してもsame-origin parent accessが残るためclosureではありません．

## 8．verificationと変更規則

focused gateは次です．

```sh
go test . -run '^TestSecurityThreatModel' -count=10
go test -race . -run '^TestSecurityThreatModel' -count=1
go vet ./...
go test ./...
git diff --check
```

`TestSecurityThreatModelInventory`は次をfail-closedで検査します．

- strict JSON decodeとEOF，`schemaVersion`，unique ID，referential integrity．
- 必須18 surfaceがflow，control，threatでcoverされること．
- severity，status，owner，closure task，residual riskが欠けないこと．
- critical／highでmedium以上の残余riskは`blocked`かつ具体的closure taskを持つこと．
- `implemented` controlがproduction symbolとtest symbolの両方を持つこと．
- evidence pathがrepository内のregular non-symlink fileで，Go ASTまたはTypeScript declarationとしてsymbolが実在すること．
- root packageの全exported `*App` methodが1回だけsurface／capability分類され，stale entryがないこと．

`TestSecurityThreatModelKnownBlockers`は行番号ではなく，HTML attribute，Go AST call，Go composite field，function-local string，TypeScript semantic pattern，workflow commandの有無で現在のblockerを固定します．markerが変わるとtestは自動成功せず，脅威status，residual risk，control，evidenceをreviewしてinventoryを更新するよう失敗します．

新しいApp exported method，import format，renderer engine，network client，native dependency，artifact stepを追加する変更は，同じPRでinventoryとverification evidenceを更新します．security documentだけを変更してproduction controlなしにstatusを引き上げてはいけません．

## 9．既存文書との関係

- [`THEME_ASSET_CONTRACT.md`](THEME_ASSET_CONTRACT.md)はportable theme packageの詳細path／URL／HTML／CSS contractとruntime blockerを定義します．
- [`RENDERER_DEPENDENCY_POLICY.md`](RENDERER_DEPENDENCY_POLICY.md)はRenderer module identity，immutable version，transactional update，contract testを定義します．
- [`RENDERER_MIGRATION_AUDIT.md`](RENDERER_MIGRATION_AUDIT.md)はPreview，Site，PDFのproduction call pathとRenderer ownershipを定義します．
- [`BUILD.md`](BUILD.md)はplatform build，native packaging，deployment targetを定義します．
- `compliance/*.json`，`bom.cdx.json`，`THIRD_PARTY_NOTICES.md`はdependency，asset，model，license evidenceを保持します．

これらの文書と本modelが矛盾する場合，runtime security statusは機械可読inventoryと現行production／test evidenceを再監査し，両方を同時に更新して解消します．
