# Shared by publish and the release verification child process. Receipts contain
# results only; commands always come from the checked-in scripts, never JSON.
function Write-ReleaseState {
    param([string]$Path, [System.Collections.IDictionary]$State)
    $parent = Split-Path -Parent $Path
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    $temporary = "$Path.tmp"
    [IO.File]::WriteAllText($temporary, ($State | ConvertTo-Json -Depth 16), [Text.UTF8Encoding]::new($false))
    [IO.File]::Move($temporary, $Path, $true)
}

function Read-ReleaseState {
    param([string]$Path)
    return (Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json -AsHashtable)
}

function Get-ReleaseTextHash {
    param([string]$Text)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { return [Convert]::ToHexString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Text))).ToLowerInvariant() }
    finally { $sha.Dispose() }
}

function Get-ReleaseTreeHash {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) { return '' }
    $files = @(Get-ChildItem -LiteralPath $Path -File -Recurse | Sort-Object FullName)
    if ($files.Count -eq 0) { return '' }
    $entries = foreach ($file in $files) {
        [IO.Path]::GetRelativePath($Path, $file.FullName).Replace('\', '/') + ':' + (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
    }
    return Get-ReleaseTextHash ($entries -join "`n")
}

function Get-ReleaseInputs {
    param([string]$Root)
    Push-Location $Root
    try {
    $commit = (& git -C $Root rev-parse HEAD)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot resolve verification commit' }
    $go = (& go version); if ($LASTEXITCODE -ne 0) { throw 'Go is unavailable' }
    $node = (& node --version); if ($LASTEXITCODE -ne 0) { throw 'Node is unavailable' }
    $npmProgram = if ($IsWindows) { 'npm.cmd' } else { 'npm' }
    $npm = (& $npmProgram --version); if ($LASTEXITCODE -ne 0) { throw 'npm is unavailable' }
    $target = @(& go env GOOS GOARCH GOFLAGS CGO_ENABLED)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect Go target' }
    $inputs = [ordered]@{commit=[string]$commit; go=[string]$go; node=[string]$node; npm=[string]$npm; powershell=$PSVersionTable.PSVersion.ToString(); target=$target}
    return @{identity=(Get-ReleaseTextHash ($inputs | ConvertTo-Json -Compress)); values=$inputs}
    } finally { Pop-Location }
}

function Invoke-ReleaseStage {
    param([string]$StatePath, [string]$Name, [string]$InputIdentity, [scriptblock]$Action, [scriptblock]$OutputHash, [switch]$Always)
    $state = Read-ReleaseState $StatePath
    $prior = $state.steps[$Name]
    $outputOK = $true
    if ($OutputHash) { $outputOK = $prior -and $prior.outputHash -and $prior.outputHash -eq (& $OutputHash) }
    if (-not $Always -and $prior -and $prior.status -eq 'passed' -and $prior.inputIdentity -eq $InputIdentity -and $outputOK) {
        $prior.lastRunSeconds = 0
        Write-ReleaseState $StatePath $state
        Write-Host "REUSE $Name"
        return
    }
    $logDir = Join-Path (Split-Path -Parent $StatePath) ([IO.Path]::GetFileNameWithoutExtension($StatePath) + '-logs')
    New-Item -ItemType Directory -Force -Path $logDir | Out-Null
    $logPath = Join-Path $logDir (($Name -replace '[^a-zA-Z0-9_-]', '-') + '-' + [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmssfff') + '.log')
    $step = @{status='running'; inputIdentity=$InputIdentity; startedAt=[DateTime]::UtcNow.ToString('o'); completedAt=$null; seconds=0; lastRunSeconds=0; outputHash=''; log=$logPath; error=''}
    $state.steps[$Name] = $step
    Write-ReleaseState $StatePath $state
    Write-Host "==> $Name"
    $watch = [Diagnostics.Stopwatch]::StartNew()
    try {
        & $Action *> $logPath
        if ($OutputHash) {
            $step.outputHash = & $OutputHash
            if (-not $step.outputHash) { throw "$Name did not produce its required output" }
        }
        $step.status = 'passed'
    } catch {
        $step.status, $step.error = 'failed', $_.Exception.Message
        throw "$Name failed: $($step.error). Log: $logPath"
    } finally {
        $watch.Stop()
        $step.seconds = [math]::Round($watch.Elapsed.TotalSeconds, 3)
        $step.lastRunSeconds = $step.seconds
        $step.completedAt = [DateTime]::UtcNow.ToString('o')
        # The action may be a child verifier that has added its own steps.
        $state = Read-ReleaseState $StatePath
        $state.steps[$Name] = $step
        Write-ReleaseState $StatePath $state
        Write-Host "$($step.status.ToUpperInvariant()) $Name ($($step.seconds)s)"
    }
}
