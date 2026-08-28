param(
    [switch]$Apply,
    [ValidateRange(0, 10)]
    [int]$KeepDatabaseBackups = 0,
    [ValidateRange(1, 100)]
    [int]$KeepRotatedLogs = 12,
    [string]$LegacyBoundaryVersion = "1.8.7"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $ScriptDir))
$RuntimeRoot = [System.IO.Path]::GetFullPath((Join-Path $ProjectRoot "runtime"))
$ReleasesRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "releases"))
$DeploymentsRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "deployments"))
$ArchivesRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "archives"))
$CurrentPointerPath = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "current.json"))
$CleanupVerificationRoot = [System.IO.Path]::GetFullPath((Join-Path $RuntimeRoot "cleanup-verification"))
$OutputRoot = [System.IO.Path]::GetFullPath((Join-Path $ProjectRoot "output"))
$LogsRoot = [System.IO.Path]::GetFullPath((Join-Path $ProjectRoot "logs"))
$TempRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath()).TrimEnd('\')

function Get-FullPath {
    param([Parameter(Mandatory = $true)][string]$Path)
    return [System.IO.Path]::GetFullPath($Path)
}

function Assert-ChildPath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Root
    )
    $resolvedPath = (Get-FullPath $Path).TrimEnd('\')
    $resolvedRoot = (Get-FullPath $Root).TrimEnd('\')
    $prefix = $resolvedRoot + '\'
    if (-not $resolvedPath.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing path outside cleanup root '$resolvedRoot': $resolvedPath"
    }
    return $resolvedPath
}

function Get-SHA256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Missing release artifact: $Path"
    }
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Read-ReleasePointer {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Missing release pointer: $Path"
    }
    $pointer = Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
    foreach ($field in @("appVersion", "mainSchemaVersion", "minuteSchemaVersion", "commit", "binary", "artifactSHA256", "zoneInfo", "zoneInfoSHA256")) {
        if ([string]::IsNullOrWhiteSpace([string]$pointer.$field)) {
            throw "Release pointer '$Path' is missing $field"
        }
    }
    $binary = Assert-ChildPath ([string]$pointer.binary) $ReleasesRoot
    $zoneInfo = Assert-ChildPath ([string]$pointer.zoneInfo) $ReleasesRoot
    if ((Get-SHA256 $binary) -ne ([string]$pointer.artifactSHA256).ToLowerInvariant()) {
        throw "Release binary hash mismatch: $binary"
    }
    if ((Get-SHA256 $zoneInfo) -ne ([string]$pointer.zoneInfoSHA256).ToLowerInvariant()) {
        throw "Release zoneinfo hash mismatch: $zoneInfo"
    }
    $releaseDir = Get-FullPath (Split-Path -Parent $binary)
    if ((Get-FullPath (Split-Path -Parent $zoneInfo)) -ne $releaseDir) {
        throw "Release pointer artifacts are not in one directory: $Path"
    }
    $archive = ""
    $hasArchivePath = $pointer.PSObject.Properties.Name -contains "databaseArchive"
    $hasArchiveHash = $pointer.PSObject.Properties.Name -contains "databaseArchiveSHA256"
    if ($hasArchivePath -xor $hasArchiveHash) {
        throw "Release pointer '$Path' has incomplete database archive credentials"
    }
    if ($hasArchivePath) {
        if ([string]::IsNullOrWhiteSpace([string]$pointer.databaseArchive) -or
            [string]::IsNullOrWhiteSpace([string]$pointer.databaseArchiveSHA256)) {
            throw "Release pointer '$Path' has empty database archive credentials"
        }
        $archive = Assert-ChildPath ([string]$pointer.databaseArchive) $ArchivesRoot
        if ((Get-SHA256 $archive) -ne ([string]$pointer.databaseArchiveSHA256).ToLowerInvariant()) {
            throw "Permanent database archive hash mismatch: $archive"
        }
    }
    return [pscustomobject]@{
        Source = Get-FullPath $Path
        AppVersion = [string]$pointer.appVersion
        MainSchemaVersion = [int]$pointer.mainSchemaVersion
        MinuteSchemaVersion = [int]$pointer.minuteSchemaVersion
        Commit = [string]$pointer.commit
        Binary = $binary
        ZoneInfo = $zoneInfo
        ReleaseDir = $releaseDir
        DatabaseArchive = $archive
    }
}

function Get-PathBytes {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return [int64]0 }
    $item = Get-Item -LiteralPath $Path -Force
    if (-not $item.PSIsContainer) { return [int64]$item.Length }
    $files = @(Get-ChildItem -LiteralPath $item.FullName -Recurse -File -Force -ErrorAction SilentlyContinue)
    if ($files.Count -eq 0) { return [int64]0 }
    return [int64](($files | Measure-Object Length -Sum).Sum)
}

function Test-DatabaseBackup {
    param(
        [Parameter(Mandatory = $true)][string]$Directory,
        [Parameter(Mandatory = $true)]$CurrentRelease
    )
    $stock = Join-Path $Directory "stock.db"
    $minute = Join-Path $Directory "minute.db"
    if (-not (Test-Path -LiteralPath $stock -PathType Leaf) -or
        -not (Test-Path -LiteralPath $minute -PathType Leaf)) {
        return $false
    }
    $output = & $CurrentRelease.Binary --db-path $stock db verify --quick-only 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "Backup integrity check failed for '$Directory': $($output -join ' ')"
        return $false
    }
    return (($output -join "`n") -match "main quick_check=ok" -and
        ($output -join "`n") -match "minute quick_check=ok")
}

function Assert-ProductionDatabases {
    param([Parameter(Mandatory = $true)]$CurrentRelease)
    $mainDB = Get-FullPath (Join-Path $ProjectRoot "data\stock.db")
    $minuteDB = Get-FullPath (Join-Path $ProjectRoot "data\minute.db")
    foreach ($path in @($mainDB, $minuteDB)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Production database is missing: $path" }
    }
    $previousMinute = $env:GO_STOCK_MINUTE_DB_PATH
    $env:GO_STOCK_MINUTE_DB_PATH = $minuteDB
    try {
        $output = @(& $CurrentRelease.Binary --db-path $mainDB db verify --quick-only 2>&1)
        $exitCode = $LASTEXITCODE
    } finally {
        $env:GO_STOCK_MINUTE_DB_PATH = $previousMinute
    }
    if ($exitCode -ne 0 -or ($output -join "`n") -notmatch "main quick_check=ok" -or
        ($output -join "`n") -notmatch "minute quick_check=ok") {
        throw "Production SQLite quick_check failed: $($output -join ' ')"
    }
    Write-Output "Verified production databases: $mainDB and $minuteDB"
}

function Assert-DatabaseArchive {
    param([Parameter(Mandatory = $true)]$Release)
    if ([string]::IsNullOrWhiteSpace([string]$Release.DatabaseArchive)) { return }
    $archive = Assert-ChildPath ([string]$Release.DatabaseArchive) $ArchivesRoot
    New-Item -ItemType Directory -Force -Path $CleanupVerificationRoot | Out-Null
    $staging = Assert-ChildPath (Join-Path $CleanupVerificationRoot ([Guid]::NewGuid().ToString("N"))) $CleanupVerificationRoot
    New-Item -ItemType Directory -Path $staging | Out-Null
    try {
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $zip = [System.IO.Compression.ZipFile]::OpenRead($archive)
        try {
            $entries = @{}
            foreach ($entry in $zip.Entries) {
                if ($entry.FullName -notin @("stock.db", "minute.db", "manifest.json") -or $entries.ContainsKey($entry.FullName)) {
                    throw "Unexpected or duplicate archive entry '$($entry.FullName)' in $archive"
                }
                $entries[$entry.FullName] = $entry
            }
            foreach ($name in @("stock.db", "minute.db", "manifest.json")) {
                if (-not $entries.ContainsKey($name)) { throw "Archive is missing ${name}: $archive" }
                [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entries[$name], (Join-Path $staging $name), $false)
            }
        } finally {
            $zip.Dispose()
        }

        $manifest = Get-Content -LiteralPath (Join-Path $staging "manifest.json") -Raw | ConvertFrom-Json
        if ([int]$manifest.formatVersion -ne 1 -or
            [int]$manifest.mainSchemaVersion -ne [int]$Release.MainSchemaVersion -or
            [int]$manifest.minuteSchemaVersion -ne [int]$Release.MinuteSchemaVersion) {
            throw "Archive manifest schema does not match App $($Release.AppVersion): $archive"
        }
        if ([string]$manifest.sourceAppVersion -ne [string]$Release.AppVersion -or
            [string]$manifest.sourceCommit -ne [string]$Release.Commit) {
            throw "Archive manifest release identity does not match App $($Release.AppVersion): $archive"
        }
        $manifestFiles = @($manifest.files)
        if ($manifestFiles.Count -ne 2 -or (($manifestFiles.name | Sort-Object) -join ',') -ne "minute.db,stock.db") {
            throw "Archive manifest must describe exactly stock.db and minute.db: $archive"
        }
        if (@($manifest.legacyTableRows.PSObject.Properties).Count -ne 17) {
            throw "Archive manifest does not contain all legacy row counts: $archive"
        }
        foreach ($file in $manifestFiles) {
            $path = Join-Path $staging ([string]$file.name)
            if ((Get-Item -LiteralPath $path).Length -ne [int64]$file.sizeBytes) {
                throw "Archive entry size mismatch: $path"
            }
            if ((Get-SHA256 $path) -ne ([string]$file.sha256).ToLowerInvariant()) {
                throw "Archive entry hash mismatch: $path"
            }
        }

        $previousMinute = $env:GO_STOCK_MINUTE_DB_PATH
        $env:GO_STOCK_MINUTE_DB_PATH = Join-Path $staging "minute.db"
        try {
            $output = @(& $Release.Binary --db-path (Join-Path $staging "stock.db") db verify --quick-only 2>&1)
            $exitCode = $LASTEXITCODE
        } finally {
            $env:GO_STOCK_MINUTE_DB_PATH = $previousMinute
        }
        if ($exitCode -ne 0 -or ($output -join "`n") -notmatch "main quick_check=ok" -or
            ($output -join "`n") -notmatch "minute quick_check=ok") {
            throw "Archive SQLite quick_check failed for App $($Release.AppVersion): $($output -join ' ')"
        }
        Write-Output "Verified rollback archive for App $($Release.AppVersion): $archive"
    } finally {
        if (Test-Path -LiteralPath $staging -PathType Container) {
            Remove-Item -LiteralPath (Assert-ChildPath $staging $CleanupVerificationRoot) -Recurse -Force
        }
        if ((Test-Path -LiteralPath $CleanupVerificationRoot -PathType Container) -and
            [System.IO.Directory]::GetFileSystemEntries($CleanupVerificationRoot).Count -eq 0) {
            Remove-Item -LiteralPath $CleanupVerificationRoot -Force
        }
    }
}

function New-CleanupTarget {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Reason,
        [Parameter(Mandatory = $true)][string]$AllowedRoot
    )
    $resolved = Assert-ChildPath $Path $AllowedRoot
    return [pscustomobject]@{
        Path = $resolved
        Reason = $Reason
        AllowedRoot = Get-FullPath $AllowedRoot
        Bytes = Get-PathBytes $resolved
    }
}

if (-not (Test-Path -LiteralPath $RuntimeRoot -PathType Container)) {
    throw "Runtime root is unavailable: $RuntimeRoot"
}
if (-not (Test-Path -LiteralPath $ReleasesRoot -PathType Container)) {
    throw "Release root is unavailable: $ReleasesRoot"
}

$currentRelease = Read-ReleasePointer $CurrentPointerPath
Assert-ProductionDatabases $currentRelease
$preservedReleaseDirs = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
$preservedReceipts = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
$preservedArchives = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
$releaseDetails = New-Object System.Collections.Generic.List[object]
$releaseCandidates = New-Object System.Collections.Generic.List[object]
$releaseCandidates.Add([pscustomobject]@{ Detail = $currentRelease; Receipt = ""; SortTime = [DateTime]::MaxValue })

$receiptFiles = @()
if (Test-Path -LiteralPath $DeploymentsRoot -PathType Container) {
    $receiptFiles = @(Get-ChildItem -LiteralPath $DeploymentsRoot -File -Filter "previous-*.json" |
        Sort-Object LastWriteTime -Descending)
}
foreach ($receipt in $receiptFiles) {
    try {
        $candidate = Read-ReleasePointer $receipt.FullName
        $releaseCandidates.Add([pscustomobject]@{
            Detail = $candidate
            Receipt = Get-FullPath $receipt.FullName
            SortTime = $receipt.LastWriteTimeUtc
        })
    } catch {
        Write-Warning "Ignoring invalid rollback receipt '$($receipt.FullName)': $($_.Exception.Message)"
    }
}

function Get-AppMajorVersion {
    param([Parameter(Mandatory = $true)][string]$AppVersion)
    try { return ([version]$AppVersion).Major }
    catch { return -1 }
}

function Add-PreservedRelease {
    param([Parameter(Mandatory = $true)]$Candidate)
    $detail = $Candidate.Detail
    if (-not $preservedReleaseDirs.Add((Get-FullPath $detail.ReleaseDir))) { return }
    $releaseDetails.Add($detail)
    if (-not [string]::IsNullOrWhiteSpace([string]$Candidate.Receipt)) {
        [void]$preservedReceipts.Add((Get-FullPath $Candidate.Receipt))
    }
    if (-not [string]::IsNullOrWhiteSpace([string]$detail.DatabaseArchive)) {
        [void]$preservedArchives.Add((Get-FullPath $detail.DatabaseArchive))
    }
}

$currentCandidate = $releaseCandidates[0]
Add-PreservedRelease $currentCandidate
$currentSchemaKey = "$($currentRelease.MainSchemaVersion)/$($currentRelease.MinuteSchemaVersion)"

# Keep the direct predecessor when it uses the same schema. This is the cheap,
# database-preserving rollback path for patch releases such as 2.5.1 -> 2.5.0.
$sameSchemaPredecessor = @($releaseCandidates |
    Where-Object {
        $_.Detail.ReleaseDir -ne $currentRelease.ReleaseDir -and
        "$($_.Detail.MainSchemaVersion)/$($_.Detail.MinuteSchemaVersion)" -eq $currentSchemaKey
    } |
    Sort-Object SortTime -Descending |
    Select-Object -First 1)
if ($sameSchemaPredecessor.Count -gt 0) { Add-PreservedRelease $sameSchemaPredecessor[0] }

# Keep one validated release for every earlier schema in the supported 2.x
# chain. The newest receipt wins when several patch releases share a schema.
$supportedSchemaGroups = @($releaseCandidates |
    Where-Object {
        (Get-AppMajorVersion $_.Detail.AppVersion) -eq 2 -and
        "$($_.Detail.MainSchemaVersion)/$($_.Detail.MinuteSchemaVersion)" -ne $currentSchemaKey
    } |
    Group-Object { "$($_.Detail.MainSchemaVersion)/$($_.Detail.MinuteSchemaVersion)" })
foreach ($group in $supportedSchemaGroups) {
    $selected = @($group.Group | Sort-Object SortTime -Descending | Select-Object -First 1)
    if ($selected.Count -gt 0) { Add-PreservedRelease $selected[0] }
}

$legacyBoundary = @($releaseCandidates |
    Where-Object { $_.Detail.AppVersion -eq $LegacyBoundaryVersion } |
    Sort-Object SortTime -Descending |
    Select-Object -First 1)
if ($legacyBoundary.Count -eq 0) {
    throw "Validated legacy rollback boundary App $LegacyBoundaryVersion is unavailable"
}
Add-PreservedRelease $legacyBoundary[0]

foreach ($release in $releaseDetails) { Assert-DatabaseArchive $release }

$running = @(Get-CimInstance Win32_Process -Filter "name='go-stock-web.exe'" -ErrorAction SilentlyContinue)
foreach ($process in $running) {
    if ([string]::IsNullOrWhiteSpace([string]$process.ExecutablePath)) { continue }
    $executable = Get-FullPath ([string]$process.ExecutablePath)
    $releasePrefix = $ReleasesRoot.TrimEnd('\') + '\'
    if (-not $executable.StartsWith($releasePrefix, [System.StringComparison]::OrdinalIgnoreCase)) { continue }
    $releaseDir = Get-FullPath (Split-Path -Parent $executable)
    if (-not $preservedReleaseDirs.Contains($releaseDir)) {
        throw "Running process $($process.ProcessId) uses a release scheduled for deletion: $releaseDir"
    }
}

$backupCandidates = New-Object System.Collections.Generic.List[object]
foreach ($backupRootName in @("db-backups", "backups")) {
    $backupRoot = Join-Path $RuntimeRoot $backupRootName
    if (-not (Test-Path -LiteralPath $backupRoot -PathType Container)) { continue }
    foreach ($directory in Get-ChildItem -LiteralPath $backupRoot -Directory) {
        $backupCandidates.Add([pscustomobject]@{
            Directory = $directory
            Root = Get-FullPath $backupRoot
        })
    }
}
$preservedBackupDirs = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
foreach ($candidate in @($backupCandidates | Sort-Object { $_.Directory.LastWriteTime } -Descending)) {
    if ($preservedBackupDirs.Count -ge $KeepDatabaseBackups) { break }
    if (Test-DatabaseBackup $candidate.Directory.FullName $currentRelease) {
        [void]$preservedBackupDirs.Add((Get-FullPath $candidate.Directory.FullName))
    }
}
if ($preservedBackupDirs.Count -lt $KeepDatabaseBackups) {
    throw "Only $($preservedBackupDirs.Count) valid database backups are available; requested $KeepDatabaseBackups"
}

$targetsByPath = @{}
function Add-Target {
    param($Target)
    if ($null -eq $Target) { return }
    $key = ([string]$Target.Path).ToLowerInvariant()
    if (-not $targetsByPath.ContainsKey($key)) { $targetsByPath[$key] = $Target }
}

$simulationsRoot = Join-Path $RuntimeRoot "simulations"
if (Test-Path -LiteralPath $simulationsRoot -PathType Container) {
    foreach ($directory in Get-ChildItem -LiteralPath $simulationsRoot -Directory) {
        Add-Target (New-CleanupTarget $directory.FullName "retired simulation" $simulationsRoot)
    }
}
foreach ($candidate in $backupCandidates) {
    $path = Get-FullPath $candidate.Directory.FullName
    if (-not $preservedBackupDirs.Contains($path)) {
        Add-Target (New-CleanupTarget $path "expired database backup" $candidate.Root)
    }
}

$releaseCommitDirs = New-Object System.Collections.Generic.List[object]
foreach ($versionDir in Get-ChildItem -LiteralPath $ReleasesRoot -Directory) {
    foreach ($commitDir in Get-ChildItem -LiteralPath $versionDir.FullName -Directory) {
        $releaseCommitDirs.Add($commitDir)
    }
}
foreach ($commitDir in $releaseCommitDirs) {
    $path = Get-FullPath $commitDir.FullName
    if (-not $preservedReleaseDirs.Contains($path)) {
        Add-Target (New-CleanupTarget $path "unreferenced release" $ReleasesRoot)
    }
}

if (Test-Path -LiteralPath $ArchivesRoot -PathType Container) {
    foreach ($archive in Get-ChildItem -LiteralPath $ArchivesRoot -File) {
        $path = Get-FullPath $archive.FullName
        if (-not $preservedArchives.Contains($path)) {
            Add-Target (New-CleanupTarget $path "archive outside supported rollback chain" $ArchivesRoot)
        }
    }
}

if (Test-Path -LiteralPath $DeploymentsRoot -PathType Container) {
    foreach ($receipt in Get-ChildItem -LiteralPath $DeploymentsRoot -File) {
        $path = Get-FullPath $receipt.FullName
        if (-not $preservedReceipts.Contains($path)) {
            Add-Target (New-CleanupTarget $path "stale rollback receipt" $DeploymentsRoot)
        }
    }
}

$playwrightOutput = Join-Path $OutputRoot "playwright"
if (Test-Path -LiteralPath $playwrightOutput -PathType Container) {
    Add-Target (New-CleanupTarget $playwrightOutput "completed browser test artifacts" $OutputRoot)
}
$validationRoot = Join-Path $RuntimeRoot "validation"
if (Test-Path -LiteralPath $validationRoot -PathType Container) {
    Add-Target (New-CleanupTarget $validationRoot "completed release validation artifacts" $RuntimeRoot)
}

$worktreePaths = New-Object 'System.Collections.Generic.List[string]'
$worktreeOutput = @(& git -C $ProjectRoot worktree list --porcelain)
if ($LASTEXITCODE -ne 0) { throw "Cannot enumerate Git worktrees before temporary cleanup" }
foreach ($line in $worktreeOutput) {
    if ($line -match '^worktree\s+(.+)$') { $worktreePaths.Add((Get-FullPath $Matches[1])) }
}
$runningProcesses = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue)
if (Test-Path -LiteralPath $TempRoot -PathType Container) {
    foreach ($directory in Get-ChildItem -LiteralPath $TempRoot -Directory -Force | Where-Object { $_.Name -like "go-stock*" }) {
        $path = Get-FullPath $directory.FullName
        $prefix = $path.TrimEnd('\') + '\'
        $worktreeConflict = @($worktreePaths | Where-Object {
            $_ -eq $path -or $_.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)
        }).Count -gt 0
        if ($worktreeConflict) {
            Write-Warning "Keeping registered Git worktree: $path"
            continue
        }
        $processConflict = @($runningProcesses | Where-Object {
            -not [string]::IsNullOrWhiteSpace([string]$_.ExecutablePath) -and
            ((Get-FullPath ([string]$_.ExecutablePath)).StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase))
        }).Count -gt 0
        if ($processConflict) {
            Write-Warning "Keeping temporary directory used by a running executable: $path"
            continue
        }
        Add-Target (New-CleanupTarget $path "completed go-stock temporary workspace" $TempRoot)
    }
}

if (Test-Path -LiteralPath $LogsRoot -PathType Container) {
    $rotatedLogs = @(Get-ChildItem -LiteralPath $LogsRoot -File -Force |
        Where-Object { $_.Name -like "info-*.log" -or $_.Name -like "error-*.log" } |
        Sort-Object LastWriteTime -Descending)
    foreach ($log in @($rotatedLogs | Select-Object -Skip $KeepRotatedLogs)) {
        Add-Target (New-CleanupTarget $log.FullName "rotated log outside newest $KeepRotatedLogs" $LogsRoot)
    }
}

$explicitProjectFiles = @(
    "data\stock-before-v142-repair-20260703-214128.db",
    "go-stock.exe",
    "go-stock-web.exe",
    "go-stock-test",
    "go-stock.bak.20260427004839"
)
foreach ($relativePath in $explicitProjectFiles) {
    $path = Join-Path $ProjectRoot $relativePath
    if (Test-Path -LiteralPath $path -PathType Leaf) {
        Add-Target (New-CleanupTarget $path "obsolete local artifact" $ProjectRoot)
    }
}
foreach ($log in Get-ChildItem -LiteralPath $ProjectRoot -File -Filter ".codex*.log") {
    Add-Target (New-CleanupTarget $log.FullName "obsolete local test log" $ProjectRoot)
}

$targets = @($targetsByPath.Values | Sort-Object Bytes -Descending)
$protectedPaths = @(
    (Get-FullPath (Join-Path $ProjectRoot "data\stock.db")),
    (Get-FullPath (Join-Path $ProjectRoot "data\minute.db")),
    $CurrentPointerPath,
    (Get-FullPath (Join-Path $RuntimeRoot "go-stock-web.pid")),
    (Get-FullPath (Join-Path $LogsRoot "info.log")),
    (Get-FullPath (Join-Path $LogsRoot "error.log")),
    (Get-FullPath (Join-Path $ProjectRoot ".git")),
    (Get-FullPath (Join-Path $ProjectRoot "frontend\node_modules"))
) + @($preservedReleaseDirs) + @($preservedArchives)
foreach ($target in $targets) {
    $targetPath = (Get-FullPath $target.Path).TrimEnd('\')
    $targetPrefix = $targetPath + '\'
    foreach ($protected in $protectedPaths) {
        $protectedPath = (Get-FullPath $protected).TrimEnd('\')
        if ($protectedPath -eq $targetPath -or
            $protectedPath.StartsWith($targetPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Cleanup target would contain protected path '$protectedPath': $targetPath"
        }
    }
}
$totalBytes = if ($targets.Count -eq 0) {
    [int64]0
} else {
    [int64](($targets | Measure-Object Bytes -Sum).Sum)
}

Write-Output "Cleanup mode: $(if ($Apply) { 'APPLY' } else { 'PREVIEW' })"
Write-Output "Project root: $ProjectRoot"
Write-Output "Preserved database backups:"
$preservedBackupDirs | Sort-Object | ForEach-Object { Write-Output "  $_" }
Write-Output "Preserved releases:"
$releaseDetails | ForEach-Object {
    Write-Output "  $($_.AppVersion) $($_.Commit) $($_.ReleaseDir)"
    if ($_.DatabaseArchive) { Write-Output "    permanent archive: $($_.DatabaseArchive)" }
}
if ($targets.Count -eq 0) {
    Write-Output "No cleanup targets found."
    exit 0
}
$targets | Select-Object Reason, @{Name = "GiB"; Expression = { [math]::Round($_.Bytes / 1GB, 3) } }, Path |
    Format-Table -AutoSize | Out-Host
Write-Output ("Total reclaimable: {0:N3} GiB ({1:N0} bytes)" -f ($totalBytes / 1GB), $totalBytes)

if (-not $Apply) {
    Write-Output "Preview only. Re-run with -Apply to remove these exact targets."
    exit 0
}

foreach ($target in $targets) {
    $path = Assert-ChildPath $target.Path $target.AllowedRoot
    if (-not (Test-Path -LiteralPath $path)) { continue }
    $item = Get-Item -LiteralPath $path -Force
    if ($item.PSIsContainer) {
        # Every recursive target is re-resolved beneath the narrow root captured
        # during discovery (runtime, output, or the system temporary directory).
        $path = Assert-ChildPath $path $target.AllowedRoot
        Remove-Item -LiteralPath $path -Recurse -Force
    } else {
        Remove-Item -LiteralPath $path -Force
    }
    Write-Output "Removed: $path"
}

foreach ($root in @($simulationsRoot, (Join-Path $RuntimeRoot "backups"), (Join-Path $RuntimeRoot "db-backups"))) {
    if (-not (Test-Path -LiteralPath $root -PathType Container)) { continue }
    foreach ($directory in Get-ChildItem -LiteralPath $root -Directory) {
        if ([System.IO.Directory]::GetFileSystemEntries($directory.FullName).Count -eq 0) {
            Remove-Item -LiteralPath (Assert-ChildPath $directory.FullName $root) -Force
        }
    }
}
foreach ($versionDir in Get-ChildItem -LiteralPath $ReleasesRoot -Directory) {
    if ([System.IO.Directory]::GetFileSystemEntries($versionDir.FullName).Count -eq 0) {
        Remove-Item -LiteralPath (Assert-ChildPath $versionDir.FullName $ReleasesRoot) -Force
    }
}

Write-Output ("Cleanup complete. Reclaimed approximately {0:N3} GiB." -f ($totalBytes / 1GB))
