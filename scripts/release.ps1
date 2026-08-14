param(
    [ValidateSet("build", "deploy", "activate", "rollback")]
    [string]$Command = "build",
    [string]$MainDB = "data\stock.db",
    [string]$MinuteDB = "data\minute.db",
    [string]$WebAddr = "127.0.0.1:34115",
    [string]$RollbackReceipt = "",
    [string]$RuntimeRootOverride = ""
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = Split-Path -Parent $ScriptDir
$RuntimeRoot = if ($RuntimeRootOverride) {
    if ([System.IO.Path]::IsPathRooted($RuntimeRootOverride)) { $RuntimeRootOverride } else { Join-Path $ProjectRoot $RuntimeRootOverride }
} else { Join-Path $ProjectRoot "runtime" }
$RuntimeRoot = [System.IO.Path]::GetFullPath($RuntimeRoot)
$MainDB = [System.IO.Path]::GetFullPath($(if ([System.IO.Path]::IsPathRooted($MainDB)) { $MainDB } else { Join-Path $ProjectRoot $MainDB }))
$MinuteDB = [System.IO.Path]::GetFullPath($(if ([System.IO.Path]::IsPathRooted($MinuteDB)) { $MinuteDB } else { Join-Path $ProjectRoot $MinuteDB }))
$ManifestPath = Join-Path $ProjectRoot "internal\releaseinfo\release_manifest.json"
$CurrentPointer = Join-Path $RuntimeRoot "current.json"
$PidFile = Join-Path $RuntimeRoot "go-stock-web.pid"

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments, [string]$Failure)
    & $Program @Arguments | Out-Host
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) { throw "$Failure (exit $exitCode)" }
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

function Get-Context {
    if (-not (Test-Path -LiteralPath $ManifestPath)) { throw "Missing release manifest: $ManifestPath" }
    $manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
    $commit = (& git -C $ProjectRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $commit) { throw "Cannot resolve git commit" }
    $releaseDir = Join-Path (Join-Path $RuntimeRoot "releases\$($manifest.appVersion)") $commit
    [pscustomobject]@{
        Manifest = $manifest
        Commit = $commit
        ReleaseDir = $releaseDir
        Binary = Join-Path $releaseDir "go-stock-web.exe"
        ZoneInfo = Join-Path $releaseDir "zoneinfo.zip"
    }
}

function Get-ZoneInfoSource {
    $goRoot = (& go env GOROOT).Trim()
    if ($LASTEXITCODE -ne 0) { throw "Cannot resolve GOROOT" }
    $source = Join-Path $goRoot "lib\time\zoneinfo.zip"
    if (-not (Test-Path -LiteralPath $source)) { throw "Go zoneinfo.zip is unavailable" }
    return $source
}

function Write-JSONAtomic {
    param([string]$Path, $Value)
    $parent = Split-Path -Parent $Path
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    $temporary = "$Path.tmp"
    $Value | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $temporary -Encoding UTF8
    Move-Item -LiteralPath $temporary -Destination $Path -Force
}

function New-Pointer {
    param($Context)
    [pscustomobject]@{
        appVersion = [string]$Context.Manifest.appVersion
        mainSchemaVersion = [int]$Context.Manifest.mainSchemaVersion
        minuteSchemaVersion = [int]$Context.Manifest.minuteSchemaVersion
        commit = [string]$Context.Commit
        binary = [System.IO.Path]::GetFullPath($Context.Binary)
        artifactSHA256 = Get-SHA256 -Path $Context.Binary
        zoneInfo = [System.IO.Path]::GetFullPath($Context.ZoneInfo)
        zoneInfoSHA256 = Get-SHA256 -Path $Context.ZoneInfo
        deployedAt = [DateTime]::UtcNow.ToString("o")
    }
}

function Invoke-Build {
    $context = Get-Context
    New-Item -ItemType Directory -Force -Path $context.ReleaseDir | Out-Null
    Invoke-Checked "go" @("test", "./...") "Go tests failed"
    Invoke-Checked "go" @("vet", "./...") "Go vet failed"
    Invoke-Checked "go" @("run", "./cmd/openapi-contract") "OpenAPI contract check failed"
    Push-Location (Join-Path $ProjectRoot "frontend")
    try { Invoke-Checked "npm" @("run", "ci") "Frontend verification failed" }
    finally { Pop-Location }
    $buildTime = [DateTime]::UtcNow.ToString("o")
    $ldflags = "-X go-stock/internal/releaseinfo.Commit=$($context.Commit) -X go-stock/internal/releaseinfo.BuildTime=$buildTime -X go-stock/internal/releaseinfo.Dirty=false"
    Invoke-Checked "go" @("build", "-tags", "webonly", "-ldflags", $ldflags, "-o", $context.Binary, ".") "Release build failed"
    Copy-Item -LiteralPath (Get-ZoneInfoSource) -Destination $context.ZoneInfo -Force
    $pointer = New-Pointer $context
    Write-JSONAtomic (Join-Path $context.ReleaseDir "build.json") $pointer
    # Invoke-Build is captured by Invoke-Deploy. Host output must not become
    # part of the returned pointer object.
    Write-Host "Built App $($pointer.appVersion): $($pointer.binary)"
    return $pointer
}

function Stop-Current {
    if (-not (Test-Path -LiteralPath $PidFile)) { return }
    $processID = [int](Get-Content -LiteralPath $PidFile -Raw)
    $process = Get-CimInstance Win32_Process -Filter "ProcessId = $processID" -ErrorAction SilentlyContinue
    if ($process) {
        $releaseRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "releases")).TrimEnd('\') + '\'
        $executable = [System.IO.Path]::GetFullPath([string]$process.ExecutablePath)
        if (-not $executable.StartsWith($releaseRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to stop process outside release root: $executable"
        }
        Stop-Process -Id $processID -Force
    }
    Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
}

function Start-Pointer {
    param($Pointer)
    $logDir = Join-Path $RuntimeRoot "logs"
    New-Item -ItemType Directory -Force -Path $logDir | Out-Null
    $previous = @{
        Web = $env:GO_STOCK_WEB_ADDR; DB = $env:GO_STOCK_DB_PATH; Minute = $env:GO_STOCK_MINUTE_DB_PATH; Zone = $env:ZONEINFO
    }
    $env:GO_STOCK_WEB_ADDR = $WebAddr
    $env:GO_STOCK_DB_PATH = $MainDB
    $env:GO_STOCK_MINUTE_DB_PATH = $MinuteDB
    $env:ZONEINFO = $Pointer.zoneInfo
    try {
        $process = Start-Process -FilePath $Pointer.binary -ArgumentList "--web" -WorkingDirectory $ProjectRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $logDir "web.out") -RedirectStandardError (Join-Path $logDir "web.err") -PassThru
    } finally {
        $env:GO_STOCK_WEB_ADDR = $previous.Web; $env:GO_STOCK_DB_PATH = $previous.DB
        $env:GO_STOCK_MINUTE_DB_PATH = $previous.Minute; $env:ZONEINFO = $previous.Zone
    }
    $process.Id | Set-Content -LiteralPath $PidFile
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        if (-not (Get-Process -Id $process.Id -ErrorAction SilentlyContinue)) { throw "Released process exited during startup" }
        try {
            $status = Invoke-RestMethod -Uri "http://$WebAddr/readyz" -TimeoutSec 2
            if ($status.appVersion -eq $Pointer.appVersion -and [int]$status.mainSchemaVersion -eq [int]$Pointer.mainSchemaVersion -and [int]$status.minuteSchemaVersion -eq [int]$Pointer.minuteSchemaVersion -and [bool]$status.readiness.ready) { return }
        } catch {}
        Start-Sleep -Seconds 1
    }
    throw "Release readiness timed out"
}

function Invoke-Deploy {
    $context = Get-Context
    if (-not (Test-Path -LiteralPath $context.Binary) -or -not (Test-Path -LiteralPath $context.ZoneInfo)) { $pointer = Invoke-Build }
    else { $pointer = New-Pointer $context }
    if (Test-Path -LiteralPath $CurrentPointer) {
        $backup = Join-Path $RuntimeRoot ("deployments\previous-" + [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss") + ".json")
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $backup) | Out-Null
        Copy-Item -LiteralPath $CurrentPointer -Destination $backup
    }
    Stop-Current
    Write-JSONAtomic $CurrentPointer $pointer
    Start-Pointer $pointer
    Write-Output "Deployed App $($pointer.appVersion) to http://$WebAddr"
}

function Invoke-Rollback {
    if (-not $RollbackReceipt -or -not (Test-Path -LiteralPath $RollbackReceipt)) { throw "rollback requires -RollbackReceipt <pointer.json>" }
    $pointer = Get-Content -LiteralPath $RollbackReceipt -Raw | ConvertFrom-Json
    foreach ($field in @("appVersion", "mainSchemaVersion", "minuteSchemaVersion", "commit", "binary", "artifactSHA256", "zoneInfo", "zoneInfoSHA256")) {
        if ([string]::IsNullOrWhiteSpace([string]$pointer.$field)) { throw "Rollback receipt is missing $field" }
    }
    if ((Get-SHA256 -Path $pointer.binary) -ne $pointer.artifactSHA256) { throw "Rollback binary hash mismatch" }
    Stop-Current
    Write-JSONAtomic $CurrentPointer $pointer
    Start-Pointer $pointer
    Write-Output "Rolled back to App $($pointer.appVersion)"
}

switch ($Command) {
    "build" { Invoke-Build | Out-Null }
    "deploy" { Invoke-Deploy }
    "activate" { Invoke-Deploy }
    "rollback" { Invoke-Rollback }
}
