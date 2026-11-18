param(
    [ValidateSet('Status','Check','Start','Stop','Restart')][string]$Action = 'Status',
    [ValidateRange(0,120)][int]$WaitSeconds = 0
)
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force
$roles = @(
    @{Name='Karte Synthetic ChatGPT MCP'; Script='Start-Karte-Synthetic-ChatGPT.ps1'; Key='chatgpt-runtime-key.clixml'},
    @{Name='Karte Synthetic Grok MCP'; Script='Start-Karte-Synthetic-Grok.ps1'; Key='grok-cloudflare-key.clixml'}
)
foreach ($role in $roles) {
    $task = Get-ScheduledTask -TaskPath '\' -TaskName $role.Name -ErrorAction SilentlyContinue
    if ($task -and ($task.Description -notlike 'Karte synthetic * MCP logon startup (current user only).' -or
        @($task.Actions).Count -ne 1 -or -not $task.Actions[0].Arguments.Contains('"' + (Join-Path $PSScriptRoot $role.Script) + '"'))) {
        throw "The task $($role.Name) belongs to a different installation."
    }
    if ($Action -in @('Start','Stop','Restart') -and -not $task) { throw "Task not registered: $($role.Name)" }
    if ($Action -in @('Stop','Restart')) {
        # Disable retries as well, so stopping is persistent across logon/recovery.
        Disable-ScheduledTask -TaskPath '\' -TaskName $role.Name | Out-Null
        Stop-ScheduledTask -TaskPath '\' -TaskName $role.Name
        $deadline=[DateTime]::UtcNow.AddSeconds(15)
        while((Get-ScheduledTask -TaskPath '\' -TaskName $role.Name).State -eq 'Running'){
            if([DateTime]::UtcNow -gt $deadline){throw "Timed out stopping $($role.Name)."}
            Start-Sleep -Milliseconds 200
        }
    }
    if ($Action -in @('Start','Restart')) {
        Enable-ScheduledTask -TaskPath '\' -TaskName $role.Name | Out-Null
        Start-ScheduledTask -TaskPath '\' -TaskName $role.Name
    }
    Get-KarteStartupStatus $role.Name (Join-Path $PSScriptRoot ('private-startup/' + $role.Key))
}
if ($Action -in @('Check','Restart')) {
    if($Action -eq 'Restart' -and $WaitSeconds -eq 0){$WaitSeconds=60}
    $deadline=[DateTime]::UtcNow.AddSeconds($WaitSeconds)
    do {
    $ready = $false
    $healthFile = Join-Path $PSScriptRoot 'synthetic-chatgpt-health.txt'
    if (Test-Path -LiteralPath $healthFile) {
        $url = [IO.File]::ReadAllText($healthFile).Trim()
        if ($url -match '^http://127\.0\.0\.1:\d+/?$') {
            $response = & curl.exe --noproxy '*' --silent --max-time 4 ($url.TrimEnd('/') + '/readyz')
            $ready = $LASTEXITCODE -eq 0 -and $response -eq 'ready'
        }
    }
    $probe = '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
    $http = $probe | & curl.exe --noproxy '*' --silent --max-time 4 --output NUL --write-out '%{http_code}' --header 'Content-Type: application/json' --data-binary '@-' 'http://127.0.0.1:8791/mcp'
    $cloudflare = & curl.exe --noproxy '*' --silent --max-time 4 --output NUL --write-out '%{http_code}' 'http://127.0.0.1:8792/ready'
    $allReady=$ready -and $http -eq '401' -and $cloudflare -eq '200'
    if($allReady -or [DateTime]::UtcNow -ge $deadline){break}
    Start-Sleep -Milliseconds 300
    } while($true)
    [pscustomobject]@{ChatGPTReady=$ready;GrokUnauthenticatedHTTP=$http;CloudflareReadyHTTP=$cloudflare}
    if (-not $allReady) { throw 'One or more runtimes are not ready.' }
}
