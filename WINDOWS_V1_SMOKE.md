# Windows v1 クリーンVM試験

対象は開発ツールを導入していないWindows 11 x64である。試験結果はrelease issueへ、candidate
SHA、ZIPのSHA-256、実行日時、実行者、画面またはログの証跡とともに記録する。

## 前提確認

- [ ] `Get-FileHash -Algorithm SHA256`の結果が`SHA256SUMS-windows.txt`と一致する
- [ ] ZIP内の`SIGNING_STATUS.txt`が`SIGNED AND VERIFIED`である
- [ ] `Get-AuthenticodeSignature`で全EXE／DLLが`Valid`であり、起動時に`Unknown publisher`が出ない
- [ ] ZIPにKarte本体、Sherpa／ONNX／PortAudio DLL、FFmpeg、`karte_data_template`、第三者ライセンス、`runtime-manifest.json`が含まれる

## 主要フロー

- [ ] 日本語・空白・絵文字を含むユーザー名／展開先から初回起動できる
- [ ] `%LOCALAPPDATA%\Karte\karte_data`が作られ、Program Files相当の読取専用展開先でも保存できる
- [ ] 実行ファイル隣接の旧`karte_data`を用意すると初回起動時に非破壊コピーされ、原本が残る
- [ ] Markdownの作成、保存、終了、再起動、再読込が成功し、Hardwrap設定が保持される
- [ ] 日本語、空白、絵文字、OneDrive配下の画像／PDFをプレビューできる
- [ ] Edgeが`PATH`にない状態でPDFプレビューとPDF出力が成功する
- [ ] WebP画像へKARTタグを書込み、再読込後に同じタグを取得できる
- [ ] WAV／MP3／M4Aの取込、録音後のM4A変換、再生が外部FFmpeg導入なしで成功する
- [ ] ASRの成功、無効設定、モデル不備の各経路で初期化表示が終了する
- [ ] ASR結果が再起動なしで画面に表示される
- [ ] 画面／範囲スクリーンショットが通常DPI、150% DPI、複数ディスプレイで成功する
- [ ] スクリーンショットのキャンセルでファイルやエラー表示が残らない
- [ ] 正常終了でき、再起動後に書込み権限エラーや欠落DLLエラーがない

## リリース判定

上記に1件でも未確認または失敗があればWindows v1公開は`BLOCKED`とする。Windows 10、ARM64、
NSISインストーラー、ウィンドウ単位キャプチャはこの試験の対象外である。
