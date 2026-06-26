param(
    [ValidateSet("start", "stop", "restart", "rebuild", "status", "open", "help")]
    [string]$Command = "restart",
    [switch]$OpenBrowser
)

$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = Split-Path -Parent $ScriptDir
$LogDir = Join-Path $ProjectRoot "logs"
$PidFile = Join-Path $LogDir "web-mode.windows.pid"
$BinaryPath = Join-Path $ProjectRoot "go-stock-web.exe"
$WebAddr = if ($env:GO_STOCK_WEB_ADDR) { $env:GO_STOCK_WEB_ADDR } else { "127.0.0.1:34115" }
$Port = [int]($WebAddr.Split(":")[-1])
$HealthUrl = "http://$WebAddr/healthz"
$AppUrl = "http://$WebAddr/"
$DevPort = 5173

function Write-Log {
    param([string]$Message)

    New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
    $line = "[{0}] {1}" -f (Get-Date -Format "yyyy-MM-dd HH:mm:ss"), $Message
    $line | Tee-Object -FilePath (Join-Path $LogDir "monitor.windows.log") -Append
}

function Show-Usage {
    @"
Usage:
  powershell -ExecutionPolicy Bypass -File scripts\restart.ps1 start
  powershell -ExecutionPolicy Bypass -File scripts\restart.ps1 stop
  powershell -ExecutionPolicy Bypass -File scripts\restart.ps1 restart
  powershell -ExecutionPolicy Bypass -File scripts\restart.ps1 rebuild
  powershell -ExecutionPolicy Bypass -File scripts\restart.ps1 status
  powershell -ExecutionPolicy Bypass -File scripts\restart.ps1 open

Commands:
  start    Start existing go-stock-web.exe without rebuilding
  stop     Stop the current web service
  restart  Stop then start existing go-stock-web.exe
  rebuild  Build frontend, build webonly backend, then restart
  status   Show listener and process status
  open     Start service and open browser

Env:
  GO_STOCK_WEB_ADDR       Default: 127.0.0.1:34115
  GOTOOLCHAIN             Default for this script: go1.25.0
  GO_STOCK_DB_LOG_LEVEL   Default: silent
  GO_STOCK_LOG_LEVEL      Default: warn
"@
}

function Get-ProcessFromPidFile {
    if (-not (Test-Path -LiteralPath $PidFile)) {
        return $null
    }

    $rawPid = (Get-Content -LiteralPath $PidFile -ErrorAction SilentlyContinue | Select-Object -First 1).Trim()
    if (-not $rawPid) {
        Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
        return $null
    }

    $pidValue = 0
    if (-not [int]::TryParse($rawPid, [ref]$pidValue)) {
        Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
        return $null
    }

    $process = Get-Process -Id $pidValue -ErrorAction SilentlyContinue
    if (-not $process) {
        Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
        return $null
    }

    return $process
}

function Get-ListenerProcessIds {
    param([int]$TargetPort = $Port)

    $connections = Get-NetTCPConnection -LocalPort $TargetPort -State Listen -ErrorAction SilentlyContinue
    if (-not $connections) {
        return @()
    }

    return @($connections | Select-Object -ExpandProperty OwningProcess -Unique)
}

function Get-WebProcessIds {
    $ids = New-Object System.Collections.Generic.HashSet[int]

    $fileProcess = Get-ProcessFromPidFile
    if ($fileProcess) {
        [void]$ids.Add([int]$fileProcess.Id)
    }

    foreach ($id in (Get-ListenerProcessIds)) {
        if ($id -gt 0) {
            [void]$ids.Add([int]$id)
        }
    }

    $query = "Name = 'go-stock-web.exe' OR Name = 'go-stock.exe'"
    foreach ($proc in (Get-CimInstance Win32_Process -Filter $query -ErrorAction SilentlyContinue)) {
        if ($proc.CommandLine -and $proc.CommandLine -like "*--web*") {
            [void]$ids.Add([int]$proc.ProcessId)
        }
    }

    return @($ids)
}

function Stop-DevServerProcess {
    $devIds = Get-ListenerProcessIds -TargetPort $DevPort
    foreach ($id in $devIds) {
        $proc = Get-CimInstance Win32_Process -Filter "ProcessId = $id" -ErrorAction SilentlyContinue
        if (-not $proc) {
            continue
        }
        if ($proc.CommandLine -and ($proc.CommandLine -like "*vite*" -or $proc.CommandLine -like "*npm*run*dev*")) {
            Write-Log "Stopping frontend dev server PID: $id"
            Stop-Process -Id $id -Force -ErrorAction SilentlyContinue
        }
    }
}

function Stop-ServiceProcess {
    $ids = Get-WebProcessIds
    if (-not $ids -or $ids.Count -eq 0) {
        Write-Log "Service is not running on http://$WebAddr"
        Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
        return
    }

    foreach ($id in $ids) {
        $process = Get-Process -Id $id -ErrorAction SilentlyContinue
        if (-not $process) {
            continue
        }

        Write-Log "Stopping process PID: $id"
        Stop-Process -Id $id -Force -ErrorAction SilentlyContinue
    }

    Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
    Write-Log "Service stopped"
}

function Test-Health {
    try {
        $response = Invoke-WebRequest -UseBasicParsing -Uri $HealthUrl -TimeoutSec 2
        return ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300)
    } catch {
        return $false
    }
}

function Wait-Healthy {
    param([int]$ProcessId)

    for ($i = 0; $i -lt 60; $i++) {
        if (Test-Health) {
            Write-Log "Health check passed: $HealthUrl"
            return
        }

        if (-not (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) {
            Write-Log "Service process exited. Check logs\web-mode.windows.out and logs\web-mode.windows.err"
            throw "Service failed to start"
        }

        Start-Sleep -Seconds 1
    }

    Write-Log "Service startup timed out. Check logs\web-mode.windows.out and logs\web-mode.windows.err"
    throw "Service startup timed out"
}

function Ensure-Binary {
    if (-not (Test-Path -LiteralPath $BinaryPath)) {
        Write-Log "Missing binary: $BinaryPath"
        Build-Service
    }
}

function Build-Service {
    Push-Location $ProjectRoot
    try {
        Write-Log "Building frontend static assets..."
        Push-Location (Join-Path $ProjectRoot "frontend")
        try {
            & npm.cmd run build
            if ($LASTEXITCODE -ne 0) {
                throw "Frontend build failed"
            }
        } finally {
            Pop-Location
        }

        Write-Log "Building webonly backend binary..."
        $previousToolchain = $env:GOTOOLCHAIN
        if (-not $env:GOTOOLCHAIN) {
            $env:GOTOOLCHAIN = "go1.25.0"
        }

        try {
            & go build -tags webonly -o $BinaryPath .
            if ($LASTEXITCODE -ne 0) {
                throw "Go build failed"
            }
        } finally {
            $env:GOTOOLCHAIN = $previousToolchain
        }
    } finally {
        Pop-Location
    }
}

function Start-ServiceProcess {
    Ensure-Binary
    New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
    Stop-DevServerProcess

    if (Test-Health) {
        Write-Log "Service is already healthy: $HealthUrl"
        if ($OpenBrowser) {
            Start-Process $AppUrl
        }
        return
    }

    $existing = Get-WebProcessIds
    if ($existing -and $existing.Count -gt 0) {
        Write-Log "Found stale web process or occupied port. Stopping before start..."
        Stop-ServiceProcess
    }

    $outLog = Join-Path $LogDir "web-mode.windows.out"
    $errLog = Join-Path $LogDir "web-mode.windows.err"

    $previousWebAddr = $env:GO_STOCK_WEB_ADDR
    $previousDBLogLevel = $env:GO_STOCK_DB_LOG_LEVEL
    $previousLogLevel = $env:GO_STOCK_LOG_LEVEL

    $env:GO_STOCK_WEB_ADDR = $WebAddr
    if (-not $env:GO_STOCK_DB_LOG_LEVEL) {
        $env:GO_STOCK_DB_LOG_LEVEL = "silent"
    }
    if (-not $env:GO_STOCK_LOG_LEVEL) {
        $env:GO_STOCK_LOG_LEVEL = "warn"
    }

    try {
        $process = Start-Process `
            -FilePath $BinaryPath `
            -ArgumentList "--web" `
            -WorkingDirectory $ProjectRoot `
            -WindowStyle Hidden `
            -RedirectStandardOutput $outLog `
            -RedirectStandardError $errLog `
            -PassThru
    } finally {
        $env:GO_STOCK_WEB_ADDR = $previousWebAddr
        $env:GO_STOCK_DB_LOG_LEVEL = $previousDBLogLevel
        $env:GO_STOCK_LOG_LEVEL = $previousLogLevel
    }

    $process.Id | Set-Content -LiteralPath $PidFile

    Write-Log "Service started, PID: $($process.Id)"
    Wait-Healthy -ProcessId $process.Id
    if ($OpenBrowser) {
        Start-Process $AppUrl
    }
}

function Show-Status {
    $listenerIds = Get-ListenerProcessIds
    if (Test-Health) {
        Write-Log "Service is healthy: $HealthUrl"
    } else {
        Write-Log "Service is not healthy: $HealthUrl"
    }

    if ($listenerIds -and $listenerIds.Count -gt 0) {
        foreach ($id in $listenerIds) {
            $proc = Get-CimInstance Win32_Process -Filter "ProcessId = $id" -ErrorAction SilentlyContinue
            if ($proc) {
                Write-Output "PID ${id}: $($proc.CommandLine)"
            } else {
                Write-Output "PID $id is listening on port $Port"
            }
        }
    } else {
        Write-Output "No process is listening on port $Port"
    }
}

switch ($Command) {
    "start" {
        Write-Log "Starting service without rebuild..."
        Start-ServiceProcess
    }
    "stop" {
        Write-Log "Stopping service..."
        Stop-ServiceProcess
        Stop-DevServerProcess
    }
    "restart" {
        Write-Log "Restarting service without rebuild..."
        Stop-ServiceProcess
        Start-ServiceProcess
    }
    "rebuild" {
        Write-Log "Rebuilding and restarting service..."
        Stop-ServiceProcess
        Build-Service
        Start-ServiceProcess
    }
    "status" {
        Show-Status
    }
    "open" {
        Write-Log "Starting service and opening browser..."
        $OpenBrowser = $true
        Start-ServiceProcess
    }
    "help" {
        Show-Usage
    }
}
