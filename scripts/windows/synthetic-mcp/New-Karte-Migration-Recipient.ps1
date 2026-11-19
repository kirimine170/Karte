param([string]$CredentialRoot = (Join-Path $PSScriptRoot 'private-startup'))
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force
$privatePath = Join-Path $CredentialRoot 'migration-private-key.clixml'
$publicPath = Join-Path $CredentialRoot 'migration-public-key.xml'
if ((Test-Path -LiteralPath $privatePath) -or (Test-Path -LiteralPath $publicPath)) { throw 'A migration recipient already exists.' }
$rsa = [Security.Cryptography.RSACng]::new(4096)
$secret = $null
try {
    $secret = ConvertTo-SecureString $rsa.ToXmlString($true) -AsPlainText -Force
    Save-KarteKey $secret $privatePath
    [IO.File]::WriteAllText($publicPath,$rsa.ToXmlString($false),[Text.UTF8Encoding]::new($false))
    [pscustomobject]@{PublicKeyPath=$publicPath;PublicKeySHA256=(Get-FileHash -LiteralPath $publicPath -Algorithm SHA256).Hash.ToLowerInvariant()}
} finally { if($secret){$secret.Dispose()};$rsa.Dispose() }
