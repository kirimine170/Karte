# windows環境でのセットアップ

## 検証環境

```txt
# System
┌──────────────────────────────────────────────────────────────────────────────┐
| OS           | Windows 10 Home                                               |
| Version      | 2009 (Build: 26100)                                           |
| ID           | 24H2                                                          |
| Branding     | Windows 11 Home                                               |
| Go Version   | go1.24.4                                                      |
| Platform     | windows                                                       |
| Architecture | amd64                                                         |
| CPU          | Intel(R) Core(TM) i5-10600 CPU @ 3.30GHz                      |
| GPU          | NVIDIA GeForce RTX 2070 SUPER (NVIDIA) - Driver: 32.0.15.8129 |
| Memory       | 32GB                                                          |
└──────────────────────────────────────────────────────────────────────────────┘

# Dependencies
┌───────────────────────────────────────────────────────┐
| Dependency | Package Name | Status    | Version       |
| WebView2   | N/A          | Installed | 142.0.3595.94 |
| Nodejs     | N/A          | Installed | 24.11.1       |
| npm        | N/A          | Installed | 11.6.2        |
| *upx       | N/A          | Available |               |
| *nsis      | N/A          | Available |               |
|                                                       |
└─────────────── * - Optional Dependency ───────────────┘
```

## npm周り

必要なパッケージをインストールする

```sh
Karte> cd frontend
Karte\frontend> npm install
Karte\frontend> npm run build
```

## wailsを動かす

```sh
Karte> wails build
# もしくは
Karte> wails dev
```

## Sherpa-onnxについて

DLLが自動で上手くリンクされない。(Exit status: 0xc0000135)

上記DLLに関しては<https://github.com/k2-fsa/sherpa-onnx-go-windows/tree/master/lib/x86_64-pc-windows-gnu>にビルド済みのものが配置されている。(x86_64 archの場合)

**重要**: 環境変数PATHを通したディレクトリ配下にonnxruntime関係のdllをまとめて配置すること。
**重要**: ただし、onnxruntime.dllは`https://github.com/microsoft/onnxruntime/releases`から落としたものを利用する(でないとなぜか起動時:コンフィグ読み込み&初期化で落ちる)

最悪の場合、onnxruntime.dllとこのdllが依存する他のdllについて、手動で解決する必要がある。依存関係についてはDependanciesというツールが利用できる。
onnxruntime.dllの手動操作については以下に記録を残しておくが、可能な限り**行わないことが望ましい**。

```none
1. ファイル特定: おおよそC:/Windows/System32/onnxruntime.dll にあるはず
2. ファイル所有者変更: dllファイルのプロパティ>セキュリティ>詳細設定 から、所有者をTrustedInstallerから自分が利用できるアカウント(Administratorsなど)に変える
3. ファイル捜査権限変更: 2で設定したアカウントでファイルに対するフルアクセス権限を持つようプロパティを変更
4. ファイルリネーム(削除): リネームによる退避を推奨。
5. 手動でDLしてきたdllを追加
6. (任意)ファイルの権限を再設定: dllファイルの詳細設定から、所有者をNT Service\TrustedInstaller に変更
```

## 文字起こしのためのconfig.json

```json
{
  "enabled": true,
  "sampleRate": 16000,
  "model": {
      "tokens": "data/asr/sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10/tokens.txt",
      "encoder": "data/asr/sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10/encoder-epoch-75-avg-11-chunk-16-left-128.int8.onnx",
      "decoder": "data/asr/sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10/decoder-epoch-75-avg-11-chunk-16-left-128.onnx",
      "joiner": "data/asr/sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10/joiner-epoch-75-avg-11-chunk-16-left-128.int8.onnx"
  },
  "decoding": {
      "method": "greedy_search"
  },
  "runtime": {
      "threads": 4,
      "provider": "cpu"
  }
}
```

enabledとmodel.tokensのセットする必要がある

モデルに関しては`templates/karte_data_template/data/asr`以下にテンプレートがあるのでそのまま利用できる
