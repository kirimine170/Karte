# HSH-02 読み取り専用 Resource Resolver 設計

Proposed。対象は G0 の Resource Graph と、既存参照を結び付ける読み取り経路。Issue #348、PR #350。

## 入力と結果

同一 Workspace で検証した、ある時点の Graph と、アダプターが採取したローカル別名表を入力にする。既存 Board の `source`、Context の `doc_id`、PDF の固定版 SHA-256、WebP の原本・派生物の参照を、まず `resource_id` に解決する。版の照合には `revision_id` と必要に応じて Blob の SHA-256 を使う。一回の処理で Graph や索引を途中で読み直さず、資料の Head を混在させない。表示・取込・共有のアダプターが、実際の読み取り時に現在のポリシーと Blob を再照合する。

| 入力 | 解決規則 |
| --- | --- |
| Board の `source` | Workspace 内のパスを、現在または信頼できる履歴の別名として照合する。移動元ファイルが消えていても、履歴に同じ資料と版があれば解決する。別の資料によるパス再利用や衝突は曖昧として扱い、タイトル・時刻で推測しない。 |
| Context の `doc_id` | 既存の Context が行う境界の空白除去を済ませた値を `legacy_doc_id` と照合する。大文字小文字、記号、Unicode の正規化形はそのまま扱う。重複は曖昧。存在や権限状態は ADR-0004 の判定を経てから公開する。 |
| PDF の引用 | #349 の `sha256:` 付き版を、指定資料の Blob SHA-256 と照合する。複数の版が同じ SHA-256 を持つ場合は、信頼できる版 ID がなければ曖昧。更新時刻・サイズだけの古い引用は従来の場所で照合し、移動先で新しい固定版に自動で結び付けない。固定アンカーは資料のページ・矩形と版を引き継ぐ。 |
| WebP の参照 | 原本と派生物を `derived_from` で結ぶ。KART と KMTD は既存のメタデータ。再エンコードでチャンクを失っても、信頼するカタログに対応があれば資料と版を解決する。メタデータだけから権限や版を推測しない。 |
| `fixed` / `follow_head` | 固定版は Head が分岐していても指定版へ解決。追従で有効な Head が1件ならその版、複数なら競合、ゼロなら未解決。時刻で勝者を選ばない。 |

ローカルでの診断は解決済み・欠損・曖昧・競合・不正な参照を区別する。スコープが限られた呼出しには、存在非開示と欠損の違いや、候補 ID・パス・件数を返さない。リゾルバーは権限を付与しない。パスを受け取るアダプターは許可ルート内の正規パスを検証し、symlink・ルート外・Windows の drive/UNC/ADS を拒否する。Case、NFC/NFD、OS のファイル名制約はアダプターで衝突として診断する。

依存探索は版の ID を訪問済み集合に登録してから展開する。必須の `embeds` を辿り、`cites` は明示指定の場合だけ追加する。`related_to`、`supersedes`、`derived_from` は依存に含めない。循環に戻る辺は記録し、同じ版を再展開しない。件数の上限超過は部分成功として扱わない。

## #347 と G0 の互換性

| 項目 | #347 単体 Markdown パッケージ | #350 G0 |
| --- | --- | --- |
| Resource | `root_resource_id` は ASCII 部分集合の legacy `doc_id` | Workspace 内の不透明な `resource_id`。元の値は `legacy_doc_id` の別名 |
| Revision | `root_revision_id` は投影後のバイト列の SHA-256。 `blob.sha256` と一致 | `revision_id` は不透明 ID、`blob.sha256` はその版のバイト列の整合性 |
| Manifest | ZIP の `manifest.json`、`format_version: "0.1"` | `type` と `schema_version: "0.1"`。ShareSnapshot はローカルな項目 ID と配布 Blob を持つ |

同じバージョン文字列でも契約は別。旧 Manifest を G0 の Manifest として解釈せず、既存アーカイブの取り込み経路は維持する。将来の対応付けは Workspace 内の別名で明示して行い、投影ハッシュを原本の版 ID に据えない。コロン・記号・Unicode の既存 ID は Context と G0 の別名では扱えるが、#347 の v0.1 書き出しでは拒否する。許容集合の拡張や新形式の配布は、旧読取側の互換が変わるため承認対象。

## Fixture と検証境界

`testdata/hybrid-sharing/resolver-scenarios.json` は G0 の混合資料 Fixture を参照する。Board の移動前後・欠損、Context の Unicode ID、PDF の固定版、WebP のメタデータ消失と原本・派生物、循環、複数 Head、競合中の固定版を含む。Go テストは期待値を置く前提が G0 型・グラフと整合することを検証する。この段階では実 Workspace、ボード、ポリシー、PDF/画像の読取をつないだ Resolver は導入しない。

HSH-02 の次の実装は、合成 Workspace に対する読み取り専用のリゾルバーと、OS 別のパス別名アダプターをテストすること。実データへの台帳導入や参照書換えの前に #254 の索引更新競合を評価する。HSH-03 の受信者別投影は、それぞれの既存経路との整合性を確認してから進める。
