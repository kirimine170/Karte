param([switch]$RegistrationOnly)
$ErrorActionPreference = 'Stop'
$launcher = Join-Path $PSScriptRoot 'Start-Karte-Synthetic-ChatGPT.ps1'
$tokens = $null
$parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($launcher, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count) { throw 'Launcher syntax failed.' }
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force

$testRoot = Join-Path $PSScriptRoot ('startup-test-' + [guid]::NewGuid().ToString('N'))
$taskName = 'Karte Synthetic Startup Test ' + [guid]::NewGuid().ToString('N')
$secure = $null
$loaded = $null
$taskCreated = $false
function Assert-Startup([bool]$Condition, [string]$Name) {
    if (-not $Condition) { throw "FAIL: $Name" }
    Write-Output "PASS: $Name"
}
try {
    [void][IO.Directory]::CreateDirectory($testRoot)
    $keyPath = Join-Path $testRoot 'synthetic.clixml'
    $synthetic = 'synthetic-only-' + [guid]::NewGuid().ToString('N')
    $secure = ConvertTo-SecureString -String $synthetic -AsPlainText -Force
    Save-KarteKey $secure $keyPath
    $loaded = Read-KarteSavedKey $keyPath
    $actual = [Net.NetworkCredential]::new('', $loaded).Password
    Assert-Startup ($actual -ceq $synthetic) 'Windows DPAPI round trip'
    Assert-Startup (-not [IO.File]::ReadAllText($keyPath).Contains($synthetic)) 'Saved file excludes plaintext synthetic key'
    $loaded.Dispose(); $loaded = $null

    $secure.Dispose()
    $secure = ConvertTo-SecureString -String ($synthetic + '-replacement') -AsPlainText -Force
    Save-KarteKey $secure $keyPath
    $loaded = Read-KarteSavedKey $keyPath
    Assert-Startup ([Net.NetworkCredential]::new('', $loaded).Password -ceq ($synthetic + '-replacement')) 'Atomic key replacement'
    $loaded.Dispose(); $loaded = $null
    Assert-Startup (@(Get-ChildItem -LiteralPath $testRoot -File).Count -eq 1) 'No credential temporary files left'

    'invalid synthetic credential' | Export-Clixml -LiteralPath $keyPath
    $rejected = $false
    try { $null = Read-KarteSavedKey $keyPath } catch { $rejected = $true }
    Assert-Startup $rejected 'Wrong saved object type rejected'
    [IO.File]::WriteAllText($keyPath, 'invalid XML')
    $rejected = $false
    try { $null = Read-KarteSavedKey $keyPath } catch { $rejected = $true }
    Assert-Startup $rejected 'Corrupt saved credential rejected'

    $probe = Join-Path $testRoot 'probe.ps1'
    $marker = Join-Path $testRoot 'task-ran.txt'
    @'
param([switch]$NonInteractive, [string]$TunnelId)
if (-not $NonInteractive -or $TunnelId -ne 'tunnel_synthetic_startup_test') { exit 3 }
[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'task-ran.txt'), 'synthetic startup probe passed')
'@ | Set-Content -LiteralPath $probe -Encoding UTF8
    Register-KarteAutostart $probe 'tunnel_synthetic_startup_test' $taskName
    $taskCreated = $true
    $task = Get-ScheduledTask -TaskName $taskName
    Assert-Startup ($task.Principal.RunLevel -eq 'Limited' -and $task.Principal.LogonType -eq 'Interactive') 'Current-user interactive task without elevation'
    Assert-Startup ($task.Settings.RestartCount -eq 10 -and $task.Settings.MultipleInstances -eq 'IgnoreNew') 'Task retry and duplicate policies'
    Assert-Startup (@($task.Triggers).Count -eq 2 -and $task.Triggers[1].Repetition.Interval -eq 'PT1M' -and -not $task.Triggers[1].Repetition.Duration) 'Logon plus indefinite one-minute recovery'
    Assert-Startup (-not $task.Actions[0].Arguments.Contains($synthetic)) 'No synthetic secret in task arguments'
    Assert-Startup ($task.Actions[0].Arguments.Contains('-ExecutionPolicy RemoteSigned')) 'Execution policy is scoped to the launcher process'

    # An existing owned task from the earlier launcher must be upgradable.
    $legacyArguments = $task.Actions[0].Arguments.Replace(' -ExecutionPolicy RemoteSigned', '')
    $legacyAction = New-ScheduledTaskAction -Execute $task.Actions[0].Execute -Argument $legacyArguments -WorkingDirectory $testRoot
    Set-ScheduledTask -TaskName $taskName -Action $legacyAction | Out-Null
    Register-KarteAutostart $probe 'tunnel_synthetic_startup_test' $taskName
    $task = Get-ScheduledTask -TaskName $taskName
    Assert-Startup ($task.Actions[0].Arguments.Contains('-ExecutionPolicy RemoteSigned')) 'Existing owned launcher task upgrades from legacy arguments'
    if (-not $RegistrationOnly) {
        Start-ScheduledTask -TaskName $taskName
        for ($attempt = 0; $attempt -lt 30; $attempt++) {
            if (Test-Path -LiteralPath $marker) { break }
            Start-Sleep -Milliseconds 200
        }
        Assert-Startup (Test-Path -LiteralPath $marker) 'Real scheduled task launches a harmless synthetic probe'
    } else {
        Write-Output 'SKIP: Task execution requires the registered user to be logged on interactively'
    }

    $foreignArguments = $task.Actions[0].Arguments + ' -Unexpected synthetic'
    $foreignAction = New-ScheduledTaskAction -Execute $task.Actions[0].Execute -Argument $foreignArguments -WorkingDirectory $testRoot
    Set-ScheduledTask -TaskName $taskName -Action $foreignAction | Out-Null
    $rejected = $false
    try { Register-KarteAutostart $probe 'tunnel_synthetic_startup_test' $taskName } catch { $rejected = $true }
    $unchanged = (Get-ScheduledTask -TaskName $taskName).Actions[0].Arguments -ceq $foreignArguments
    Assert-Startup ($rejected -and $unchanged) 'A different task with the same name remains unchanged'
} finally {
    if ($taskCreated) {
        Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    }
    if ($loaded) { $loaded.Dispose() }
    if ($secure) { $secure.Dispose() }
    $actual = $null
    $synthetic = $null
    if (Test-Path -LiteralPath $testRoot) {
        # Delete only files this test created, with explicit paths.
        foreach ($name in @('synthetic.clixml', 'probe.ps1', 'task-ran.txt')) {
            $path = Join-Path $testRoot $name
            if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path }
        }
        Remove-Item -LiteralPath $testRoot
    }
}
