[CmdletBinding()]
param(
    [ValidateSet('start', 'stop', 'restart', 'status', 'open', 'help')]
    [string]$Command = 'open',
    [switch]$ResearchCenter,
    [switch]$NoBrowser
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = [System.IO.Path]::GetFullPath((Join-Path $ScriptDir '..'))
$RuntimeRoot = Join-Path $ProjectRoot 'runtime'
$SourceRuntimeRoot = Join-Path $RuntimeRoot 'source-dev'
$StatePath = Join-Path $SourceRuntimeRoot 'processes.json'
$BackendAddr = '127.0.0.1:34115'
$BackendPort = 34115
$FrontendAddr = '127.0.0.1:5173'
$FrontendPort = 5173
$FrontendURL = "http://$FrontendAddr"
$ResearchCenterURL = "$FrontendURL/#/research2?name=%E8%82%A1%E7%A5%A8%E6%8E%A8%E8%8D%90%E8%AE%B0%E5%BD%95"

function Show-Usage {
    @"
Usage:
  pwsh -NoProfile -File scripts\source-run.ps1 start|stop|restart|status|open

Runs the current Go source at $BackendAddr and the Vite frontend at $FrontendAddr.
"@
}

function Get-ListenerProcessId {
    param([int]$Port)
    $ids = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
        Select-Object -ExpandProperty OwningProcess -Unique)
    if ($ids.Count -gt 1) { throw "Multiple processes listen on port $Port" }
    if ($ids.Count -eq 0) { return 0 }
    return [int]$ids[0]
}

function Get-ProcessIdentity {
    param([Diagnostics.Process]$Process)
    return @{pid = [int]$Process.Id; startedAt = $Process.StartTime.ToUniversalTime().ToString('o'); executable = [string]$Process.Path}
}

function Get-MatchingProcess {
    param($Identity)
    if ($null -eq $Identity) { return $null }
    try { $process = [Diagnostics.Process]::GetProcessById([int]$Identity.pid) }
    catch [ArgumentException] { return $null }
    if ($process.HasExited) { $process.Dispose(); return $null }
    if ($process.Path -ne [string]$Identity.executable -or $process.StartTime.ToUniversalTime().Ticks -ne ([datetimeoffset]$Identity.startedAt).UtcDateTime.Ticks) {
        $process.Dispose()
        throw "Source runtime PID $($Identity.pid) no longer identifies the recorded process"
    }
    return $process
}

function Read-SourceState {
    if (-not (Test-Path -LiteralPath $StatePath -PathType Leaf)) { return $null }
    try { return Get-Content -LiteralPath $StatePath -Raw | ConvertFrom-Json }
    catch { throw "Source runtime state is invalid: $($_.Exception.Message)" }
}

function Write-SourceState {
    param($State)
    New-Item -ItemType Directory -Force -Path $SourceRuntimeRoot | Out-Null
    $temporary = "$StatePath.tmp"
    [IO.File]::WriteAllText($temporary, ($State | ConvertTo-Json -Depth 8), [Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $temporary -Destination $StatePath -Force
}

function Remove-SourceState {
    Remove-Item -LiteralPath $StatePath -Force -ErrorAction SilentlyContinue
}

function Test-BackendReady {
    try {
        $status = Invoke-RestMethod -Uri "http://$BackendAddr/readyz" -TimeoutSec 2
        return [bool]$status.readiness.ready
    } catch { return $false }
}

function Test-FrontendReady {
    try {
        $response = Invoke-WebRequest -Uri $FrontendURL -TimeoutSec 2
        return $response.StatusCode -eq 200
    } catch { return $false }
}

function Wait-Ready {
    param([scriptblock]$Condition, [string]$Name, [int]$TimeoutSeconds = 60)
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if (& $Condition) { return }
        Start-Sleep -Milliseconds 500
    }
    throw "$Name did not become ready within $TimeoutSeconds seconds"
}

function Assert-SourcePrerequisites {
    foreach ($command in @('git', 'go.exe', 'node.exe', 'npm.cmd', 'pwsh.exe')) {
        if (-not (Get-Command $command -ErrorAction SilentlyContinue)) { throw "Required command is unavailable: $command" }
    }
    foreach ($path in @('frontend\node_modules', 'internal\migrations\backup.go')) {
        if (-not (Test-Path -LiteralPath (Join-Path $ProjectRoot $path))) {
            throw "Source runtime prerequisite is missing: $path. If sparse checkout is enabled, run: git sparse-checkout disable"
        }
    }
    $missingSources = @(& git -C $ProjectRoot ls-files -v -- '*.go' | Where-Object { $_.StartsWith('S ') })
    if ($missingSources.Count -gt 0) { throw "Sparse checkout omits Go source. Run: git sparse-checkout disable" }
}

function Stop-RecordedProcessTree {
    param($Identity)
    $process = Get-MatchingProcess $Identity
    if ($null -eq $process) { return }
    $processId = [int]$process.Id
    $process.Dispose()
    & taskkill.exe /PID $processId /T /F | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not stop source runtime process tree $processId" }
}

function Stop-SourceRuntime {
    $state = Read-SourceState
    if ($null -eq $state) {
        Write-Host 'Source runtime is not running.'
        return
    }
    try { Stop-RecordedProcessTree $state.frontend.supervisor } finally { Stop-RecordedProcessTree $state.backend.supervisor }
    Remove-SourceState
    Write-Host 'Stopped source backend and Vite frontend.'
}

function Stop-DeployedRuntimeIfNeeded {
    if ((Get-ListenerProcessId $BackendPort) -eq 0) { return }
    $restartScript = Join-Path $ScriptDir 'restart.ps1'
    if (-not (Test-Path -LiteralPath $restartScript -PathType Leaf)) { throw "Port $BackendPort is occupied and the deployed-runtime stop script is unavailable" }
    & pwsh.exe -NoProfile -File $restartScript -Command stop
    if ($LASTEXITCODE -ne 0 -or (Get-ListenerProcessId $BackendPort) -ne 0) {
        throw "Port $BackendPort is occupied by a process that is not safe to replace"
    }
}

function Start-SourceRuntime {
    $state = Read-SourceState
    if ($null -ne $state) {
        $backend = Get-MatchingProcess $state.backend.supervisor
        $frontend = Get-MatchingProcess $state.frontend.supervisor
        try {
            if ($null -ne $backend -and $null -ne $frontend -and (Test-BackendReady) -and (Test-FrontendReady)) {
                Write-Host "Source runtime already ready: $FrontendURL"
                return
            }
        } finally {
            if ($null -ne $backend) { $backend.Dispose() }
            if ($null -ne $frontend) { $frontend.Dispose() }
        }
        Stop-SourceRuntime
    }

    Assert-SourcePrerequisites
    if ((Get-ListenerProcessId $FrontendPort) -ne 0) { throw "Port $FrontendPort is occupied by an unknown frontend process" }
    Stop-DeployedRuntimeIfNeeded
    if ((Get-ListenerProcessId $BackendPort) -ne 0) { throw "Port $BackendPort remains occupied after stopping the deployed runtime" }

    New-Item -ItemType Directory -Force -Path $SourceRuntimeRoot | Out-Null
    $goProcess = $null
    $npmProcess = $null
    try {
        $goProgram = (Get-Command go.exe -ErrorAction Stop).Source
        $goProcess = Start-Process -FilePath $goProgram -ArgumentList @('run', '-tags', 'dev', '.', '--web-addr', $BackendAddr) -WorkingDirectory $ProjectRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $SourceRuntimeRoot 'backend.out.log') -RedirectStandardError (Join-Path $SourceRuntimeRoot 'backend.err.log') -PassThru
        Wait-Ready { Test-BackendReady } 'Source backend' 300
        $backendListener = Get-ListenerProcessId $BackendPort
        if ($backendListener -eq 0) { throw 'Source backend did not own its listen port' }

        $npmProgram = (Get-Command npm.cmd -ErrorAction Stop).Source
        $previousBackend = $env:GO_STOCK_DEV_BACKEND
        $env:GO_STOCK_DEV_BACKEND = "http://$BackendAddr"
        try {
            $npmProcess = Start-Process -FilePath $npmProgram -ArgumentList @('run', 'dev') -WorkingDirectory (Join-Path $ProjectRoot 'frontend') -WindowStyle Hidden -RedirectStandardOutput (Join-Path $SourceRuntimeRoot 'frontend.out.log') -RedirectStandardError (Join-Path $SourceRuntimeRoot 'frontend.err.log') -PassThru
        } finally {
            $env:GO_STOCK_DEV_BACKEND = $previousBackend
        }
        Wait-Ready { Test-FrontendReady } 'Vite frontend' 30
        $frontendListener = Get-ListenerProcessId $FrontendPort
        if ($frontendListener -eq 0) { throw 'Vite frontend did not own its listen port' }

        Write-SourceState @{
            formatVersion = 1
            projectRoot = $ProjectRoot
            startedAt = [DateTime]::UtcNow.ToString('o')
            backend = @{supervisor = Get-ProcessIdentity $goProcess; listenerPid = $backendListener}
            frontend = @{supervisor = Get-ProcessIdentity $npmProcess; listenerPid = $frontendListener}
        }
        Write-Host "Source runtime ready: $FrontendURL"
    } catch {
        if ($null -ne $npmProcess) { Stop-RecordedProcessTree (Get-ProcessIdentity $npmProcess) }
        if ($null -ne $goProcess) { Stop-RecordedProcessTree (Get-ProcessIdentity $goProcess) }
        throw
    } finally {
        if ($null -ne $goProcess) { $goProcess.Dispose() }
        if ($null -ne $npmProcess) { $npmProcess.Dispose() }
    }
}

function Show-SourceRuntimeStatus {
    $state = Read-SourceState
    if ($null -eq $state) { Write-Host 'Source runtime is not running.'; return }
    [pscustomobject]@{
        Backend = "http://$BackendAddr"
        BackendReady = Test-BackendReady
        Frontend = $FrontendURL
        FrontendReady = Test-FrontendReady
        BackendListenerPID = $state.backend.listenerPid
        FrontendListenerPID = $state.frontend.listenerPid
        StartedAt = $state.startedAt
    } | Format-List
}

switch ($Command) {
    'help' { Show-Usage }
    'start' { Start-SourceRuntime }
    'stop' { Stop-SourceRuntime }
    'restart' { Stop-SourceRuntime; Start-SourceRuntime }
    'status' { Show-SourceRuntimeStatus }
    'open' {
        Start-SourceRuntime
        $openURL = if ($ResearchCenter) { $ResearchCenterURL } else { $FrontendURL }
        if (-not $NoBrowser) { Start-Process -FilePath $openURL }
    }
}
