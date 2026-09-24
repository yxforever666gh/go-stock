param(
    [ValidateSet('start','stop','restart','status','open')][string]$Command = 'restart',
    [switch]$OpenBrowser,
    [switch]$NoBrowser
)
$ErrorActionPreference = 'Stop'
if ($Command -eq 'open') {
    & (Join-Path $PSScriptRoot 'release.ps1') start
} else {
    & (Join-Path $PSScriptRoot 'release.ps1') $Command
}
if (-not $NoBrowser -and ($OpenBrowser -or $Command -eq 'open')) {
    Start-Process 'http://127.0.0.1:34115/#/prediction'
}
