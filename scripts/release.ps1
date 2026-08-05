param(
    [ValidateSet("build", "deploy", "rollback")]
    [string]$Command = "build",
    [string]$MainDB = "data\stock.db",
    [string]$MinuteDB = "data\minute.db",
    [string]$WebAddr = "127.0.0.1:34115",
    [string]$RollbackReceipt = "",
    [string]$ReplayBundle = "runtime\backups\pre-refactor-20260806-001309",
    [string]$ReplayBundleManifest = "release\replay_bundle_manifest.json",
    [string]$RuntimeRootOverride = "",
    [switch]$SimulateReadinessFailure,
    [ValidateRange(1, 300)]
    [int]$ReadinessAttempts = 60,
    [ValidateRange(1, 300)]
    [int]$RollbackHealthAttempts = 60
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = Split-Path -Parent $ScriptDir
$ReplayBundleFullPath = if ([System.IO.Path]::IsPathRooted($ReplayBundle)) { $ReplayBundle } else { Join-Path $ProjectRoot $ReplayBundle }
$ReplayBundleManifestFullPath = if ([System.IO.Path]::IsPathRooted($ReplayBundleManifest)) { $ReplayBundleManifest } else { Join-Path $ProjectRoot $ReplayBundleManifest }
$DefaultRuntimeRoot = Join-Path $ProjectRoot "runtime"
$RuntimeRoot = if ($RuntimeRootOverride) {
    if ([System.IO.Path]::IsPathRooted($RuntimeRootOverride)) { $RuntimeRootOverride } else { Join-Path $ProjectRoot $RuntimeRootOverride }
} else { $DefaultRuntimeRoot }
$RuntimeRoot = [System.IO.Path]::GetFullPath($RuntimeRoot)
$MainDB = [System.IO.Path]::GetFullPath($(if ([System.IO.Path]::IsPathRooted($MainDB)) { $MainDB } else { Join-Path $ProjectRoot $MainDB }))
$MinuteDB = [System.IO.Path]::GetFullPath($(if ([System.IO.Path]::IsPathRooted($MinuteDB)) { $MinuteDB } else { Join-Path $ProjectRoot $MinuteDB }))
$ReleasesRoot = Join-Path $RuntimeRoot "releases"
$BackupsRoot = Join-Path $RuntimeRoot "backups"
$DeploymentsRoot = Join-Path $RuntimeRoot "deployments"
$CurrentPointer = Join-Path $RuntimeRoot "current.json"
$PidFile = Join-Path $RuntimeRoot "go-stock-web.pid"
$ManifestSource = Join-Path $ProjectRoot "internal\releaseinfo\release_manifest.json"
$ReadyURL = "http://$WebAddr/readyz"
$LiveURL = "http://$WebAddr/livez"

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments, [string]$Failure)
    $output = @(& $Program @Arguments 2>&1)
    $exitCode = $LASTEXITCODE
    foreach ($line in $output) { Write-Host $line }
    if ($exitCode -ne 0) {
        $details = ($output | Select-Object -Last 20 | ForEach-Object { [string]$_ }) -join [Environment]::NewLine
        if ($details) { throw "$Failure (exit $exitCode)`n$details" }
        throw "$Failure (exit $exitCode)"
    }
}

function Test-SamePath {
    param([string]$Left, [string]$Right)
    return [System.IO.Path]::GetFullPath($Left).TrimEnd('\') -eq [System.IO.Path]::GetFullPath($Right).TrimEnd('\')
}

function Assert-SafeDescendant {
    param([string]$Parent, [string]$Child, [string]$Description)
    $parentPath = [System.IO.Path]::GetFullPath($Parent).TrimEnd('\')
    $childPath = [System.IO.Path]::GetFullPath($Child)
    $prefix = $parentPath + '\'
    if (-not $childPath.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "$Description must be below $parentPath`: $childPath"
    }
}

function Assert-PathHasNoLinks {
    param([string]$Root, [string]$Path, [string]$Description)
    Assert-SafeDescendant $Root $Path $Description
    if (-not (Test-Path -LiteralPath $Root) -or -not (Test-Path -LiteralPath $Path)) {
        throw "$Description and its isolation root must already exist"
    }
    $rootPath = [System.IO.Path]::GetFullPath($Root).TrimEnd('\')
    $currentPath = [System.IO.Path]::GetFullPath($Path)
    while ($true) {
        $item = Get-Item -LiteralPath $currentPath -Force
        $isReparsePoint = ([int]$item.Attributes -band [int][System.IO.FileAttributes]::ReparsePoint) -ne 0
        if ($isReparsePoint -or $item.LinkType) {
            throw "$Description cannot contain a link or reparse point: $currentPath"
        }
        if (Test-SamePath $currentPath $rootPath) { break }
        $parentPath = Split-Path -Parent $currentPath
        if (-not $parentPath -or (Test-SamePath $parentPath $currentPath)) {
            throw "$Description escapes its isolation root: $Path"
        }
        $currentPath = $parentPath
    }

    if (-not (Get-Item -LiteralPath $Path -Force).PSIsContainer) {
        $hardLinks = @(& fsutil.exe hardlink list $Path 2>&1)
        if ($LASTEXITCODE -ne 0) {
            throw "Cannot verify hard-link isolation for $Description`: $Path"
        }
        $hardLinks = @($hardLinks | ForEach-Object { ([string]$_).Trim() } | Where-Object { $_ })
        if ($hardLinks.Count -ne 1) {
            throw "$Description must not be hard-linked (found $($hardLinks.Count) links): $Path"
        }
    }
}

function Get-WebPort {
    $lastColon = $WebAddr.LastIndexOf(':')
    if ($lastColon -lt 0) { throw "Web address must include a port: $WebAddr" }
    $parsedPort = 0
    if (-not [int]::TryParse($WebAddr.Substring($lastColon + 1), [ref]$parsedPort) -or $parsedPort -lt 1 -or $parsedPort -gt 65535) {
        throw "Web address has an invalid port: $WebAddr"
    }
    return $parsedPort
}

function Assert-SimulationIsolation {
    if (-not $SimulateReadinessFailure) { return }
    if ($Command -ne "deploy") { throw "-SimulateReadinessFailure is only valid with deploy" }
    if (-not $RuntimeRootOverride) { throw "Simulated readiness failure requires -RuntimeRootOverride" }
    if ((Get-WebPort) -eq 34115) { throw "Simulated readiness failure cannot use production port 34115" }

    $productionMainDB = Join-Path $ProjectRoot "data\stock.db"
    $productionMinuteDB = Join-Path $ProjectRoot "data\minute.db"
    if ((Test-SamePath $MainDB $productionMainDB) -or (Test-SamePath $MinuteDB $productionMinuteDB)) {
        throw "Simulated readiness failure cannot use a production database"
    }
    if ((Test-SamePath $RuntimeRoot $DefaultRuntimeRoot) -or (Test-SamePath $RuntimeRoot $ProjectRoot)) {
        throw "Simulated readiness failure requires a dedicated runtime directory"
    }
    $runtimeDriveRoot = [System.IO.Path]::GetPathRoot($RuntimeRoot)
    if (Test-SamePath $RuntimeRoot $runtimeDriveRoot) { throw "Runtime root cannot be a drive root: $RuntimeRoot" }
    $isolationRoot = Split-Path -Parent $RuntimeRoot
    if (-not $isolationRoot -or (Test-SamePath $isolationRoot ([System.IO.Path]::GetPathRoot($isolationRoot)))) {
        throw "Simulated readiness failure requires runtime below a dedicated isolation root"
    }
    Assert-PathHasNoLinks $isolationRoot $MainDB "Simulated main database"
    Assert-PathHasNoLinks $isolationRoot $MinuteDB "Simulated minute database"
}

function Write-ReleasePointer {
    param($Pointer)
    $temporaryPointer = "$CurrentPointer.tmp"
    $Pointer | ConvertTo-Json | Set-Content -LiteralPath $temporaryPointer -Encoding UTF8
    Move-Item -LiteralPath $temporaryPointer -Destination $CurrentPointer -Force
}

function Get-ReleaseContext {
    if (-not (Test-Path -LiteralPath $ManifestSource)) { throw "Missing release manifest: $ManifestSource" }
    $manifest = Get-Content -LiteralPath $ManifestSource -Raw | ConvertFrom-Json
    $commit = (& git -C $ProjectRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $commit) { throw "Cannot resolve git commit" }
    $dirty = [bool]((& git -C $ProjectRoot status --porcelain).Count -gt 0)
    $releaseDir = Join-Path (Join-Path $ReleasesRoot $manifest.appVersion) $commit
    [pscustomobject]@{
        Manifest = $manifest
        Commit = $commit
        Dirty = $dirty
        ReleaseDir = $releaseDir
        Binary = Join-Path $releaseDir "go-stock-web.exe"
    }
}

function Assert-UniqueMainBranch {
    $current = (& git -C $ProjectRoot branch --show-current).Trim()
    if ($LASTEXITCODE -ne 0 -or $current -ne "main") {
        throw "Release candidates must be built from main (current: $current)"
    }
    $localBranches = @(& git -C $ProjectRoot for-each-ref --format="%(refname:short)" refs/heads)
    if ($LASTEXITCODE -ne 0) { throw "Cannot enumerate local branches" }
    $localBranches = @($localBranches | ForEach-Object { $_.Trim() } | Where-Object { $_ })
    if ($localBranches.Count -ne 1 -or $localBranches[0] -ne "main") {
        throw "Release workspace must contain only the local main branch (found: $($localBranches -join ', '))"
    }
    $remoteBranches = @(@(& git -C $ProjectRoot for-each-ref --format="%(refname)" refs/remotes/origin) |
        ForEach-Object { $_.Trim() } |
        Where-Object { $_ -and $_ -ne "refs/remotes/origin/HEAD" })
    if ($LASTEXITCODE -ne 0) { throw "Cannot enumerate origin branches" }
    if ($remoteBranches.Count -ne 1 -or $remoteBranches[0] -ne "refs/remotes/origin/main") {
        throw "Release workspace must contain only origin/main (found: $($remoteBranches -join ', '))"
    }
}

function Assert-CleanMainCommit {
    param($Context)
    Assert-UniqueMainBranch
    $currentCommit = (& git -C $ProjectRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $currentCommit -ne $Context.Commit) {
        throw "Release source commit changed during preflight"
    }
    $changes = @(& git -C $ProjectRoot status --porcelain)
    if ($LASTEXITCODE -ne 0) { throw "Cannot verify release worktree state" }
    if ($changes.Count -gt 0) {
        throw "Release worktree changed during preflight; commit or discard generated changes before building"
    }
}

function Invoke-GoQualityGate {
    Invoke-Checked "go" @("vet", "./...") "go vet failed"
    Invoke-Checked "go" @("mod", "tidy", "-diff") "go.mod/go.sum are not tidy"
    $changedGoFiles = @(& git -C $ProjectRoot diff --name-only --diff-filter=ACMR 1.5.0 HEAD -- "*.go")
    if ($LASTEXITCODE -ne 0) { throw "Cannot enumerate Go files changed since 1.5.0" }
    $unformatted = New-Object System.Collections.Generic.List[string]
    foreach ($relativePath in $changedGoFiles) {
        $fullPath = Join-Path $ProjectRoot $relativePath
        if (-not (Test-Path -LiteralPath $fullPath)) { continue }
        $result = @(& gofmt -l -- $fullPath)
        if ($LASTEXITCODE -ne 0) { throw "gofmt failed for $relativePath" }
        foreach ($item in $result) { if ($item) { $unformatted.Add($relativePath) } }
    }
    if ($unformatted.Count -gt 0) {
        throw "Go lint failed; gofmt required: $($unformatted -join ', ')"
    }
}

function Assert-FrozenReplay {
    $resolvedBundle = (Resolve-Path -LiteralPath $ReplayBundleFullPath).Path
    $resolvedReplayManifest = (Resolve-Path -LiteralPath $ReplayBundleManifestFullPath).Path
    Invoke-Checked "go" @(
        "run", "./cmd/go-stock-cli", "release", "verify-replay-bundle",
        "--bundle", $resolvedBundle,
        "--manifest", $resolvedReplayManifest
    ) "Frozen replay bundle verification failed"
}

function Assert-CandidateFrozenReplay {
    param([string]$Binary)
    $resolvedBundle = (Resolve-Path -LiteralPath $ReplayBundleFullPath).Path
    $resolvedReplayManifest = (Resolve-Path -LiteralPath $ReplayBundleManifestFullPath).Path
    Invoke-Checked $Binary @(
        "release", "verify-replay-bundle",
        "--bundle", $resolvedBundle,
        "--manifest", $resolvedReplayManifest
    ) "Candidate frozen replay bundle verification failed"
}

function Invoke-Preflight {
    param($Context)
    if ($Context.Dirty) { throw "Release candidates require a clean main commit" }
    Assert-UniqueMainBranch
    Push-Location $ProjectRoot
    try {
        Invoke-Checked "go" @("run", "./cmd/openapi-contract") "OpenAPI generated files or Go routes are inconsistent"
        Invoke-Checked "go" @("test", "./...") "Go tests failed"
        Invoke-GoQualityGate
        Assert-FrozenReplay
        Push-Location (Join-Path $ProjectRoot "frontend")
        try {
            Invoke-Checked "npm.cmd" @("run", "ci") "Frontend CI checks failed"
        } finally { Pop-Location }
        Assert-CleanMainCommit $Context
    } finally { Pop-Location }
}

function Build-Candidate {
    param($Context)
    Invoke-Preflight $Context
    $buildInfoPath = Join-Path $Context.ReleaseDir "build_info.json"
    if ((Test-Path -LiteralPath $Context.Binary) -or (Test-Path -LiteralPath $buildInfoPath)) {
        if (-not (Test-Path -LiteralPath $Context.Binary) -or -not (Test-Path -LiteralPath $buildInfoPath)) {
            throw "Incomplete candidate already exists for this commit: $($Context.ReleaseDir)"
        }
        $existingHash = Assert-CandidateMetadata $Context
        return $existingHash
    }
    New-Item -ItemType Directory -Force -Path $Context.ReleaseDir | Out-Null
    $buildTime = (Get-Date).ToUniversalTime().ToString("o")
    $dirtyValue = $Context.Dirty.ToString().ToLowerInvariant()
    $ldflags = "-X go-stock/internal/releaseinfo.Commit=$($Context.Commit) -X go-stock/internal/releaseinfo.BuildTime=$buildTime -X go-stock/internal/releaseinfo.Dirty=$dirtyValue"
    Push-Location $ProjectRoot
    try {
        Invoke-Checked "go" @("build", "-tags", "webonly", "-trimpath", "-ldflags", $ldflags, "-o", $Context.Binary, ".") "Windows Web build failed"
    } finally { Pop-Location }
    Copy-Item -LiteralPath $ManifestSource -Destination (Join-Path $Context.ReleaseDir "release_manifest.json") -Force
    Copy-Item -LiteralPath $ReplayBundleManifestFullPath -Destination (Join-Path $Context.ReleaseDir "replay_bundle_manifest.json") -Force
    $hash = (Get-FileHash -LiteralPath $Context.Binary -Algorithm SHA256).Hash.ToLowerInvariant()
    [pscustomobject]@{ artifactSHA256 = $hash; buildTime = $buildTime; commit = $Context.Commit; dirty = $Context.Dirty } |
        ConvertTo-Json | Set-Content -LiteralPath (Join-Path $Context.ReleaseDir "build_info.json") -Encoding UTF8
    return $hash
}

function Assert-CandidateMetadata {
    param($Context)
    $buildInfoPath = Join-Path $Context.ReleaseDir "build_info.json"
    if (-not (Test-Path -LiteralPath $Context.Binary) -or -not (Test-Path -LiteralPath $buildInfoPath)) {
        throw "Candidate artifact or build_info.json is missing"
    }
    $buildInfo = Get-Content -LiteralPath $buildInfoPath -Raw | ConvertFrom-Json
    $actualHash = (Get-FileHash -LiteralPath $Context.Binary -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($buildInfo.commit -ne $Context.Commit -or [bool]$buildInfo.dirty -or $buildInfo.artifactSHA256 -ne $actualHash) {
        throw "Candidate metadata does not match the clean main artifact"
    }
    $inspectOutput = @(& $Context.Binary "release" "inspect" 2>&1)
    $inspectExitCode = $LASTEXITCODE
    if ($inspectExitCode -ne 0) {
        throw "Candidate embedded metadata inspection failed (exit $inspectExitCode)"
    }
    try {
        $embeddedJSON = ($inspectOutput | ForEach-Object { [string]$_ }) -join [Environment]::NewLine
        $embedded = $embeddedJSON | ConvertFrom-Json
    } catch {
        throw "Candidate embedded metadata is not valid JSON: $($_.Exception.Message)"
    }
    if ($embedded.build.commit -ne $Context.Commit -or
        [bool]$embedded.build.dirty -or
        $embedded.build.artifactSHA256 -ne $actualHash -or
        $embedded.build.buildTime -ne $buildInfo.buildTime) {
        throw "Candidate embedded BuildInfo does not match its artifact and sidecar"
    }
    if ($embedded.manifest.appVersion -ne $Context.Manifest.appVersion -or
        $embedded.manifest.currentStrategyVersion -ne $Context.Manifest.currentStrategyVersion -or
        $embedded.manifest.strategyConfigHash -ne $Context.Manifest.strategyConfigHash -or
        [int]$embedded.manifest.mainSchemaVersion -ne [int]$Context.Manifest.mainSchemaVersion -or
        [int]$embedded.manifest.minuteSchemaVersion -ne [int]$Context.Manifest.minuteSchemaVersion -or
        $embedded.configHash -ne $Context.Manifest.strategyConfigHash) {
        throw "Candidate embedded release manifest does not match the source manifest"
    }
    $candidateManifestPath = Join-Path $Context.ReleaseDir "release_manifest.json"
    $candidateReplayManifestPath = Join-Path $Context.ReleaseDir "replay_bundle_manifest.json"
    if (-not (Test-Path -LiteralPath $candidateManifestPath) -or -not (Test-Path -LiteralPath $candidateReplayManifestPath)) {
        throw "Candidate release manifests are missing"
    }
    if ((Get-FileHash -LiteralPath $candidateManifestPath -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $ManifestSource -Algorithm SHA256).Hash) {
        throw "Candidate release manifest does not match the source manifest"
    }
    if ((Get-FileHash -LiteralPath $candidateReplayManifestPath -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $ReplayBundleManifestFullPath -Algorithm SHA256).Hash) {
        throw "Candidate replay manifest does not match the source manifest"
    }
    return $actualHash
}

function Stop-WebService {
    param([string]$ExpectedBinary = "")
    $port = Get-WebPort
    $initialIds = @(Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique)
    foreach ($processId in $initialIds) {
        if ($ExpectedBinary) {
            $listener = Get-CimInstance Win32_Process -Filter "ProcessId = $processId" -ErrorAction Stop
            if (-not $listener.ExecutablePath -or -not (Test-SamePath ([string]$listener.ExecutablePath) $ExpectedBinary)) {
                throw "Refusing to stop PID $processId on $WebAddr because its binary does not match $ExpectedBinary"
            }
        } elseif ($SimulateReadinessFailure) {
            throw "Refusing to stop an unowned listener during a simulated failure"
        }
    }
    try { Invoke-WebRequest -UseBasicParsing -Method Post -Uri "http://$WebAddr/api/shutdown" -TimeoutSec 3 | Out-Null } catch {}
    Start-Sleep -Milliseconds 600
    $ids = @(Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique)
    foreach ($processId in $ids) {
        if ($ExpectedBinary) {
            $listener = Get-CimInstance Win32_Process -Filter "ProcessId = $processId" -ErrorAction Stop
            if (-not $listener.ExecutablePath -or -not (Test-SamePath ([string]$listener.ExecutablePath) $ExpectedBinary)) {
                throw "Refusing to stop PID $processId on $WebAddr because its binary does not match $ExpectedBinary"
            }
        } elseif ($SimulateReadinessFailure) {
            throw "Refusing to force-stop an unowned listener during a simulated failure"
        }
        Stop-Process -Id $processId -Force -ErrorAction Stop
    }
    Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
}

function Start-Candidate {
    param([string]$Binary)
    $oldMainDB = $env:GO_STOCK_DB_PATH
    $oldMinuteDB = $env:GO_STOCK_MINUTE_DB_PATH
    $oldWebAddr = $env:GO_STOCK_WEB_ADDR
    $outLog = Join-Path $RuntimeRoot "web.out.log"
    $errLog = Join-Path $RuntimeRoot "web.err.log"
    try {
        $env:GO_STOCK_DB_PATH = (Resolve-Path -LiteralPath $MainDB).Path
        $env:GO_STOCK_MINUTE_DB_PATH = (Resolve-Path -LiteralPath $MinuteDB).Path
        $env:GO_STOCK_WEB_ADDR = $WebAddr
        $process = Start-Process -FilePath $Binary -ArgumentList "--web" -WorkingDirectory $ProjectRoot -WindowStyle Hidden -RedirectStandardOutput $outLog -RedirectStandardError $errLog -PassThru
    } finally {
        $env:GO_STOCK_DB_PATH = $oldMainDB
        $env:GO_STOCK_MINUTE_DB_PATH = $oldMinuteDB
        $env:GO_STOCK_WEB_ADDR = $oldWebAddr
    }
    $process.Id | Set-Content -LiteralPath $PidFile
    return $process
}

function Wait-WebHealth {
    param([int]$ProcessId)
    for ($attempt = 0; $attempt -lt $RollbackHealthAttempts; $attempt++) {
        if (-not (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) { throw "Web process exited before health check" }
        try {
            $response = Invoke-RestMethod -Uri "http://$WebAddr/healthz" -TimeoutSec 2
            if ($response.ok) { return }
        } catch {}
        Start-Sleep -Seconds 1
    }
    throw "Web process did not become healthy"
}

function Assert-SingleListener {
    param([int]$ExpectedProcessId = 0)
    $port = Get-WebPort
    $ids = @(Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique)
    if ($ids.Count -ne 1) { throw "Expected exactly one listener on $WebAddr, found $($ids.Count)" }
    if ($ExpectedProcessId -and [int]$ids[0] -ne $ExpectedProcessId) {
        throw "Listener on $WebAddr belongs to PID $($ids[0]), expected $ExpectedProcessId"
    }
}

function Get-PreviousReleasePointer {
    if (Test-Path -LiteralPath $CurrentPointer) {
        $pointer = Get-Content -LiteralPath $CurrentPointer -Raw | ConvertFrom-Json
        if (-not $pointer.binary -or -not $pointer.artifactSHA256 -or -not (Test-Path -LiteralPath $pointer.binary)) {
            throw "Current release pointer is invalid: $CurrentPointer"
        }
        $actualHash = (Get-FileHash -LiteralPath $pointer.binary -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne ([string]$pointer.artifactSHA256).ToLowerInvariant()) {
            throw "Current release pointer artifact hash does not match its binary"
        }
        return $pointer
    }
    $port = Get-WebPort
    $ids = @(Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique)
    if ($ids.Count -eq 0) { return $null }
    if ($ids.Count -ne 1) { throw "Cannot adopt release pointer with $($ids.Count) listeners on $WebAddr" }
    $process = Get-CimInstance Win32_Process -Filter "ProcessId = $($ids[0])" -ErrorAction Stop
    $binary = [string]$process.ExecutablePath
    if (-not $binary -or -not (Test-Path -LiteralPath $binary)) { throw "Cannot resolve the existing Web binary" }
    $version = "legacy"
    try { $version = [string](Invoke-RestMethod -Uri "http://$WebAddr/healthz" -TimeoutSec 2).version } catch {}
    return [pscustomobject]@{
        appVersion = $version
        commit = "unknown"
        binary = (Resolve-Path -LiteralPath $binary).Path
        artifactSHA256 = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

function Wait-ExactReadiness {
    param($Context, [string]$ArtifactHash, [int]$ProcessId)
    for ($attempt = 0; $attempt -lt $ReadinessAttempts; $attempt++) {
        if (-not (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) { throw "Candidate process exited before readiness" }
        try {
            $status = Invoke-RestMethod -Uri $ReadyURL -TimeoutSec 2
            if ($status.appVersion -eq $Context.Manifest.appVersion -and
                $status.commit -eq $Context.Commit -and
                $status.artifactSHA256 -eq $ArtifactHash -and
                [int]$status.mainSchemaVersion -eq [int]$Context.Manifest.mainSchemaVersion -and
                [int]$status.minuteSchemaVersion -eq [int]$Context.Manifest.minuteSchemaVersion -and
                $status.currentStrategyVersion -eq $Context.Manifest.currentStrategyVersion -and
                $status.strategyConfigHash -eq $Context.Manifest.strategyConfigHash -and
                $status.configHash -eq $Context.Manifest.strategyConfigHash -and
                -not [bool]$status.dirty -and
                $status.strategyMode -eq "paused" -and
                $status.readiness.ready) { return $status }
        } catch {}
        Start-Sleep -Seconds 1
    }
    throw "Candidate readiness did not match release manifest"
}

function Invoke-DatabaseCommand {
    param([string]$Binary, [string]$Path, [string]$MinutePath, [string[]]$Arguments)
    $oldMinute = $env:GO_STOCK_MINUTE_DB_PATH
    $env:GO_STOCK_MINUTE_DB_PATH = $MinutePath
    try { Invoke-Checked $Binary (@("--db-path", $Path) + $Arguments) "Database command failed: $($Arguments -join ' ')" }
    finally { $env:GO_STOCK_MINUTE_DB_PATH = $oldMinute }
}

function Restore-Backup {
    param([string]$BackupDir, [string]$ExpectedBinary = "")
    Stop-WebService -ExpectedBinary $ExpectedBinary
    Restore-DatabaseFile (Join-Path $BackupDir "stock.db") $MainDB
    Restore-DatabaseFile (Join-Path $BackupDir "minute.db") $MinuteDB
}

function Restore-DatabaseFile {
    param([string]$Source, [string]$Destination)
    if (-not (Test-Path -LiteralPath $Source)) { throw "Restore source is missing: $Source" }
    $restoreTemp = "$Destination.restore-$PID.tmp"
    $replaceBackup = "$Destination.replace-$PID.bak"
    try {
        Copy-Item -LiteralPath $Source -Destination $restoreTemp -Force
        foreach ($sidecar in @("$Destination-wal", "$Destination-shm", "$Destination-journal")) {
            Remove-Item -LiteralPath $sidecar -Force -ErrorAction SilentlyContinue
            if (Test-Path -LiteralPath $sidecar) { throw "Failed to remove SQLite sidecar before restore: $sidecar" }
        }
        if (Test-Path -LiteralPath $Destination) {
            [System.IO.File]::Replace($restoreTemp, $Destination, $replaceBackup, $true)
            Remove-Item -LiteralPath $replaceBackup -Force
        } else {
            Move-Item -LiteralPath $restoreTemp -Destination $Destination
        }
    } finally {
        Remove-Item -LiteralPath $restoreTemp -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $replaceBackup -Force -ErrorAction SilentlyContinue
    }
}

function Deploy-Candidate {
    param($Context)
    if ($Context.Dirty) { throw "Deployments require a clean main commit" }
    Assert-UniqueMainBranch
    if (-not (Test-Path -LiteralPath $Context.Binary)) {
        [void](Build-Candidate $Context)
    } else {
        Assert-CandidateFrozenReplay $Context.Binary
    }
    $artifactHash = Assert-CandidateMetadata $Context
    $timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $rehearsalDir = Join-Path $BackupsRoot "$timestamp-rehearsal"
    $rehearsalMainDB = Join-Path $rehearsalDir "stock.db"
    $rehearsalMinuteDB = Join-Path $rehearsalDir "minute.db"
    Invoke-DatabaseCommand $Context.Binary $MainDB $MinuteDB @("db", "backup", "--output", $rehearsalDir)
    Invoke-DatabaseCommand $Context.Binary $rehearsalMainDB $rehearsalMinuteDB @("db", "migrate")
    Invoke-DatabaseCommand $Context.Binary $rehearsalMainDB $rehearsalMinuteDB @("db", "verify")

    $previous = Get-PreviousReleasePointer
    if ($previous -and -not (Test-Path -LiteralPath $CurrentPointer)) {
        Write-ReleasePointer $previous
    }
    $backupDir = Join-Path $BackupsRoot $timestamp
    $oldServiceStopped = $false
    $liveBackupComplete = $false
    $liveMutationStarted = $false
    try {
        Stop-WebService -ExpectedBinary $(if ($previous) { $previous.binary } else { "" })
        $oldServiceStopped = $true
        Invoke-DatabaseCommand $Context.Binary $MainDB $MinuteDB @("db", "backup", "--output", $backupDir)
        $liveBackupComplete = $true
        $liveMutationStarted = $true
        Invoke-DatabaseCommand $Context.Binary $MainDB $MinuteDB @("db", "migrate")
        Invoke-DatabaseCommand $Context.Binary $MainDB $MinuteDB @("db", "verify")
        $process = Start-Candidate $Context.Binary
        $expectedHash = if ($SimulateReadinessFailure) { "0" * 64 } else { $artifactHash }
        $ready = Wait-ExactReadiness $Context $expectedHash $process.Id
        Assert-SingleListener $process.Id
        $pointer = [pscustomobject]@{ appVersion = $Context.Manifest.appVersion; commit = $Context.Commit; binary = $Context.Binary; artifactSHA256 = $artifactHash }
        Write-ReleasePointer $pointer
        New-Item -ItemType Directory -Force -Path $DeploymentsRoot | Out-Null
        [pscustomobject]@{ deployedAt = (Get-Date).ToUniversalTime().ToString("o"); previous = $previous; current = $pointer; backupDir = $backupDir; readiness = $ready } |
            ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $DeploymentsRoot "$timestamp.json") -Encoding UTF8
    } catch {
        $deploymentError = $_.Exception.Message
        $rollbackReady = $false
        $rollbackError = $null
        try {
            if ($oldServiceStopped) {
                Stop-WebService -ExpectedBinary $Context.Binary
            }
            if ($liveBackupComplete) {
                Restore-DatabaseFile (Join-Path $backupDir "stock.db") $MainDB
                Restore-DatabaseFile (Join-Path $backupDir "minute.db") $MinuteDB
                Invoke-DatabaseCommand $Context.Binary $MainDB $MinuteDB @("db", "verify", "--quick-only")
            } elseif ($liveMutationStarted) {
                throw "Live database mutation started without a complete rollback backup: $backupDir"
            }
            if (-not $previous) { throw "No previous release is available to restart" }
            Write-ReleasePointer $previous
            if ($oldServiceStopped) {
                $previousProcess = Start-Candidate $previous.binary
                Wait-WebHealth $previousProcess.Id
                Assert-SingleListener $previousProcess.Id
            } else {
                throw "Old service stop did not complete; refusing to start a duplicate previous process"
            }
            $rollbackReady = $true
        } catch {
            $rollbackError = $_.Exception.Message
        }
        $receiptError = $null
        try {
            New-Item -ItemType Directory -Force -Path $DeploymentsRoot | Out-Null
            [pscustomobject]@{ failedAt = (Get-Date).ToUniversalTime().ToString("o"); previous = $previous; candidate = $Context.Binary; backupDir = $backupDir; error = $deploymentError; rollbackReady = $rollbackReady; rollbackError = $rollbackError } |
                ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $DeploymentsRoot "$timestamp.failed.json") -Encoding UTF8
        } catch {
            $receiptError = $_.Exception.Message
        }
        $combinedError = "Deployment failed: $deploymentError"
        if ($rollbackError) { $combinedError += "`nRollback failed: $rollbackError" }
        if ($receiptError) { $combinedError += "`nFailed receipt could not be written: $receiptError" }
        throw $combinedError
    }
    Assert-SafeDescendant $RuntimeRoot $ReleasesRoot "Releases root"
    $releaseRootPrefix = [System.IO.Path]::GetFullPath($ReleasesRoot).TrimEnd('\') + '\'
    $releaseDirectories = @(Get-ChildItem -LiteralPath $ReleasesRoot -Filter go-stock-web.exe -Recurse -File |
        Sort-Object LastWriteTime -Descending |
        ForEach-Object { $_.Directory.FullName } | Select-Object -Unique)
    $keepDirectories = @{}
    foreach ($protectedDirectory in @($Context.ReleaseDir, $(if ($previous) { Split-Path -Parent $previous.binary } else { $null }))) {
        if ($protectedDirectory) {
            $resolvedProtected = [System.IO.Path]::GetFullPath($protectedDirectory)
            if ($resolvedProtected.StartsWith($releaseRootPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
                $keepDirectories[$resolvedProtected.ToLowerInvariant()] = $true
            }
        }
    }
    foreach ($directory in $releaseDirectories) {
        if ($keepDirectories.Count -ge 3) { break }
        $keepDirectories[[System.IO.Path]::GetFullPath($directory).ToLowerInvariant()] = $true
    }
    foreach ($directory in $releaseDirectories) {
        $resolved = [System.IO.Path]::GetFullPath($directory)
        if (-not $resolved.StartsWith($releaseRootPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to prune release outside $ReleasesRoot`: $resolved"
        }
        if ($keepDirectories.ContainsKey($resolved.ToLowerInvariant())) { continue }
        $releaseItem = Get-Item -LiteralPath $resolved -Force
        if (([int]$releaseItem.Attributes -band [int][System.IO.FileAttributes]::ReparsePoint) -ne 0 -or $releaseItem.LinkType) {
            throw "Refusing to prune release through a link or reparse point: $resolved"
        }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}

function Invoke-Rollback {
    if (-not $RollbackReceipt) { throw "-RollbackReceipt is required" }
    $receipt = Get-Content -LiteralPath $RollbackReceipt -Raw | ConvertFrom-Json
    Restore-Backup $receipt.backupDir $receipt.current.binary
    Invoke-DatabaseCommand $receipt.current.binary $MainDB $MinuteDB @("db", "verify", "--quick-only")
    if (-not $receipt.previous) { throw "Deployment receipt has no previous release pointer" }
    Write-ReleasePointer $receipt.previous
    $process = Start-Candidate $receipt.previous.binary
    Wait-WebHealth $process.Id
    Assert-SingleListener $process.Id
}

Assert-SimulationIsolation
Assert-SafeDescendant $RuntimeRoot $ReleasesRoot "Releases root"
Assert-SafeDescendant $RuntimeRoot $BackupsRoot "Backups root"
Assert-SafeDescendant $RuntimeRoot $DeploymentsRoot "Deployments root"
New-Item -ItemType Directory -Force -Path $RuntimeRoot, $ReleasesRoot, $BackupsRoot, $DeploymentsRoot | Out-Null
$context = Get-ReleaseContext
switch ($Command) {
    "build" { $hash = Build-Candidate $context; Write-Output "candidate=$($context.Binary) sha256=$hash" }
    "deploy" { Deploy-Candidate $context; Write-Output "deployed=$($context.Binary)" }
    "rollback" { Invoke-Rollback; Write-Output "rollback complete" }
}
