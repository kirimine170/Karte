# Karte（暫定 v0.1）

Markdown を正本に、CSV を @import で取り込み、ライブプレビューできる最小スターター。

## 使い方
```bash
go mod tidy
go run ./cmd/karte init
go run ./cmd/karte serve -p 1313
# -> http://localhost:1313/content/report.html
```

## 構成
- `.mdsys/` … システム領域（indexなど）
- `content/` … 文書（Markdown 正本）
- `data/` … CSV 等
- `themes/default/` … レイアウトや共通CSS

## 今後
- 検索API（ファイル名検索→@import挿入）
- 差分ビルド・依存グラフ
- Marp 連携（外部 marp-cli）
- CSV フィルタ・集計
- ACL メタデータの統合管理
```

