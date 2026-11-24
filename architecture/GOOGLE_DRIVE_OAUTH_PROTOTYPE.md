# Google Drive OAuth 検証

Karteのツールバーにある「Google Drive」から、Googleアカウントで接続し、Drive上のUTF-8 Markdownを新規作成・読み込み・更新できます。既存のファイルはGoogle Pickerで選ぶとKarteに認可されます。新規ファイルはマイドライブ直下に保存されます。

## Google Cloudの設定

1. 検証用Google Cloudプロジェクトで **Google Drive API** と **Google Picker API** を有効にします。
2. Google Auth Platformで、所有ドメインをAuthorized domainsに登録し、アプリのホームページとプライバシーポリシーを設定します。公開時の所有権確認にはGoogle Search Consoleを使います。
3. Audienceを利用範囲に合わせます。組織内アカウントで試す場合は組織配下のプロジェクトで **Internal** を選びます。**External / Testing** の場合は使用するアカウントをテストユーザーに追加します。External TestingのDrive認可で発行される更新トークンは通常7日で失効します。
4. Data Accessに `https://www.googleapis.com/auth/drive.file` を追加します。
5. Clientsで種類が **Desktop app** のOAuthクライアントを作り、JSONをダウンロードします。JSONはリポジトリ外に保存してください。Karteは既定ブラウザを開き、PKCE付きの認可コードを端末内の `http://127.0.0.1:<動的ポート>/oauth/callback` で受け取ります。独自ドメインは同意画面の設定に使います。

[Google Auth Platform](https://support.google.com/cloud/answer/15544987?hl=ja) · [ブランドの確認](https://developers.google.com/identity/verification/authentication-policy-compliance) · [デスクトップOAuth](https://developers.google.com/identity/protocols/oauth2/native-app) · [デスクトップ向けGoogle Picker](https://developers.google.com/workspace/drive/picker/guides/desktop-mobile-picker)

## 実アカウントで試す

Karteを起動するシェルで、ダウンロードしたファイルを指定します。Windows PowerShellの例です。

```powershell
$env:KARTE_GOOGLE_OAUTH_CLIENT_FILE = 'C:\path\outside-repo\desktop-client.json'
wails dev
```

1. ツールバーの **Google Drive → 接続** を選択し、ブラウザの同意画面を完了します。ダイアログに接続アカウントのメールアドレスが表示されます。
2. **新規Markdown** を選択し、`note.md` と本文を入力して **Driveに保存** を押します。**ブラウザで開く** から、同じファイルをDriveで確認します。
3. GoogleドキュメントのMarkdownモードで内容を変更し、保存が反映されたらKarte側で **開く／再読み込み** を押します。YAMLフロントマターや `@import` を含むテスト文書では、往復後のソースも確認します。
4. Driveに別途置いた `.md` を **Driveからファイルを選ぶ** で開きます。編集権限があるファイルはKarteから更新できます。
5. **接続を解除** を押すとKarteのセッションを破棄します。Googleアカウント側の許可を取り消す場合はGoogleアカウントの接続アプリ設定を使います。

トークンは起動中のGoプロセスのメモリで管理し、再起動後は再接続します。Driveでの変更は手動で再読み込みします。保存前に最後に読んだ本文のチェックサムを照合します。照合と更新は別のリクエストなので、この短い間隔に発生した同時編集は試作の検出範囲外です。検証には専用のテスト文書を使ってください。

## 自動テスト

`go test ./internal/googledrive` はローカルのHTTPサーバーでコールバック、PKCEとstate、認可の中断、トークンの更新と失効、認可済みのMarkdownの読み書き、保存直前に検出できる競合をテストします。`frontend` の `npm test -- --run` は画面の認可とファイル操作をテストします。GitHub ActionsにはGoogleアカウントやOAuthクライアントの認証情報が不要です。
