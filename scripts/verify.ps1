[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("fast", "domain", "release")]
    [string]$Tier,

    [ValidateSet("data", "research", "research2", "migrations", "frontend", "api", "tools")]
    [string]$Domain = "",

    [string[]]$GoPackage = @(),
    [string]$GoTest = "",
    [string[]]$FrontendTest = @(),

    [switch]$SkipGoBuild
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $ScriptDir))
$FrontendRoot = Join-Path $ProjectRoot "frontend"
$MainGoPackages = @(".", "./backend/...", "./internal/...", "./cmd/...")
$NpmProgram = if ($IsWindows) { "npm.cmd" } else { "npm" }

function Invoke-Step {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Program,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory
    )

    Write-Host "==> $Name"
    $stopwatch = [Diagnostics.Stopwatch]::StartNew()
    Push-Location $WorkingDirectory
    try {
        & $Program @Arguments | Out-Host
        $exitCode = $LASTEXITCODE
    } finally {
        Pop-Location
        $stopwatch.Stop()
    }
    if ($exitCode -ne 0) {
        throw "$Name failed with exit code $exitCode after $([math]::Round($stopwatch.Elapsed.TotalSeconds, 2))s"
    }
    Write-Host "PASS $Name ($([math]::Round($stopwatch.Elapsed.TotalSeconds, 2))s)"
}

function Assert-GoPackages {
    param([string[]]$Packages)
    foreach ($package in $Packages) {
        if ([string]::IsNullOrWhiteSpace($package)) {
            throw "GoPackage cannot be empty"
        }
        if ($package -eq "./..." -or $package -match "node_modules") {
            throw "Broad or frontend dependency Go package scopes are not allowed: $package"
        }
    }
}

function Resolve-FrontendTests {
    param([string[]]$Tests)
    $resolved = @()
    foreach ($test in $Tests) {
        if ([string]::IsNullOrWhiteSpace($test)) {
            throw "FrontendTest cannot be empty"
        }
        $candidate = if ([System.IO.Path]::IsPathRooted($test)) {
            [System.IO.Path]::GetFullPath($test)
        } else {
            [System.IO.Path]::GetFullPath((Join-Path $FrontendRoot $test))
        }
        $frontendPrefix = $FrontendRoot.TrimEnd('\', '/') + [System.IO.Path]::DirectorySeparatorChar
        if (-not $candidate.StartsWith($frontendPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Frontend test is outside the frontend directory: $test"
        }
        if (-not $candidate.EndsWith(".test.mjs", [System.StringComparison]::OrdinalIgnoreCase) -or
            -not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            throw "Frontend test does not exist or is not a .test.mjs file: $test"
        }
        $resolved += [System.IO.Path]::GetRelativePath($FrontendRoot, $candidate).Replace('\', '/')
    }
    return $resolved
}

function Invoke-GoTest {
    param([string]$Name, [string[]]$Packages, [string]$TestPattern = "")
    Assert-GoPackages $Packages
    $arguments = @("test") + $Packages
    if (-not [string]::IsNullOrWhiteSpace($TestPattern)) {
        $arguments += @("-run", $TestPattern)
    }
    Invoke-Step $Name "go" $arguments $ProjectRoot
}

function Invoke-FrontendTests {
    param([string]$Name, [string[]]$Tests = @())
    if ($Tests.Count -eq 0) {
        $discovered = @(Get-ChildItem -LiteralPath (Join-Path $FrontendRoot "src") -Recurse -File -Filter "*.test.mjs")
        Write-Host "Discovered $($discovered.Count) frontend test files."
        Invoke-Step $Name $NpmProgram @("run", "test:runtime") $FrontendRoot
        return
    }
    $resolved = Resolve-FrontendTests $Tests
    Invoke-Step $Name "node" (@("--test") + $resolved) $FrontendRoot
}

if ($Tier -eq "fast" -and $GoPackage.Count -eq 0 -and $FrontendTest.Count -eq 0) {
    throw "fast verification requires -GoPackage and/or -FrontendTest"
}
if ($Tier -eq "fast" -and -not [string]::IsNullOrWhiteSpace($GoTest) -and $GoPackage.Count -eq 0) {
    throw "-GoTest requires at least one -GoPackage"
}
if ($Tier -eq "domain" -and [string]::IsNullOrWhiteSpace($Domain)) {
    throw "domain verification requires -Domain"
}
if ($Tier -ne "fast" -and ($GoPackage.Count -ne 0 -or $FrontendTest.Count -ne 0 -or -not [string]::IsNullOrWhiteSpace($GoTest))) {
    throw "GoPackage, GoTest, and FrontendTest selectors are only valid with -Tier fast"
}
if ($SkipGoBuild -and $Tier -ne "release") {
    throw "-SkipGoBuild is only valid with -Tier release"
}

$environmentNames = @(
    "GO_STOCK_RUN_INTEGRATION",
    "RUN_INTEGRATION_TESTS",
    "GO_STOCK_LIVE_EASTMONEY",
    "GO_STOCK_DB_LOG_LEVEL",
    "GO_STOCK_DB_PATH",
    "GO_STOCK_MINUTE_DB_PATH"
)
$previousEnvironment = @{}
foreach ($name in $environmentNames) {
    $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}

$validationBase = if (Test-Path -LiteralPath "H:\Download" -PathType Container) {
    "H:\Download\go-stock-validation"
} else {
    Join-Path ([System.IO.Path]::GetTempPath()) "go-stock-validation"
}
$validationBase = [System.IO.Path]::GetFullPath($validationBase).TrimEnd('\', '/')
$validationRun = [System.IO.Path]::GetFullPath((Join-Path $validationBase ([Guid]::NewGuid().ToString("N"))))
if (-not $validationRun.StartsWith($validationBase + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Validation directory escaped its intended root"
}
New-Item -ItemType Directory -Force -Path $validationRun | Out-Null

$overall = [Diagnostics.Stopwatch]::StartNew()
try {
    [Environment]::SetEnvironmentVariable("GO_STOCK_RUN_INTEGRATION", $null, "Process")
    [Environment]::SetEnvironmentVariable("RUN_INTEGRATION_TESTS", $null, "Process")
    [Environment]::SetEnvironmentVariable("GO_STOCK_LIVE_EASTMONEY", $null, "Process")
    [Environment]::SetEnvironmentVariable("GO_STOCK_DB_LOG_LEVEL", "silent", "Process")
    # Tests own their disposable fixtures. Clearing inherited application paths
    # preserves configuration fallback tests and prevents separate Go package
    # processes from sharing one validation database.
    [Environment]::SetEnvironmentVariable("GO_STOCK_DB_PATH", $null, "Process")
    [Environment]::SetEnvironmentVariable("GO_STOCK_MINUTE_DB_PATH", $null, "Process")

    Invoke-Step "diff whitespace check" "git" @("-c", "core.safecrlf=false", "diff", "--check") $ProjectRoot

    switch ($Tier) {
        "fast" {
            if ($GoPackage.Count -ne 0) {
                Invoke-GoTest "targeted Go tests" $GoPackage $GoTest
            }
            if ($FrontendTest.Count -ne 0) {
                Invoke-FrontendTests "targeted frontend tests" $FrontendTest
            }
            Write-Host "Not run: domain and release verification."
        }
        "domain" {
            switch ($Domain) {
                "data" { Invoke-GoTest "data domain tests" @("./backend/data") }
                "research" {
                    Invoke-GoTest "research domain tests" @("./backend/research")
                    Invoke-GoTest "research data boundary tests" @("./backend/data") "Research"
                    Invoke-GoTest "research application boundary tests" @(".") "Research"
                }
                "research2" {
                    Invoke-GoTest "research2 domain tests" @("./backend/research2")
                    Invoke-GoTest "research2 data boundary tests" @("./backend/data") "Research2"
                    Invoke-GoTest "research2 application boundary tests" @(".") "Research2"
                }
                "migrations" { Invoke-GoTest "migration domain tests" @("./internal/migrations") }
                "frontend" { Invoke-FrontendTests "frontend runtime tests" }
                "api" {
                    Invoke-Step "OpenAPI contract check" "go" @("run", "./cmd/openapi-contract") $ProjectRoot
                    Invoke-GoTest "API boundary tests" @(".") "(API|Route|Contract)"
                }
                "tools" { Invoke-Step "network-audit unit tests" "go" @("test", "./...") (Join-Path $ProjectRoot "tools\network-audit") }
            }
            Write-Host "Not run: release verification."
        }
        "release" {
            Invoke-GoTest "main Go tests" $MainGoPackages
            Invoke-Step "network-audit unit tests" "go" @("test", "./...") (Join-Path $ProjectRoot "tools\network-audit")
            Invoke-Step "Go vet" "go" (@("vet") + $MainGoPackages) $ProjectRoot
            Invoke-Step "root go.mod check" "go" @("mod", "tidy", "-diff") $ProjectRoot
            Invoke-Step "network-audit go.mod check" "go" @("mod", "tidy", "-diff") (Join-Path $ProjectRoot "tools\network-audit")
            Invoke-Step "OpenAPI contract check" "go" @("run", "./cmd/openapi-contract") $ProjectRoot
            Invoke-FrontendTests "frontend runtime tests"
            Invoke-Step "frontend lint" $NpmProgram @("run", "lint") $FrontendRoot
            Invoke-Step "frontend production build" $NpmProgram @("run", "build") $FrontendRoot
            if (-not $SkipGoBuild) {
                $binaryName = if ($IsWindows) { "go-stock-verify.exe" } else { "go-stock-verify" }
                Invoke-Step "Go production build" "go" @("build", "-trimpath", "-o", (Join-Path $validationRun $binaryName), ".") $ProjectRoot
            }
            Write-Host "Full local release gate passed. Deployment was not run."
        }
    }

    $overall.Stop()
    Write-Host "Verification tier '$Tier' passed in $([math]::Round($overall.Elapsed.TotalSeconds, 2))s."
} catch {
    $overall.Stop()
    Write-Error "Verification tier '$Tier' failed after $([math]::Round($overall.Elapsed.TotalSeconds, 2))s: $($_.Exception.Message)"
    exit 1
} finally {
    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name], "Process")
    }
    if (Test-Path -LiteralPath $validationRun -PathType Container) {
        $resolvedCleanup = [System.IO.Path]::GetFullPath($validationRun)
        if (-not $resolvedCleanup.StartsWith($validationBase + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to clean validation directory outside its intended root"
        }
        Remove-Item -LiteralPath $resolvedCleanup -Recurse -Force
    }
    if (Test-Path -LiteralPath $validationBase -PathType Container) {
        $remainingValidationFiles = @(Get-ChildItem -LiteralPath $validationBase -Force)
        if ($remainingValidationFiles.Count -eq 0) {
            Remove-Item -LiteralPath $validationBase -Force
        }
    }
}
