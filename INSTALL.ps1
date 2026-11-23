# Cloudware Update Center — one-time bootstrap, intentionally non-automatic.
#Requires -Version 5.1
$ErrorActionPreference = 'Stop'
$root = 'E:\Cloudware'
$source = Join-Path $PSScriptRoot 'CLOUDWARE-UPDATE-CENTER.exe'
$destination = Join-Path $root 'tools\CloudwareUpdateCenter'
if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw 'Update Center executable missing.' }
if (-not (Test-Path -LiteralPath $root -PathType Container)) { throw 'Cloudware root not found.' }
$hasCompose = @('compose.yaml','compose.yml','docker-compose.yaml','docker-compose.yml') | Where-Object { Test-Path -LiteralPath (Join-Path $root $_) -PathType Leaf }
if (-not $hasCompose) { throw 'Cloudware Compose file not found.' }
if (-not (Test-Path -LiteralPath $destination -PathType Container)) {
  New-Item -ItemType Directory -Path $destination -Force | Out-Null
}
$target = Join-Path $destination 'CLOUDWARE-UPDATE-CENTER.exe'
if (Test-Path -LiteralPath $target -PathType Leaf) {
  $backup = Join-Path $destination ('CLOUDWARE-UPDATE-CENTER.before-' + [guid]::NewGuid().ToString('N') + '.exe')
  Copy-Item -LiteralPath $target -Destination $backup -ErrorAction Stop
}
Copy-Item -LiteralPath $source -Destination $target -Force -ErrorAction Stop
Write-Host '[PASS] Cloudware Update Center binary copied. Cloudware app was NOT modified.'
Write-Host '[INFO] GitHub private-repo access requires GitHub CLI authentication. No application release is installed by this bootstrap.'
