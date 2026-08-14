param(
    [ValidateSet("start", "stop", "restart", "rebuild", "status", "open", "help")]
    [string]$Command = "restart",
    [switch]$OpenBrowser
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = Split-Path -Parent $ScriptDir
$RuntimeRoot = Join-Path $ProjectRoot "runtime"
$CurrentPointer = Join-Path $RuntimeRoot "current.json"
$LogDir = Join-Path $ProjectRoot "logs"
$PidFile = Join-Path $RuntimeRoot "go-stock-web.pid"
$WebAddr = if ($env:GO_STOCK_WEB_ADDR) { $env:GO_STOCK_WEB_ADDR } else { "127.0.0.1:34115" }
$Port = [int]($WebAddr.Split(":")[-1])
$ReadyURL = "http://$WebAddr/readyz"
$AppURL = "http://$WebAddr/"

function Write-Log {
    param([string]$Message)
    New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
    $line = "[{0}] {1}" -f (Get-Date -Format "yyyy-MM-dd HH:mm:ss"), $Message
    $line | Tee-Object -FilePath (Join-Path $LogDir "monitor.windows.log") -Append
}

function Get-SHA256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    $stream = [System.IO.File]::OpenRead($Path)
    try {
        $sha256 = [System.Security.Cryptography.SHA256]::Create()
        try {
            return (($sha256.ComputeHash($stream) | ForEach-Object { $_.ToString("x2") }) -join "")
        } finally {
            $sha256.Dispose()
        }
    } finally {
        $stream.Dispose()
    }
}

function Show-Usage {
    @"
Usage:
  powershell -ExecutionPolicy Bypass -File scripts\restart.ps1 start|stop|restart|status|open

All service operations use the exact immutable artifact recorded in runtime\current.json.
The rebuild command is intentionally disabled; use scripts\release.ps1 build/deploy.
"@
}

function Resolve-Pointer {
    if (-not (Test-Path -LiteralPath $CurrentPointer)) {
        throw "Release pointer is missing: $CurrentPointer"
    }
    try { $pointer = Get-Content -LiteralPath $CurrentPointer -Raw | ConvertFrom-Json }
    catch { throw "Release pointer is invalid JSON: $($_.Exception.Message)" }
    foreach ($field in @("appVersion", "mainSchemaVersion", "minuteSchemaVersion", "commit", "binary", "artifactSHA256", "zoneInfo", "zoneInfoSHA256")) {
        if ($null -eq $pointer.$field -or [string]::IsNullOrWhiteSpace([string]$pointer.$field)) {
            throw "Release pointer is missing $field"
        }
    }
    foreach ($path in @([string]$pointer.binary, [string]$pointer.zoneInfo)) {
        if (-not [System.IO.Path]::IsPathRooted($path) -or -not (Test-Path -LiteralPath $path)) {
            throw "Release pointer path is unavailable: $path"
        }
        $releaseRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "releases")).TrimEnd('\') + '\'
        $resolved = [System.IO.Path]::GetFullPath((Resolve-Path -LiteralPath $path).Path)
        if (-not $resolved.StartsWith($releaseRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Release pointer path is outside runtime releases: $resolved"
        }
    }
    $binaryHash = Get-SHA256 -Path $pointer.binary
    $zoneHash = Get-SHA256 -Path $pointer.zoneInfo
    if ($binaryHash -ne ([string]$pointer.artifactSHA256).ToLowerInvariant()) { throw "Release binary SHA256 mismatch" }
    if ($zoneHash -ne ([string]$pointer.zoneInfoSHA256).ToLowerInvariant()) { throw "Release zoneinfo SHA256 mismatch" }

    $inspectOutput = @(& $pointer.binary "release" "inspect" 2>&1)
    if ($LASTEXITCODE -ne 0) { throw "release inspect failed for pointer binary" }
    try { $inspect = (($inspectOutput | ForEach-Object { [string]$_ }) -join [Environment]::NewLine) | ConvertFrom-Json }
    catch { throw "release inspect returned invalid JSON" }
    if ($inspect.manifest.appVersion -ne $pointer.appVersion -or
        [int]$inspect.manifest.mainSchemaVersion -ne [int]$pointer.mainSchemaVersion -or
        [int]$inspect.manifest.minuteSchemaVersion -ne [int]$pointer.minuteSchemaVersion -or
        $inspect.build.commit -ne $pointer.commit -or
        ([string]$inspect.build.artifactSHA256).ToLowerInvariant() -ne $binaryHash -or
        [bool]$inspect.build.dirty) {
        throw "Release pointer does not match embedded release identity"
    }
    return $pointer
}

function Get-ListenerProcessIds {
    return @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique)
}

function Get-ListenerProcess {
    $ids = Get-ListenerProcessIds
    if ($ids.Count -eq 0) { return $null }
    if ($ids.Count -ne 1) { throw "Expected one listener on $WebAddr, found $($ids.Count)" }
    return Get-CimInstance Win32_Process -Filter "ProcessId = $($ids[0])" -ErrorAction Stop
}

function Assert-ListenerMatchesPointer {
    param($Pointer, $Process)
    if (-not $Process -or -not $Process.ExecutablePath) { throw "Cannot resolve listener executable" }
    $actual = [System.IO.Path]::GetFullPath([string]$Process.ExecutablePath)
    $expected = [System.IO.Path]::GetFullPath([string]$Pointer.binary)
    if (-not $actual.Equals($expected, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Listener PID $($Process.ProcessId) is not the pointer artifact: $actual"
    }
}

function Wait-PortReleased {
    param([int]$StoppedProcessId, [int]$TimeoutMilliseconds = 10000)
    $deadline = [DateTime]::UtcNow.AddMilliseconds($TimeoutMilliseconds)
    do {
        $listenerIds = @(Get-ListenerProcessIds)
        if ($listenerIds.Count -eq 0) { return }
        $unknownIds = @($listenerIds | Where-Object { [int]$_ -ne $StoppedProcessId })
        if ($unknownIds.Count -gt 0) {
            throw "Port $Port was claimed by unknown listener PID(s): $($unknownIds -join ', ')"
        }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Port $Port did not release after stopping pointer PID $StoppedProcessId"
}

function Stop-ServiceProcess {
    $pointer = Resolve-Pointer
    $listener = Get-ListenerProcess
    if (-not $listener) {
        Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
        Write-Log "Service is not running on http://$WebAddr"
        return
    }
    Assert-ListenerMatchesPointer $pointer $listener
    Write-Log "Stopping pointer process PID: $($listener.ProcessId)"
    Stop-Process -Id $listener.ProcessId -Force
    Wait-PortReleased -StoppedProcessId $listener.ProcessId
    Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
}

function Test-ExactReadiness {
    param($Pointer)
    try {
        $status = Invoke-RestMethod -Uri $ReadyURL -TimeoutSec 2
        return ($status.appVersion -eq $Pointer.appVersion -and
            $status.commit -eq $Pointer.commit -and
            ([string]$status.artifactSHA256).ToLowerInvariant() -eq ([string]$Pointer.artifactSHA256).ToLowerInvariant() -and
            [int]$status.mainSchemaVersion -eq [int]$Pointer.mainSchemaVersion -and
            [int]$status.minuteSchemaVersion -eq [int]$Pointer.minuteSchemaVersion -and
            [bool]$status.readiness.ready)
    } catch { return $false }
}

function Wait-ExactReadiness {
    param($Pointer, [int]$ProcessId)
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        if (Test-ExactReadiness $Pointer) { Write-Log "Exact readiness passed: $ReadyURL"; return }
        if (-not (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) { throw "Pointer process exited during startup" }
        Start-Sleep -Seconds 1
    }
    throw "Pointer process readiness did not match runtime/current.json"
}

function Start-ServiceProcess {
    $pointer = Resolve-Pointer
    $listener = Get-ListenerProcess
    if ($listener) {
        Assert-ListenerMatchesPointer $pointer $listener
        if (-not (Test-ExactReadiness $pointer)) { throw "Pointer process is listening but exact readiness failed" }
        Write-Log "Pointer service is already ready: $ReadyURL"
        if ($OpenBrowser) { Start-Process $AppURL }
        return
    }
    New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
    $outLog = Join-Path $LogDir "web-mode.windows.out"
    $errLog = Join-Path $LogDir "web-mode.windows.err"
    $oldWebAddr, $oldZoneInfo = $env:GO_STOCK_WEB_ADDR, $env:ZONEINFO
    $oldDBLog, $oldLog = $env:GO_STOCK_DB_LOG_LEVEL, $env:GO_STOCK_LOG_LEVEL
    $env:GO_STOCK_WEB_ADDR = $WebAddr
    $env:ZONEINFO = (Resolve-Path -LiteralPath $pointer.zoneInfo).Path
    if (-not $env:GO_STOCK_DB_LOG_LEVEL) { $env:GO_STOCK_DB_LOG_LEVEL = "silent" }
    if (-not $env:GO_STOCK_LOG_LEVEL) { $env:GO_STOCK_LOG_LEVEL = "warn" }
    try {
        $process = Start-Process -FilePath $pointer.binary -ArgumentList "--web" -WorkingDirectory $ProjectRoot -WindowStyle Hidden -RedirectStandardOutput $outLog -RedirectStandardError $errLog -PassThru
    } finally {
        $env:GO_STOCK_WEB_ADDR, $env:ZONEINFO = $oldWebAddr, $oldZoneInfo
        $env:GO_STOCK_DB_LOG_LEVEL, $env:GO_STOCK_LOG_LEVEL = $oldDBLog, $oldLog
    }
    $process.Id | Set-Content -LiteralPath $PidFile
    Write-Log "Started pointer artifact PID: $($process.Id)"
    Wait-ExactReadiness $pointer $process.Id
    if ($OpenBrowser) { Start-Process $AppURL }
}

function Show-Status {
    $pointer = Resolve-Pointer
    $listener = Get-ListenerProcess
    if (-not $listener) { Write-Output "No process is listening on port $Port"; return }
    Assert-ListenerMatchesPointer $pointer $listener
    Write-Output "PID $($listener.ProcessId): $($listener.ExecutablePath)"
    if (Test-ExactReadiness $pointer) { Write-Log "Pointer service is ready: $ReadyURL" }
    else { throw "Pointer listener failed exact readiness" }
}

switch ($Command) {
    "start" { Start-ServiceProcess }
    "stop" { Stop-ServiceProcess }
    "restart" { Stop-ServiceProcess; Start-ServiceProcess }
    "rebuild" { throw "Direct production rebuild is disabled. Use scripts\release.ps1 build, then scripts\release.ps1 deploy." }
    "status" { Show-Status }
    "open" { $OpenBrowser = $true; Start-ServiceProcess }
    "help" { Show-Usage }
}
