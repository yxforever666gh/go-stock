param(
    [string]$ShortcutName = "Go-Stock 1.7.1",
    [string]$DesktopPath = [Environment]::GetFolderPath("Desktop")
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = Split-Path -Parent $ScriptDir
$RestartScript = Join-Path $ScriptDir "restart.ps1"
$IconPath = Join-Path $ProjectRoot "build\app.ico"

if (-not (Test-Path -LiteralPath $RestartScript)) { throw "Restart script is missing: $RestartScript" }
if (-not (Test-Path -LiteralPath $IconPath)) { throw "Application icon is missing: $IconPath" }
if (-not (Test-Path -LiteralPath $DesktopPath)) { New-Item -ItemType Directory -Path $DesktopPath -Force | Out-Null }

$PowerShell = Get-Command powershell.exe -ErrorAction Stop
$ShortcutPath = Join-Path $DesktopPath ($ShortcutName + ".lnk")
$Shell = New-Object -ComObject WScript.Shell
$Shortcut = $Shell.CreateShortcut($ShortcutPath)
$Shortcut.TargetPath = $PowerShell.Source
$Shortcut.Arguments = '-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $RestartScript + '" open -ResearchCenter'
$Shortcut.WorkingDirectory = $ProjectRoot
$Shortcut.IconLocation = $IconPath + ",0"
$Shortcut.Description = "Start Go-Stock 1.7.1 and open Research Center"
$Shortcut.Save()

Write-Output $ShortcutPath
