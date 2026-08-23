# Karte Format asset discovery contract v0.1

## 状態と適用範囲

この文書は，T-011でKarte側に先行実装したportable theme assetの検証規則を定義します．将来のKarteRenderer `karte-format.yaml` schema v1にある`markdown.layout`，`markdown.styles`，`marp.themes`，`assets.directory`を前提にしますが，manifestのparseとschema validationはRendererの責務です．Karteの現在のRenderer pinはこのmanifest APIをまだ公開していないため，`internal/themeasset.Discover`は検証済みのentrypoint一覧と`assets.directory`を引数として受け取ります．

この先行実装はpackageの読取，参照発見，境界検証，inventory作成までです．preview，site，PDFへassetを配信，複製，rewrite，embedするruntime接続は行いません．

## 現行経路の監査結果

| 経路 | 現在の解決 | portable package上の問題 |
| --- | --- | --- |
| Renderer layout | `<data-root>/themes/default/preview.html`を優先し，なければ`layout.html`を読む | 現在pinは固定pathを通常の`os.ReadFile`で読み，layout symlinkのpackage外到達を防がない．CSS参照を発見もrewriteもしない |
| editor preview | `RenderString(dataRoot，content)`のHTMLをiframeへ書き，Markdownの一部の`img src`だけを`/image/<data-root-relative>`へ変更する | template内の相対URLはtheme file基準ではなくiframe document基準になる．CSS `url()`，`@font-face`，`link href`，`srcset`は未処理である |
| `themes/custom.css` | raw CSSを読書きし，frontendの`style`要素へ挿入する | path discovery，size上限，remote／`file:`拒否，package inventoryがない．format packageとは別のlegacy機能である |
| static site | `RenderMarkdown`のHTMLだけをstaging `public`へ書く | theme assetを`public`へ複製せず，inline CSSの相対URLは出力HTMLの階層を基準に解決される．full buildでは手置きassetも保持されない |
| PDF | `/image/`と`data/image/`の一部の`img src`だけをdata URI化し，一時directoryのHTMLを`AllowLocalFiles: true`でRendererへ渡す | 相対font／CSS／imageは一時directory基準となり壊れる．絶対`file:`参照はpackage外local fileを読む可能性があり，remote参照はengine依存で非決定的である |
| frontend KaTeX／Mermaid | Vite bundleとRenderer内蔵data fontを別経路で利用する | application assetであり，format package inventoryには含めない |

repositoryの`themes/default/preview.html`と`layout.html`はjsDelivr上のPico，KaTeX，Mermaidを参照し，active scriptも含みます．さらにrepository rootの`themes/`は`templates/karte_data_template`に存在せず，`cmd/buildmatrix`がartifactへ複製するtemplateにも入りません．`internal/themeasset.TestLegacyDefaultThemeAuditIsExplicit`はこれらを成功fixtureと混ぜず，期待された既知findingとして固定します．

## Package境界

1つのformat package directoryが唯一のsecurity境界です．`assets.directory`のv0.1値は`assets`とし，fontは`assets/fonts/`，imageは`assets/images/`へ置きます．manifest pathと参照後のcanonical targetは，小文字ASCII，数字，dot，underscore，hyphenだけで構成します．絶対path，Windows drive path，backslash，NUL，percent-encoded path，dot segment，case-fold collision，Windows reserved device nameを拒否します．

entrypointはHTMLまたはCSSです．典型例は次の4種です．

- `markdown/layout.html`
- `markdown/base.css`などの`markdown.styles`
- `marp/karte.css`などの`marp.themes`
- layoutやstylesheetから再帰的に参照するlocal `.css`

すべてのdirectoryとfileを`Lstat`し，symlinkと非regular fileを拒否します．読取は`os.Root`に閉じたhandleから行い，open前後のfile identityを照合してから，size上限内のbyte snapshotを作ります．発見，hash，parseはそのsnapshotだけを使います．runtime接続後も，validation後に元pathを無条件で再openしてはいけません．同じsnapshotを配信するか，confined handleからatomic stagingへ複製する必要があります．

default上限は256 files，package全体32 MiB，各file 8 MiB，parse対象HTML／CSS 1 MiBです．結果はpath順に安定化し，各assetのbyte数とSHA-256を返します．

## URLとCSS規則

local参照は参照元fileのdirectoryを基準に解決します．`?query`と`#fragment`はfilesystem lookup pathから分離し，inventoryはqueryとfragmentを除いたcanonical targetをhashします．fragment-only参照は許可します．

次を拒否します．

- `http:`，`https:`，`file:`などscheme付きURL，protocol-relative URL，root-absolute URL
- `data:` URL．embedded font／imageもpackage inventory外になるため例外にしない
- package rootを越える参照，percent-encoded local path，backslashを含むpath
- 不明な拡張子，または`assets.directory`の規定subdirectory外にあるfont／image
- SVG．内部scriptや二次URLの再帰検査をv0.1が提供しないため，PNG，JPEG，GIF，WebP，AVIFだけをportable imageとする

CSSはregexではなくfail-closed lexerで走査します．comments，quoted／unquoted `url()`，quoted／`url()`形式の`@import`，文字列，blockを区別し，unterminated tokenをerrorにします．CSS escapeは別表現によるscanner迂回を防ぐためv0.1では拒否します．string candidateを持てる`image-set()`も拒否します．local `@import`は`.css`だけを許可して再帰走査します．Marp entrypoint内の`@import "default"`，`"gaia"`，`"uncover"`だけはRenderer内蔵themeとしてinventory外の明示builtinに分類します．

`@font-face`は次の形に限定します．

```css
@font-face {
  font-family: "Project Sans";
  src: url("../assets/fonts/project-sans.woff2?v=1") format("woff2");
  font-display: swap;
}
```

1つの`@font-face`にpackage内WOFF2 `url()`を1つだけ要求し，`local()`，TTF，OTF，WOFFを拒否します．これによりhostにinstall済みのfontで出力が変化しません．validatorはbyte inventoryと拡張子contractを固定しますが，font／image decoderとしての完全なbinary妥当性検査はしません．runtime配信時は固定MIME，`nosniff`，対象engineのdecode error処理も必要です．

## HTML template規則

HTML parserで`src`，`href`，`srcset`，`poster`，`data`，`style`と`style`要素を走査します．stylesheetとimageは同じpackage規則で検証し，発見したstylesheetを再帰走査します．`srcset`はlocal URLと1つ以下の`w`または`x`descriptorに限定します．

network accessを静的inventoryから迂回できるため，`script`，`iframe`，`object`，`embed`，`base`，inline event handler，meta refresh，`srcdoc`を拒否します．file navigationを行うanchor `href`も拒否し，fragment-only anchorだけを許可します．

## Fixtureと検証

成功fixtureは`internal/themeasset/testdata/valid`です．future `karte-format.yaml` schemaに合わせ，owner-relative CSS，query／fragment付きasset，WOFF2，responsive image，inline style，recursive stylesheet，Marp builtin importを含みます．fixture内のfont／image byteはreference discovery用placeholderであり，表示品質fixtureではありません．

focused検証は次で実行します．

```sh
go test ./internal/themeasset
```

testはremote，`data:`，traversal，encoded path，missing file，CSS escape，unterminated comment，`local()` font，非WOFF2，`image-set()`，HTML active content，event handler，`srcset`，root／内部symlink，file count／byte上限，query／fragment分離，決定的reportをcoverします．

## Runtime接続のblocker

- T-005でRenderer release／pin方針が確定し，T-009の`karte-format.yaml` APIをKarteが利用できるimmutable versionへ更新される必要がある．現在pinは変更しない
- 後続T-012で，manifest selectionとentrypointをpreview，site，PDFへ一貫して渡す必要がある
- Rendererでlayout loadをpackage-confinedかつsymlink-safeにし，asset discovery resultまたはsnapshotを受け渡すAPIが必要である
- previewはconfined theme asset handlerとCSPを用意し，siteは決定的な出力prefixへassetをcopyしてURLをrewriteし，PDFは同じsnapshotをembedまたは保存したdirectory構造で解決する必要がある
- legacy `themes/default`と`themes/custom.css`をformat packageへmigrationし，repository themeをtemplate／全platform artifactへ一度だけ梱包するownership規則が必要である

これらが完了するまでは，`Discover`の成功をruntime安全性の保証として扱いません．現在のlegacy preview，site，PDF経路はこのcontractをまだ消費していません．
