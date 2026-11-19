param(
    [Parameter(Mandatory=$true)][string]$RecipientPublicKeyPath,
    [Parameter(Mandatory=$true)][string]$OutputPath,
    [string]$CredentialRoot = (Join-Path $PSScriptRoot 'private-startup')
)
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force
if(Test-Path -LiteralPath $OutputPath){throw 'Output already exists.'}
$rsa=[Security.Cryptography.RSACng]::new()
try {
    $rsa.FromXmlString([IO.File]::ReadAllText($RecipientPublicKeyPath))
    if($rsa.KeySize -lt 4096){throw 'A 4096-bit recipient key is required.'}
    $encrypted=[ordered]@{}
    foreach($name in @('chatgpt-runtime-key','grok-cloudflare-key')) {
        $key=Read-KarteSavedKey (Join-Path $CredentialRoot ($name+'.clixml'))
        $buffer=[IntPtr]::Zero
        $bytes=$null
        try {
            $buffer=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($key)
            $bytes=[Text.Encoding]::UTF8.GetBytes([Runtime.InteropServices.Marshal]::PtrToStringBSTR($buffer))
            if($bytes.Length -gt (($rsa.KeySize/8)-66)){throw 'Credential exceeds RSA-OAEP size limit.'}
            $encrypted[$name]=[Convert]::ToBase64String($rsa.Encrypt($bytes,[Security.Cryptography.RSAEncryptionPadding]::OaepSHA256))
        } finally {
            if($bytes){[Array]::Clear($bytes,0,$bytes.Length)}
            if($buffer -ne [IntPtr]::Zero){[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($buffer)}
            $key.Dispose()
        }
    }
    $envelope=[ordered]@{version=1;algorithm='RSA-OAEP-SHA256';recipient_sha256=(Get-FileHash -LiteralPath $RecipientPublicKeyPath -Algorithm SHA256).Hash.ToLowerInvariant();credentials=$encrypted}
    [IO.File]::WriteAllText([IO.Path]::GetFullPath($OutputPath),($envelope|ConvertTo-Json -Depth 4),[Text.UTF8Encoding]::new($false))
    [pscustomobject]@{Path=[IO.Path]::GetFullPath($OutputPath);RecipientSHA256=$envelope.recipient_sha256;EncryptedCredentials=$encrypted.Count}
} finally {$rsa.Dispose()}
