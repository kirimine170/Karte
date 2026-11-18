param(
    [Parameter(Mandatory=$true)][string]$EnvelopePath,
    [string]$CredentialRoot = (Join-Path $PSScriptRoot 'private-startup')
)
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force
$envelope=[IO.File]::ReadAllText($EnvelopePath)|ConvertFrom-Json
$publicPath=Join-Path $CredentialRoot 'migration-public-key.xml'
$expected=(Get-FileHash -LiteralPath $publicPath -Algorithm SHA256).Hash.ToLowerInvariant()
if($envelope.version -ne 1 -or $envelope.algorithm -cne 'RSA-OAEP-SHA256' -or $envelope.recipient_sha256 -cne $expected){throw 'Credential envelope does not match this recipient.'}
$names=@('chatgpt-runtime-key','grok-cloudflare-key')
if(@($envelope.credentials.PSObject.Properties).Count -ne 2){throw 'Unexpected credential fields.'}
foreach($name in $names){if(Test-Path -LiteralPath (Join-Path $CredentialRoot ($name+'.clixml'))){throw 'Destination credentials already exist.'}}
$private=Read-KarteSavedKey (Join-Path $CredentialRoot 'migration-private-key.clixml')
$buffer=[IntPtr]::Zero
$rsa=[Security.Cryptography.RSACng]::new()
$decrypted=@{}
try {
    $buffer=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($private)
    $rsa.FromXmlString([Runtime.InteropServices.Marshal]::PtrToStringBSTR($buffer))
    foreach($name in $names){
        $bytes=$null
        try {
            $bytes=$rsa.Decrypt([Convert]::FromBase64String($envelope.credentials.$name),[Security.Cryptography.RSAEncryptionPadding]::OaepSHA256)
            if($bytes.Length -eq 0){throw 'Empty credential in envelope.'}
            $decrypted[$name]=ConvertTo-SecureString ([Text.Encoding]::UTF8.GetString($bytes)) -AsPlainText -Force
        } finally {if($bytes){[Array]::Clear($bytes,0,$bytes.Length)}}
    }
    # Authenticate/decrypt both fields before writing either destination file.
    foreach($name in $names){Save-KarteKey $decrypted[$name] (Join-Path $CredentialRoot ($name+'.clixml'))}
    Write-Output 'Imported both credentials into this Windows user DPAPI store.'
} finally {
    foreach($key in $decrypted.Values){$key.Dispose()}
    $rsa.Dispose();$private.Dispose()
    if($buffer -ne [IntPtr]::Zero){[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($buffer)}
}
