# Karte Context Protocol V1

## Purpose

Karte Context Protocolは，人間とEphyが同じPersonal Context Coreへsearch／read／propose／review／receiptを要求するためのversioned contractである．V1.0はsearch／read request-responseを追加し，既存Karte–Ephy proposal／receipt V1.1を互換維持する．

## Ownership

- Karteはcanonical document，`doc_id`，検索，読取，privacy，provenance，reviewed mutationを所有する．
- Ephyはrequestを発行し，返却されたcontextをuntrusted dataとして利用する．
- filesystem spoolはoffline transportであり，domain modelではない．将来のMCP facadeも同じserviceを呼ぶ．

## Layout

```text
KARTE_DATA_DIR/
  content/
  .mdsys/context/v1/
    requests/<request_id>.json
    responses/<request_id>.json
    processed/<request_id>.json
    policy.json
```

Clientはrequestを同一directoryのtemporary fileへ書き，flush後にrenameする．Karteはresponseを同じ方法で確定してからrequestを`processed`へ移す．response作成後に停止した場合，同じrequest hashのretryは既存responseを再利用する．異なるpayloadが同じ`request_id`を再利用した場合はprotocol conflictとして拒否し，既存responseを上書きしない．

## Operations

### search

`query.text`，`query.top_k`，scopeを受け，ranked resultを返す．各resultは`doc_id`，title，project，kind，tags，sensitivity，relative path，updated time，canonical SHA-256，snippet，score，provenanceを含む．V1 lexical scoreはprotocol contractではないため，将来のembedding providerへ差し替えられる．

### read

`doc_id`とscopeを受け，現在のcanonical Markdown bodyと同じmetadataを返す．pathだけをidentityとしてreadしない．hashが変わった場合，clientは新しいresultとして扱う．

### propose／review／receipt

既存[`KARTE_EPHY_OUTBOX.md`](KARTE_EPHY_OUTBOX.md) V1.1を利用する．search／read responseから直接canonical writeへ進まず，必ずproposalとhuman reviewを通す．

## Scope and privacy

Requestはactor，project allow-list，tag filter，sensitivity ceilingを宣言する．Karteはlocal policyとrequest scopeの積集合をeffective scopeとする．V1 default policyではEphyは`internal`まで利用でき，`confidential`／`restricted`は明示policyがない限り拒否する．denied documentはresult count，title，snippet，path，存在有無へ現れない．

frontmatterに`sensitivity`がない文書は`internal`として扱う．不明な値，missing／duplicate `doc_id`，malformed frontmatter，symlinkはindexから除外し，本文を含まないdiagnosticにする．

## Logging

通常logに残せるのはprotocol version，request ID，operation，status，duration，result count，error codeである．query text，snippet，title，path，document body，tag，person／organization名は保存しない．

## Failure model

- Karte未起動：client timeout．requestはretry可能．
- invalid request：`status=invalid`と安全なerror codeを返す．
- permission denied：`status=denied`．対象documentの存在を示さない．
- missing `doc_id`：`status=not_found`．permission deniedとUI文言を区別しすぎない．
- protocol mismatch：処理せず`unsupported_protocol`を返す．
- stale response／request ID reuse：clientがprotocol conflictとして拒否し，既存responseを維持する．

## Runtime data-root continuity

Ephy同梱版のlauncherは，選択した絶対`KARTE_DATA_DIR`を`Karte.app`隣接の`.karte-data-dir`へatomicに記録する．Karteの選択順は，明示`KARTE_DATA_DIR`，development workspace，`.karte-data-dir`，platform既定値である．この記録により，DockやFinderからKarteだけを再起動しても空の別保管庫へ切り替わらない．

Karteは起動後，現在のPIDを`KARTE_DATA_DIR/.mdsys/runtime/karte.pid`へatomicに公開し，正常終了時は自分がownerである場合だけ削除する．Ephy launcherは実行ファイルのpathだけでなく，期待するdata root内のPID markerと実process identityが一致するKarteだけを再利用する．同じbundleが別data rootで動作中の場合はそのprocessを流用せず，期待root用のinstanceを起動する．`karte.pid`はprocess discovery用のephemeral markerであり，document identity，lock，authorization，またはcanonical stateではない．

## Compatibility

Contract正本は`schemas/karte-context/v1`に置く．Karte側を先にmergeし，ephy-runtimeのbyte-for-byte fixture checkを後続で更新する．V1内のoptional field追加はreaderがunknown fieldを拒否するため，schema versionを上げずに行わない．
