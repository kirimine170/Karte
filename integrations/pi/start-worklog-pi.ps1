param(
    [Parameter(Mandatory = $true)][string]$PiBinary,
    [Parameter(Mandatory = $true)][string]$DataRoot,
    [string]$KarteBinary = (Join-Path $PSScriptRoot '../../build/bin/karte-worklog.exe'),
    [string[]]$PiArguments = @()
)
$ErrorActionPreference = 'Stop'
$env:KARTE_WORKLOG_ROOT = (Resolve-Path -LiteralPath $DataRoot).Path
$env:KARTE_WORKLOG_BIN = (Resolve-Path -LiteralPath $KarteBinary).Path
& $PiBinary -e (Join-Path $PSScriptRoot 'karte-worklog.ts') @PiArguments
exit $LASTEXITCODE
