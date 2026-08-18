param(
    [switch]$Apply,
    [ValidateRange(1, 10)]
    [int]$KeepReleases = 2,
    [ValidateRange(1, 10)]
    [int]$KeepDatabaseBackups = 1
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
    foreach ($field in @("appVersion", "commit", "binary", "artifactSHA256", "zoneInfo", "zoneInfoSHA256")) {
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
    $sum = (Get-ChildItem -LiteralPath $item.FullName -Recurse -File -Force -ErrorAction SilentlyContinue |
        Measure-Object Length -Sum).Sum
    if ($null -eq $sum) { return [int64]0 }
    return [int64]$sum
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
$preservedReleaseDirs = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
[void]$preservedReleaseDirs.Add($currentRelease.ReleaseDir)
$preservedReceipts = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
$releaseDetails = New-Object System.Collections.Generic.List[object]
$releaseDetails.Add($currentRelease)

$receiptFiles = @()
if (Test-Path -LiteralPath $DeploymentsRoot -PathType Container) {
    $receiptFiles = @(Get-ChildItem -LiteralPath $DeploymentsRoot -File -Filter "previous-*.json" |
        Sort-Object LastWriteTime -Descending)
}
foreach ($receipt in $receiptFiles) {
    if ($preservedReleaseDirs.Count -ge $KeepReleases) { break }
    try {
        $candidate = Read-ReleasePointer $receipt.FullName
        if ($preservedReleaseDirs.Add($candidate.ReleaseDir)) {
            [void]$preservedReceipts.Add((Get-FullPath $receipt.FullName))
            $releaseDetails.Add($candidate)
        }
    } catch {
        Write-Warning "Ignoring invalid rollback receipt '$($receipt.FullName)': $($_.Exception.Message)"
    }
}
if ($preservedReleaseDirs.Count -lt $KeepReleases) {
    throw "Only $($preservedReleaseDirs.Count) validated releases are available; requested $KeepReleases"
}

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

if (Test-Path -LiteralPath $DeploymentsRoot -PathType Container) {
    foreach ($receipt in Get-ChildItem -LiteralPath $DeploymentsRoot -File) {
        $path = Get-FullPath $receipt.FullName
        if (-not $preservedReceipts.Contains($path)) {
            Add-Target (New-CleanupTarget $path "stale rollback receipt" $DeploymentsRoot)
        }
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
        # Recursive cleanup is restricted to runtime-owned directories. Project-root
        # artifacts above are files only and can never become recursive targets.
        $path = Assert-ChildPath $path $RuntimeRoot
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
