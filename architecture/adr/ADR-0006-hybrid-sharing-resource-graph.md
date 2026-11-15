# ADR-0006 Karte の版固定と Portable Package

## 状態

Proposed。単体 Markdown の個人向け転送を先行実装。

## 実装した契約

- Workspace は既存の Karte データルートと Context Policy を使う。アーカイブに Workspace のパス、端末 ID、ポリシー、Grant は書き出さない。
- Markdown の Resource ID は既存の `doc_id`。同じ ID の文書が取り込み先にある場合、同じバイト列なら既存文書を使い、異なる場合は取り込みを中止する。
- Package 内の Root Revision ID は配布用 Markdown の SHA-256。元文書のバイト列とプロジェクト・機密区分を Context から照合し、配布用の固定版を作る。版はパスや更新時刻に依存しない。
- 配布用 Markdown は本文、`doc_id`、タイトル、プロジェクト、種別、タグ、機密区分を含む。既定タイトルのない文書では元ファイル名の拡張子を除いた部分がタイトルになる。任意のフロントマター属性は省く。
- ZIP には非圧縮の `manifest.json` と `blobs/sha256/<digest>` が各 1 件入る。読み込み時にエントリ名・容量・CRC・SHA-256 を照合し、Markdown の上限は 2 MiB。マニフェストのプロパティ順はスキーマの記載順、空白を除いた表現は Go の JSON 出力に合わせる。
- 個人の端末間で利用する。書き出しに既存の `export`、取り込みに `review` を適用する。取り込みは確認後にポリシーを再評価し、標準のファイルロックと原子的な書き込みを使う。ハッシュは内容の整合性を確認するためのもので、送信者の認証には別の仕組みが必要。

## 互換性

| 対象 | 連携 |
| --- | --- |
| 既存 Markdown | 対応範囲内の legacy `doc_id` を保持。元のファイルと任意の属性はそのまま残る。 |
| Context Protocol | 既存の分類、ポリシー、監査を使用。取り込んだ文書は通常の Markdown として索引で見つかる。 |
| WebP の KART/KMTD、CSV、PDF、音声 | 次段階では、専用の資料 ID と出典を持つ別 Blob として扱う。WebP 全体のハッシュは外部に保持する。 |
| Board、決定ログ、Ephy v2 | それぞれ固有の素材参照、権限、台帳を持つ。現行の経路を引き続き使用する。 |

## 次の設計判断

- 永続的なリソース ID がない素材に、移動しても追跡できる ID をどう割り当てるか。パスは別名として保持し、既存の `karte-asset://` は互換性を調査する。
- `embeds`、`cites`、`derived_from`、`related_to`、`supersedes` と Markdown・PDF・CSV・音声のアンカーを定義する。書き出し時の依存は版固定とし、許可されない資料を本文、プレビュー、索引、アーカイブから一貫して除外する。存在を示せる状態にも個別の認可が必要。
- Workspace Sync では不変の親子関係と複数 Head を保持する。同時更新の調停、削除情報の伝播、再送の重複排除と transport 認証は Workspace Sync の契約で決める。
