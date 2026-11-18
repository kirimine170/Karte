param([switch]$ConfigureOnly)
$ErrorActionPreference = 'Stop'
# Existing encrypted credentials are reused. First-time setup prompts locally.
& (Join-Path $PSScriptRoot 'Start-Karte-Synthetic-ChatGPT.ps1') -SetupAutostart -ConfigureOnly:$ConfigureOnly
& (Join-Path $PSScriptRoot 'Start-Karte-Synthetic-Grok.ps1') -SetupAutostart -ConfigureOnly:$ConfigureOnly
Write-Host 'Both synthetic MCP startup tasks are configured.'
& (Join-Path $PSScriptRoot 'Manage-Karte-Synthetic-Startup.ps1') -Action Status
