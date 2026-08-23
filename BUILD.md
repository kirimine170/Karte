# Karte ビルドガイド

このプロジェクトは、Goで実装されたビルドマトリックスタイツール（`cmd/buildmatrix/main.go`）を使用してビルドします。

## Karte Renderer依存関係

Markdown・Marp・PDFのレンダリングは、別リポジトリの
[`kirimine170/Karte_renderer`](https://github.com/kirimine170/Karte_renderer)を使用します。
`go.mod`では検証済みのexact versionを固定しているため，通常の`go mod download`と
ビルド時に自動取得されます．兄弟directoryへのcloneは不要です．

依存更新では`main`やbranch名を指定せず，採用するSemVer tagまたはcanonical
pseudo-versionを明示します．次のscriptがmodule identity，module filesのdiff，
Renderer test，Karte contract test，Karte全testを検証します．

```bash
./scripts/update-karte-renderer.sh --version v0.2.0
```

repository名（`Karte_renderer`）と現在のGo module宣言（`KarteRenderer`）が異なるため，
公開repositoryの固定versionを`replace`で参照しています．通常のtag付き`require`へ
移るには，外部repositoryのrenameか`replace`右辺をtag運用するかの決定と初回releaseが
必要です．更新，失敗時restore，明示rollback，互換性ruleの詳細は
[`RENDERER_DEPENDENCY_POLICY.md`](RENDERER_DEPENDENCY_POLICY.md)を参照してください．

## ビルドマトリックスタイツール

`buildmatrix` は、`build/targets.json` に定義された複数のプラットフォームを一度にビルドできるGoプログラムです。

### ビルドマトリックスタイツールのビルド

```bash
go build -o buildmatrix ./cmd/buildmatrix
```

> **Windowsにおける補足**
> 出力ファイル名をbuildmatrix.exeにしないと
> 実行時にどのプログラムを使用して開くか聞かれ、実行ファイルのしての実行が妨げられます。

### 基本的な使用方法

```bash
# すべてのターゲットをビルド
./buildmatrix --all

# 特定のターゲットのみビルド
./buildmatrix --targets darwin
./buildmatrix --targets darwin-arm64
./buildmatrix --targets darwin,linux

# 依存関係を更新してからビルド
./buildmatrix --targets darwin --prep

# クリーンビルド（既存の成果物を削除）
./buildmatrix --targets darwin --clean

# クリーンビルド + 依存関係更新
./buildmatrix --targets darwin --clean --prep
```

### 利用可能なターゲット

`build/targets.json` で定義されています：

- `darwin` - macOS (Universal Binary) - Intel Mac / Apple Silicon の両方で動作（Mac向け配布の標準）
- `darwin-arm64` - macOS (Apple Silicon専用) - M1/M2/M3など
- `darwin-amd64` - macOS (Intel Mac専用)
- `windows` - Windows
- `linux` - Linux

### Apple Silicon (M1/M2/M3) の場合

Apple Silicon Macでビルドする場合、以下のいずれかを使用できます：

```bash
# Universal Binary（推奨: すべてのMacで動作）
./buildmatrix --targets darwin

# Apple Silicon専用（ファイルサイズが小さく、パフォーマンスが良い）
./buildmatrix --targets darwin-arm64
```

Universal Binaryはファイルサイズが大きくなりますが、Intel MacとApple Siliconの両方で動作します。Apple Silicon 上で `darwin-arm64` のみを配布すると Intel Mac（例: MacBook Pro/Intel Mac）では起動できないため、Mac向けに配布する成果物は `darwin` を使用してください。

#### macOS のアーキテクチャ依存について

macOS の音声入力 / ASR 実装は `github.com/gordonklaus/portaudio` と `github.com/k2-fsa/sherpa-onnx-go-macos` に依存します。`sherpa-onnx-go-macos` は macOS arm64 / amd64 の両方の dylib を含むため、薄い `darwin-arm64` と `darwin-amd64` ビルドでは同じ録音 / ASR 実装を使います。

一方、`darwin` Universal Binary は 1 つの `.app` に両アーキテクチャをまとめるため、外部ネイティブ依存の同梱・検証が複雑になります。そのため Universal Binary は互換性確認用ターゲットとして残しつつ、`-tags universal` により録音 / ASR をスタブ化します。CI の配布成果物は、機能を揃えたアーキテクチャ別の `darwin-arm64` / `darwin-amd64` を優先します。

PDF 出力では WebKit の `createPDFWithConfiguration:` を使うため，配布版のmacOS support floorとdeployment targetは11.0です．main executable，dylib，framework，pluginを含むbundle内の全Mach-O sliceを11.0へ統一します．

配布用のPortAudio，ONNX Runtime，sherpa-onnxはHomebrew bottleやbuild host上の未検証binaryをコピーしません．次のscriptが固定source revisionとPortAudio archive SHA-256からarchitecture別に再構築します．Karteの同梱設定と既定値はいずれもCPU providerであり，macOS 11以降の互換性を保てないONNX Runtime 1.27のCoreML providerは配布buildで無効化します．

```bash
export DEVELOPER_DIR=/Applications/Xcode_16.4.app/Contents/Developer
export KARTE_EXPECTED_XCODE_VERSION=16.4
export KARTE_EXPECTED_XCODE_BUILD=16F6
export KARTE_EXPECTED_SDK_VERSION=15.5
export KARTE_EXPECTED_SDK_BUILD=24F74
export MACOSX_DEPLOYMENT_TARGET=11.0

KARTE_NATIVE_COLD=1 \
  ./scripts/build-macos-native-runtime.sh \
    .cache/macos-native/darwin-arm64 \
    darwin-arm64
```

`scripts/package-macos-runtime.sh`へは，生成された`portaudio/lib`を`KARTE_PORTAUDIO_LIB_DIR`，`sherpa/lib`を`KARTE_SHERPA_LIB_DIR`として渡します．package後は`verify-macos-deployment-target.sh`が`Contents`全体を再帰し，thin／universal Mach-Oの全sliceを検査します．tool欠落，走査失敗，解析不能なload command，bundle外symlink，11.0と異なるdeployment targetはいずれも配布失敗になります．CIは再構築結果をrecipe hash付きcacheへ保存しますが，cache hit時もnative libraryのarchitecture／deployment target検証，同梱2025 modelのload／短い推論，最終bundle gateを省略しません．

### 再現可能なmacOS native／Wails build

macOSのgenerateとbuildは同じcanonical wrapperを通します．対応toolchainは次の固定値です．

| 用途 | runner／host | `DEVELOPER_DIR` | Xcode | macOS SDK |
| --- | --- | --- | --- | --- |
| release baseline | macOS 15 | `/Applications/Xcode_16.4.app/Contents/Developer` | `16.4`／`16F6` | `15.5`／`24F74` |
| compatibility | macOS 26 | `/Applications/Xcode_26.0.1.app/Contents/Developer` | `26.0.1`／`17A400` | `26.0`／`25A352` |

各rowは`DEVELOPER_DIR`をexact pathで指定し，`xcodebuild -version`，
`xcrun --show-sdk-version`，`xcrun --show-sdk-build-version`，SDK path，Xcode clangを
実測します．期待値との不一致，SDK／clangが選択したXcode外にある場合，曖昧な出力は
fail-closedです．toolchain manifest，native cache key，native stampにはXcode version／build，
SDK version／build，clang identity SHA-256が入ります．

まず上表の1行を環境変数へ設定し，`go.mod`のWails versionと同じCLIを導入します．

```bash
wails_version="$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)"
test -n "$wails_version"
go install "github.com/wailsapp/wails/v2/cmd/wails@${wails_version}"

export DEVELOPER_DIR=/Applications/Xcode_26.0.1.app/Contents/Developer
export KARTE_EXPECTED_XCODE_VERSION=26.0.1
export KARTE_EXPECTED_XCODE_BUILD=17A400
export KARTE_EXPECTED_SDK_VERSION=26.0
export KARTE_EXPECTED_SDK_BUILD=25A352
export MACOSX_DEPLOYMENT_TARGET=11.0

native_root="$PWD/.cache/macos-native/darwin-arm64"
KARTE_NATIVE_COLD=1 \
  ./scripts/build-macos-native-runtime.sh "$native_root" darwin-arm64

./scripts/macos-wails.sh \
  --native-root "$native_root" \
  --target darwin-arm64 \
  generate
npm --prefix frontend run build
./scripts/macos-wails.sh \
  --native-root "$native_root" \
  --target darwin-arm64 \
  buildmatrix --targets darwin-arm64 --clean
./scripts/macos-wails.sh \
  --native-root "$native_root" \
  --target darwin-arm64 \
  manifest "$PWD/dist/macos-toolchain.json"
```

wrapperは`go list -m`のmodule pinと`go version -m`のWails CLI metadataがexact match
することを検証し，generateとbuildmatrixの双方を同一の`CGO_CFLAGS`，
`CGO_CXXFLAGS`，`CGO_LDFLAGS`，`DYLD_LIBRARY_PATH`で実行します．envをshell codeとして
出力しないため，`eval`は不要です．spaceを含むnative／project pathもfixtureで固定します．

`KARTE_NATIVE_COLD=1`はcaller指定の`KARTE_NATIVE_WORK_DIR`を拒否し，
`RUNNER_TEMP`または`TMPDIR`実体配下にfresh work rootを作ります．stamp miss時もGit sourceは
pinned commitかつtracked／staged／untracked／submodule changeがないことを検証し，PortAudioは
SHA-256検証済archiveからfresh build sessionへ毎回展開します．成果物は別stageで全検証後に
置換され，途中失敗では既存runtimeを保持します．

局所regression fixtureは次で実行します．UTTypeはframeworkなしの期待symbol link failureと，
frameworkありの実link／実行を確認します．loader fixtureはWails v2.13のbindings子processを
実行し，`DYLD_LIBRARY_PATH`なしの失敗とwrapper経由の成功を確認します．

```bash
bash -n \
  scripts/build-macos-native-runtime.sh \
  scripts/lib/macos-native-source.sh \
  scripts/lib/macos-toolchain.sh \
  scripts/macos-toolchain-manifest.sh \
  scripts/macos-wails.sh
./scripts/test-build-macos-native-runtime.sh
./scripts/test-macos-wails-toolchain.sh
```

CIのmacOS 15／Intel rowは従来どおりrelease artifactを公開します．macOS 26 compatibility
rowはcold native rebuild，bindings generate，Wails build，runtime package，deployment gate，
展開後startup smokeまで行いますが，application ZIPをartifact／releaseへ公開しません．
toolchain manifestと失敗時を含むstartup smoke diagnosticsだけを証跡として保存します．

T-086の未取得証跡は外部blockerです．2026-08-23時点の局所hostはmacOS 26.5.2ですが，
導入済みtoolchainはXcode 15.3（15E204a）／SDK 14.4であり，Xcode 26によるproduction
buildはこの局所検証では実行していません．また，GitHub `macos-26` rowのactual runは
workflowをpushしてGitHub Actionsが完走するまで未取得です．fixture成功をこれらの実build
証跡として扱ってはいけません．


## CI と配布用成果物

v1.0.0の正式releaseでは，以下のrolling releaseだけで公開判定を行わない．
[v1.0 release criteria](RELEASE_CRITERIA_V1.md)に従い，同一candidate SHAの
test，4 platform artifact，install smoke，checksum，rollback dry-runを確認する．

`.github/workflows/ci.yml` の `Desktop Build` は、PRでは Apple Silicon macOS / Intel macOS / Linux amd64 / Windows amd64 のビルド確認を行います。`main` への push で同じビルドがすべて成功すると、成果物を ZIP 化して GitHub Releases の `latest-main-successful-build` にアップロードします。

- `Karte-macOS-apple-silicon.zip` - Apple Silicon macOS 版（録音 / ASR 依存を含む）
- `Karte-macOS-intel.zip` - Intel macOS 版（録音 / ASR 依存を含む）
- `Karte-linux-amd64.zip` - Linux amd64 版
- `Karte-windows-amd64.zip` - Windows amd64 版

このリリースは「最後に成功した main ブランチのビルド」を指すローリングリリースです。新しい main ビルドが成功するたびに同じタグと添付ファイルが更新されます。

Windows v1候補は`.github/workflows/windows-release.yml`で別に生成します。このworkflowは公式
FFmpeg 8.0.3の固定コミットを`--disable-gpl --disable-nonfree`でビルドし、Karte本体、
FFmpeg、Sherpa／ONNX／PortAudio／MinGW DLL、初期データテンプレート、第三者ライセンス、
runtime manifestを1つのZIPへまとめます。設定値は次のsecretを使用します。

ASR modelはGit LFSから取得しません．`scripts/fetch-asr-models.sh`が公式sherpa-onnx release
assetを取得し，固定したarchive SHA-256と各ONNX fileのSHA-256を検証してから
`karte_data_template`へ配置します．ローカルで配布成果物を作る場合はbuild前に同scriptを実行します．
この変更は現行checkoutとCIのLFS依存を除去しますが，GitHub側に保存済みの過去のLFS objectや
storage使用量を削除するものではありません．

- `WINDOWS_CERTIFICATE_BASE64`: Authenticode証明書（PFX）のBase64
- `WINDOWS_CERTIFICATE_PASSWORD`: PFXのパスワード

tagからのbuildでは署名を必須とし、ZIP内の全EXE／DLLへ署名後に`signtool verify /pa`を実行します。
手動dispatchだけは検証用のunsigned RCを許可できますが、v1公開条件には使用できません。workflowが
作成するGitHub Releaseはdraftであり、[WindowsクリーンVM試験](WINDOWS_V1_SMOKE.md)を完了する
まで公開しません。

## ビルド成果物

ビルド成果物は、`build/targets.json` で指定された `artifactDir` に出力されます：

- `darwin` → `dist/darwin/`
- `darwin-arm64` → `dist/darwin-arm64/`
- `darwin-amd64` → `dist/darwin-amd64/`
- `windows` → `dist/windows/`
- `linux` → `dist/linux/`

Windowsのrelease buildでは`dist/windows/`に次も追加されます。

- `ffmpeg.exe`とFFmpeg共有DLL
- Sherpa／ONNX／PortAudio／MinGWランタイムDLL
- `karte_data_template/`
- `licenses/`
- `runtime-manifest.json`
- `SIGNING_STATUS.txt`（署名workflowで追加）

## 方法2: Wailsコマンドを直接使用

最もシンプルな方法ですが、手動で依存関係の管理が必要です。
macOSの配布／compatibility buildには使用できません．macOSでは上記の
`scripts/macos-wails.sh`だけをcanonical entrypointとして使用してください．

```bash
# フロントエンドのビルド
cd frontend
npm install
npm run build
cd ..

# Wailsアプリケーションのビルド
wails build -platform darwin/universal  # Universal Binary
wails build -platform darwin/arm64       # Apple Silicon専用
wails build -platform darwin/amd64       # Intel Mac専用
```

成果物は `build/bin/` に出力されます。

## トラブルシューティング

### package-lock.jsonのプラットフォーム間の不一致

MacとWindowsでpackage-lock.jsonの内容が異なる場合、以下の手順で統一できます：

```bash
cd frontend

# 既存のpackage-lock.jsonを削除
rm package-lock.json

# node_modulesも削除
rm -rf node_modules

# クリーンな状態から再インストール
npm install

# package-lock.jsonをコミット
git add package-lock.json
git commit -m "Update package-lock.json for cross-platform compatibility"
```

**注意**: `buildmatrix`ツールは自動的に`npm ci`を使用してpackage-lock.jsonを厳密に守ります。これにより、プラットフォーム間の一貫性が保たれます。

### フロントエンドのビルドエラー

```bash
cd frontend
rm -rf node_modules package-lock.json
npm install
npm run build
```

### Go依存関係のエラー

```bash
go mod tidy
go mod download
```

### Wailsが見つからない

```bash
wails_version="$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)"
test -n "$wails_version"
go install "github.com/wailsapp/wails/v2/cmd/wails@${wails_version}"
```

### ビルドマトリックスタイツールのヘルプ

```bash
./buildmatrix --help
```

## アーキテクチャの選択

Mac向けビルドでは、以下のアーキテクチャを選択できます：

- **universal** (`darwin`): Intel MacとApple Siliconの両方で動作するUniversal Binary（推奨）
- **arm64** (`darwin-arm64`): Apple Silicon（M1/M2/M3など）専用
- **amd64** (`darwin-amd64`): Intel Mac専用

Universal Binaryはファイルサイズが大きくなりますが、すべてのMacで動作します。
