param(
    [Parameter(Mandatory = $true)]
    [string]$Target,

    [Parameter(Mandatory = $true)]
    [string]$Archive,

    [Parameter(Mandatory = $true)]
    [string]$LogDirectory,

    [ValidateRange(1, 600)]
    [int]$TimeoutSeconds = 60
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$processJobType = "KarteStartupSmoke.ProcessJob" -as [type]
if ($null -eq $processJobType) {
    Add-Type -Path (Join-Path $PSScriptRoot "windows-startup-smoke-job.cs")
}

if ($Target -ne "windows") {
    throw "unsupported Windows desktop smoke target: $Target"
}

$archivePath = (Resolve-Path -LiteralPath $Archive).Path
if (-not [IO.Path]::IsPathFullyQualified($archivePath)) {
    throw "artifact archive path is not absolute: $archivePath"
}
[IO.Directory]::CreateDirectory($LogDirectory) | Out-Null
$logRoot = (Resolve-Path -LiteralPath $LogDirectory).Path
$smokeRoot = Join-Path ([IO.Path]::GetTempPath()) ("karte-startup-smoke-windows-" + [Guid]::NewGuid().ToString("N"))
[IO.Directory]::CreateDirectory($smokeRoot) | Out-Null
$extractRoot = Join-Path $smokeRoot "extracted artifact"
$dataDirectory = Join-Path $smokeRoot "data-windows"
$markerDirectory = Join-Path $smokeRoot "markers-windows"
$markerPath = Join-Path $markerDirectory "DOM ready.marker"
$process = $null
$trackedProcesses = @{}

function Update-TrackedProcessTree {
    param([int]$RootProcessId)

    $queue = [Collections.Generic.Queue[int]]::new()
    $seen = @{}
    $queue.Enqueue($RootProcessId)
    while ($queue.Count -gt 0) {
        $currentProcessId = $queue.Dequeue()
        if ($seen.ContainsKey($currentProcessId)) {
            continue
        }
        $seen[$currentProcessId] = $true
        $instance = Get-CimInstance Win32_Process -Filter "ProcessId = $currentProcessId" -ErrorAction SilentlyContinue
        if ($null -ne $instance) {
            $processKey = [string]$currentProcessId
            $creationDate = [string]$instance.CreationDate
            if ($trackedProcesses.ContainsKey($processKey) -and $trackedProcesses[$processKey].CreationDate -ne $creationDate) {
                continue
            }
            $trackedProcesses[$processKey] = [PSCustomObject]@{
                ProcessId = [int]$instance.ProcessId
                CreationDate = $creationDate
                ExecutablePath = [string]$instance.ExecutablePath
                CommandLine = [string]$instance.CommandLine
            }
        }
        Get-CimInstance Win32_Process -Filter "ParentProcessId = $currentProcessId" -ErrorAction SilentlyContinue |
            ForEach-Object { $queue.Enqueue([int]$_.ProcessId) }
    }
}

function Get-LiveTrackedProcesses {
    $live = @()
    foreach ($tracked in $trackedProcesses.Values) {
        $current = Get-CimInstance Win32_Process -Filter "ProcessId = $($tracked.ProcessId)" -ErrorAction SilentlyContinue
        if ($null -ne $current -and [string]$current.CreationDate -eq $tracked.CreationDate) {
            $live += $tracked
        }
    }
    return $live
}

function Stop-LiveTrackedProcesses {
    param(
        [string]$LogName,
        [int]$WaitMilliseconds = 5000
    )

    $live = @(Get-LiveTrackedProcesses)
    $taskkillFailures = @()
    foreach ($tracked in $live) {
        & taskkill.exe /PID $tracked.ProcessId /T /F 2>&1 |
            Out-File -LiteralPath (Join-Path $logRoot $LogName) -Encoding utf8 -Append
        if ($LASTEXITCODE -ne 0) {
            $taskkillFailures += "PID=$($tracked.ProcessId) exit=$LASTEXITCODE"
        }
    }

    $deadline = [DateTime]::UtcNow.AddMilliseconds($WaitMilliseconds)
    do {
        $remaining = @(Get-LiveTrackedProcesses)
        if ($remaining.Count -eq 0) {
            return $live.Count
        }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)

    $remaining = @(Get-LiveTrackedProcesses)
    if ($remaining.Count -gt 0) {
        $remaining |
            Format-List |
            Out-File -LiteralPath (Join-Path $logRoot ("remaining-" + $LogName)) -Encoding utf8
        $failureSummary = if ($taskkillFailures.Count -gt 0) {
            "; taskkill failures: " + ($taskkillFailures -join ", ")
        }
        else {
            ""
        }
        throw "failed to terminate $($remaining.Count) tracked process(es) within ${WaitMilliseconds}ms$failureSummary"
    }
    return $live.Count
}

function Copy-SmokeDiagnostics {
    $diagnosticIndex = 0
    if (Test-Path -LiteralPath $dataDirectory) {
        $diagnostics = @(
            Get-ChildItem -LiteralPath $dataDirectory -Recurse -File -ErrorAction SilentlyContinue |
                Where-Object { $_.Extension -eq ".log" -or $_.Extension -eq ".jsonl" }
        )
        foreach ($diagnostic in $diagnostics) {
            $diagnosticIndex++
            $destination = Join-Path $logRoot ("data-{0:D4}-{1}" -f $diagnosticIndex, $diagnostic.Name)
            Copy-Item -LiteralPath $diagnostic.FullName -Destination $destination -Force -ErrorAction SilentlyContinue
        }
    }
    if (Test-Path -LiteralPath $extractRoot) {
        Get-ChildItem -LiteralPath $extractRoot -Recurse -Force -ErrorAction SilentlyContinue |
            Select-Object -First 500 -ExpandProperty FullName |
            Out-File -LiteralPath (Join-Path $logRoot "artifact-layout.txt") -Encoding utf8
    }
}

try {
    $extractLog = Join-Path $logRoot "extraction.log"
    & go run ./cmd/artifactsmoke -archive $archivePath -destination $extractRoot *>&1 |
        Out-File -LiteralPath $extractLog -Encoding utf8
    if ($LASTEXITCODE -ne 0) {
        throw "safe artifact extraction failed with code $LASTEXITCODE"
    }

    $executable = Join-Path $extractRoot "karte.exe"
    if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
        throw "extracted Windows executable is missing: $executable"
    }
    [IO.Directory]::CreateDirectory($dataDirectory) | Out-Null
    [IO.Directory]::CreateDirectory($markerDirectory) | Out-Null

    $windowsRoot = $env:SystemRoot
    if ([string]::IsNullOrWhiteSpace($windowsRoot) -or -not [IO.Path]::IsPathFullyQualified($windowsRoot)) {
        throw "SystemRoot is unavailable for a clean Windows runtime PATH"
    }
    $cleanRuntimePath = @(
        (Join-Path $windowsRoot "System32")
        $windowsRoot
        (Join-Path $windowsRoot "System32\Wbem")
        (Join-Path $windowsRoot "System32\WindowsPowerShell\v1.0")
    ) -join ";"
    $hadDataOverride = Test-Path Env:KARTE_DATA_DIR
    $oldDataOverride = $env:KARTE_DATA_DIR
    $hadReadyMarker = Test-Path Env:KARTE_STARTUP_SMOKE_READY_FILE
    $oldReadyMarker = $env:KARTE_STARTUP_SMOKE_READY_FILE
    $hadRuntimePath = Test-Path Env:PATH
    $oldRuntimePath = $env:PATH
    try {
        $env:KARTE_DATA_DIR = $dataDirectory
        $env:KARTE_STARTUP_SMOKE_READY_FILE = $markerPath
        $env:PATH = $cleanRuntimePath
        $process = [KarteStartupSmoke.ProcessJob]::Start(
            $executable,
            $extractRoot,
            (Join-Path $logRoot "windows-stdout.log"),
            (Join-Path $logRoot "windows-stderr.log")
        )
        Update-TrackedProcessTree -RootProcessId $process.ProcessId
    }
    finally {
        if ($hadDataOverride) {
            $env:KARTE_DATA_DIR = $oldDataOverride
        }
        else {
            Remove-Item Env:KARTE_DATA_DIR -ErrorAction SilentlyContinue
        }
        if ($hadReadyMarker) {
            $env:KARTE_STARTUP_SMOKE_READY_FILE = $oldReadyMarker
        }
        else {
            Remove-Item Env:KARTE_STARTUP_SMOKE_READY_FILE -ErrorAction SilentlyContinue
        }
        if ($hadRuntimePath) {
            $env:PATH = $oldRuntimePath
        }
        else {
            Remove-Item Env:PATH -ErrorAction SilentlyContinue
        }
    }

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while (-not $process.HasExited) {
        Update-TrackedProcessTree -RootProcessId $process.ProcessId
        if ([DateTime]::UtcNow -ge $deadline) {
            Get-CimInstance Win32_Process |
                Select-Object ProcessId, ParentProcessId, Name, ExecutablePath, CommandLine |
                Format-List |
                Out-File -LiteralPath (Join-Path $logRoot "windows-timeout-processes.log") -Encoding utf8
            throw "Windows startup smoke timed out after $TimeoutSeconds seconds"
        }
        Start-Sleep -Milliseconds 200
    }
    $process.WaitForExit()
    Update-TrackedProcessTree -RootProcessId $process.ProcessId

    if ($process.ExitCode -ne 0) {
        throw "Windows startup smoke exited with code $($process.ExitCode)"
    }
    if (-not (Test-Path -LiteralPath $markerPath -PathType Leaf)) {
        throw "Windows startup smoke exited without a DOM-ready marker: $markerPath"
    }
    $marker = [IO.File]::ReadAllText($markerPath).TrimEnd([char[]]"`r`n")
    if ($marker -ne "karte-dom-ready-v1") {
        throw "Windows startup smoke wrote an invalid DOM-ready marker: $marker"
    }

    if (-not $process.WaitForEmpty(5000)) {
        "active job processes=$($process.ActiveProcessCount)" |
            Out-File -LiteralPath (Join-Path $logRoot "windows-orphan-job.log") -Encoding utf8
        throw "Windows startup smoke left processes in its launch job"
    }
    $orphans = @(Get-LiveTrackedProcesses)
    if ($orphans.Count -gt 0) {
        $orphans |
            Format-List |
            Out-File -LiteralPath (Join-Path $logRoot "windows-orphan-processes.log") -Encoding utf8
        Stop-LiveTrackedProcesses -LogName "windows-orphan-taskkill.log" | Out-Null
        throw "Windows startup smoke left $($orphans.Count) tracked child process(es)"
    }
}
finally {
    $cleanupFailures = @()
    if ($null -ne $process) {
        try {
            Update-TrackedProcessTree -RootProcessId $process.ProcessId
            if (-not $process.WaitForEmpty(0)) {
                "active job processes before cleanup=$($process.ActiveProcessCount)" |
                    Out-File -LiteralPath (Join-Path $logRoot "windows-final-job.log") -Encoding utf8
                $process.Terminate(1)
                if (-not $process.WaitForEmpty(5000)) {
                    throw "Windows startup smoke job remained active after termination"
                }
            }
        }
        catch {
            $cleanupFailures += $_
        }
        try {
            Stop-LiveTrackedProcesses -LogName "windows-final-taskkill.log" | Out-Null
        }
        catch {
            $cleanupFailures += $_
        }
        $process.Dispose()
    }
    try {
        Copy-SmokeDiagnostics
    }
    catch {
        $cleanupFailures += $_
    }
    try {
        if (Test-Path -LiteralPath $smokeRoot) {
            Remove-Item -LiteralPath $smokeRoot -Recurse -Force
        }
    }
    catch {
        $cleanupFailures += $_
    }
    if ($cleanupFailures.Count -gt 0) {
        $cleanupFailures |
            Out-String |
            Out-File -LiteralPath (Join-Path $logRoot "windows-final-cleanup-errors.log") -Encoding utf8
        throw "Windows startup smoke cleanup failed; see windows-final-cleanup-errors.log"
    }
}
