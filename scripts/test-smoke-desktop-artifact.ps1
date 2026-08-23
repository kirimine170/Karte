param()

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$runner = Join-Path $PSScriptRoot "smoke-desktop-artifact.ps1"
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ("karte desktop smoke fixture " + [Guid]::NewGuid().ToString("N"))
[IO.Directory]::CreateDirectory($fixtureRoot) | Out-Null
Push-Location -LiteralPath $repositoryRoot

function New-SmokeFixtureArchive {
    param(
        [string]$Mode,
        [string]$HelperExecutable
    )

    $payload = Join-Path $fixtureRoot ("$Mode payload")
    [IO.Directory]::CreateDirectory($payload) | Out-Null
    Copy-Item -LiteralPath $HelperExecutable -Destination (Join-Path $payload "karte.exe")
    [IO.File]::WriteAllText((Join-Path $payload "fixture-mode"), $Mode)
    $archive = Join-Path $fixtureRoot ("$Mode artifact.zip")
    Compress-Archive -Path (Join-Path $payload "*") -DestinationPath $archive -Force
    return $archive
}

function Assert-FixtureChildStopped {
    param([string]$LogDirectory)

    $childLogs = @(Get-ChildItem -LiteralPath $LogDirectory -File -Filter "data-*-child.log")
    if ($childLogs.Count -ne 1) {
        throw "expected one copied child diagnostic in $LogDirectory，found $($childLogs.Count)"
    }
    $childProcessId = [int]([IO.File]::ReadAllText($childLogs[0].FullName).Trim())
    $child = Get-CimInstance Win32_Process -Filter "ProcessId = $childProcessId" -ErrorAction SilentlyContinue
    if ($null -ne $child) {
        throw "startup smoke left fixture child process $childProcessId running"
    }
}

function Invoke-ExpectedSmokeFailure {
    param(
        [string]$Archive,
        [string]$LogDirectory,
        [int]$TimeoutSeconds
    )

    $failed = $false
    try {
        & $runner -Target windows -Archive $Archive -LogDirectory $LogDirectory -TimeoutSeconds $TimeoutSeconds
    }
    catch {
        $failed = $true
    }
    if (-not $failed) {
        throw "startup smoke unexpectedly passed for $Archive"
    }
}

try {
    $sourcePath = Join-Path $fixtureRoot "fixture-main.go"
    $helperExecutable = Join-Path $fixtureRoot "fixture-karte.exe"
    @'
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "child" {
		time.Sleep(30 * time.Second)
		return
	}
	modeBytes, err := os.ReadFile("fixture-mode")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	mode := strings.TrimSpace(string(modeBytes))
	if mode == "success" {
		marker, err := os.OpenFile(os.Getenv("KARTE_STARTUP_SMOKE_READY_FILE"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if _, err = marker.WriteString("karte-dom-ready-v1\n"); err == nil {
			err = marker.Sync()
		}
		if closeErr := marker.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}

	child := exec.Command(os.Args[0], "child")
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	childLog := filepath.Join(os.Getenv("KARTE_DATA_DIR"), "child.log")
	if err := os.WriteFile(childLog, []byte(fmt.Sprintf("%d\n", child.Process.Pid)), 0o600); err != nil {
		_ = child.Process.Kill()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if mode == "failure" {
		os.Exit(7)
	}
	if err := child.Wait(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
'@ | Set-Content -LiteralPath $sourcePath -Encoding utf8

    & go build -trimpath -o $helperExecutable $sourcePath
    if ($LASTEXITCODE -ne 0) {
        throw "failed to build Windows startup smoke fixture"
    }

    $successArchive = New-SmokeFixtureArchive -Mode success -HelperExecutable $helperExecutable
    $failureArchive = New-SmokeFixtureArchive -Mode failure -HelperExecutable $helperExecutable
    $timeoutArchive = New-SmokeFixtureArchive -Mode timeout -HelperExecutable $helperExecutable

    $oldDataDirectory = $env:KARTE_DATA_DIR
    $oldReadyMarker = $env:KARTE_STARTUP_SMOKE_READY_FILE
    $expectedRuntimePath = $env:PATH
    $env:KARTE_DATA_DIR = "fixture-data-sentinel"
    $env:KARTE_STARTUP_SMOKE_READY_FILE = "fixture-marker-sentinel"
    try {
        & $runner `
            -Target windows `
            -Archive $successArchive `
            -LogDirectory (Join-Path $fixtureRoot "success logs") `
            -TimeoutSeconds 10

        $failureLogs = Join-Path $fixtureRoot "failure logs"
        Invoke-ExpectedSmokeFailure -Archive $failureArchive -LogDirectory $failureLogs -TimeoutSeconds 10
        Assert-FixtureChildStopped -LogDirectory $failureLogs

        $timeoutLogs = Join-Path $fixtureRoot "timeout logs"
        Invoke-ExpectedSmokeFailure -Archive $timeoutArchive -LogDirectory $timeoutLogs -TimeoutSeconds 1
        Assert-FixtureChildStopped -LogDirectory $timeoutLogs

        if ($env:KARTE_DATA_DIR -ne "fixture-data-sentinel" -or
            $env:KARTE_STARTUP_SMOKE_READY_FILE -ne "fixture-marker-sentinel") {
            throw "Windows startup smoke runner did not restore its inherited environment"
        }
        if ($env:PATH -ne $expectedRuntimePath) {
            throw "Windows startup smoke runner did not restore PATH"
        }
    }
    finally {
        if ($null -eq $oldDataDirectory) {
            Remove-Item Env:KARTE_DATA_DIR -ErrorAction SilentlyContinue
        }
        else {
            $env:KARTE_DATA_DIR = $oldDataDirectory
        }
        if ($null -eq $oldReadyMarker) {
            Remove-Item Env:KARTE_STARTUP_SMOKE_READY_FILE -ErrorAction SilentlyContinue
        }
        else {
            $env:KARTE_STARTUP_SMOKE_READY_FILE = $oldReadyMarker
        }
    }

    Write-Host "Windows desktop artifact smoke script tests passed"
}
finally {
    Pop-Location
    if (Test-Path -LiteralPath $fixtureRoot) {
        Remove-Item -LiteralPath $fixtureRoot -Recurse -Force
    }
}
