param([string]$OutputPath = (Join-Path $PSScriptRoot 'karte-synthetic-runtime-20261118.zip'))
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression
$root = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\') + '\'
$files = [Collections.Generic.SortedDictionary[string,string]]::new([StringComparer]::Ordinal)
function Add-PackageFile([string]$Relative) {
    $full = [IO.Path]::GetFullPath((Join-Path $root $Relative))
    if (-not $full.StartsWith($root,[StringComparison]::OrdinalIgnoreCase)) { throw 'Package source escaped its root.' }
    $item = Get-Item -LiteralPath $full -Force
    if ($item.PSIsContainer) { throw 'Expected a package file.' }
    $parent = $item
    while ($parent -and $parent.FullName.Length -ge $root.TrimEnd('\').Length) {
        if ($parent.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Package sources must not contain reparse points.' }
        $parent = if ($parent -is [IO.DirectoryInfo]) { $parent.Parent } else { $parent.Directory }
    }
    $files[$Relative.Replace('\','/')] = $full
}
function Add-PackageDirectory([string]$Relative) {
    $directory = Get-Item -LiteralPath (Join-Path $root $Relative) -Force
    if ($directory.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Package directories must not contain reparse points.' }
    foreach ($item in Get-ChildItem -LiteralPath $directory.FullName -Force) {
        if ($item.PSIsContainer) { Add-PackageDirectory ($Relative + '/' + $item.Name) }
        else { Add-PackageFile ($Relative + '/' + $item.Name) }
    }
}
$karteName = if (Test-Path -LiteralPath (Join-Path $root 'karte-mcp-ea4b9b2.exe')) { 'karte-mcp-ea4b9b2.exe' } else { 'karte-mcp.exe' }
foreach ($name in @('Karte-Synthetic-Startup.psm1','Start-Karte-Synthetic-ChatGPT.ps1','Start-Karte-Synthetic-Grok.ps1','Install-Karte-Synthetic-Autostart.ps1','Manage-Karte-Synthetic-Startup.ps1','Test-Karte-ChatGPT-Startup.ps1','Test-Karte-Synthetic-Autostart.ps1','Test-Karte-Migration-Credentials.ps1','Test-Karte-Runtime-Ports.ps1','Build-Karte-Synthetic-Package.ps1','New-Karte-Migration-Recipient.ps1','Export-Karte-Migration-Credentials.ps1','Import-Karte-Migration-Credentials.ps1','AUTOSTART-AND-MIGRATION.md',$karteName,'openai-tunnel-client-v0.0.21/tunnel-client.exe','openai-tunnel-client-v0.0.21/cloudflared.exe')) { Add-PackageFile $name }
Add-PackageDirectory 'synthetic-live-workspace/content'
foreach ($name in @('context/v1/policy.json','decision-agents.json','decision-log.json')) { Add-PackageFile ('synthetic-live-workspace/.mdsys/' + $name) }
$manifest = @($files.GetEnumerator() | ForEach-Object { [ordered]@{path=$_.Key;sha256=(Get-FileHash -LiteralPath $_.Value -Algorithm SHA256).Hash.ToLowerInvariant()} })
$OutputPath = [IO.Path]::GetFullPath($OutputPath)
if (Test-Path -LiteralPath $OutputPath) { throw 'Output already exists. Choose a new output filename.' }
$temporary = $OutputPath + '.partial-' + [guid]::NewGuid().ToString('N')
$stream = $null
$zip = $null
try {
    $stream = [IO.File]::Open($temporary,[IO.FileMode]::CreateNew,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
    $zip = [IO.Compression.ZipArchive]::new($stream,[IO.Compression.ZipArchiveMode]::Create,$true)
    foreach ($pair in $files.GetEnumerator()) {
        $entry = $zip.CreateEntry($pair.Key,[IO.Compression.CompressionLevel]::Fastest)
        $input = $null; $output = $null
        try { $input=[IO.File]::OpenRead($pair.Value); $output=$entry.Open(); $input.CopyTo($output) }
        finally { if($output){$output.Dispose()}; if($input){$input.Dispose()} }
    }
    $entry = $zip.CreateEntry('runtime-manifest.json')
    $writer = [IO.StreamWriter]::new($entry.Open(),[Text.UTF8Encoding]::new($false))
    try { $writer.Write(($manifest | ConvertTo-Json -Depth 4)) } finally { $writer.Dispose() }
    $zip.Dispose(); $zip=$null; $stream.Dispose(); $stream=$null
    [IO.File]::Move($temporary,$OutputPath)
    [pscustomobject]@{Path=$OutputPath;Files=$files.Count;SHA256=(Get-FileHash -LiteralPath $OutputPath -Algorithm SHA256).Hash.ToLowerInvariant()}
} finally {
    if($zip){$zip.Dispose()}; if($stream){$stream.Dispose()}
    if(Test-Path -LiteralPath $temporary){Remove-Item -LiteralPath $temporary}
}
