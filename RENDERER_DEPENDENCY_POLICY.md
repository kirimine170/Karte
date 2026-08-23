# Karte Renderer依存更新方針

## 現在の境界

KarteがimportするGo module pathは`github.com/kirimine170/KarteRenderer`です．一方，現在のGitHub repositoryは`github.com/kirimine170/Karte_renderer`であるため，Karteの`go.mod`は前者を`require`し，後者の検証済みversionへ`replace`しています．依存更新scriptはこの構造を維持し，repositoryやmodule pathの移行を自動では行いません．

通常のtag付き`require`へ移行するには，Renderer側で次のどちらかを決定し，初回releaseを公開する必要があります．これはKarte repository内の変更だけでは完了しません．

1．GitHub repositoryを`KarteRenderer`へrenameし，現在のmodule pathと一致させてからtagを公開する．

2．現在のrepository名を維持し，Karteの`replace`右辺を公開済みSemVer tagへ固定する運用を正式に採用する．

外部決定とreleaseが完了するまでは，現行のpseudo-version pinを維持します．`main`，`master`，`HEAD`，`latest`，branch名，raw commit hashをKarteの依存指定としてcommitしてはいけません．

## 互換性contract

Rendererの公開互換性は，Karteが利用する`RenderString`，`RenderMarkdown`，`ExportHTMLPDF`，`PDFOptions`と，それらの出力・error・path処理を含みます．更新候補は，次のgateをすべて通過した場合だけ採用できます．

- Renderer module自身の`go test -count=1 github.com/kirimine170/KarteRenderer/...`．
- `TestKarteRendererDependencyContractFixtures`と`TestExportHTMLToPDFWithRendererUsesTemporaryHTMLInput`．
- Karte全体の`go test -count=1 ./...`．
- `go mod verify`，解決後module path／replace path／versionの一致，`go.mod`と`go.sum`以外にtracked diffがないこと．

これらのcontractを壊す変更は，Renderer側のrelease noteと移行手順なしに採用しません．Renderer側のversioning規則は同repositoryの`RELEASE.md`を正とし，Karte側ではexact versionだけをpinします．

## 更新手順

最初にworktreeをcleanにし，採用するexact versionを決めます．Go toolchainと`GOCACHE`には実行userからの書込権限が必要です．`frontend/node_modules`はGoのpackage走査とtracked diffの対象外なので，依存更新のために削除・移動しません．build cacheへ書き込めない環境ではGoが多数の派生path errorを出すことがありますが，scriptは環境失敗として停止し，module filesを復元します．公開済みSemVer tagを使う例は次のとおりです．

```bash
./scripts/update-karte-renderer.sh --version v0.2.0
```

外部tag公開前の明示的な検証でpseudo-versionを使う場合も，完全なversionをcallerが指定します．

```bash
./scripts/update-karte-renderer.sh \
  --version v0.0.0-20260816031944-738a366b22ba
```

scriptは次を一つのtransactionとして実行します．

1．開始時の`go.mod`と`go.sum`を保存し，既存module filesがtidyであることを確認する．

2．指定versionがcanonical SemVerであり，Goが同じexact versionへ解決することを確認する．

3．取得したRendererの`go.mod`が`github.com/kirimine170/KarteRenderer`を宣言することを確認する．

4．現行`replace`右辺だけを指定versionへ更新し，`go mod tidy`と`go mod verify`を実行する．

5．上記のRenderer test，Karte contract test，Karte全testをcacheなしで実行する．

6．tracked diffが`go.mod`と`go.sum`だけであることを再確認し，review用diffとrollback commandを表示する．

成功時は`go.mod`と`go.sum`だけが未commit差分として残ります．表示されたdiffと解決versionをreviewしてから，dependency update専用commitにします．scriptが作成した`frontend/dist/.placeholder`は成功・失敗のどちらでも削除されます．

## 失敗時restoreとrollback

解決，module path検証，tidy，verify，testのいずれかが失敗した場合，scriptは開始時の`go.mod`と`go.sum`をbyte-for-byteで復元します．`git reset`や`git checkout`は実行しません．restore自体を検証できなかった場合はexit code 125で明示的に失敗します．

既に採用・commitされたRenderer versionを以前のknown-good versionへ戻す場合は，clean worktreeで以前のexact versionを指定します．rollbackも更新と同じ解決・module path・diff・contract gateを通ります．

```bash
./scripts/update-karte-renderer.sh --rollback v0.1.1
```

成功した更新直後の出力には，開始時versionを使ったrollback commandが表示されます．公開済みtagを移動・削除してrollbackしてはいけません．不具合のあるreleaseはRenderer側で新しいpatch releaseとして修正し，緊急時だけKarteを以前のknown-good exact versionへ戻します．

## scriptのself-test

実repositoryやnetworkを変更せずにtransaction挙動を検証できます．

```bash
bash -n scripts/update-karte-renderer.sh scripts/test-update-karte-renderer.sh
./scripts/test-update-karte-renderer.sh
```

self-testは，明示version必須，mutable selector拒否，SemVerとpseudo-versionの成功，解決version不一致，module path不一致，tidy／verify／Renderer test／contract test／全testの失敗，byte-for-byte restore，一時placeholder cleanup，明示rollbackを一時Git fixtureで決定的に検証します．
