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
    if (-not $state.Contains('stepHistory')) { $state.stepHistory = @() }
    if ($prior) {
        $snapshot = $prior | ConvertTo-Json -Depth 12 | ConvertFrom-Json -AsHashtable
        $snapshot.name = $Name
        if ($snapshot.status -eq 'running') { $snapshot.status='interrupted'; $snapshot.error='Previous execution did not end normally'; $snapshot.seconds=$null }
        $state.stepHistory += $snapshot
    }
    $outputOK = $true
    if ($OutputHash) { $outputOK = $prior -and $prior.outputHash -and $prior.outputHash -eq (& $OutputHash) }
    if (-not $Always -and $prior -and $prior.status -eq 'passed' -and $prior.inputIdentity -eq $InputIdentity -and $outputOK) {
        $prior.lastRunSeconds = 0
        $state.stepHistory += @{name=$Name;status='reused';at=[DateTime]::UtcNow.ToString('o');inputIdentity=$InputIdentity}
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
    "START $Name $($step.startedAt)" | Set-Content -LiteralPath $logPath
    $watch = [Diagnostics.Stopwatch]::StartNew()
    try {
        & $Action *>> $logPath
        if ($OutputHash) {
            $step.outputHash = & $OutputHash
            if (-not $step.outputHash) { throw "$Name did not produce its required output" }
        }
        $step.status = 'passed'
    } catch {
        $step.status, $step.error = 'failed', $_.Exception.Message
        "ERROR $($step.error)" | Add-Content -LiteralPath $logPath
        throw "$Name failed: $($step.error). Log: $logPath"
    } finally {
        $watch.Stop()
        $step.seconds = [math]::Round($watch.Elapsed.TotalSeconds, 3)
        $step.lastRunSeconds = $step.seconds
        $step.completedAt = [DateTime]::UtcNow.ToString('o')
        "END $Name $($step.status) $($step.seconds)s" | Add-Content -LiteralPath $logPath
        # The action may be a child verifier that has added its own steps.
        $state = Read-ReleaseState $StatePath
        $state.steps[$Name] = $step
        Write-ReleaseState $StatePath $state
        Write-Host "$($step.status.ToUpperInvariant()) $Name ($($step.seconds)s)"
    }
}

# Top-level attempts own elapsed time. Nested stages are diagnostics, never
# added to execution totals. Each attempt has its own durable file.
$script:ReleaseAttemptPath = ''
function Start-ReleaseAttempt {
    param([string]$Root)
    $script:ReleaseAttemptPath = Join-Path $Root ('attempt-'+[Guid]::NewGuid().ToString('N')+'.json')
    Write-ReleaseState $script:ReleaseAttemptPath @{startedAt=[DateTime]::UtcNow.ToString('o');endedAt=$null;seconds=$null;status='running';error='';steps=@{};diagnostics=@();publishRecord=''}
}

function Connect-ReleaseAttempt {
    param([string]$RecordPath)
    $attempt = Read-ReleaseState $script:ReleaseAttemptPath
    if ($attempt.publishRecord -eq $RecordPath) { return }
    $record = Read-ReleaseState $RecordPath
    if (-not $record.Contains('attempts')) {
        $record.attempts=@()
        $record.legacyTimingUnknown=([datetimeoffset]$record.createdAt -lt [datetimeoffset]$attempt.startedAt)
    }
    foreach ($path in $record.attempts) {
        $old = Read-ReleaseState $path
        if ($old.status -eq 'running') {
            $old.status='interrupted'; $old.error='Previous execution did not end normally'; $old.observedAt=[DateTime]::UtcNow.ToString('o')
            foreach($diagnostic in $old.diagnostics){if($diagnostic.status -eq 'running'){$diagnostic.status='interrupted';$diagnostic.seconds=$null}}
            Write-ReleaseState $path $old
        }
    }
    $record.attempts += $script:ReleaseAttemptPath
    $attempt.publishRecord=$RecordPath
    Write-ReleaseState $script:ReleaseAttemptPath $attempt
    Write-ReleaseState $RecordPath $record
}

function Get-ReleaseTiming {
    param($Record)
    $attempts=@($Record.attempts | ForEach-Object { Read-ReleaseState $_ } | Sort-Object { [datetimeoffset]$_.startedAt })
    $first=[datetimeoffset]$attempts[0].startedAt
    if ($Record.legacyTimingUnknown) { $first=[datetimeoffset]$Record.createdAt }
    $last=$attempts[-1]
    $end=if($last.endedAt){[datetimeoffset]$last.endedAt}else{[datetimeoffset]::UtcNow}
    $execution=0.0; $gap=0.0; $unknown=[bool]$Record.legacyTimingUnknown
    for($i=0;$i -lt $attempts.Count;$i++) {
        if($null -eq $attempts[$i].seconds){$unknown=$true}else{$execution += $attempts[$i].seconds}
        if($i -gt 0) {
            if($attempts[$i-1].endedAt){$gap += [math]::Max(0.0,([datetimeoffset]$attempts[$i].startedAt-[datetimeoffset]$attempts[$i-1].endedAt).TotalSeconds)}else{$unknown=$true}
        }
    }
    return @{elapsedSeconds=[math]::Round(($end-$first).TotalSeconds,3);executionSeconds=$(if($unknown){$null}else{[math]::Round($execution,3)});knownExecutionSeconds=[math]::Round($execution,3);currentSeconds=$last.seconds;interruptionSeconds=$(if($unknown){$null}else{[math]::Round($gap,3)});knownInterruptionSeconds=[math]::Round($gap,3)}
}

function Complete-ReleaseAttempt {
    param([string]$Status,[string]$ErrorText,[double]$Seconds)
    $attempt=Read-ReleaseState $script:ReleaseAttemptPath
    $attempt.status=$Status; $attempt.error=$ErrorText; $attempt.seconds=[math]::Round($Seconds,3); $attempt.endedAt=[DateTime]::UtcNow.ToString('o')
    Write-ReleaseState $script:ReleaseAttemptPath $attempt
    if ($attempt.publishRecord) {
        $record=Read-ReleaseState $attempt.publishRecord
        $record.timing=Get-ReleaseTiming $record
        Write-ReleaseState $attempt.publishRecord $record
        $t=$record.timing
    } else {
        $t=Get-ReleaseTiming @{attempts=@($script:ReleaseAttemptPath);legacyTimingUnknown=$false;createdAt=$attempt.startedAt}
    }
        $active=if($null -eq $t.executionSeconds){'unknown (known '+$t.knownExecutionSeconds+'s)'}else{"$($t.executionSeconds)s"}
        $gap=if($null -eq $t.interruptionSeconds){'unknown (known '+$t.knownInterruptionSeconds+'s)'}else{"$($t.interruptionSeconds)s"}
        Write-Host "Release elapsed $($t.elapsedSeconds)s; accumulated execution $active; this attempt $($t.currentSeconds)s; interruption intervals $gap"
    Write-Host "Attempt: $script:ReleaseAttemptPath"
}

function Invoke-ReleaseDiagnostic {
    param([string]$Name,[scriptblock]$Action)
    $watch=[Diagnostics.Stopwatch]::StartNew()
    $entry=@{name=$Name;startedAt=[DateTime]::UtcNow.ToString('o');status='running';seconds=$null;error=''}
    $index=-1
    if($script:ReleaseAttemptPath) {
        $attempt=Read-ReleaseState $script:ReleaseAttemptPath
        $index=$attempt.diagnostics.Count; $attempt.diagnostics += $entry
        Write-ReleaseState $script:ReleaseAttemptPath $attempt
    }
    try { & $Action; $entry.status='passed' }
    catch { $entry.status='failed';$entry.error=$_.Exception.Message;throw }
    finally {
        $entry.seconds=[math]::Round($watch.Elapsed.TotalSeconds,3);$entry.endedAt=[DateTime]::UtcNow.ToString('o')
        if($index -ge 0) {
            $attempt=Read-ReleaseState $script:ReleaseAttemptPath
            $attempt.diagnostics[$index]=$entry
            Write-ReleaseState $script:ReleaseAttemptPath $attempt
        }
        Write-Host "$Name $($entry.status) ($($entry.seconds)s)"
    }
}
