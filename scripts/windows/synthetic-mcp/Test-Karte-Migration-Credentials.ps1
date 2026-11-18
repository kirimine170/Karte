$ErrorActionPreference='Stop'
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force
$root=Join-Path $PSScriptRoot ('migration-test-'+[guid]::NewGuid().ToString('N'))
$source=Join-Path $root 'source'
$recipient=Join-Path $root 'recipient'
$other=Join-Path $root 'other'
[void][IO.Directory]::CreateDirectory($source)
try {
    $values=@{'chatgpt-runtime-key'='synthetic-openai-only';'grok-cloudflare-key'='eyJsynthetic-migration-only'}
    foreach($name in $values.Keys){$key=ConvertTo-SecureString $values[$name] -AsPlainText -Force;try{Save-KarteKey $key (Join-Path $source ($name+'.clixml'))}finally{$key.Dispose()}}
    $null=& (Join-Path $PSScriptRoot 'New-Karte-Migration-Recipient.ps1') -CredentialRoot $recipient
    $envelope=Join-Path $root 'envelope.json'
    $null=& (Join-Path $PSScriptRoot 'Export-Karte-Migration-Credentials.ps1') -CredentialRoot $source -RecipientPublicKeyPath (Join-Path $recipient 'migration-public-key.xml') -OutputPath $envelope
    $text=[IO.File]::ReadAllText($envelope)
    foreach($value in $values.Values){if($text.Contains($value)){throw 'Plaintext synthetic key in envelope.'}}
    Write-Output 'PASS: Migration envelope excludes plaintext credentials'
    $tampered=$text|ConvertFrom-Json
    $bytes=[Convert]::FromBase64String($tampered.credentials.'grok-cloudflare-key')
    $bytes[0]=$bytes[0] -bxor 1
    $tampered.credentials.'grok-cloudflare-key'=[Convert]::ToBase64String($bytes)
    $tamperPath=Join-Path $root 'tampered.json'
    [IO.File]::WriteAllText($tamperPath,($tampered|ConvertTo-Json -Depth 4))
    $rejected=$false
    try{& (Join-Path $PSScriptRoot 'Import-Karte-Migration-Credentials.ps1') -CredentialRoot $recipient -EnvelopePath $tamperPath}catch{$rejected=$true}
    if(-not $rejected -or (Test-Path -LiteralPath (Join-Path $recipient 'chatgpt-runtime-key.clixml'))){throw 'Tampered envelope was not rejected before writes.'}
    Write-Output 'PASS: Tampered envelope rejected before destination credentials are written'
    $null=& (Join-Path $PSScriptRoot 'Import-Karte-Migration-Credentials.ps1') -CredentialRoot $recipient -EnvelopePath $envelope
    foreach($name in $values.Keys){$key=Read-KarteSavedKey (Join-Path $recipient ($name+'.clixml'));try{if([Net.NetworkCredential]::new('',$key).Password -cne $values[$name]){throw 'Migrated credential mismatch.'}}finally{$key.Dispose()}}
    Write-Output 'PASS: Both encrypted credentials restored into recipient DPAPI store'
    $rejected=$false
    try{& (Join-Path $PSScriptRoot 'Import-Karte-Migration-Credentials.ps1') -CredentialRoot $recipient -EnvelopePath $envelope}catch{$rejected=$true}
    if(-not $rejected){throw 'Existing destination credentials were overwritten.'}
    Write-Output 'PASS: Existing destination credentials cannot be overwritten'
    $null=& (Join-Path $PSScriptRoot 'New-Karte-Migration-Recipient.ps1') -CredentialRoot $other
    $rejected=$false
    try{& (Join-Path $PSScriptRoot 'Import-Karte-Migration-Credentials.ps1') -CredentialRoot $other -EnvelopePath $envelope}catch{$rejected=$true}
    if(-not $rejected){throw 'Wrong recipient accepted envelope.'}
    Write-Output 'PASS: A different recipient cannot import the envelope'
} finally {
    $absolute=[IO.Path]::GetFullPath($root)
    $expected=[IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\')+'\migration-test-'
    if(-not $absolute.StartsWith($expected,[StringComparison]::OrdinalIgnoreCase)){throw 'Refusing test cleanup outside the test root.'}
    if(Test-Path -LiteralPath $absolute){Remove-Item -LiteralPath $absolute -Recurse -Force}
}
