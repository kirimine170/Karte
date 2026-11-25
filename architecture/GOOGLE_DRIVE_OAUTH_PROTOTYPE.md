# Google Drive OAuth 検証

Karteのツールバーにある「Google Drive」から、マイドライブ直下の `Karte` フォルダにあるUTF-8 Markdown、Googleスプレッドシート、写真を扱えます。初回接続でKarteがフォルダを作り、再接続時はアクセス可能な同名フォルダを再利用します。Drive側で追加したファイルは、Google Pickerで個別に認可すると一覧に表示されます。

## 配布版で接続する

Karteの **Google Drive → 接続** を押すと、既定ブラウザでGoogleのログイン・同意画面が開きます。利用者に必要なのはGoogleアカウントです。Cloud Consoleでの設定やOAuthクライアントの組み込みは配布元が行います。

## 配布元でのGoogle Cloudの設定

1. 検証用Google Cloudプロジェクトで **Google Drive API**、**Google Picker API**、**Google Sheets API** を有効にします。既存のOAuthクライアントと `drive.file` をそのまま使えます。
2. Google Auth Platformで、所有ドメインをAuthorized domainsに登録し、アプリのホームページとプライバシーポリシーを設定します。公開時の所有権確認にはGoogle Search Consoleを使います。
3. Audienceを利用範囲に合わせます。組織内アカウントで試す場合は組織配下のプロジェクトで **Internal** を選びます。**External / Testing** の場合は使用するアカウントをテストユーザーに追加します。External TestingのDrive認可で発行される更新トークンは通常7日で失効します。一般のGoogleアカウントに配布する場合は **External** を本番公開に切り替え、同意画面のブランド確認を行います。
4. Data Accessに `https://www.googleapis.com/auth/drive.file` を追加します。
5. Clientsで種類が **Desktop app** のOAuthクライアントを作り、JSONをダウンロードします。JSONはリポジトリ外に保存してください。Karteは既定ブラウザを開き、PKCE付きの認可コードを端末内の `http://127.0.0.1:<動的ポート>/oauth/callback` で受け取ります。独自ドメインは同意画面の設定に使います。

[Google Auth Platform](https://support.google.com/cloud/answer/15544987?hl=ja) · [ブランドの確認](https://developers.google.com/identity/verification/authentication-policy-compliance) · [デスクトップOAuth](https://developers.google.com/identity/protocols/oauth2/native-app) · [デスクトップ向けGoogle Picker](https://developers.google.com/workspace/drive/picker/guides/desktop-mobile-picker)

### 配布用ビルド

配布元のビルド環境で `KARTE_GOOGLE_OAUTH_DISTRIBUTION_CLIENT_JSON` にデスクトップクライアントのJSON全文を設定し、通常の `go run ./cmd/buildmatrix --targets windows --clean` を実行します。他OSも同じ変数を使えます。GitHub ActionsのWindowsリリースでは、同名のリポジトリシークレットを設定するとビルドに渡されます。

PowerShellで手動ビルドするときは、リポジトリ外のファイルから読み込みます。

```powershell
$env:KARTE_GOOGLE_OAUTH_DISTRIBUTION_CLIENT_JSON = [IO.File]::ReadAllText('C:\path\outside-repo\desktop-client.json')
try {
    go run ./cmd/buildmatrix --targets windows --clean
} finally {
    Remove-Item Env:KARTE_GOOGLE_OAUTH_DISTRIBUTION_CLIENT_JSON
}
```

デスクトップクライアントの情報は配布バイナリから取得できる前提です。Webアプリやサービスアカウントの認証情報、ユーザーのトークンには別の保管経路を使います。設定を組み込んでいないビルドでは、Karteが配布元への案内を表示します。

### 開発中だけ個別のクライアントを使う

`KARTE_GOOGLE_OAUTH_CLIENT_FILE` は組み込んだクライアントを上書きする開発用設定です。Windows PowerShellの例です。

```powershell
$env:KARTE_GOOGLE_OAUTH_CLIENT_FILE = 'C:\path\outside-repo\desktop-client.json'
wails dev
```

検証対象を特定のGoogleアカウントに限定する場合は、起動前に `KARTE_GOOGLE_OAUTH_EXPECTED_ACCOUNT` にそのメールアドレスを設定します。

## 実アカウントで試す

1. ツールバーの **Google Drive → 接続** を選択し、ブラウザの同意画面を完了します。ダイアログに接続アカウントのメールアドレスが表示されます。
2. **新規Markdown** を選択し、`note.md` と本文を入力して **Driveに保存** を押します。**ブラウザで開く** から、同じファイルをDriveで確認します。
3. GoogleドキュメントのMarkdownモードで内容を変更し、保存が反映されたらKarte側で **開く／再読み込み** を押します。YAMLフロントマターや `@import` を含むテスト文書では、往復後のソースも確認します。
4. Drive側で `Karte` フォルダに追加したファイルは **Karteフォルダのファイルを選ぶ** で開きます。編集権限があるファイルはKarteから更新できます。
5. **接続を解除** を押すとKarteのセッションを破棄します。Googleアカウント側の許可を取り消す場合はGoogleアカウントの接続アプリ設定を使います。

トークンは起動中のGoプロセスのメモリで管理し、再起動後は再接続します。Driveでの変更は手動で再読み込みします。保存前に最後に読んだ本文のチェックサムを照合します。照合と更新は別のリクエストなので、この短い間隔に発生した同時編集は試作の検出範囲外です。検証には専用のテスト文書を使ってください。

### スプレッドシートと写真

- **新規スプレッドシート** でCSVを入力するか、**ローカルファイルを取り込む** でCSVを選びます。**Driveに保存** でGoogleスプレッドシートを作成します。初回の値の型はGoogleのCSVインポート規則に従います。
- Karteではスプレッドシートの先頭のグリッドタブをUTF-8 CSVとして読み込みます。既存のシートを保存すると、変更したセルに文字列を書き込みます。空欄にしたセルは値を消します。他のタブ、既存の書式、変更していない数式は維持します。CSVは2 MiB・10万セルまでです。
- **ローカルファイルを取り込む** から5 MiB以下のJPEG、PNG、GIF、WebPを選ぶと、プレビューして元の画像データをDriveに保存できます。**写真を差し替える** から同じ形式の写真を選ぶと、Drive上の同じファイルを更新できます。
- **ブラウザで開く** から、同じスプレッドシートをGoogle Sheetsで編集したり、同じ写真をGoogle Driveで表示したりできます。

## Drive関係検索

配布元は内部検証用OAuthプロジェクトの **Data Access** に `https://www.googleapis.com/auth/drive.readonly` を追加します。一般配布に使う場合はGoogleの制限付きスコープの確認を先に済ませます。保存とPickerは既存の `drive.file` 接続を使います。 [Google Driveの権限](https://developers.google.com/workspace/drive/api/guides/api-specific-auth) · [デスクトップ向けPicker](https://developers.google.com/workspace/drive/picker/guides/desktop-mobile-picker)

1. **Drive関係検索 → 読み取り権限で接続** を開き、そのアカウントのマイドライブにある `Karte` フォルダを選択します。GoogleにはDrive全体の読み取り権限を求めます。Karteの対象は選択フォルダ配下です。
2. Driveの説明欄に `Karte tags: 展示, HackSick` を書きます。関係を登録するには、別の行に `Karte link: https://drive.google.com/open?id=...` を追記します。リンクは1行につき1件です。
3. Karteのタグを複数選ぶと、すべてに一致するファイルが表示されます。ファイルを選ぶと、共通タグ、リンク先、被リンクとそのグラフを確認できます。**Driveで開く** で原本を開きます。
4. 画面を開くとき、開いている間の60秒ごと、および手動の **更新** で索引を作り直します。名前変更やサブフォルダ間の移動でファイルIDは維持されます。同じファイルの版更新後も説明欄が残ります。フォルダ外へ移動、権限喪失、ゴミ箱への移動は次回更新で検索に反映します。索引とトークンはメモリに保持し、解除時に消去します。

**ローカルファイルを取り込む** からMarkdownを追加すると、既存のYAMLタグと本文のDriveリンクを説明欄に引き継ぎます。WebPのタグも引き継ぎます。既存のKarte画像に付属するYAMLタグは **Karteの写真を選ぶ → 写真とタグをDriveにコピー** で取り込みます。Drive側から直接追加した旧Markdown・WebPは、説明欄にタグ行がない場合に旧タグも読み取ります。**引き継ぎ用のタグ・リンクをコピー** から、旧タグと一意に解決できたリンクをDriveの説明欄へ追記できます。空の `Karte tags:` 行はタグの解除として扱います。

説明欄はDriveでそのファイルを閲覧できるユーザーにも見えます。ダウンロード後の再アップロードや別アカウントへの移行は別のファイルIDになるため、その場合はタグやリンクの移植が必要です。

## 自動テスト

`go test ./internal/googledrive` はローカルのHTTPサーバーでコールバック、PKCEとstate、認可の中断、トークンの更新と失効、認可済みのMarkdownの読み書き、保存直前に検出できる競合をテストします。`go test ./cmd/buildmatrix` はテスト用のクライアントを組み込んで、利用者側の設定なしでブラウザ認可が始まることを確認します。`frontend` の `npm test -- --run` は画面の認可とファイル操作をテストします。GitHub Actionsの自動テストにはGoogleアカウントやOAuthクライアントの認証情報が不要です。
