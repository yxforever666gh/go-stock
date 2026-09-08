param(
    [ValidateSet("build", "deploy", "activate", "rollback", "publish")]
    [string]$Command = "build",
    [string]$MainDB = "data\stock.db",
    [string]$MinuteDB = "data\minute.db",
    [string]$WebAddr = "127.0.0.1:34115",
    [string]$RollbackReceipt = "",
    [string]$RuntimeRootOverride = "",
    [string]$NotesFile = '',
    [string]$Resume = ''
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
if ($PSVersionTable.PSVersion -lt [version]'7.2') { throw 'Release commands require PowerShell 7.2 or later (pwsh)' }

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $ScriptDir))
$RuntimeRoot = if ($RuntimeRootOverride) {
    if ([System.IO.Path]::IsPathRooted($RuntimeRootOverride)) { $RuntimeRootOverride } else { Join-Path $ProjectRoot $RuntimeRootOverride }
} else { Join-Path $ProjectRoot "runtime" }
$RuntimeRoot = [System.IO.Path]::GetFullPath($RuntimeRoot)
$ReleaseRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "releases"))
$DeploymentsRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "deployments"))
$ArchivesRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "archives"))
$RestoreRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "restore-staging"))
$FailedMigrationsRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "failed-migrations"))
$MainDB = [System.IO.Path]::GetFullPath($(if ([System.IO.Path]::IsPathRooted($MainDB)) { $MainDB } else { Join-Path $ProjectRoot $MainDB }))
$MinuteDB = [System.IO.Path]::GetFullPath($(if ([System.IO.Path]::IsPathRooted($MinuteDB)) { $MinuteDB } else { Join-Path $ProjectRoot $MinuteDB }))
$ManifestPath = Join-Path $ProjectRoot "internal\releaseinfo\release_manifest.json"
$CurrentPointer = Join-Path $RuntimeRoot "current.json"
$PidFile = Join-Path $RuntimeRoot "go-stock-web.pid"
$script:DeploymentReceiptPath = ''
. (Join-Path $ScriptDir 'release-state.ps1')

function Enter-ReleaseLock {
    New-Item -ItemType Directory -Force -Path $DeploymentsRoot | Out-Null
    try { return [IO.File]::Open((Join-Path $DeploymentsRoot '.publish.lock'), [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None) }
    catch { throw 'Another release operation holds this project lock' }
}

function Assert-ChildPath {
    param([string]$Path, [string]$Root)
    $resolvedPath = [System.IO.Path]::GetFullPath($Path).TrimEnd('\')
    $resolvedRoot = [System.IO.Path]::GetFullPath($Root).TrimEnd('\')
    if (-not $resolvedPath.StartsWith($resolvedRoot + '\', [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing path outside '$resolvedRoot': $resolvedPath"
    }
    return $resolvedPath
}

function Assert-DatabasePaths {
    if ($MainDB -eq $MinuteDB) { throw "Main and minute database paths must differ" }
    foreach ($path in @($MainDB, $MinuteDB)) {
        if ([string]::IsNullOrWhiteSpace([System.IO.Path]::GetFileName($path))) { throw "Invalid database path: $path" }
        if (-not (Test-Path -LiteralPath (Split-Path -Parent $path) -PathType Container)) {
            throw "Database parent directory is unavailable: $path"
        }
    }
}

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments, [string]$Failure)
    Push-Location $ProjectRoot
    try {
    & $Program @Arguments
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) { throw "$Failure (exit $exitCode)" }
    } finally { Pop-Location }
}

function Get-SHA256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "Missing file: $Path" }
    $stream = [System.IO.File]::OpenRead($Path)
    try {
        $sha256 = [System.Security.Cryptography.SHA256]::Create()
        try { return (($sha256.ComputeHash($stream) | ForEach-Object { $_.ToString("x2") }) -join "") }
        finally { $sha256.Dispose() }
    } finally { $stream.Dispose() }
}

function Get-Context {
    if (-not (Test-Path -LiteralPath $ManifestPath)) { throw "Missing release manifest: $ManifestPath" }
    $manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
    $commit = (& git -C $ProjectRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $commit) { throw "Cannot resolve git commit" }
    $releaseDir = Join-Path (Join-Path $ReleaseRoot ([string]$manifest.appVersion)) $commit
    [pscustomobject]@{
        Manifest = $manifest
        Commit = $commit
        ReleaseDir = $releaseDir
        Binary = Join-Path $releaseDir "go-stock-web.exe"
        ZoneInfo = Join-Path $releaseDir "zoneinfo.zip"
    }
}

function Assert-VersionTagMatchesCommit {
    param($Context)
    $version = [string]$Context.Manifest.appVersion
    $tagRef = "refs/tags/$version"
    $tagCommit = [string](& git -C $ProjectRoot rev-list -n 1 $tagRef)
    $tagExitCode = $LASTEXITCODE
    $tagCommit = $tagCommit.Trim()
    if ($tagExitCode -ne 0 -or -not $tagCommit) {
        throw "Release tag $version is required before promotion or deploy"
    }
    if (-not $tagCommit.Equals([string]$Context.Commit, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Release tag $version points to $tagCommit, expected $($Context.Commit)"
    }
    if (((& git -C $ProjectRoot cat-file -t $tagRef) -join '') -ne 'tag') { throw "Release tag $version must be annotated" }
}

function Get-ZoneInfoSource {
    Push-Location $ProjectRoot
    try {
    $goRoot = (& go env GOROOT).Trim()
    if ($LASTEXITCODE -ne 0) { throw "Cannot resolve GOROOT" }
    $source = Join-Path $goRoot "lib\time\zoneinfo.zip"
    if (-not (Test-Path -LiteralPath $source)) { throw "Go zoneinfo.zip is unavailable" }
    return $source
    } finally { Pop-Location }
}

function Write-JSONAtomic {
    param([string]$Path, $Value)
    $parent = Split-Path -Parent $Path
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    $temporary = "$Path.tmp"
    $Value | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $temporary -Encoding UTF8
    Move-Item -LiteralPath $temporary -Destination $Path -Force
}

function Read-ReleasePointer {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "Missing release pointer: $Path" }
    $pointer = Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
    foreach ($field in @("appVersion", "mainSchemaVersion", "minuteSchemaVersion", "commit", "binary", "artifactSHA256", "zoneInfo", "zoneInfoSHA256")) {
        if ([string]::IsNullOrWhiteSpace([string]$pointer.$field)) { throw "Release pointer is missing ${field}: $Path" }
    }
    $pointer.binary = Assert-ChildPath ([string]$pointer.binary) $ReleaseRoot
    $pointer.zoneInfo = Assert-ChildPath ([string]$pointer.zoneInfo) $ReleaseRoot
    if ((Get-SHA256 $pointer.binary) -ne ([string]$pointer.artifactSHA256).ToLowerInvariant()) { throw "Release binary hash mismatch" }
    if ((Get-SHA256 $pointer.zoneInfo) -ne ([string]$pointer.zoneInfoSHA256).ToLowerInvariant()) { throw "Release zoneinfo hash mismatch" }
    return $pointer
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
    Assert-VersionTagMatchesCommit $context
    $dirty = (& git -C $ProjectRoot status --porcelain)
    if ($LASTEXITCODE -ne 0) { throw "Cannot inspect git worktree" }
    if ($dirty) { throw "Release builds require a clean git worktree" }
    if (Test-Path -LiteralPath $context.ReleaseDir) { return Read-BuildArtifact $context }
    $recordPath = Join-Path $DeploymentsRoot "build-$($context.Commit).json"
    if (-not (Test-Path -LiteralPath $recordPath)) {
        Write-ReleaseState $recordPath @{formatVersion=1; projectRoot=$ProjectRoot; commit=$context.Commit; steps=@{}; verificationIdentity=''; frontendHash=''}
    }
    Invoke-ReleaseValidation $recordPath
    Invoke-ReleaseCandidateBuild $context $recordPath
    return Read-BuildArtifact $context
}

function Invoke-ReleaseValidation {
    param([string]$RecordPath)
    $record = Read-ReleaseState $RecordPath
    $inputs = Get-ReleaseInputs $ProjectRoot
    if ($record.verificationIdentity -eq $inputs.identity -and $record.frontendHash -and $record.frontendHash -eq (Get-ReleaseTreeHash (Join-Path $ProjectRoot 'frontend/dist'))) {
        Write-Host 'REUSE complete verification'
        return
    }
    $oldHTTP, $oldHTTPS = $env:HTTP_PROXY, $env:HTTPS_PROXY
    try {
        $env:HTTP_PROXY, $env:HTTPS_PROXY = 'http://127.0.0.1:7890', 'http://127.0.0.1:7890'
        Invoke-Checked 'pwsh' @('-NoProfile','-File',(Join-Path $ScriptDir 'verify.ps1'),'-Tier','release','-SkipGoBuild','-ReleaseRecord',$RecordPath) 'Release verification failed'
    } finally { $env:HTTP_PROXY, $env:HTTPS_PROXY = $oldHTTP, $oldHTTPS }
}

function Get-ArtifactInspection {
    param([string]$Binary)
    $output = @(& $Binary release inspect)
    if ($LASTEXITCODE -ne 0) { throw 'Artifact release inspect failed' }
    return (($output -join "`n") | ConvertFrom-Json)
}

function Assert-ArtifactIdentity {
    param($Pointer)
    $inspect = Get-ArtifactInspection $Pointer.binary
    if ($inspect.manifest.appVersion -ne $Pointer.appVersion -or $inspect.build.commit -ne $Pointer.commit -or
        [int]$inspect.manifest.mainSchemaVersion -ne [int]$Pointer.mainSchemaVersion -or
        [int]$inspect.manifest.minuteSchemaVersion -ne [int]$Pointer.minuteSchemaVersion -or
        ([string]$inspect.build.artifactSHA256).ToLowerInvariant() -ne ([string]$Pointer.artifactSHA256).ToLowerInvariant() -or [bool]$inspect.build.dirty) {
        throw 'Artifact embedded identity does not match build.json'
    }
}

function Read-BuildArtifact {
    param($Context)
    $pointer = Read-ReleasePointer (Join-Path $Context.ReleaseDir 'build.json')
    if ($pointer.appVersion -ne $Context.Manifest.appVersion -or $pointer.commit -ne $Context.Commit -or
        [int]$pointer.mainSchemaVersion -ne [int]$Context.Manifest.mainSchemaVersion -or
        [int]$pointer.minuteSchemaVersion -ne [int]$Context.Manifest.minuteSchemaVersion -or
        $pointer.binary -ne $Context.Binary -or $pointer.zoneInfo -ne $Context.ZoneInfo) { throw 'Build metadata does not match the requested release' }
    Assert-ArtifactIdentity $pointer
    return $pointer
}

function Invoke-CandidateCompiler {
    param($Context, [string]$Binary)
    $buildTime = [DateTime]::UtcNow.ToString("o")
    $ldflags = "-s -w -X go-stock/internal/releaseinfo.Commit=$($Context.Commit) -X go-stock/internal/releaseinfo.BuildTime=$buildTime -X go-stock/internal/releaseinfo.Dirty=false"
    Invoke-Checked 'go' @('build','-trimpath','-ldflags',$ldflags,'-o',$Binary,'.') 'Candidate build failed'
    $signature = Get-AuthenticodeSignature -LiteralPath $Binary
    if ([string]$signature.Status -notin @('Valid','NotSigned')) { throw "Candidate has invalid signature: $($signature.Status)" }
}

function Invoke-ReleaseCandidateBuild {
    param($Context, [string]$RecordPath)
    $record = Read-ReleaseState $RecordPath
    $inputs = Get-ReleaseInputs $ProjectRoot
    if ($record.commit -ne $Context.Commit -or $record.verificationIdentity -ne $inputs.identity -or
        -not $record.frontendHash -or $record.frontendHash -ne (Get-ReleaseTreeHash (Join-Path $ProjectRoot 'frontend/dist'))) { throw 'Candidate inputs have not passed verification' }
    if (@(& git -C $ProjectRoot status --porcelain).Count) { throw 'Candidate build requires a clean checkout' }
    if (Test-Path -LiteralPath $Context.ReleaseDir) {
        $existing = Read-BuildArtifact $Context
        if ($existing.PSObject.Properties.Name -contains 'verificationIdentity' -and
            ($existing.verificationIdentity -ne $inputs.identity -or $existing.PSObject.Properties.Name -notcontains 'frontendHash' -or $existing.frontendHash -ne $record.frontendHash)) {
            throw 'Immutable artifact has different build inputs; restore its toolchain or prepare a new commit/version'
        }
        Write-Host 'REUSE immutable candidate artifact'
        return
    }
    $stagingRoot = Join-Path $ReleaseRoot '.staging'
    $staging = Assert-ChildPath (Join-Path $stagingRoot ([Guid]::NewGuid().ToString('N'))) $ReleaseRoot
    New-Item -ItemType Directory -Force -Path $staging | Out-Null
    try {
        $candidate = [pscustomobject]@{Manifest=$Context.Manifest; Commit=$Context.Commit; Binary=(Join-Path $staging 'go-stock-web.exe'); ZoneInfo=(Join-Path $staging 'zoneinfo.zip')}
        Invoke-CandidateCompiler $Context $candidate.Binary
        Copy-Item -LiteralPath (Get-ZoneInfoSource) -Destination $candidate.ZoneInfo
        $pointer = New-Pointer $candidate
        Assert-ArtifactIdentity $pointer
        $pointer.binary, $pointer.zoneInfo = $Context.Binary, $Context.ZoneInfo
        $pointer | Add-Member -NotePropertyName verificationIdentity -NotePropertyValue $inputs.identity
        $pointer | Add-Member -NotePropertyName frontendHash -NotePropertyValue $record.frontendHash
        Write-JSONAtomic (Join-Path $staging 'build.json') $pointer
        if ((& git -C $ProjectRoot rev-parse HEAD).Trim() -ne $Context.Commit -or @(& git -C $ProjectRoot status --porcelain).Count) { throw 'Checkout changed during candidate build' }
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Context.ReleaseDir) | Out-Null
        # Same-volume directory rename fails rather than nesting or overwriting.
        [IO.Directory]::Move($staging, $Context.ReleaseDir)
    } finally {
        if (Test-Path -LiteralPath $staging) { Remove-Item -LiteralPath (Assert-ChildPath $staging $stagingRoot) -Recurse -Force }
    }
}

function Get-ReleaseListener {
    $port = ([Uri]("http://"+$WebAddr)).Port
    $ids = @(Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique)
    if ($ids.Count -eq 0) { return $null }
    if ($ids.Count -ne 1) { throw 'Multiple processes own the release port' }
    return Get-CimInstance Win32_Process -Filter "ProcessId = $($ids[0])" -ErrorAction Stop
}

function Assert-ReleaseProcess {
    param($Pointer, $Process, [int]$ExpectedID = 0)
    if (-not $Process -or -not $Process.ExecutablePath -or
        -not ([IO.Path]::GetFullPath($Process.ExecutablePath)).Equals([IO.Path]::GetFullPath($Pointer.binary), [StringComparison]::OrdinalIgnoreCase) -or
        ($ExpectedID -and [int]$Process.ProcessId -ne $ExpectedID)) { throw 'Listener process does not match the exact release artifact/PID' }
}

function Get-ExactReleaseStatus {
    param($Pointer, [int]$ExpectedID = 0)
    if (-not $ExpectedID -and (Test-Path -LiteralPath $PidFile)) { $ExpectedID = [int](Get-Content -LiteralPath $PidFile -Raw) }
    Assert-ReleaseProcess $Pointer (Get-ReleaseListener) $ExpectedID
    $status = Invoke-RestMethod -Uri "http://$WebAddr/readyz" -TimeoutSec 2
    if ($status.appVersion -ne $Pointer.appVersion -or $status.commit -ne $Pointer.commit -or
        ([string]$status.artifactSHA256).ToLowerInvariant() -ne ([string]$Pointer.artifactSHA256).ToLowerInvariant() -or $status.dirty -or
        [int]$status.mainSchemaVersion -ne [int]$Pointer.mainSchemaVersion -or [int]$status.minuteSchemaVersion -ne [int]$Pointer.minuteSchemaVersion) { throw 'Runtime identity differs from release artifact' }
    foreach ($flag in @('migrations','database','services','scheduler','ready')) { if (-not $status.readiness.$flag) { throw "Readiness $flag is false" } }
    return $status
}

function Stop-Current {
    $pointer = Read-ReleasePointer $CurrentPointer
    $process = Get-ReleaseListener
    $fileID = if (Test-Path -LiteralPath $PidFile) { [int](Get-Content -LiteralPath $PidFile -Raw) } else { 0 }
    if (-not $process -and $fileID) { $process = Get-CimInstance Win32_Process -Filter "ProcessId = $fileID" -ErrorAction SilentlyContinue }
    if ($process) {
        Assert-ReleaseProcess $pointer $process $fileID
        $processID = [int]$process.ProcessId
        Stop-Process -Id $processID -Force
        try { Wait-Process -Id $processID -Timeout 15 -ErrorAction SilentlyContinue } catch {}
        $deadline = [DateTime]::UtcNow.AddSeconds(10)
        while ($listener = Get-ReleaseListener) {
            Assert-ReleaseProcess $pointer $listener $processID
            if ([DateTime]::UtcNow -ge $deadline) { throw 'Release port did not close' }
            Start-Sleep -Milliseconds 100
        }
        Write-Host "Stopped $($pointer.binary) (PID $processID)"
    }
    Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
}

function Start-Pointer {
    param($Pointer)
    $logDir = Join-Path $RuntimeRoot "logs"
    New-Item -ItemType Directory -Force -Path $logDir | Out-Null
    $previous = @{ Web = $env:GO_STOCK_WEB_ADDR; DB = $env:GO_STOCK_DB_PATH; Minute = $env:GO_STOCK_MINUTE_DB_PATH; Zone = $env:ZONEINFO }
    $env:GO_STOCK_WEB_ADDR = $WebAddr
    # Explicit release paths used to discard the application's default WAL
    # query parameters, leaving the live service in DELETE journal mode.
    $mainSeparator = if ($MainDB.Contains("?")) { "&" } else { "?" }
    $minuteSeparator = if ($MinuteDB.Contains("?")) { "&" } else { "?" }
    $env:GO_STOCK_DB_PATH = $MainDB + $mainSeparator + "_pragma=cache_size(-524288)&_pragma=journal_mode(WAL)"
    $env:GO_STOCK_MINUTE_DB_PATH = $MinuteDB + $minuteSeparator + "_pragma=cache_size(-524288)&_pragma=journal_mode(WAL)"
    $env:ZONEINFO = $Pointer.zoneInfo
    try {
        $process = Start-Process -FilePath $Pointer.binary -WorkingDirectory $ProjectRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $logDir "web.out") -RedirectStandardError (Join-Path $logDir "web.err") -PassThru
    } finally {
        $env:GO_STOCK_WEB_ADDR = $previous.Web; $env:GO_STOCK_DB_PATH = $previous.DB
        $env:GO_STOCK_MINUTE_DB_PATH = $previous.Minute; $env:ZONEINFO = $previous.Zone
    }
    $process.Id | Set-Content -LiteralPath $PidFile
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        if (-not (Get-Process -Id $process.Id -ErrorAction SilentlyContinue)) { throw "Released process exited during startup" }
        try {
            [void](Get-ExactReleaseStatus $Pointer $process.Id)
            return
        } catch {}
        Start-Sleep -Seconds 1
    }
    throw "Release readiness timed out"
}

function Invoke-DatabaseJSON {
    param([string]$Binary, [string[]]$Arguments)
    $previousMinute = $env:GO_STOCK_MINUTE_DB_PATH
    $env:GO_STOCK_MINUTE_DB_PATH = $MinuteDB
    try {
        $output = @(& $Binary --json --db-path $MainDB @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
    } finally { $env:GO_STOCK_MINUTE_DB_PATH = $previousMinute }
    if ($exitCode -ne 0) { throw "Database command failed: $($output -join ' ')" }
    try { return (($output -join "`n") | ConvertFrom-Json) }
    catch { throw "Database command returned invalid JSON: $($output -join ' ')" }
}

function New-RollbackReceipt {
    param($Pointer, [string]$ArchivePath = "", [string]$ArchiveSHA256 = "")
    $receipt = [ordered]@{
        appVersion = [string]$Pointer.appVersion
        mainSchemaVersion = [int]$Pointer.mainSchemaVersion
        minuteSchemaVersion = [int]$Pointer.minuteSchemaVersion
        commit = [string]$Pointer.commit
        binary = [string]$Pointer.binary
        artifactSHA256 = [string]$Pointer.artifactSHA256
        zoneInfo = [string]$Pointer.zoneInfo
        zoneInfoSHA256 = [string]$Pointer.zoneInfoSHA256
        deployedAt = [string]$Pointer.deployedAt
    }
    if ($ArchivePath) {
        $receipt.databaseArchive = [System.IO.Path]::GetFullPath($ArchivePath)
        $receipt.databaseArchiveSHA256 = $ArchiveSHA256.ToLowerInvariant()
        $receipt.archivedMainDB = $MainDB
        $receipt.archivedMinuteDB = $MinuteDB
    }
    return $receipt
}

function Get-SchemaTransition {
    param($PreviousPointer, $NewPointer)
    if ($null -eq $PreviousPointer -or $null -eq $NewPointer) {
        throw "Schema transition requires both previous and new release pointers"
    }
    $mainDelta = [int]$NewPointer.mainSchemaVersion - [int]$PreviousPointer.mainSchemaVersion
    $minuteDelta = [int]$NewPointer.minuteSchemaVersion - [int]$PreviousPointer.minuteSchemaVersion
    if ($mainDelta -lt 0 -or $minuteDelta -lt 0) {
        throw "Use rollback with an archive receipt to downgrade either database schema"
    }
    if ($mainDelta -gt 1 -or $minuteDelta -gt 1) {
        throw "Maintenance deployment supports at most a one-version upgrade for each database"
    }
    return [pscustomobject]@{
        RequiresMigration = ($mainDelta -gt 0 -or $minuteDelta -gt 0)
        MainChanged = ($mainDelta -gt 0)
        MinuteChanged = ($minuteDelta -gt 0)
        MainDelta = $mainDelta
        MinuteDelta = $minuteDelta
    }
}

function New-DatabaseArchive {
    param($PreviousPointer, $NewPointer)
    $transition = Get-SchemaTransition $PreviousPointer $NewPointer
    if (-not $transition.RequiresMigration) {
        throw "Database archive is only created for a schema upgrade"
    }
    New-Item -ItemType Directory -Force -Path $ArchivesRoot | Out-Null
    $name = "pre-$($NewPointer.appVersion)-main$($NewPointer.mainSchemaVersion)-minute$($NewPointer.minuteSchemaVersion)-$([DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss')).zip"
    $path = Assert-ChildPath (Join-Path $ArchivesRoot $name) $ArchivesRoot
    $result = Invoke-DatabaseJSON $NewPointer.binary @("db", "archive", "--output", $path, "--source-app-version", [string]$PreviousPointer.appVersion, "--source-commit", [string]$PreviousPointer.commit)
    if ($null -eq $result.archive) { throw "Archive command did not return archive metadata" }
    $reportedPath = Assert-ChildPath ([string]$result.archive.path) $ArchivesRoot
    if ($reportedPath -ne $path) { throw "Archive command returned an unexpected path" }
    $actualHash = Get-SHA256 $path
    if ($actualHash -ne ([string]$result.archive.sha256).ToLowerInvariant()) { throw "Permanent archive hash mismatch" }
    Write-Host "Verified permanent database archive: $path"
    return [pscustomobject]@{ Path = $path; SHA256 = $actualHash }
}

function Expand-VerifiedDatabaseArchive {
    param($Receipt)
    if ([string]::IsNullOrWhiteSpace([string]$Receipt.databaseArchive) -or [string]::IsNullOrWhiteSpace([string]$Receipt.databaseArchiveSHA256)) {
        throw "Rollback receipt does not contain a database archive"
    }
    if ([string]$Receipt.archivedMainDB -ne $MainDB -or [string]$Receipt.archivedMinuteDB -ne $MinuteDB) {
        throw "Rollback receipt database paths do not match this deployment"
    }
    $archive = Assert-ChildPath ([string]$Receipt.databaseArchive) $ArchivesRoot
    if ((Get-SHA256 $archive) -ne ([string]$Receipt.databaseArchiveSHA256).ToLowerInvariant()) { throw "Rollback database archive hash mismatch" }
    New-Item -ItemType Directory -Force -Path $RestoreRoot | Out-Null
    $staging = Assert-ChildPath (Join-Path $RestoreRoot ([Guid]::NewGuid().ToString("N"))) $RestoreRoot
    New-Item -ItemType Directory -Path $staging | Out-Null
    $verified = $false
    try {
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $zip = [System.IO.Compression.ZipFile]::OpenRead($archive)
        try {
            $entries = @{}
            foreach ($entry in $zip.Entries) {
                if ($entry.FullName -notin @("stock.db", "minute.db", "manifest.json") -or $entries.ContainsKey($entry.FullName)) {
                    throw "Unexpected or duplicate archive entry: $($entry.FullName)"
                }
                $entries[$entry.FullName] = $entry
            }
            foreach ($name in @("stock.db", "minute.db", "manifest.json")) {
                if (-not $entries.ContainsKey($name)) { throw "Archive is missing $name" }
                [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entries[$name], (Join-Path $staging $name), $false)
            }
        } finally { $zip.Dispose() }
        $manifest = Get-Content -LiteralPath (Join-Path $staging "manifest.json") -Raw | ConvertFrom-Json
        if ([int]$manifest.formatVersion -ne 1 -or [int]$manifest.mainSchemaVersion -ne [int]$Receipt.mainSchemaVersion -or [int]$manifest.minuteSchemaVersion -ne [int]$Receipt.minuteSchemaVersion) {
            throw "Archive manifest schema does not match the rollback release"
        }
        if ([string]$manifest.sourceAppVersion -ne [string]$Receipt.appVersion -or [string]$manifest.sourceCommit -ne [string]$Receipt.commit) {
            throw "Archive manifest release identity does not match the rollback release"
        }
        $manifestFiles = @($manifest.files)
        if ($manifestFiles.Count -ne 2 -or (($manifestFiles.name | Sort-Object) -join ',') -ne 'minute.db,stock.db') {
            throw "Archive manifest must contain exactly stock.db and minute.db"
        }
        if (@($manifest.legacyTableRows.PSObject.Properties).Count -ne 17) {
            throw "Archive manifest must contain all 17 legacy table row counts"
        }
        foreach ($file in $manifestFiles) {
            $path = Join-Path $staging ([string]$file.name)
            if ((Get-Item -LiteralPath $path).Length -ne [int64]$file.sizeBytes) { throw "Archive file size mismatch: $($file.name)" }
            if ((Get-SHA256 $path) -ne ([string]$file.sha256).ToLowerInvariant()) { throw "Archive file hash mismatch: $($file.name)" }
        }
        $previousMinute = $env:GO_STOCK_MINUTE_DB_PATH
        $env:GO_STOCK_MINUTE_DB_PATH = Join-Path $staging "minute.db"
        try {
            $output = @(& $Receipt.binary --db-path (Join-Path $staging "stock.db") db verify --quick-only 2>&1)
            $exitCode = $LASTEXITCODE
        } finally { $env:GO_STOCK_MINUTE_DB_PATH = $previousMinute }
        if ($exitCode -ne 0 -or ($output -join "`n") -notmatch "main quick_check=ok" -or ($output -join "`n") -notmatch "minute quick_check=ok") {
            throw "Restored archive failed SQLite quick_check: $($output -join ' ')"
        }
        $verified = $true
        return $staging
    } finally {
        if (-not $verified -and (Test-Path -LiteralPath $staging -PathType Container)) {
            Remove-Item -LiteralPath (Assert-ChildPath $staging $RestoreRoot) -Recurse -Force
        }
    }
}

function Install-RestoredDatabases {
    param([string]$Staging)
    Assert-DatabasePaths
    $staging = Assert-ChildPath $Staging $RestoreRoot
    New-Item -ItemType Directory -Force -Path $FailedMigrationsRoot | Out-Null
    $failed = Assert-ChildPath (Join-Path $FailedMigrationsRoot ([DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss") + "-" + [Guid]::NewGuid().ToString("N"))) $FailedMigrationsRoot
    New-Item -ItemType Directory -Path $failed | Out-Null
    $moved = New-Object System.Collections.Generic.List[object]
    try {
        foreach ($item in @(
            @{ Source = $MainDB; Name = "stock.db" }, @{ Source = "$MainDB-wal"; Name = "stock.db-wal" }, @{ Source = "$MainDB-shm"; Name = "stock.db-shm" },
            @{ Source = $MinuteDB; Name = "minute.db" }, @{ Source = "$MinuteDB-wal"; Name = "minute.db-wal" }, @{ Source = "$MinuteDB-shm"; Name = "minute.db-shm" }
        )) {
            if (Test-Path -LiteralPath $item.Source -PathType Leaf) {
                $destination = Join-Path $failed $item.Name
                Move-Item -LiteralPath $item.Source -Destination $destination
                $moved.Add([pscustomobject]@{ Original = $item.Source; Saved = $destination })
            }
        }
        Move-Item -LiteralPath (Join-Path $staging "stock.db") -Destination $MainDB
        Move-Item -LiteralPath (Join-Path $staging "minute.db") -Destination $MinuteDB
    } catch {
        Remove-Item -LiteralPath $MainDB -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $MinuteDB -Force -ErrorAction SilentlyContinue
        foreach ($item in $moved) {
            if (Test-Path -LiteralPath $item.Saved) { Move-Item -LiteralPath $item.Saved -Destination $item.Original -Force }
        }
        throw
    }
    Remove-Item -LiteralPath $staging -Recurse -Force
    return $failed
}

function Restore-FromReceipt {
    param($Receipt)
    $staging = Expand-VerifiedDatabaseArchive $Receipt
    return Install-RestoredDatabases $staging
}

function Remove-RestorationSafetyCopy {
    param([string]$Path)
    if (-not $Path) { return }
    $path = Assert-ChildPath $Path $FailedMigrationsRoot
    if (Test-Path -LiteralPath $path -PathType Container) { Remove-Item -LiteralPath $path -Recurse -Force }
}

function Get-PendingSchemaMaintenance {
    foreach ($file in @(Get-ChildItem -LiteralPath $DeploymentsRoot -Filter 'maintenance-*.json' -File -ErrorAction SilentlyContinue)) {
        $state = Read-ReleaseState $file.FullName
        if ($state.status -eq 'started') { [pscustomobject]@{ Path=$file.FullName; State=$state } }
    }
}

function Complete-SchemaMaintenance {
    param([string]$ReceiptPath, [string]$Status)
    foreach ($pending in @(Get-PendingSchemaMaintenance)) {
        if ([IO.Path]::GetFullPath($pending.State.rollbackReceipt) -eq [IO.Path]::GetFullPath($ReceiptPath)) {
            $pending.State.status = $Status
            Write-ReleaseState $pending.Path $pending.State
        }
    }
}

function Invoke-Deploy {
    Assert-DatabasePaths
    $context = Get-Context
    Assert-VersionTagMatchesCommit $context
    $pointer = Read-BuildArtifact $context
    $previous = if (Test-Path -LiteralPath $CurrentPointer) { Read-ReleasePointer $CurrentPointer } else { $null }
    foreach ($pending in @(Get-PendingSchemaMaintenance)) {
        $reconciled = $false
        if ($previous -and $previous.commit -eq $pending.State.commit -and $previous.artifactSHA256 -eq $pending.State.artifactSHA256) {
            try { [void](Get-ExactReleaseStatus $previous); $reconciled = $true } catch {}
        }
        if (-not $reconciled) {
            $script:DeploymentReceiptPath = Assert-ChildPath $pending.State.rollbackReceipt $DeploymentsRoot
            throw "Interrupted schema maintenance; restore the original archive with -Command rollback -RollbackReceipt `"$script:DeploymentReceiptPath`", then resume publish"
        }
        Complete-SchemaMaintenance $pending.State.rollbackReceipt 'complete'
    }
    if ($previous -and $previous.commit -eq $pointer.commit -and $previous.artifactSHA256 -eq $pointer.artifactSHA256) {
        try { [void](Get-ExactReleaseStatus $pointer); Write-Host 'REUSE already running exact release'; return } catch {}
    }
    $schemaTransition = if ($null -ne $previous) { Get-SchemaTransition $previous $pointer } else { $null }
    $receiptPath = Join-Path $DeploymentsRoot ("previous-" + [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss") + '-' + [Guid]::NewGuid().ToString('N') + ".json")
    $script:DeploymentReceiptPath = $receiptPath
    $archive = $null
    $maintenanceReceipt = $null
    $migrationStarted = $false
    $safetyCopy = ""
    try {
        if ($null -ne $schemaTransition -and $schemaTransition.RequiresMigration) {
            Stop-Current
            $archive = New-DatabaseArchive $previous $pointer
            $maintenanceReceipt = New-RollbackReceipt $previous $archive.Path $archive.SHA256
            Write-JSONAtomic $receiptPath $maintenanceReceipt
            Write-ReleaseState (Join-Path $DeploymentsRoot ("maintenance-" + [Guid]::NewGuid().ToString('N') + '.json')) @{
                status='started'; commit=$pointer.commit; artifactSHA256=$pointer.artifactSHA256; rollbackReceipt=$receiptPath
            }
            $migrationStarted = $true
            [void](Invoke-DatabaseJSON $pointer.binary @("db", "migrate"))
            if ($schemaTransition.MainChanged) {
                [void](Invoke-DatabaseJSON $pointer.binary @("db", "compact", "--database", "main"))
            }
            [void](Invoke-DatabaseJSON $pointer.binary @("db", "verify"))
        } elseif ($null -ne $previous) {
            Write-JSONAtomic $receiptPath (New-RollbackReceipt $previous)
            Stop-Current
        }
        Write-JSONAtomic $CurrentPointer $pointer
        Start-Pointer $pointer
    } catch {
        $deployError = $_
        try {
            Stop-Current
            if ($null -ne $previous) {
                if ($migrationStarted) {
                    $safetyCopy = Restore-FromReceipt ([pscustomobject]$maintenanceReceipt)
                }
                Write-JSONAtomic $CurrentPointer $previous
                Start-Pointer $previous
                Remove-RestorationSafetyCopy $safetyCopy
                if ($migrationStarted) { Complete-SchemaMaintenance $receiptPath 'rolled_back' }
            }
        } catch {
            throw "Deployment failed: $($deployError.Exception.Message). Automatic rollback also failed: $($_.Exception.Message)"
        }
        throw "Deployment failed and was rolled back safely: $($deployError.Exception.Message)"
    }
    if ($migrationStarted) { Complete-SchemaMaintenance $receiptPath 'complete' }
    Write-Output "Deployed App $($pointer.appVersion) to http://$WebAddr"
    if ($archive) { Write-Output "Permanent pre-schema archive: $($archive.Path)" }
}

function Invoke-Rollback {
    Assert-DatabasePaths
    if (-not $RollbackReceipt) { throw "rollback requires -RollbackReceipt <pointer.json>" }
    $receiptPath = Assert-ChildPath $RollbackReceipt $DeploymentsRoot
    $pointer = Read-ReleasePointer $receiptPath
    $hasArchive = $pointer.PSObject.Properties.Name -contains "databaseArchive"
    if ($hasArchive -and [string]::IsNullOrWhiteSpace([string]$pointer.databaseArchive)) {
        throw "Rollback receipt has an empty database archive path"
    }
    $safetyCopy = ""
    Stop-Current
    try {
        if ($hasArchive) { $safetyCopy = Restore-FromReceipt $pointer }
        Write-JSONAtomic $CurrentPointer $pointer
        Start-Pointer $pointer
        Remove-RestorationSafetyCopy $safetyCopy
        if ($hasArchive) { Complete-SchemaMaintenance $receiptPath 'rolled_back' }
    } catch {
        throw "Rollback failed; retained database safety copy at '$safetyCopy': $($_.Exception.Message)"
    }
    Write-Output "Rolled back to App $($pointer.appVersion)"
    if ($hasArchive) { Write-Output "Restored both databases from $($pointer.databaseArchive)" }
}

. (Join-Path $ScriptDir 'release-publish.ps1')
if ($MyInvocation.InvocationName -ne ".") {
    if (($NotesFile -or $Resume) -and $Command -ne 'publish') { throw 'NotesFile and Resume belong to publish' }
    $releaseLock = Enter-ReleaseLock
    try { switch ($Command) {
        'publish' { Invoke-Publish }
        "build" { Invoke-Build | Out-Null }
        "deploy" { Invoke-Deploy }
        "activate" { Invoke-Deploy }
        "rollback" { Invoke-Rollback }
    } } finally { $releaseLock.Dispose() }
}
