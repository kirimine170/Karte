$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force
& (Join-Path $PSScriptRoot 'Test-Karte-ChatGPT-Startup.ps1')
foreach ($file in Get-ChildItem -LiteralPath $PSScriptRoot -File | Where-Object {$_.Extension -in @('.ps1','.psm1') -and $_.Name -match 'Karte.*(Startup|Autostart|Package|ChatGPT|Grok)'}) {
    $tokens=$null; $errors=$null
    [void][Management.Automation.Language.Parser]::ParseFile($file.FullName,[ref]$tokens,[ref]$errors)
    if($errors.Count){throw "Syntax errors in $($file.Name): $($errors.Message -join ', ')"}
}
Write-Output 'PASS: Startup script syntax'

$root=Join-Path $PSScriptRoot ('autostart-test-'+[guid]::NewGuid().ToString('N'))
$name='Karte Synthetic Child Probe '+[guid]::NewGuid().ToString('N')
$created=$false
$childId=$null
[void][IO.Directory]::CreateDirectory($root)
try {
    foreach($value in @('eyJsynthetic123','cloudflared.exe service install eyJsynthetic123')) {
        $inputKey=ConvertTo-SecureString $value -AsPlainText -Force
        $parsed=$null
        try {
            $parsed=ConvertTo-KarteCloudflareToken $inputKey
            if([Net.NetworkCredential]::new('', $parsed).Password -cne 'eyJsynthetic123'){throw 'Token normalization failed.'}
        } finally {if($parsed){$parsed.Dispose()};$inputKey.Dispose()}
    }
    Write-Output 'PASS: Raw and dashboard-command Cloudflare tokens normalize without execution'
    foreach($value in @('invalid',"eyJsynthetic123`nwhoami",'eyJsynthetic123; whoami')) {
        $inputKey=ConvertTo-SecureString $value -AsPlainText -Force
        $rejected=$false
        try {$unexpected=ConvertTo-KarteCloudflareToken $inputKey; $unexpected.Dispose()}catch{$rejected=$true}finally{$inputKey.Dispose()}
        if(-not $rejected){throw 'Malformed Cloudflare input was accepted.'}
    }
    Write-Output 'PASS: Malformed and multiline Cloudflare inputs rejected'

    $probe=Join-Path $root 'probe.ps1'
    $child=Join-Path $root 'child.ps1'
    @'
[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'child-pid.txt'), [string]$PID)
while($true){Start-Sleep -Seconds 1}
'@ | Set-Content -LiteralPath $child -Encoding UTF8
    @'
param([switch]$NonInteractive,[switch]$NamedTunnel)
if(-not $NonInteractive -or -not $NamedTunnel){exit 3}
Import-Module (Join-Path (Split-Path -Parent $PSScriptRoot) 'Karte-Synthetic-Startup.psm1')
$job=New-KarteProcessJob
$exe=Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe'
$arguments='-NoProfile -NonInteractive -File "{0}"' -f (Join-Path $PSScriptRoot 'child.ps1')
$child=Start-Process -FilePath $exe -ArgumentList $arguments -WindowStyle Hidden -PassThru
$job.Assign($child)
try {while($true){Start-Sleep -Seconds 1}} finally {if(-not $child.HasExited){Stop-Process -InputObject $child};$job.Dispose()}
'@ | Set-Content -LiteralPath $probe -Encoding UTF8
    Register-KarteAutostart -ScriptPath $probe -Name $name -Grok -Paused
    $created=$true
    $task=Get-ScheduledTask -TaskName $name
    if($task.Settings.Enabled){throw 'Staged task should be disabled.'}
    Write-Output 'PASS: Migration staging leaves automatic startup disabled'
    Enable-ScheduledTask -TaskName $name | Out-Null
    Start-ScheduledTask -TaskName $name
    $marker=Join-Path $root 'child-pid.txt'
    for($i=0;$i -lt 40;$i++){if(Test-Path -LiteralPath $marker){break};Start-Sleep -Milliseconds 200}
    if(-not(Test-Path -LiteralPath $marker)){throw 'Scheduled child did not start.'}
    $childId=[int][IO.File]::ReadAllText($marker)
    Disable-ScheduledTask -TaskName $name | Out-Null
    Stop-ScheduledTask -TaskName $name
    for($i=0;$i -lt 30;$i++){if(-not(Get-Process -Id $childId -ErrorAction SilentlyContinue)){break};Start-Sleep -Milliseconds 200}
    if(Get-Process -Id $childId -ErrorAction SilentlyContinue){throw 'Stopping the scheduled task left its child running.'}
    Write-Output 'PASS: Stopping a scheduled task removes its background child'
} finally {
    if($created){Stop-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue;Unregister-ScheduledTask -TaskName $name -Confirm:$false -ErrorAction SilentlyContinue}
    if($childId -and (Get-Process -Id $childId -ErrorAction SilentlyContinue)){Stop-Process -Id $childId}
    foreach($file in @('probe.ps1','child.ps1','child-pid.txt')){$p=Join-Path $root $file;if(Test-Path -LiteralPath $p){Remove-Item -LiteralPath $p}}
    Remove-Item -LiteralPath $root
}
