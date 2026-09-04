[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $ScriptDir))
$VerifyScript = Join-Path $ScriptDir "verify.ps1"

function Get-WorkspaceSnapshot {
    return @(& git -C $ProjectRoot status --porcelain=v1)
}

function Get-DatabaseSnapshot {
    $names = @("stock.db", "stock.db-wal", "stock.db-shm", "minute.db", "minute.db-wal", "minute.db-shm")
    $rows = foreach ($name in $names) {
        $path = Join-Path (Join-Path $ProjectRoot "data") $name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            [pscustomobject]@{ Name = $name; Exists = $false; Length = 0; SHA256 = "" }
            continue
        }
        $item = Get-Item -LiteralPath $path
        [pscustomobject]@{
            Name = $name
            Exists = $true
            Length = $item.Length
            SHA256 = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
        }
    }
    return @($rows)
}

function Convert-SnapshotToJSON {
    param([Parameter(Mandatory = $true)]$Value)
    return ($Value | ConvertTo-Json -Compress -Depth 4)
}

function Invoke-ResearchDomain {
    param([Parameter(Mandatory = $true)][ValidateSet("research", "research2")][string]$Domain)
    & pwsh -NoProfile -File $VerifyScript -Tier domain -Domain $Domain
    if ($LASTEXITCODE -ne 0) {
        throw "$Domain offline verification failed with exit code $LASTEXITCODE"
    }
}

$workspaceBefore = Convert-SnapshotToJSON (Get-WorkspaceSnapshot)
$databasesBefore = Convert-SnapshotToJSON (Get-DatabaseSnapshot)

Invoke-ResearchDomain "research"
Invoke-ResearchDomain "research2"

$workspaceAfter = Convert-SnapshotToJSON (Get-WorkspaceSnapshot)
$databasesAfter = Convert-SnapshotToJSON (Get-DatabaseSnapshot)

if ($workspaceAfter -ne $workspaceBefore) {
    throw "research verification changed the Git working tree"
}
if ($databasesAfter -ne $databasesBefore) {
    throw "research verification changed a production database, WAL, or SHM file"
}

Write-Host "Research 1 and Research 2 offline verification passed without production database changes."
