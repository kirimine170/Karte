$ErrorActionPreference = "Stop"
$scriptPath = Join-Path $PSScriptRoot "capture-windows-native-compliance.ps1"
$sandbox = Join-Path ([IO.Path]::GetTempPath()) ("karte-native-compliance-" + [guid]::NewGuid().ToString("N"))
$artifact = Join-Path $sandbox "artifact"
$outside = Join-Path $sandbox "outside"

try {
    New-Item -ItemType Directory -Force $artifact, $outside | Out-Null
    & $scriptPath -ArtifactRoot $artifact -ConfinementProbe "safe/missing/file"

    $rootJunction = Join-Path $sandbox "artifact-junction"
    New-Item -ItemType Junction -Path $rootJunction -Target $artifact | Out-Null
    try {
        & $scriptPath -ArtifactRoot $rootJunction -ConfinementProbe "."
        throw "Expected ArtifactRoot junction rejection"
    }
    catch {
        if (-not $_.Exception.Message.Contains("ArtifactRoot reparse point is not allowed")) { throw }
    }

    $licenseAncestor = Join-Path $artifact "THIRD_PARTY_LICENSES"
    New-Item -ItemType Junction -Path $licenseAncestor -Target $outside | Out-Null
    try {
        & $scriptPath -ArtifactRoot $artifact -ConfinementProbe "THIRD_PARTY_LICENSES/native/LICENSE"
        throw "Expected license ancestor junction rejection"
    }
    catch {
        if (-not $_.Exception.Message.Contains("contains a reparse point")) { throw }
    }
}
finally {
    if (Test-Path -LiteralPath $sandbox) {
        Remove-Item -LiteralPath $sandbox -Recurse -Force
    }
}

Write-Host "Windows native compliance confinement fixture passed"
