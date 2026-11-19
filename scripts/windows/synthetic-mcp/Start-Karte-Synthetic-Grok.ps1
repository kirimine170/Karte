param(
    [switch]$LocalCheck, [switch]$PublishOnly, [switch]$TokenOnly, [switch]$NamedTunnel,
    [switch]$SetupAutostart, [switch]$ConfigureOnly,
    [switch]$NonInteractive, [switch]$ReplaceKey, [switch]$Status
)

$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force
$workspace = Join-Path $PSScriptRoot 'synthetic-live-workspace'
$karte = Join-Path $PSScriptRoot 'karte-mcp-ea4b9b2.exe'
if (-not (Test-Path -LiteralPath $karte)) { $karte = Join-Path $PSScriptRoot 'karte-mcp.exe' }
$cloudflared = Join-Path $PSScriptRoot 'openai-tunnel-client-v0.0.21/cloudflared.exe'
$oldRoot = $env:KARTE_DATA_DIR
$oldTunnelToken = $env:TUNNEL_TOKEN
$server = $null
$tunnel = $null
$credential = $null
$keyFile = Join-Path $PSScriptRoot 'private-startup/grok-cloudflare-key.clixml'
$taskName = 'Karte Synthetic Grok MCP'
$lock = $null
$locked = $false
$job = $null

if ($Status) { Get-KarteStartupStatus $taskName $keyFile; return }
$runtimePorts = Get-KarteRuntimePorts $PSScriptRoot
$origin = $runtimePorts.Origin
if ($SetupAutostart) { $NamedTunnel = $true }
if ($NonInteractive -and ($SetupAutostart -or $ReplaceKey)) { throw 'Key setup requires an interactive window.' }
if (($ReplaceKey -or $ConfigureOnly) -and -not $SetupAutostart) { throw 'Use these options with -SetupAutostart.' }
if ($NonInteractive -and -not ($NamedTunnel -or $LocalCheck)) { throw 'Unattended startup requires -NamedTunnel.' }

if ($NamedTunnel -and ($LocalCheck -or $PublishOnly -or $TokenOnly)) {
    throw 'Use -NamedTunnel by itself.'
}

function Show-GrokAuthorization {
    $token = & $karte --issue-decision-token agent:grok
    if ($LASTEXITCODE -ne 0 -or $token -notmatch '^agent:grok\.[A-Za-z0-9_-]{43}$') {
        throw 'Could not issue the synthetic Grok credential.'
    }
    Write-Host 'Enter this value in the Authorization field in Grok Bot:'
    Write-Host "Bearer $token"
}

foreach ($item in @($workspace, $karte, $cloudflared)) {
    if (-not (Test-Path -LiteralPath $item)) { throw "Missing test dependency: $item" }
}
try {
    if ($NamedTunnel) {
        if ((Test-Path -LiteralPath $keyFile) -and -not $ReplaceKey) {
            $credential = Read-KarteSavedKey $keyFile
        } elseif ($NonInteractive) {
            throw 'No saved Cloudflare token. Run this script with -SetupAutostart once.'
        } else {
            $inputValue = Read-Host 'Cloudflare Tunnel token or dashboard copy command (input hidden)' -AsSecureString
            try { $credential = ConvertTo-KarteCloudflareToken $inputValue } finally { $inputValue.Dispose() }
        }
        if ($SetupAutostart) {
            Save-KarteKey $credential $keyFile
            Register-KarteAutostart -ScriptPath $PSCommandPath -Name $taskName -Grok -Paused:$ConfigureOnly
            if (-not $ConfigureOnly) { Start-ScheduledTask -TaskName $taskName }
            Write-Host 'Grok encrypted tunnel credential and current-user automatic startup configured.'
            return
        }
    }
    $env:KARTE_DATA_DIR = $workspace
    if ($TokenOnly) {
        Show-GrokAuthorization
        return
    }
    $hasher = [Security.Cryptography.SHA256]::Create()
    try { $lockId = [BitConverter]::ToString($hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($PSCommandPath.ToLowerInvariant()))).Replace('-', '') } finally { $hasher.Dispose() }
    $lock = [Threading.Mutex]::new($false, ('Local\KarteSyntheticGrok-' + $lockId))
    try { $locked = $lock.WaitOne(0) } catch [Threading.AbandonedMutexException] { $locked = $true }
    if (-not $locked) { Write-Host 'The Grok runtime is already running.'; return }
    if (Get-NetTCPConnection -LocalPort $runtimePorts.HTTP -State Listen -ErrorAction SilentlyContinue) {
        throw ('Port '+$runtimePorts.HTTP+' is already in use. No existing process was stopped.')
    }
    if ($NamedTunnel -and (Get-NetTCPConnection -LocalPort $runtimePorts.Metrics -State Listen -ErrorAction SilentlyContinue)) {
        throw ('Port '+$runtimePorts.Metrics+' is already in use. No existing process was stopped.')
    }
    $job = New-KarteProcessJob
    $server = Start-Process -FilePath $karte -ArgumentList @('--agent-http', ('127.0.0.1:'+$runtimePorts.HTTP), '--shared-context') -WindowStyle Hidden -PassThru
    $job.Assign($server)
    $probe = '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
    $probeStatus = $null
    $serverOwnsListener = $false
    for ($attempt = 0; $attempt -lt 15; $attempt++) {
        if ($server.HasExited) { throw 'The synthetic Karte MCP server could not start.' }
        $probeStatus = $probe | & curl.exe --noproxy '*' --silent --connect-timeout 1 --max-time 2 --output NUL --write-out '%{http_code}' --header 'Content-Type: application/json' --data-binary '@-' "$origin/mcp"
        $serverOwnsListener = Test-KarteListenerOwner $runtimePorts.HTTP $server.Id
        if ($probeStatus -eq '401' -and $serverOwnsListener) { break }
        Start-Sleep -Milliseconds 150
    }
    if ($probeStatus -ne '401' -or -not $serverOwnsListener) { throw 'The synthetic Karte process does not own a ready loopback MCP listener.' }
    Write-Output 'Synthetic Karte MCP is listening on loopback. Unauthorized requests return HTTP 401.'
    if ($LocalCheck) { return }

    if ($NamedTunnel) {
        $keyBuffer = [IntPtr]::Zero
        try {
            $keyBuffer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($credential)
            $env:TUNNEL_TOKEN = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($keyBuffer)
        } finally {
            if ($keyBuffer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($keyBuffer) }
        }
        Write-Host 'Use https://karte-test.kirimine170.com/mcp after setting the published route. Press Ctrl+C to stop.'
        $tunnel = Start-Process -FilePath $cloudflared -ArgumentList @('tunnel', '--no-autoupdate', '--protocol', 'http2', '--metrics', ('127.0.0.1:'+$runtimePorts.Metrics), 'run') -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $PSScriptRoot 'synthetic-grok-tunnel.stdout.log') -RedirectStandardError (Join-Path $PSScriptRoot 'synthetic-grok-tunnel.stderr.log')
        $job.Assign($tunnel)
        [pscustomobject]@{ControllerId=$PID;ServerId=$server.Id;ServerStartTicks=[string]$server.StartTime.ToUniversalTime().Ticks;TunnelId=$tunnel.Id;TunnelStartTicks=[string]$tunnel.StartTime.ToUniversalTime().Ticks;HTTPPort=$runtimePorts.HTTP;MetricsPort=$runtimePorts.Metrics;StartedUtc=[DateTime]::UtcNow.ToString('o')} | ConvertTo-Json -Compress | Set-Content -LiteralPath (Join-Path $PSScriptRoot 'synthetic-grok-runtime.json') -Encoding UTF8
        while ($true) {
            $server.Refresh(); $tunnel.Refresh()
            if ($server.HasExited) { throw "The Grok MCP server exited with code $($server.ExitCode)." }
            if ($tunnel.HasExited) { throw "Cloudflare Tunnel exited with code $($tunnel.ExitCode)." }
            Start-Sleep -Seconds 1
        }
    }

    if ($PublishOnly) {
        $tunnel = Start-Process -FilePath $cloudflared -ArgumentList @('tunnel', '--no-autoupdate', '--protocol', 'http2', '--url', $origin) -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $PSScriptRoot 'synthetic-grok-tunnel.stdout.log') -RedirectStandardError (Join-Path $PSScriptRoot 'synthetic-grok-tunnel.stderr.log')
        $job.Assign($tunnel)
        $tunnel.Id | Set-Content -LiteralPath (Join-Path $PSScriptRoot 'synthetic-grok-tunnel.pid') -Encoding Ascii
        if ($tunnel.WaitForExit(2700000) -and $tunnel.ExitCode -ne 0) {
            throw "Cloudflare Quick Tunnel exited with code $($tunnel.ExitCode)."
        }
        return
    }

    Show-GrokAuthorization
    Write-Host 'Use the displayed HTTPS address with /mcp. Press Ctrl+C to stop the test tunnel.'
    & $cloudflared tunnel --no-autoupdate --protocol http2 --url $origin
    if ($LASTEXITCODE -ne 0) { throw "Cloudflare Quick Tunnel exited with code $LASTEXITCODE." }
} finally {
    if ($tunnel -and -not $tunnel.HasExited) { Stop-Process -InputObject $tunnel }
    if ($server -and -not $server.HasExited) { Stop-Process -InputObject $server }
    if ($job) { $job.Dispose() }
    $env:KARTE_DATA_DIR = $oldRoot
    $env:TUNNEL_TOKEN = $oldTunnelToken
    if ($credential) { $credential.Dispose() }
    if ($locked) { $lock.ReleaseMutex() }
    if ($lock) { $lock.Dispose() }
}
