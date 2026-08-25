param(
    [string]$ShortcutName = "Go-Stock 1.7.8",
    [string]$DesktopPath = [Environment]::GetFolderPath("Desktop")
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = Split-Path -Parent $ScriptDir
$LauncherName = ([char]0x542F).ToString() + [char]0x52A8 + [char]0x9879 + [char]0x76EE + ".cmd"
$Launcher = Join-Path $ProjectRoot $LauncherName
$IconPath = Join-Path $ProjectRoot "build\app.ico"

if (-not (Test-Path -LiteralPath $Launcher)) { throw "Launcher is missing: $Launcher" }
if (-not (Test-Path -LiteralPath $IconPath)) { throw "Application icon is missing: $IconPath" }
if (-not (Test-Path -LiteralPath $DesktopPath)) { New-Item -ItemType Directory -Path $DesktopPath -Force | Out-Null }

$ShortcutPath = Join-Path $DesktopPath ($ShortcutName + ".lnk")
$Shell = New-Object -ComObject WScript.Shell
$Shortcut = $Shell.CreateShortcut($ShortcutPath)
$Shortcut.TargetPath = $Launcher
$Shortcut.Arguments = ""
$Shortcut.WorkingDirectory = $ProjectRoot
$Shortcut.IconLocation = $IconPath + ",0"
$Shortcut.Description = "Start Go-Stock 1.7.8 and open Research Center"
$Shortcut.Save()

Write-Output $ShortcutPath
