$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'Karte-Synthetic-Startup.psm1') -Force
$root = Join-Path $PSScriptRoot ('startup-test-runtime-' + [guid]::NewGuid().ToString('N'))
$children = @()
$job = $null
function Assert-Runtime([bool]$Condition, [string]$Name) {
    if (-not $Condition) { throw "FAIL: $Name" }
    Write-Output "PASS: $Name"
}
function Save-TestRecord($Record) {
    [IO.File]::WriteAllText((Join-Path $root 'synthetic-grok-runtime.json'), ($Record | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
}
function Start-TestListener([string]$Executable, [int]$Status, [string]$MarkerName) {
    $marker = Join-Path $root $MarkerName
    $process = Start-Process -FilePath $Executable -ArgumentList ('"{0}" {1}' -f $marker, $Status) -WindowStyle Hidden -PassThru
    $script:children += $process
    $job.Assign($process)
    for ($attempt = 0; $attempt -lt 80; $attempt++) {
        if ((Test-Path -LiteralPath $marker) -and [IO.File]::ReadAllText($marker).Trim()) { break }
        if ($process.HasExited) { throw 'Synthetic listener exited.' }
        Start-Sleep -Milliseconds 100
    }
    if (-not (Test-Path -LiteralPath $marker)) { throw 'Synthetic listener timed out.' }
    return [pscustomobject]@{ Process = $process; Port = [int][IO.File]::ReadAllText($marker) }
}
try {
    [void][IO.Directory]::CreateDirectory((Join-Path $root 'private-startup'))
    $ports = Get-KarteRuntimePorts $root
    Assert-Runtime ($ports.HTTP -eq 8791 -and $ports.Metrics -eq 8792) 'Legacy runtime ports remain the defaults'
    $configPath = Join-Path $root 'private-startup/runtime-ports.json'
    [IO.File]::WriteAllText($configPath, '{"GrokHTTP":18791,"GrokMetrics":18792}')
    $ports = Get-KarteRuntimePorts $root
    Assert-Runtime ($ports.HTTP -eq 18791 -and $ports.Metrics -eq 18792 -and $ports.Origin -ceq 'http://127.0.0.1:18791') 'Configured ports keep the origin on loopback'
    foreach ($invalid in @(
        '{"GrokHTTP":18791}',
        '{"GrokHTTP":18791,"GrokMetrics":18792,"Bind":"0.0.0.0"}',
        '{"GrokHTTP":"18791","GrokMetrics":18792}',
        '{"GrokHTTP":true,"GrokMetrics":18792}',
        '{"GrokHTTP":18791.5,"GrokMetrics":18792}',
        '{"GrokHTTP":1023,"GrokMetrics":18792}',
        '{"GrokHTTP":18791,"GrokMetrics":65536}',
        '{"GrokHTTP":18640,"GrokMetrics":18792}',
        '{"GrokHTTP":18791,"GrokMetrics":18791}',
        'invalid JSON'
    )) {
        [IO.File]::WriteAllText($configPath, $invalid)
        $rejected = $false
        try { $null = Get-KarteRuntimePorts $root } catch { $rejected = $true }
        if (-not $rejected) { throw 'Invalid runtime configuration was accepted.' }
    }
    Write-Output 'PASS: Invalid ports, types, extra fields and reserved parliament port rejected'
    Remove-Item -LiteralPath $configPath

    # Real loopback HTTP fixtures exercise PID/path/start-time attribution.
    # They contain no Karte data, credentials or external network access.
    $sourcePath = Join-Path $root 'Listener.cs'
    @'
using System;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
public static class Listener {
    public static void Main(string[] args) {
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        File.WriteAllText(args[0], ((IPEndPoint)listener.LocalEndpoint).Port.ToString());
        while (true) {
            using (var client = listener.AcceptTcpClient()) {
                client.ReceiveTimeout = 2000;
                var stream = client.GetStream();
                try {
                    var reader = new StreamReader(stream, Encoding.ASCII, false, 1024, true);
                    string line;
                    do { line = reader.ReadLine(); } while (!String.IsNullOrEmpty(line));
                    var bytes = Encoding.ASCII.GetBytes("HTTP/1.1 " + args[1] + " Test\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
                    stream.Write(bytes, 0, bytes.Length);
                } catch (IOException) { }
            }
        }
    }
}
'@ | Set-Content -LiteralPath $sourcePath -Encoding UTF8
    $compiler = Join-Path $env:SystemRoot 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'
    if (-not (Test-Path -LiteralPath $compiler)) { $compiler = Join-Path $env:SystemRoot 'Microsoft.NET/Framework/v4.0.30319/csc.exe' }
    $serverPath = Join-Path $root 'karte-mcp.exe'
    & $compiler '/nologo' '/target:exe' ('/out:' + $serverPath) $sourcePath
    if ($LASTEXITCODE -ne 0) { throw 'Synthetic listener compilation failed.' }
    [void][IO.Directory]::CreateDirectory((Join-Path $root 'openai-tunnel-client-v0.0.21'))
    $tunnelPath = Join-Path $root 'openai-tunnel-client-v0.0.21/cloudflared.exe'
    $foreignPath = Join-Path $root 'unrelated.exe'
    Copy-Item -LiteralPath $serverPath -Destination $tunnelPath
    Copy-Item -LiteralPath $serverPath -Destination $foreignPath
    $job = New-KarteProcessJob
    $owned = Start-TestListener $serverPath 401 'server.port'
    $metrics = Start-TestListener $tunnelPath 200 'metrics.port'
    $foreign = Start-TestListener $foreignPath 401 'foreign.port'
    $ports = [pscustomobject]@{HTTP = $owned.Port; Metrics = $metrics.Port}
    $record = [ordered]@{
        ServerId = $owned.Process.Id
        ServerStartTicks = [string]$owned.Process.StartTime.ToUniversalTime().Ticks
        TunnelId = $metrics.Process.Id
        TunnelStartTicks = [string]$metrics.Process.StartTime.ToUniversalTime().Ticks
    }
    Save-TestRecord $record
    Assert-Runtime (Test-KarteGrokOwnership $root $ports) 'Owned executables, start times and loopback listeners match'
    $record.ServerStartTicks = '0'
    Save-TestRecord $record
    Assert-Runtime (-not (Test-KarteGrokOwnership $root $ports)) 'A stale server start time cannot establish readiness'
    $record.ServerStartTicks = [string]$owned.Process.StartTime.ToUniversalTime().Ticks
    $record.TunnelStartTicks = '0'
    Save-TestRecord $record
    Assert-Runtime (-not (Test-KarteGrokOwnership $root $ports)) 'A stale tunnel start time cannot establish readiness'
    $record.TunnelStartTicks = [string]$metrics.Process.StartTime.ToUniversalTime().Ticks
    $record.ServerId = $foreign.Process.Id
    $record.ServerStartTicks = [string]$foreign.Process.StartTime.ToUniversalTime().Ticks
    Save-TestRecord $record
    $foreignPorts = [pscustomobject]@{HTTP = $foreign.Port; Metrics = $metrics.Port}
    $request = '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
    $http = $request | & curl.exe --noproxy '*' --silent --max-time 3 --output NUL --write-out '%{http_code}' --header 'Content-Type: application/json' --data-binary '@-' ('http://127.0.0.1:' + $foreign.Port + '/mcp')
    $ready = & curl.exe --noproxy '*' --silent --max-time 3 --output NUL --write-out '%{http_code}' ('http://127.0.0.1:' + $metrics.Port + '/ready')
    Assert-Runtime ($http -eq '401' -and $ready -eq '200') 'Unrelated HTTP fixtures reproduce the misleading readiness responses'
    Assert-Runtime (-not (Test-KarteGrokOwnership $root $foreignPorts)) 'Unrelated HTTP 401 plus metrics 200 cannot establish Karte readiness'
    Assert-Runtime (-not $foreign.Process.HasExited) 'Readiness inspection leaves the unrelated service running'
    Remove-Item -LiteralPath (Join-Path $root 'synthetic-grok-runtime.json')
    Assert-Runtime (-not (Test-KarteGrokOwnership $root $ports)) 'Missing runtime metadata fails closed'
    [IO.File]::WriteAllText((Join-Path $root 'synthetic-grok-runtime.json'), 'invalid JSON')
    Assert-Runtime (-not (Test-KarteGrokOwnership $root $ports)) 'Corrupt runtime metadata fails closed'
} finally {
    if ($job) { $job.Dispose() }
    foreach ($process in $children) {
        if (-not $process.HasExited) { $process.Kill() }
        [void]$process.WaitForExit(5000)
        $process.Dispose()
    }
    $parent = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    $target = [IO.Path]::GetFullPath($root)
    if (-not $target.StartsWith($parent, [StringComparison]::OrdinalIgnoreCase) -or
        (Split-Path -Leaf $target) -notmatch '^startup-test-runtime-[a-f0-9]{32}$') { throw 'Unsafe test cleanup path.' }
    if (Test-Path -LiteralPath $target) { Remove-Item -LiteralPath $target -Recurse -Force }
}
