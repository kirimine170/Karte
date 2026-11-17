# macOS Receipt OCR PoC

Issue #219向けに，macOS標準のVision frameworkを単体CLIから評価するPoCです．Karte本体，`app.go`，Wails binding，`go.mod`には統合しません．画像は外部へ送信されません．

## 構成

- `ReceiptOCR.swift`: Vision OCR，処理時間，CPU time，resident memory，bounding boxをJSON出力
- `GenerateFixtures.swift`: 日本語レシートの合成fixtureを3解像度と90度回転で生成
- `benchmark.py`: `auto`／`cpu`を反復し，latency p50／p95と期待フィールドrecallを集計
- `run-benchmark.sh`: build，fixture生成，benchmarkの再現コマンド

## 実行

```sh
ITERATIONS=10 poc/macos-receipt-ocr/run-benchmark.sh
```

結果は`poc/macos-receipt-ocr/results.json`へ出力されます．実レシートを評価する場合は，個人情報を除去した画像を`fixtures/`へ追加してください．合成fixtureと同じ期待値でない画像は，`benchmark.py`の`EXPECTED`を評価セットに合わせて変更する必要があります．

単一画像の確認は次のとおりです．

```sh
/tmp/karte-receipt-ocr-poc/receipt-ocr \
  --image path/to/receipt.png \
  --compute auto
```

`auto`はVisionにcompute deviceの選択を委ねます．これはMPS／GPU利用を保証する指定ではありません．`cpu`はmacOS 14以降ではcompute stage APIを使い，各stageをCPUへ固定します．macOS 13以前だけは互換用の旧APIを使います．

## 指標の解釈

- latencyは同期`perform`のみを計測し，プロセス起動時間を含みません．各反復は別プロセスなので，毎回cold startに近い値です．
- CPU timeは`getrusage`のuser＋system差分です．wall timeより大きくなり得ます．
- memoryはOCR直前／直後のresident size差分です．プロセス全体の最大RSSではありません．
- field recallは店舗名，日付，時刻，金額12項目のsubstring一致率です．文字単位accuracyではありません．
- GPU／Neural Engine使用率はVision公開APIから取得できないため，このPoCでは直接計測しません．必要ならInstrumentsのEnergy Log／Metal System Traceを別途使用します．
