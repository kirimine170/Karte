# Issue #219 macOSレシートOCR PoC検証記録

検証日: 2026-08-13

検証環境: MacBook Pro（Apple M2 Max，12 CPU cores，64 GB RAM），macOS 26.5.2，arm64，Swift 5.10

## 結論

採用判断は **Adopt with conditions** です．macOS Visionは追加モデルやcloud通信なしで導入でき，Karteと同一プロセスに大きなML dependencyを持ち込まずに済むため，macOS baselineとして採用候補に値します．一方，今回の実行環境ではVision serviceがsandboxで拒否され，sandbox外実行も許可されなかったため，Issueの完了条件であるp50／p95と抽出精度の実測は未完了です．匿名化した実レシートで再計測し，閾値を満たすことを採用条件とします．

## 調査結果

- Issue #219は「macOSレシートOCR PoCのbaselineを実装・計測」で，完了条件は「MPS/CPUのp50/p95と抽出精度を記録」です．Karte連携はPoC外です．
- Issueコメントは0件，`219`を参照する関連PRは0件でした．
- リポジトリ内に既存OCR処理，OCR dependency，OCR用sample／fixtureはありませんでした．ASR用のsherpa-onnxはありますがOCRには使用されていません．
- KarteはWails v2.13.0で，Goの`App`公開methodが生成bindingを介してTypeScriptから呼ばれる構成です．
- macOS固有APIは既に`//go:build darwin`とcgo Objective-C bridgeでCocoa／WebKitを使っており，OS固有adapterを分離できる構成です．
- 本PoCはKarte本体へ結合せず，Swift CLIからVisionを直接呼び出します．追加のGo／npm dependencyはありません．

## 試した方式とPoC構成

方式はApple Visionの`VNRecognizeTextRequest`です．認識言語を`ja-JP`と`en-US`，recognition levelを`accurate`，language correctionを有効にしました．`auto`はVisionの自動device選択，`cpu`はCPU-only baselineです．

fixture generatorは次を生成します．

| fixture | size | 対象 |
| --- | ---: | --- |
| receipt-small | 600×1200 | 小サイズ，日本語，数字，金額，日付，店舗名，複数行 |
| receipt-medium | 1200×2400 | 標準サイズ，同上 |
| receipt-large | 2400×4800 | 大サイズ，同上 |
| receipt-rotated-90 | 2400×1200 | 横向き／90度回転への耐性 |

OCR出力にはテキスト，confidence，bounding box，wall latency，user／system CPU time，OCR前後のresident memoryを含めます．benchmarkは各fixtureとcompute modeを既定10回ずつ実行し，p50／p95，CPU time，memory，12フィールドのrecallをJSONへ集計します．

## 測定結果

| 項目 | 結果 |
| --- | --- |
| build | 成功，arm64 macOS向けSwift CLIを生成 |
| OCR実行 | sandbox内では失敗 |
| failure | Vision accurate: `nilError`，fast CPU: CVPixelBuffer生成エラー`-6662` |
| sandbox外再実行 | 実行許可が得られず未実施 |
| latency p50／p95 | 未計測 |
| 抽出精度／field recall | 未計測 |
| CPU | 計測コード実装済み，値は未計測 |
| GPU／Neural Engine | auto時にOSが選択するため利用を保証・直接計測できない |
| RAM | OCR前後resident差分の計測コード実装済み，値は未計測 |
| dependency size | 外部dependency 0，OS標準Vision／CoreML／AppKit／ImageIOのみ．最適化したPoC CLIは132 KiB |

合成fixtureは評価経路の再現用であり，実世界の影，傾き，しわ，感熱紙の退色，背景ノイズを代表しません．採用前には匿名化した実レシートを最低30枚用意し，店舗，書体，撮影条件，縦横，解像度を分散させる必要があります．

## メリット

- cloudへ送信せず，完全にローカルで処理できる．
- OCRモデルのdownloadや配布が不要で，Karteのdependency sizeを増やさない．
- 日本語と英数字の混在，confidence，bounding boxを標準APIで取得できる．
- ローカルLLMとは別モデルをKarteが保持しないため，常駐RAMの増加を抑えやすい．

## デメリット

- macOS専用で，Windows／Linuxには別実装が必要になる．
- Visionのauto modeがGPU，Neural Engine，CPUのどれを使ったかを安定した公開APIで観測しにくく，「MPS baseline」と厳密には呼べない．
- CPU-onlyはmacOS 14以降のcompute stage APIで指定できるが，macOS 13以前を対象にする場合はdeprecatedな互換APIが必要になる．
- Visionは文字認識までで，店舗名，合計，税，日付の意味付けは別parserが必要になる．
- 合成fixtureだけでは実レシート精度を判断できない．

## Karte本体へ統合する場合の設計案

採用条件を満たした後に，`internal/ocr`へ小さなinterfaceを置き，`ocr_darwin` adapterからVision bridgeまたは署名済みhelper executableを呼びます．非macOSは明示的なunsupported adapterにします．Wails境界は`App`へ直接ロジックを書かず，application serviceの非同期jobを1 methodだけ公開し，画像pathとoptionを入力，text line，confidence，bounding box，timingを返す形が適切です．

ローカルLLMとの同時実行を考慮し，OCRは1並列，cancel可能，画像長辺を上限付きで縮小，LLM生成中はCPU-onlyを避けるかqueueする設計を推奨します．文字認識とレシート項目parserは別packageに分離します．

## 採用条件

1. sandbox外または署名済みKarte app contextで，auto／CPU各10回以上のp50／p95を記録する．
2. 匿名化した日本語実レシート30枚以上で，店舗名，日付，合計金額のfield recallを個別に記録する．
3. ローカルLLM単独，OCR単独，同時実行の3条件でwall latency，CPU time，peak RSS，Energy Impactを比較する．
4. autoを「MPS」と表記せず，InstrumentsでGPU／Neural Engine利用を確認する．
5. 受入閾値を先に決め，満たさない場合はproduction統合へ進まない．

## 再現方法

```sh
ITERATIONS=10 poc/macos-receipt-ocr/run-benchmark.sh
```

成功時の生データは`poc/macos-receipt-ocr/results.json`へ生成されます．fixtureと結果は生成物としてgit管理しません．
