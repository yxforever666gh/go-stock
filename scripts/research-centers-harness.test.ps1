$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$base = if (Test-Path -LiteralPath "H:\Download") { "H:\Download" } else { [IO.Path]::GetTempPath() }
$fixture = [IO.Path]::GetFullPath((Join-Path $base ("research-verification-fixture-" + [Guid]::NewGuid().ToString("N"))))
$scriptUnderTest = Join-Path $PSScriptRoot "research-centers.test.ps1"
try {
    New-Item -ItemType Directory -Path (Join-Path $fixture "scripts"), (Join-Path $fixture "data") | Out-Null
    Copy-Item -LiteralPath $scriptUnderTest -Destination (Join-Path $fixture "scripts")
    & git -C $fixture init --quiet
    if ($LASTEXITCODE -ne 0) { throw "fixture git init failed" }
    [IO.File]::WriteAllText((Join-Path $fixture ".gitignore"), "data/")
    [IO.File]::WriteAllText((Join-Path $fixture "tracked.txt"), "staged-before")
    & git -C $fixture add .gitignore tracked.txt
    if ($LASTEXITCODE -ne 0) { throw "fixture git add failed" }
    $mock = @'
param([string]$Tier, [string]$Domain, [string[]]$FrontendTest)
$root = Split-Path -Parent $PSScriptRoot
$scenario = [IO.File]::ReadAllText((Join-Path $root "data/scenario"))
if ($Tier -eq "domain") {
    switch ($scenario) {
        "working" { [IO.File]::WriteAllText((Join-Path $root "tracked.txt"), "working-after") }
        "staged" {
            [IO.File]::WriteAllText((Join-Path $root "tracked.txt"), "staged-after")
            & git -C $root add tracked.txt
        }
        "untracked" { [IO.File]::WriteAllText((Join-Path $root "untracked.txt"), "untracked-after") }
        "failed" {
            [IO.File]::WriteAllText((Join-Path $root "tracked.txt"), "failed-after")
            throw "mock verification failure"
        }
        "failed-db" {
            [IO.File]::WriteAllText((Join-Path $root "data/stock.db"), "observed-app-write")
            throw "mock verification failure"
        }
    }
}
exit 0
'@
    [IO.File]::WriteAllText((Join-Path $fixture "scripts/verify.ps1"), $mock)
    foreach ($scenario in @("clean", "working", "staged", "untracked", "failed", "failed-db")) {
        [IO.File]::WriteAllText((Join-Path $fixture "tracked.txt"), "staged-before")
        & git -C $fixture add tracked.txt
        [IO.File]::WriteAllText((Join-Path $fixture "tracked.txt"), "working-before")
        [IO.File]::WriteAllText((Join-Path $fixture "untracked.txt"), "untracked-before")
        [IO.File]::WriteAllText((Join-Path $fixture "data/stock.db"), "database-before")
        [IO.File]::WriteAllText((Join-Path $fixture "data/scenario"), $scenario)
        $arguments = @("-NoProfile", "-File", (Join-Path $fixture "scripts/research-centers.test.ps1"))
        if ($scenario -eq "failed-db") { $arguments += "-CheckProductionDatabases" }
        $output = (& pwsh @arguments 2>&1 | Out-String)
        $code = $LASTEXITCODE
        if ($scenario -eq "clean") {
            if ($code -ne 0) { throw "clean fixture failed: $output" }
        } else {
            if ($code -eq 0) { throw "$scenario mutation was not detected" }
            $expected = if ($scenario -eq "failed-db") { "hashes do not attribute" } else { "Workspace content or staging changed" }
            if (-not $output.Contains($expected)) { throw "$scenario missing finally observation: $output" }
            if ($scenario.StartsWith("failed") -and -not $output.Contains("Research Go verification failed")) {
                throw "$scenario lost original verification error: $output"
            }
        }
        Write-Host "PASS research verification fixture: $scenario"
    }
} finally {
    $prefix = [IO.Path]::GetFullPath($base).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    if (-not $fixture.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { throw "fixture escaped temporary root" }
    if (Test-Path -LiteralPath $fixture) { Remove-Item -LiteralPath $fixture -Recurse -Force }
}
