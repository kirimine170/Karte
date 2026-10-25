# Instagram Post lab

Instagramタブに，画像・キャプションの編集画面を追加しています．Markdownの編集状態を保持しながら切り替えられます．画像をInstagramへ送信する機能はありません．

## 責務

| Karte_renderer | Karte |
| --- | --- |
| 下書きJSONの契約，投稿制約の暫定ルール | 投稿エディタ，画像の追加・削除・並べ替え |
| 切り出しの幾何計算，画像の描画 | ドラッグ・スライダーなどの入力操作 |
| フィード・プロフィールのHTMLとCSS | タブのライフサイクル・状態の保持 |
| キャプションの折り返し・省略位置の計測 | 下書きの自動保存・読み込み |
| ブラウザの画像処理とCLI用Go画像出力 | ネイティブの保存ダイアログ・ファイル書き込み |

`frontend/public/postlab`はアプリ側の編集UIです．iframeで既存のMarkdownエディタからDOMとCSSを分離します．初回選択時に読み込み，タブを戻しても同じフレームを維持します．画像本体はメモリ上で扱い，テキストと設定だけをlocalStorageへ保存します．リロード後の元画像は再選択してください．

`frontend/public/postlab-renderer`は，固定したGo依存の`instagram.WebAssets()`から生成した配布用キャッシュです．ここを直接編集しません．依存の更新後はルートで次を実行します．

```sh
go run ./cmd/postlab-assets
go run ./cmd/postlab-assets --check
```

バックエンドのテストでも依存側の埋め込みアセットと配布ファイルの一致を検証します．描画ロジックの修正はKarte_rendererで行ってから，Karteの依存と配布キャッシュを更新します．この構成により，Nodeだけを使うフロントエンドCIでも事前生成した同じ描画アセットを利用できます．

デスクトップ版のJPEG・下書き保存は，フレームが親へ送った要求を`PostLab`コンポーネントが受け，Wailsの保存APIに渡します．送信元フレームとoriginを照合し，利用者がネイティブダイアログで選んだ保存先にだけ書き込みます．ブラウザでの開発時は通常のダウンロードを使います．Ctrl/Cmd+Sもフレーム内では下書きの保存になります．

## CLIとの互換性

下書きは`karte.instagram-draft`のバージョン1です．元画像本体を含まないため，CLI利用時はJSONと同じルート配下に元画像を配置します．ブラウザで保存するファイル名はベース名です．同名ファイルを複数使うときは，CLI側で衝突しない相対パスへ変更してください．試作品のサンプルSVGは表示用で，CLI入力にはJPEG・PNG・WebPを使います．

```sh
karte-renderer instagram preview draft.json preview.html
karte-renderer instagram image --index 1 draft.json post.jpg
```

JPEGの切り出し座標はEXIFの向き補正後です．CLIは画像から寸法を読み直し，JSON内の情報用`sourceRect`をそのまま信頼しません．ブラウザとGoの再サンプリング・圧縮の実装差は残ります．GUIの起動やWailsへの接続は必要ありません．

端末名は論理画面幅・高さのプリセットです．実際の別OSフォントを厳密に再現するものではありません．投稿制約値は暫定で，確認状況と再現範囲を画面内の「仕様と制約」に明記しています．
