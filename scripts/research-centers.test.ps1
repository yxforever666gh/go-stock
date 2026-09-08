[CmdletBinding()]
param([switch]$CheckProductionDatabases)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$ProjectRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$VerifyScript = Join-Path $PSScriptRoot "verify.ps1"
$SharedFrontendTests = @(
    "src/composables/useResearchRequests.test.mjs",
    "src/components/settings/research-settings.test.mjs",
    "src/components/research-pages.test.mjs",
    "src/components/research-audit/audit-model.test.mjs",
    "src/utils/research-performance.test.mjs",
    "src/utils/research-trade-chart.test.mjs",
    "src/charting/research-chart-adapter.test.mjs"
)

function Read-Git {
    param([string[]]$Arguments)
    $result = @(& git -C $ProjectRoot -c core.quotepath=false -c core.safecrlf=false @Arguments)
    if ($LASTEXITCODE -ne 0) { throw "Cannot inspect Git workspace: $($Arguments -join ' ')" }
    return ($result -join [Environment]::NewLine)
}

function Get-WorkspaceSnapshot {
    $untracked = (Read-Git @("ls-files", "--others", "--exclude-standard", "-z")).Split([char]0, [StringSplitOptions]::RemoveEmptyEntries)
    $files = foreach ($relative in ($untracked | Sort-Object)) {
        $path = Join-Path $ProjectRoot $relative
        [pscustomobject]@{ Path = $relative; SHA256 = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash }
    }
    return ([ordered]@{
        Status = Read-Git @("status", "--porcelain=v1", "--untracked-files=all")
        Index = Read-Git @("diff", "--cached", "--binary", "--no-ext-diff", "--no-textconv")
        Working = Read-Git @("diff", "--binary", "--no-ext-diff", "--no-textconv")
        Untracked = @($files)
    } | ConvertTo-Json -Compress -Depth 4)
}

function Get-DatabaseSnapshot {
    $rows = foreach ($name in @("stock.db", "stock.db-wal", "stock.db-shm", "minute.db", "minute.db-wal", "minute.db-shm")) {
        $path = Join-Path (Join-Path $ProjectRoot "data") $name
        $hash = if (Test-Path -LiteralPath $path -PathType Leaf) { (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash } else { "absent" }
        [pscustomobject]@{ Name = $name; SHA256 = $hash }
    }
    return ($rows | ConvertTo-Json -Compress)
}

$workspaceBefore = Get-WorkspaceSnapshot
$databasesBefore = if ($CheckProductionDatabases) { Get-DatabaseSnapshot } else { $null }
$failures = [Collections.Generic.List[string]]::new()
try {
    # Every Go test owns a temporary fixture. The shared domain runs both
    # centers and their shared dependencies once, including concurrent cases.
    & pwsh -NoProfile -File $VerifyScript -Tier domain -Domain research-shared
    if ($LASTEXITCODE -ne 0) { throw "Research Go verification failed with exit code $LASTEXITCODE" }
    & $VerifyScript -Tier fast -FrontendTest $SharedFrontendTests
    if ($LASTEXITCODE -ne 0) { throw "Shared frontend research verification failed with exit code $LASTEXITCODE" }
} catch {
    $failures.Add($_.Exception.Message)
} finally {
    try {
        if ((Get-WorkspaceSnapshot) -ne $workspaceBefore) {
            $failures.Add("Workspace content or staging changed during verification, including existing dirty and untracked files.")
        }
    } catch { $failures.Add("Workspace comparison could not complete: $($_.Exception.Message)") }
    if ($CheckProductionDatabases) {
        try {
            if ((Get-DatabaseSnapshot) -ne $databasesBefore) {
                $failures.Add("Production database/WAL/SHM changes were observed. Concurrent application writes are possible; hashes do not attribute these changes to tests.")
            }
        } catch { $failures.Add("Production database observation could not complete: $($_.Exception.Message)") }
    }
}
if ($failures.Count -ne 0) { throw ($failures -join [Environment]::NewLine) }
Write-Host "Research 1, Research 2, and shared behavior verification passed using disposable test fixtures."
if ($CheckProductionDatabases) { Write-Host "Optional production database observation: hashes unchanged." }
