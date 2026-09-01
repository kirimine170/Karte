# ADR-0003：KarteがPersonal Context Coreを所有する

## Status

Accepted．実装中．

## Context

Karte–Ephy V1.1は，EphyがKarteのcanonical Markdownをread-onlyで走査し，reviewed outboxへcreate／append proposalを書く境界を採用した．この境界で誤書込は防げたが，検索index，ranking，document read，privacy scopeがEphy側に残るため，Karteを単なる保存先として扱っている．人間とEphyがPersonal Contextを共同操作するには，canonical identityと検索・読取の意味を同じ所有者へ集約する必要がある．

## Decision

Karteは，canonical Markdown，安定した`doc_id`，project／kind／tag，relations，provenance，sensitivity，search／read，reviewed mutation，receiptを所有するPersonal Context Coreになる．Ephyはconversation，task／goal，tool execution，working memory，permission promptを所有し，Karte Context Protocolのversioned clientとしてPersonal Contextを利用する．

Context Protocolは，local filesystem上のatomic request／response spoolをV1 transportとする．Skillsは検索・保存方針を記述するがdata transportにはしない．MCPは将来の薄いfacadeとして許可するが，canonical implementationやdata storeにはしない．localhost HTTPを必須にしない．

検索V1はKarte内のdeterministic lexical baselineから始め，embedding／vector providerは差替可能にする．これにより，private／offline環境でread pathを先に完成させ，Qdrant等をprotocolのblockerにしない．Restricted dataはprivacy ADRとsecurity matrixが完了するまでdefault denyとし，Internal synthetic corpusで実装を進める．

## Consequences

- Ephy内のdirect filesystem scanはmigration fallbackであり，正式なPersonal Context read pathではない．
- search resultとread resultは`doc_id`，canonical SHA-256，scope，provenanceを保持する．
- 人間UIとEphy clientは同じContext Coreを利用する．
- query本文，document本文，個人情報を通常logへ保存しない．
- protocol変更はKarteを先に更新し，ephy-runtimeのschema／fixture／contract testを同期する．
- canonical writeは既存reviewed outbox以外へ拡張しない．move／rename／deleteはV1非対応のまま維持する．

## Critical path

`Karte T-021 protocol → Karte T-106 search／read → Ephy T-116 client → Ephy T-117 grounding → Ephy T-110 UI → Ephy T-118 E2E`．

Restricted data利用のGateはKarte T-104／Ephy T-114である．embedding品質改善はKarte T-020，評価はT-019で追跡する．

## Traceability

- Parent Issue：https://github.com/kirimine170/Karte/issues/288
- Query processor：https://github.com/kirimine170/Karte/issues/286
- Privacy／provenance：https://github.com/kirimine170/Karte/issues/287
- Ephy parent：https://github.com/kirimine170/ephy-runtime/issues/43
- Karte task sheet：https://docs.google.com/spreadsheets/d/1Px0MACPdErnLUAZwnsCRxQ74bY83k4MBC-xh8yPie2U/edit#gid=1002822573
- Ephy task sheet：https://docs.google.com/spreadsheets/d/1b6-QifgaXWl3TeMMEf7yTq3VxduIrLewyVVlGhiVudo/edit#gid=1509234070

## Date

2026-09-01．
