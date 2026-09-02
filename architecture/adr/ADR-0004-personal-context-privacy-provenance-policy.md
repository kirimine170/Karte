# ADR-0004：Personal Contextのprivacy／provenance policyを単一判定へ統合する

## Status

Accepted．

## Context

Context Protocol V1の初期実装はsearch／readでprojectとsensitivityを交差したが，proposal review，export，audit，学習利用は同じ判定を通っていなかった．この状態では，検索で隠した文書をproposalやexportから扱える可能性があり，機密区分の意味も運用ごとにずれる．人物／組織に関する情報はprojectを横断するため，directoryだけではアクセス境界を表現できない．

## Decision

Karteを唯一のpolicy ownerとし，actor，capability，project，tag，sensitivity，provenanceの積集合で判定する．同じ`Policy.Authorize`をsearch，read，Ephy proposal，human review，export，学習利用へ適用する．transportやUIは独自判定を持たない．

### Sensitivity

| 区分 | 意味 | 初期policy |
| --- | --- | --- |
| `public` | 公開してもよい情報．ただし公開操作そのものは別途reviewする． | Ephy／humanが利用可能． |
| `internal` | 個人workspace内で通常利用する非公開情報．frontmatter省略時の既定値． | Ephy／humanが利用可能． |
| `confidential` | 個人情報，契約，限定共有資料など，明示許可したactorだけが扱う情報． | humanのみ．Ephyはdeny． |
| `restricted` | credential，医療・法務等の高機密情報，または明示的に隔離した情報． | humanのみ．Ephyはdeny． |

actorの`sensitivity_ceiling`を上げるだけでは利用できない．actorは対象capabilityとprojectも明示的に許可されなければならない．学習利用の`learn` capabilityはEphyにもhumanにも既定付与しない．

### Project，master project，tag

- directoryはproject優先の所有・保存境界であり，tagを表現しない．
- tagはdirectoryと独立した横断filterである．actor policyの`allowed_tags`は少なくとも1件の一致を要求し，`denied_tags`は1件でも一致すれば常にdenyする．これにより`person:<id>`や`organization:<id>`をproject横断で制御できる．
- `master_projects`はKarteごとのlocal policyで宣言し，名前をprotocolへ固定しない．人物，組織，日記のうち複数projectを横断する情報だけをmaster projectへ置く．単一project内の情報はそのprojectへ置く．
- master projectは特権的な迂回路ではない．actorのproject allow-listに明示されるか，`*`が指定された場合だけ参照できる．分類不能時はEphyが相談proposalを返し，自動保存しない．
- 既存UIが作成した`content/`配下の文書で，`doc_id`はあるがproject／kindを持たずproject directoryにも入っていないものは，移行互換のためvirtualに`project=legacy`，`kind=note`として分類する．これはfileを移動せず，actor policyに`legacy`または`*`がなければ参照を許可しない．新規のEphy proposalでは引き続きprojectを必須とする．

### Provenance

- canonical documentのprovenanceはcanonical relative pathとcontent SHA-256で表す．pathはidentityではなく，`doc_id`がidentityである．
- proposalのprovenanceは`source_refs.type`で表し，actor policyの`provenance_types`に含まれないsourceを持つresourceはdenyする．`*`は全typeを許可する．
- provenanceは出典追跡であり，信頼済み命令を意味しない．Ephyは取得本文を常にuntrusted dataとして扱う．

### Capability

`search`，`read`，`propose`，`review`，`export`，`learn`を独立して許可する．既定ではEphyが`search`／`read`／`propose`，local humanが`search`／`read`／`review`／`export`を持つ．`learn`は明示opt-inのみである．

Ephy proposalは表示前とaccept直前の両方で検証する．appendはcanonical project／kind／sensitivityと一致しなければならず，append patchで`sensitivity`を変更できない．export APIはcanonical relative pathだけを受け取り，Karteが保存済みcanonical documentを再読込・renderする．caller supplied HTMLを受け付けない．

### Non-disclosure and audit

- scopeが限定されたactorには，存在するがdenyされた`doc_id`と存在しない`doc_id`を同じ`denied`として返す．document，diagnostic，title，snippet，path，tagを返さない．全project／全sensitivity／無tag制約のhumanだけが確定的な`not_found`を受け取れる．
- auditは`.mdsys/context/v1/audit`へatomic JSONとして保存する．保存項目はaudit version，event ID，correlation hash，actor type，actor ID hash，operation，status，result count，error code，timestampだけである．
- raw query，doc_id，title，snippet，relative path，document body，tag，person／organization名をauditへ保存しない．通常logにもこれらを追加しない．
- Frontend event logはcomponent，action，timestampだけを永続化し，stateはbackendでも必ず破棄する．Frontend側もconsole／memoryへ入れる前にquery，path，filename，candidate ID，任意error textを除去する．
- Context Protocol／Ephy outbox directoryは`0700`，audit／event log／receipt／export fileは`0600`を適用し，既存directoryも起動時に狭める．

## Policy example

```json
{
  "protocol_version": "1.0",
  "master_projects": ["personal-master"],
  "actors": {
    "ephy": {
      "sensitivity_ceiling": "internal",
      "projects": ["*"],
      "denied_tags": ["person:private"],
      "provenance_types": ["canonical", "ephy-conversation", "karte-context"],
      "capabilities": ["search", "read", "propose"]
    },
    "human": {
      "sensitivity_ceiling": "restricted",
      "projects": ["*"],
      "provenance_types": ["*"],
      "capabilities": ["search", "read", "review", "export"]
    }
  }
}
```

## Consequences

- Restricted dataをEphyへ許可する場合，local policyのsensitivity，project，tag，provenance，capabilityをすべて明示する必要がある．
- 既存policyに`capabilities`または`provenance_types`がない場合，互換性のためactor typeの既定capabilityまたは全provenance typeを適用する．明示した空配列はそれぞれを全denyする．
- vector index，MCP facade，model training adapterを追加しても，canonical serviceのpolicy判定を迂回できない．
- 人物／組織tagの命名規則はlocal vocabularyとして進化でき，directory migrationを要求しない．

## Verification

- sensitivity 4区分，project，master project，allowed／denied tag，provenance，capabilityを組み合わせたsynthetic security matrixをCIで実行する．
- denied／missingのnon-disclosure，proposal payload非露出，append sensitivity downgrade拒否，export拒否，audit秘密情報非保存を回帰testに固定する．

## Traceability

- Karte Issue：https://github.com/kirimine170/Karte/issues/287
- Karte parent：https://github.com/kirimine170/Karte/issues/288
- Ephy Task：T-114
- Ephy parent：https://github.com/kirimine170/ephy-runtime/issues/43
- Ephy task sheet：https://docs.google.com/spreadsheets/d/1b6-QifgaXWl3TeMMEf7yTq3VxduIrLewyVVlGhiVudo/edit#gid=1509234070

## Date

2026-09-02．
