# Karte machine-readable contracts

`karte-ephy/v1`は，EphyからKarteへ渡すV1.1 proposalと，KarteからEphyへ返すreceiptのJSON Schema Draft 2020-12 contractを管理する．V1.1はproject-first placement hint，confidence，consultation，create全体提案，append差分を定義する．`fixtures`は両repositoryで同じ意味内容を持つsynthetic dataだけを含む．contract変更時はEphy Runtime側のschema／fixtureとADRを同時に更新する．

`karte-context/v1`は，Karteが所有するPersonal Context Coreへsearch／readを要求するV1.0 request／response contractを管理する．offline filesystem spoolはtransportであり，schemaは将来のMCP facadeでも再利用する．Karte側を先に更新し，ephy-runtime側のbyte-for-byte fixture checkを同期する．

`karte-ephy/v2`と`karte-context/v2`は Step 3 の限定自動採用，安定 event／doc ID，版・hash と出典，現在 grant／privacy に従う read を定義する．v1 に権限を追加せず，別 namespace を使う．設定方法と未実装範囲は [Runtime record v2 の実装手順](../architecture/RUNTIME_RECORDS_V2_SETUP.md)を参照する．v1/v2 合計 44 JSON を Runtime と byte 単位で照合する．
