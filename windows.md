# Windowsガイド

Karte v1の対象はWindows 11 x64、配布形式は署名付きZIPです。利用者はGo、Node.js、
FFmpeg、wkhtmltopdf、Sherpa／ONNX DLLを別途導入する必要はありません。

## 配布ZIPからの起動

1. `SHA256SUMS-windows.txt`とZIPのSHA-256を照合する。
2. ZIPをユーザーが書き込める任意のフォルダーへ展開する。
3. `Karte.exe`のデジタル署名が有効であることを確認して起動する。

ZIPにはKarte本体、FFmpeg、Sherpa／ONNX／PortAudioとMinGWのランタイムDLL、初期データ
テンプレート、第三者ライセンス、`runtime-manifest.json`が含まれます。Windowsの
`System32`にDLLを追加・置換したり、所有権やACLを変更したりしないでください。

## ユーザーデータ

Windowsでは設定、コンテンツ、キャッシュを次に保存します。

```text
%LOCALAPPDATA%\Karte\karte_data
```

従来版の実行ファイル隣接`karte_data`があり、新しい保存先がまだ存在しない場合は、初回起動時に
新しい保存先へ非破壊コピーします。元のディレクトリは削除せず、既存の新保存先も上書きしません。
移行前には旧データを別の場所へバックアップしてください。

開発や診断では`KARTE_DATA_DIR`で保存先を上書きできます。

```powershell
$env:KARTE_DATA_DIR = 'D:\Karte-Test\karte_data'
.\Karte.exe
```

## PDFエンジン

PDF出力はKarte RendererがChromium系ブラウザーを次の順で探索します。

1. Rendererの明示設定
2. `PATH`上のEdge、Chrome、Brave、Chromium
3. `Program Files (x86)`、`Program Files`、`LOCALAPPDATA`配下の標準インストール先

通常のWindows 11に含まれるMicrosoft Edgeを利用できるため、wkhtmltopdfの導入や自動
ダウンロードは行いません。企業ポリシーなどでEdgeを削除した場合はChromeなどを導入するか、
Rendererのブラウザー実行ファイル設定を明示してください。

## FFmpeg

正式配布ZIPにはFFmpeg 8.0.3をLGPL構成（`--disable-gpl --disable-nonfree`）で同梱します。
バージョン、取得元、固定コミット、ビルド設定、各ファイルのSHA-256は
`runtime-manifest.json`で確認できます。

探索順は`KARTE_FFMPEG_BINARY`、互換用`FFMPEG_PATH`、実行ファイル隣接の同梱版、`PATH`
です。開発時だけ任意のバイナリへ差し替える例は次のとおりです。

```powershell
$env:KARTE_FFMPEG_BINARY = 'C:\tools\ffmpeg\bin\ffmpeg.exe'
wails dev
```

## ASRランタイム

配布ZIPではKarte本体と同じディレクトリに必要なDLLを配置します。DLL欠落エラーが出た場合は、
ZIPを一部だけコピーせず、すべての内容を同じディレクトリ構成で再展開してください。

ASRの設定は`%LOCALAPPDATA%\Karte\karte_data\data\asr\config.json`にあります。初期
テンプレートにはモデルと設定例が含まれます。成功、無効設定、初期化失敗、タイムアウトのいずれでも
初期化表示は終了します。

## スクリーンショット

Windowsの画面／範囲スクリーンショットはOS標準の画面切り取りUIを起動します。複数ディスプレイと
DPIスケーリングはWindows側で処理され、選択結果はWebPとして保存されます。ウィンドウ単位の
キャプチャはv1対象外です。

## 開発環境

- Go 1.25.0（`go.mod`）
- Node.js 22.13.0（`.node-version`）
- Wails CLI v2（`go.mod`のWailsバージョン）
- MSYS2 MinGW64のGCC、pkg-config、PortAudio

```powershell
C:\msys64\usr\bin\pacman.exe -S --noconfirm --needed `
  mingw-w64-x86_64-gcc `
  mingw-w64-x86_64-pkg-config `
  mingw-w64-x86_64-portaudio

$env:CGO_ENABLED = '1'
$env:PKG_CONFIG_PATH = 'C:\msys64\mingw64\lib\pkgconfig'
$env:PATH = 'C:\msys64\mingw64\bin;' + $env:PATH

Set-Location frontend
npm ci
npm run build
Set-Location ..
wails dev
```

配布用成果物は`.github/workflows/windows-release.yml`で、固定FFmpegのビルド、全EXE／DLLの
Authenticode署名、`signtool verify /pa`、起動スモーク、ZIPとSHA-256作成を行います。署名証明書が
ない手動実行ではunsigned RCを作れますが、v1公開には使用できません。

## 診断

- `SIGNING_STATUS.txt`: 配布物の署名状態
- `runtime-manifest.json`: FFmpegの出所、構成、SHA-256
- `%LOCALAPPDATA%\Karte\karte_data`: 実際のデータ保存先
- [クリーンVM試験](WINDOWS_V1_SMOKE.md): 正式公開前の必須確認項目

欠落DLL、`Unknown publisher`、データ保存時のアクセス拒否、外部ツールの手動導入要求が1件でも
あれば、Windows v1の公開条件は未達です。
