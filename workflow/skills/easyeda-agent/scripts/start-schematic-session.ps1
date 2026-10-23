param([Parameter(Mandatory=$true)][string]$Easyeda)
$ErrorActionPreference = 'Stop'
# Run before drawing; no editor reload, save, or design mutation is issued here.
if (-not (Test-Path -LiteralPath $Easyeda -PathType Leaf)) { throw 'CLI executable missing' }
& $Easyeda daemon restart --autosave-debounce 0 --auto-update-skill=false
if ($LASTEXITCODE -ne 0) { throw "Daemon restart failed: $LASTEXITCODE" }
