# Karte 合成MCP：Windows自動起動と1050への移設

対象は今回の合成Workspaceです。Karteデスクトップアプリを開く必要はありません。
ChatGPT用のSecure MCP Tunnelと、Grok用のHTTP MCP＋Cloudflare Tunnelを
ログオン時にバックグラウンド起動します。停止・異常終了後は1分間隔で再試行します。

## 最初の設定

このフォルダでPowerShellから実行します。

```powershell
.\Install-Karte-Synthetic-Autostart.ps1
```

初回だけOpenAI実行用キーとCloudflareトークンを非表示欄に入力します。
Cloudflareは管理画面からコピーした1行コマンドも受け付けますが、実行はしません。
トークンだけを取り出して保存します。既存の保存済みキーがあれば再入力しません。

秘密値は`private-startup/`のCLIXMLにWindows DPAPIで暗号化します。
復号できるのは、保存した端末の同じWindowsユーザーだけです。
タスクの引数・Git・移設ZIPには秘密値を含めません。

登録するタスクは`Karte Synthetic ChatGPT MCP`と`Karte Synthetic Grok MCP`です。
どちらも現在のユーザーのInteractiveログオン・Limited権限で動作します。
実行ポリシーの指定はタスクの起動プロセスだけに適用し、Windows全体やユーザーの設定は変更しません。
同じ起動元を指す旧形式の登録済みタスクも更新できます。
ログオン20秒後に起動し、ログオン中は1分ごとの起動確認も行います。
稼働中のタスクは重複起動しません。Windows Job Objectにより、起動元が
強制終了した場合も、その起動元が管理する子プロセスを終了します。

**Windows起動後、最初のユーザーログオン前には動作しません。**
ログオン後の画面ロック中は動作します。スリープや電源断の間は接続できません。
常時稼働する1050側に置く場合も、対象ユーザーのログオンが必要です。

## 確認・停止・再開

```powershell
.\Manage-Karte-Synthetic-Startup.ps1 -Action Status
.\Manage-Karte-Synthetic-Startup.ps1 -Action Check
.\Manage-Karte-Synthetic-Startup.ps1 -Action Stop
.\Manage-Karte-Synthetic-Startup.ps1 -Action Start
.\Manage-Karte-Synthetic-Startup.ps1 -Action Restart
```

既存サービスとポートが競合する場合は、停止してから`private-startup/runtime-ports.json`を作成します。
省略時はHTTP 8791、metrics 8792です。1050では次の設定を使います。

```json
{"GrokHTTP":18791,"GrokMetrics":18792}
```

どちらもloopback専用です。1024〜65535の異なる整数を指定し、議会用18640は指定できません。
Cloudflareの既存Karte公開経路のoriginも、HTTP側のポートへ合わせます。
ポート設定は端末ごとのファイルであり、移設ZIPには含めません。

`Stop`は自動再試行も無効にします。`Start`で再度有効化します。
`Check`はChatGPTの`/readyz`、Grokの未認証HTTP 401、Cloudflareの`/ready`を検査します。
最終確認は実際のChatGPT・Grokから検索・読取し、監査記録を照合します。

失敗時は`synthetic-chatgpt-tunnel.stderr.log`と
`synthetic-grok-tunnel.stderr.log`をローカルで確認します。
Grokの子プロセスIDは`synthetic-grok-runtime.json`に保存されます。

キーを差し替える場合：

```powershell
.\Manage-Karte-Synthetic-Startup.ps1 -Action Stop
.\Start-Karte-Synthetic-ChatGPT.ps1 -SetupAutostart -ReplaceKey -ConfigureOnly
.\Start-Karte-Synthetic-Grok.ps1 -SetupAutostart -ReplaceKey -ConfigureOnly
.\Manage-Karte-Synthetic-Startup.ps1 -Action Start
```

## 移設パッケージ

```powershell
.\Build-Karte-Synthetic-Package.ps1 -OutputPath .\karte-synthetic-runtime-20261118.zip
```

ZIPには起動スクリプト、検証済みバイナリ、合成Markdown、AI別policy、
AIトークンのハッシュ設定を含めます。秘密値のCLIXML、ログ、Git履歴、
実Workspaceは含めません。各ファイルのSHA-256は`runtime-manifest.json`に記録します。

## キーを再入力せずに移す場合

1050で`New-Karte-Migration-Recipient.ps1`を実行し、公開鍵ファイルだけを元PCへ渡します。
元PCで次を実行します。

```powershell
.\Export-Karte-Migration-Credentials.ps1 -RecipientPublicKeyPath .\1050-public-key.xml -OutputPath .\1050.sealed.json
```

生成した暗号文だけを1050へ渡し、次を実行します。

```powershell
.\Import-Karte-Migration-Credentials.ps1 -EnvelopePath .\1050.sealed.json
.\Install-Karte-Synthetic-Autostart.ps1 -ConfigureOnly
```

RSA-OAEP-SHA256で1050の公開鍵宛てに暗号化し、復号後は1050のユーザー用DPAPIで保存します。
秘密値や復号用の秘密鍵をチャットやGitへ渡す必要はありません。
暗号文の宛先違い・改ざんを拒否し、既存の保存済みキーは上書きしません。
`Test-Karte-Migration-Credentials.ps1`で合成キーによる転送を検証できます。

## kirimine-laptop（1050）への切替

接続先の端末名とアドレスは各利用環境で確認する。
Codexの登録先は既存の1050用リモートホストです。
このセッションからのSSH（22）とWinRM（5985）は応答していません。
接続手段を増設せず、既存のCodexリモート接続側で作業します。

1. パッケージを1050の専用フォルダ（例：
   `C:\Users\kirim\Documents\ChatGPT\karte-synthetic-mcp`）に展開する。
   ephy・議会のフォルダやタスク・ポート18640には触れない。
2. `runtime-manifest.json`の各SHA-256を確認し、
   `Test-Karte-Synthetic-Autostart.ps1`を実行する。
3. `Install-Karte-Synthetic-Autostart.ps1 -ConfigureOnly`で
   1050側のユーザー用にキーを登録する。タスクは無効状態で準備される。
   元PCのDPAPIファイルをコピーしても復号できない。
4. 元PCの自動起動を停止してから、1050で`-Action Start`を実行する。
   2台が別々のWorkspaceを同時に配信しないようにする。
5. 1050で`-Action Check`を確認し、ChatGPTとGrokで
   `kartehandofflive`の検索、`synthetic-live-shared`の読取、
   `kartehandoff-decision-autonomous-live`の決定読取を行う。
   1050側の監査記録に各AIのアクセスが残ることを確認する。
6. 問題があれば1050で`-Action Stop`し、元PCで`-Action Start`して戻す。

同じOpenAI Tunnel IDとGrok固定URLを使うため、移設だけで
ChatGPT・Grokのコネクタを作り直す必要はありません。
GrokのAIトークンを再発行せず、パッケージ内のハッシュ設定を継承します。

## 検証

```powershell
.\Test-Karte-Synthetic-Autostart.ps1
.\Test-Karte-Runtime-Ports.ps1
```

暗号化保存と差替え、不正な保存データ、Cloudflare入力の正規化、
ログオンと継続再試行、移設時の無効登録、タスクの実起動、
強制停止時の子プロセス終了を合成データで検査します。
実際の電源再投入・ログオンの確認は、実施した端末ごとに別途記録します。
CIでは`Test-Karte-ChatGPT-Startup.ps1 -RegistrationOnly`で登録と互換更新を検証し、
Interactiveログオンが必要な実起動は省略したことを明示します。
ポート試験は合成HTTPプロセスを使い、別サービスの401とmetrics 200を正常稼働と扱わないこと、
実行ファイル・開始時刻・loopback listenerの所有一致を検証します。
所有確認用の古いruntime記録は、`Restart`で新しい記録へ更新します。
