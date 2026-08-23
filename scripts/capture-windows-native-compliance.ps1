param(
    [Parameter(Mandatory = $true)]
    [string]$ArtifactRoot,
    [string]$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")),
    [string]$Pacman = "C:\msys64\usr\bin\pacman.exe",
    [string]$Cygpath = "C:\msys64\usr\bin\cygpath.exe",
    [string]$ConfinementProbe = ""
)

$ErrorActionPreference = "Stop"
$ArtifactRoot = [IO.Path]::GetFullPath($ArtifactRoot)
$RepositoryRoot = [IO.Path]::GetFullPath($RepositoryRoot)

function Assert-NoArtifactReparseAncestors([string]$Path, [string]$Label) {
    $fullPath = [IO.Path]::GetFullPath($Path)
    if ($fullPath -ne $ArtifactRoot -and
        -not $fullPath.StartsWith($ArtifactRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "$Label escapes artifact root: $Path"
    }
    $rootItem = Get-Item -Force -LiteralPath $ArtifactRoot
    if (-not $rootItem.PSIsContainer) { throw "ArtifactRoot is not a directory: $ArtifactRoot" }
    if (($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "ArtifactRoot reparse point is not allowed: $ArtifactRoot"
    }
    $relative = [IO.Path]::GetRelativePath($ArtifactRoot, $fullPath)
    if ($relative -eq ".") { return }
    $current = $ArtifactRoot
    foreach ($segment in ($relative -split '[\\/]')) {
        if ([string]::IsNullOrEmpty($segment)) { continue }
        $current = Join-Path $current $segment
        if (-not (Test-Path -LiteralPath $current)) { break }
        $item = Get-Item -Force -LiteralPath $current
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "$Label contains a reparse point: $current"
        }
    }
}

Assert-NoArtifactReparseAncestors $ArtifactRoot "ArtifactRoot"
if (-not [string]::IsNullOrEmpty($ConfinementProbe)) {
    Assert-NoArtifactReparseAncestors (Join-Path $ArtifactRoot $ConfinementProbe) "ConfinementProbe"
    return
}

$registry = Get-Content -Raw (Join-Path $RepositoryRoot "compliance\native-components.json") | ConvertFrom-Json
$licenseDirectory = Join-Path $ArtifactRoot "THIRD_PARTY_LICENSES\native"
$metadataDirectory = Join-Path $ArtifactRoot "compliance\packages"
Assert-NoArtifactReparseAncestors $licenseDirectory "Native license directory"
Assert-NoArtifactReparseAncestors $metadataDirectory "Package metadata directory"
New-Item -ItemType Directory -Force $licenseDirectory, $metadataDirectory | Out-Null
Assert-NoArtifactReparseAncestors $licenseDirectory "Native license directory"
Assert-NoArtifactReparseAncestors $metadataDirectory "Package metadata directory"

function Get-SHA256([string]$Path) {
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Get-ArtifactFile([string]$Relative) {
    if ([IO.Path]::IsPathRooted($Relative) -or $Relative.Contains("..")) {
        throw "Artifact path is not confined: $Relative"
    }
    $path = [IO.Path]::GetFullPath((Join-Path $ArtifactRoot $Relative))
    if (-not $path.StartsWith($ArtifactRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Artifact path escapes root: $Relative"
    }
    Assert-NoArtifactReparseAncestors $path "Artifact file"
    $item = Get-Item -LiteralPath $path
    if ($item.PSIsContainer) { throw "Artifact file is a directory: $Relative" }
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Artifact reparse point is not allowed: $Relative"
    }
    return $path
}

function Get-Component([string]$ID) {
    $component = @($registry.components | Where-Object { $_.id -eq $ID })
    if ($component.Count -ne 1) { throw "Native registry component is missing or duplicated: $ID" }
    return $component[0]
}

function New-FileRecord([string]$Relative) {
    $path = Get-ArtifactFile $Relative
    return [ordered]@{
        artifactPath = $Relative.Replace("\", "/")
        bytes = (Get-Item -LiteralPath $path).Length
        sha256 = Get-SHA256 $path
    }
}

function Write-DeterministicJson([string]$Path, [object]$Value) {
    $json = $Value | ConvertTo-Json -Depth 20
    [IO.File]::WriteAllText($Path, $json + "`n", [Text.UTF8Encoding]::new($false))
}

$records = [Collections.Generic.List[object]]::new()
$module = (& go list -m -json github.com/k2-fsa/sherpa-onnx-go-windows | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or -not $module.Dir -or -not $module.Version) {
    throw "Unable to resolve checksum-pinned sherpa-onnx-go-windows module"
}

foreach ($definition in @(
    [ordered]@{
        id = "native:onnxruntime-windows"
        files = @("onnxruntime.dll")
        license = (Join-Path $RepositoryRoot "compliance\native-license-sources\onnxruntime-LICENSE")
    },
    [ordered]@{
        id = "native:sherpa-onnx-windows"
        files = @("sherpa-onnx-c-api.dll", "sherpa-onnx-cxx-api.dll")
        license = (Join-Path $module.Dir "LICENSE")
    }
)) {
    $component = Get-Component $definition.id
    if ($component.properties.packageManager -ne "go-module" -or
        $component.properties.packageName -ne $module.Path -or
        $component.properties.packageSource -notmatch [Regex]::Escape($module.Version)) {
        throw "Go native package pin does not match registry: $($definition.id)"
    }
    $licenseName = $definition.id.Replace(":", "-") + "-LICENSE"
    $licenseTarget = Join-Path $licenseDirectory $licenseName
    Assert-NoArtifactReparseAncestors $licenseTarget "Native license target"
    Copy-Item -Force -LiteralPath $definition.license -Destination $licenseTarget
    $files = @($definition.files | ForEach-Object { New-FileRecord $_ })
    $records.Add([ordered]@{
        componentId = $definition.id
        packageManager = "go-module"
        packageName = $module.Path
        packageVersion = $component.version
        packageSource = $component.properties.packageSource
        licensePath = ("THIRD_PARTY_LICENSES/native/" + $licenseName)
        licenseSha256 = Get-SHA256 $licenseTarget
        files = $files
        properties = [ordered]@{ goModuleVersion = $module.Version; goModuleSum = $module.Sum }
    })
}

foreach ($componentID in @(
    "native:portaudio-windows",
    "native:gcc-runtime-windows",
    "native:libstdcxx-windows",
    "native:winpthreads-windows"
)) {
    $component = Get-Component $componentID
    $relativeDLL = $component.distributionPath
    $artifactDLL = Get-ArtifactFile $relativeDLL
    $sourceDLL = Join-Path "C:\msys64\mingw64\bin" ([IO.Path]::GetFileName($relativeDLL))
    $owner = (& $Pacman -Qoq $sourceDLL).Trim()
    if ($LASTEXITCODE -ne 0 -or $owner -ne $component.properties.packageName) {
        throw "pacman owner $owner does not match registry package $($component.properties.packageName) for $componentID"
    }
    $query = (& $Pacman -Q $owner).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $query.StartsWith($owner + " ")) {
        throw "pacman -Q failed for $owner"
    }
    $version = $query.Substring($owner.Length + 1)
    $archives = @(Get-ChildItem -File "C:\msys64\var\cache\pacman\pkg\$owner-$version-*.pkg.tar.*" | Where-Object { $_.Name -notlike "*.sig" })
    if ($archives.Count -ne 1) {
        throw "Expected one cached signed package archive for $query; found $($archives.Count)"
    }
    $sourceURL = $component.properties.packageSourcePrefix + $archives[0].Name
    $sourceSHA256 = Get-SHA256 $archives[0].FullName

    $licenseFiles = @(& $Pacman -Qlq $owner | Where-Object { $_ -match "/share/licenses/.+/.+" })
    if ($LASTEXITCODE -ne 0 -or $licenseFiles.Count -eq 0) {
        throw "No installed license evidence is owned by $owner"
    }
    $licenseName = $componentID.Replace(":", "-") + "-LICENSE"
    $licenseTarget = Join-Path $licenseDirectory $licenseName
    Assert-NoArtifactReparseAncestors $licenseTarget "Native license target"
    $licenseText = ""
    foreach ($msysPath in ($licenseFiles | Sort-Object -Unique)) {
        $windowsPath = (& $Cygpath -w $msysPath).Trim()
        if ($LASTEXITCODE -ne 0) { throw "cygpath failed for $msysPath" }
        $licenseText += "----- $msysPath -----`r`n"
        $licenseText += [IO.File]::ReadAllText($windowsPath)
        $licenseText += "`r`n"
    }
    [IO.File]::WriteAllText($licenseTarget, $licenseText, [Text.UTF8Encoding]::new($false))

    $metadataRelative = "compliance/packages/$($componentID.Replace(':', '-')).json"
    $metadataPath = Join-Path $ArtifactRoot $metadataRelative
    Assert-NoArtifactReparseAncestors $metadataPath "Package metadata target"
    Write-DeterministicJson $metadataPath ([ordered]@{
        schemaVersion = 1
        packageQuery = $query
        packageSource = $sourceURL
        packageSourceSha256 = $sourceSHA256
    })
    $records.Add([ordered]@{
        componentId = $componentID
        packageManager = "pacman"
        packageName = $owner
        packageVersion = $version
        packageSource = $sourceURL
        packageSourceSha256 = $sourceSHA256
        packageQuery = $query
        packageMetadataPath = $metadataRelative
        packageMetadataSha256 = Get-SHA256 $metadataPath
        licensePath = ("THIRD_PARTY_LICENSES/native/" + $licenseName)
        licenseSha256 = Get-SHA256 $licenseTarget
        files = @((New-FileRecord $relativeDLL))
    })
}

$manifest = [ordered]@{
    schemaVersion = 1
    platform = "windows"
    packages = @($records | Sort-Object { $_.componentId })
}
$manifestPath = Join-Path $ArtifactRoot "compliance\native-build.json"
Assert-NoArtifactReparseAncestors $manifestPath "Native manifest target"
Write-DeterministicJson $manifestPath $manifest
Write-Host "Captured exact Windows native package，DLL，source，checksum，and license metadata at $manifestPath"
