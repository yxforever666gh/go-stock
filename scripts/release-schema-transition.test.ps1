$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

. (Join-Path $PSScriptRoot "release.ps1")

function New-TestPointer {
    param([int]$Main, [int]$Minute)
    return [pscustomobject]@{
        mainSchemaVersion = $Main
        minuteSchemaVersion = $Minute
    }
}

function Assert-Transition {
    param(
        [int]$PreviousMain,
        [int]$PreviousMinute,
        [int]$NewMain,
        [int]$NewMinute,
        [bool]$RequiresMigration,
        [bool]$MainChanged,
        [bool]$MinuteChanged
    )
    $actual = Get-SchemaTransition (New-TestPointer $PreviousMain $PreviousMinute) (New-TestPointer $NewMain $NewMinute)
    if ($actual.RequiresMigration -ne $RequiresMigration -or $actual.MainChanged -ne $MainChanged -or $actual.MinuteChanged -ne $MinuteChanged) {
        throw "Unexpected transition $PreviousMain/$PreviousMinute -> $NewMain/${NewMinute}: $($actual | ConvertTo-Json -Compress)"
    }
}

function Assert-TransitionRejected {
    param([int]$PreviousMain, [int]$PreviousMinute, [int]$NewMain, [int]$NewMinute)
    try {
        [void](Get-SchemaTransition (New-TestPointer $PreviousMain $PreviousMinute) (New-TestPointer $NewMain $NewMinute))
    } catch {
        return
    }
    throw "Expected transition $PreviousMain/$PreviousMinute -> $NewMain/$NewMinute to be rejected"
}

Assert-Transition 14 2 14 2 $false $false $false
Assert-Transition 14 2 15 2 $true $true $false
Assert-Transition 14 2 14 3 $true $false $true
Assert-Transition 14 2 15 3 $true $true $true
Assert-Transition 15 3 16 3 $true $true $false
Assert-Transition 15 3 16 4 $true $true $true
Assert-Transition 16 3 17 3 $true $true $false
Assert-Transition 16 3 17 4 $true $true $true
Assert-Transition 17 3 18 3 $true $true $false
Assert-Transition 17 3 18 4 $true $true $true

Assert-TransitionRejected 14 2 16 2
Assert-TransitionRejected 14 2 14 4
Assert-TransitionRejected 14 2 13 2
Assert-TransitionRejected 14 2 14 1
Assert-TransitionRejected 15 3 17 3
Assert-TransitionRejected 15 3 16 5
Assert-TransitionRejected 15 3 14 3
Assert-TransitionRejected 15 3 15 2
Assert-TransitionRejected 16 3 18 3
Assert-TransitionRejected 16 3 17 5
Assert-TransitionRejected 16 3 15 3
Assert-TransitionRejected 16 3 16 2
Assert-TransitionRejected 17 3 19 3
Assert-TransitionRejected 17 3 18 5
Assert-TransitionRejected 17 3 16 3
Assert-TransitionRejected 17 3 17 2

$previousPointer = [pscustomobject]@{
    appVersion = "2.2.0"
    mainSchemaVersion = 17
    minuteSchemaVersion = 3
    commit = "fixture"
    binary = "fixture.exe"
    artifactSHA256 = ("a" * 64)
    zoneInfo = "zoneinfo.zip"
    zoneInfoSHA256 = ("b" * 64)
    deployedAt = "2026-08-28T00:00:00Z"
}
$receipt = New-RollbackReceipt $previousPointer (Join-Path ([System.IO.Path]::GetTempPath()) "go-stock-release-test.zip") ("c" * 64)
if ($receipt.archivedMainDB -ne $MainDB -or $receipt.archivedMinuteDB -ne $MinuteDB) {
    throw "Rollback receipt did not retain both database paths"
}

Write-Output "release schema transition tests passed"
