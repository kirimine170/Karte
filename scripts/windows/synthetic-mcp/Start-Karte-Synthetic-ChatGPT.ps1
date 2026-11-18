param(
    [string]$TunnelId = 'tunnel_6af95c8125f08191958866aa8ce69054',
    [switch]$SetupAutostart, [switch]$ConfigureOnly,
    [switch]$NonInteractive, [switch]$ReplaceKey, [switch]$Status
)
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force
$workspace = Join-Path $PSScriptRoot 'synthetic-live-workspace'
$karte = Join-Path $PSScriptRoot 'karte-mcp-ea4b9b2.exe'
if (-not (Test-Path -LiteralPath $karte)) { $karte = Join-Path $PSScriptRoot 'karte-mcp.exe' }
$tunnel = Join-Path $PSScriptRoot 'openai-tunnel-client-v0.0.21/tunnel-client.exe'
$healthFile = Join-Path $PSScriptRoot 'synthetic-chatgpt-health.txt'
$keyFile = Join-Path $PSScriptRoot 'private-startup/chatgpt-runtime-key.clixml'
$taskName = 'Karte Synthetic ChatGPT MCP'
if ($Status) { Get-KarteStartupStatus $taskName $keyFile; return }
if ($NonInteractive -and ($SetupAutostart -or $ReplaceKey)) { throw 'Key setup requires an interactive window.' }
if (($ReplaceKey -or $ConfigureOnly) -and -not $SetupAutostart) { throw 'Use these options with -SetupAutostart.' }
foreach ($item in @($workspace, $karte, $tunnel)) {
    if (-not (Test-Path -LiteralPath $item)) { throw "Missing test dependency: $item" }
}
if (-not $TunnelId -and -not $NonInteractive) { $TunnelId = Read-Host 'Tunnel ID' }
if (-not $TunnelId) { throw 'A Tunnel ID is required.' }
$TunnelId = $TunnelId.Trim()
if ($TunnelId -notmatch '^tunnel_[A-Za-z0-9_-]+$') { throw 'Invalid Tunnel ID.' }

$secureKey = $null
$lock = $null
$locked = $false
$job = $null
$client = $null
try {
    if ($SetupAutostart) {
        if ((Test-Path -LiteralPath $keyFile) -and -not $ReplaceKey) { $secureKey = Read-KarteSavedKey $keyFile }
        else { $secureKey = Read-Host 'OpenAI Tunnel runtime API key (input hidden)' -AsSecureString }
        Save-KarteKey $secureKey $keyFile
        Register-KarteAutostart $PSCommandPath $TunnelId $taskName -Paused:$ConfigureOnly
        if (-not $ConfigureOnly) { Start-ScheduledTask -TaskName $taskName }
        Write-Host 'ChatGPT encrypted credential and current-user automatic startup configured.'
        return
    }
    $hasher = [Security.Cryptography.SHA256]::Create()
    try { $lockId = [BitConverter]::ToString($hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($PSCommandPath.ToLowerInvariant()))).Replace('-', '') } finally { $hasher.Dispose() }
    $lock = [Threading.Mutex]::new($false, ('Local\KarteSyntheticChatGPT-' + $lockId))
    try { $locked = $lock.WaitOne(0) } catch [Threading.AbandonedMutexException] { $locked = $true }
    if (-not $locked) { Write-Host 'The ChatGPT tunnel is already running.'; return }
    if (Test-Path -LiteralPath $keyFile) { $secureKey = Read-KarteSavedKey $keyFile }
    elseif ($NonInteractive) { throw 'No saved runtime key. Run this script with -SetupAutostart once.' }
    else { $secureKey = Read-Host 'OpenAI Tunnel runtime API key (input hidden)' -AsSecureString }
    if (-not $secureKey -or $secureKey.Length -eq 0) { throw 'A runtime API key is required.' }

    $oldRoot = $env:KARTE_DATA_DIR
    $oldAgentToken = $env:KARTE_DECISION_AGENT_TOKEN
    $oldTunnelKey = $env:KARTE_SYNTHETIC_TUNNEL_KEY
    $keyBuffer = [IntPtr]::Zero
    try {
        $env:KARTE_DATA_DIR = $workspace
        $agentToken = & $karte --issue-decision-token agent:chatgpt
        if ($LASTEXITCODE -ne 0 -or $agentToken -notmatch '^agent:chatgpt\.[A-Za-z0-9_-]{43}$') { throw 'Could not initialize the synthetic Workspace agent.' }
        $env:KARTE_DECISION_AGENT_TOKEN = $agentToken
        $keyBuffer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureKey)
        $env:KARTE_SYNTHETIC_TUNNEL_KEY = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($keyBuffer)
        if (Test-Path -LiteralPath $healthFile) { Remove-Item -LiteralPath $healthFile }
        $command = "'" + $karte.Replace('\', '/') + "' --shared-context"
        $arguments = 'run --control-plane.tunnel-id "{0}" --control-plane.api-key env:KARTE_SYNTHETIC_TUNNEL_KEY --mcp.command "{1}" --health.listen-addr 127.0.0.1:0 --health.url-file "{2}" --log.http-raw-unsafe=false' -f $TunnelId,$command,$healthFile
        $job = New-KarteProcessJob
        $client = Start-Process -FilePath $tunnel -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $PSScriptRoot 'synthetic-chatgpt-tunnel.stdout.log') -RedirectStandardError (Join-Path $PSScriptRoot 'synthetic-chatgpt-tunnel.stderr.log')
        $job.Assign($client)
        $client.WaitForExit()
        if ($client.ExitCode -ne 0) { throw "Tunnel client stopped with exit code $($client.ExitCode)." }
    } finally {
        $env:KARTE_SYNTHETIC_TUNNEL_KEY = $oldTunnelKey
        $env:KARTE_DECISION_AGENT_TOKEN = $oldAgentToken
        $env:KARTE_DATA_DIR = $oldRoot
        if ($keyBuffer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($keyBuffer) }
    }
} finally {
    if ($client -and -not $client.HasExited) { Stop-Process -InputObject $client }
    if ($job) { $job.Dispose() }
    if ($secureKey) { $secureKey.Dispose() }
    if ($locked) { $lock.ReleaseMutex() }
    if ($lock) { $lock.Dispose() }
}
