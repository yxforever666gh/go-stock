param([string]$JobPath='')

# A one-shot child completes the existing candidate operation even if the
# publishing terminal disappears. No service, retry loop or JSON commands.
function Get-BuildProcessIdentity {
    param([Diagnostics.Process]$Process)
    return @{pid=$Process.Id;startedAt=$Process.StartTime.ToUniversalTime().ToString('o');executable=$Process.Path}
}

function Get-MatchingBuildProcess {
    param($Identity)
    if (-not $Identity) { return $null }
    try { $process=[Diagnostics.Process]::GetProcessById([int]$Identity.pid) }
    catch [ArgumentException] { return $null }
    if ($process.HasExited) { $process.Dispose(); return $null }
    if ($process.Path -ne $Identity.executable -or $process.StartTime.ToUniversalTime().Ticks -ne ([datetimeoffset]$Identity.startedAt).UtcDateTime.Ticks) {
        $process.Dispose(); throw 'Build PID identity mismatch; refusing to attach or restart'
    }
    return $process
}

function Start-CandidateWorker {
    param([string]$Path,$Job)
    # The child owns its logs; the publishing terminal owns no output pipes.
    # ArgumentList preserves spaces without invoking a command shell.
    try {
        New-Item -ItemType File -Path $Job.stdout,$Job.stderr | Out-Null
        $info=[Diagnostics.ProcessStartInfo]::new((Get-Command pwsh -CommandType Application | Select-Object -First 1).Source)
        $info.WorkingDirectory=$ProjectRoot;$info.UseShellExecute=$false;$info.CreateNoWindow=$true
        foreach($argument in @('-NoProfile','-File',(Join-Path $ScriptDir 'release-build-process.ps1'),'-JobPath',$Path)){$info.ArgumentList.Add($argument)}
        $process=[Diagnostics.Process]::Start($info)
    } catch {
        $Job.status='failed';$Job.error="Worker could not start: $($_.Exception.Message)"
        Write-ReleaseState $Path $Job
        throw
    }
    return $process
}

function Wait-CandidateWorker {
    param([string]$Path,[string]$InputIdentity)
    $heartbeat=[Diagnostics.Stopwatch]::StartNew()
    $handshake=[Diagnostics.Stopwatch]::StartNew()
    while ($true) {
        $job=Read-ReleaseState $Path
        if ($job.inputIdentity -ne $InputIdentity) { throw 'Build inputs changed; cannot attach to previous build' }
        if ($job.status -in @('passed','failed','interrupted') -and $job.nativeStatus -notin @('launching','running')) {
            if($job.status -ne 'interrupted') {
                $finishing=Get-MatchingBuildProcess $job.process
                if($finishing){[void]$finishing.WaitForExit(250);$finishing.Dispose();continue}
            }
            return $job
        }
        $process=if($job.status -in @('launching','running')){Get-MatchingBuildProcess $job.process}else{$null}
        $native=if($job.nativeStatus -eq 'running'){Get-MatchingBuildProcess $job.nativeProcess}else{$null}
        if (-not $process -and -not $native) {
            $latest=Read-ReleaseState $Path
            if($latest.status -ne $job.status -or $latest.nativeStatus -ne $job.nativeStatus){continue}
            if ($job.status -eq 'launching' -or $job.nativeStatus -eq 'launching') {
                if(Test-Path -LiteralPath "$Path.bootstrap.log"){throw "Candidate worker bootstrap failed: $(Get-Content -LiteralPath "$Path.bootstrap.log" -Raw)"}
                if($handshake.Elapsed.TotalSeconds -lt 15){Start-Sleep -Milliseconds 200;continue}
                throw 'Build launch outcome unknown; inspect the build journal before restarting'
            }
            $job.status='interrupted';$job.error='Previous execution did not end normally';$job.observedAt=[DateTime]::UtcNow.ToString('o')
            $job.nativeStatus='interrupted'
            Write-ReleaseState $Path $job
            "INTERRUPTED $($job.error)" | Add-Content -LiteralPath $job.log
            return $job
        }
        if($process){$process.Dispose()};if($native){$native.Dispose()}
        if ($heartbeat.Elapsed.TotalSeconds -ge 15) {
            [Console]::WriteLine("Candidate build still running; journal: $Path")
            $heartbeat.Restart()
        }
        Start-Sleep -Milliseconds 250
    }
}

function Invoke-TrackedCandidateBuild {
    param($Context,[string]$RecordPath)
    $inputs=(Get-ReleaseInputs $ProjectRoot).identity+':'+(Get-ReleaseTreeHash (Join-Path $ProjectRoot 'frontend/dist'))
    $record=Read-ReleaseState $RecordPath
    if(-not $record.Contains('buildJobs')){$record.buildJobs=@()}
    Wait-ExistingCandidateBuilds
    if(Test-Path -LiteralPath $Context.ReleaseDir) { Assert-CandidateArtifactInputs $Context $RecordPath; return }
    $path=Join-Path $DeploymentsRoot ('build-job-'+[Guid]::NewGuid().ToString('N')+'.json')
    $job=@{status='launching';command='go build candidate';projectRoot=$ProjectRoot;runtimeRoot=$RuntimeRoot;recordPath=$RecordPath;inputIdentity=$inputs;process=$null;nativeProcess=$null;nativeStatus='not_started';exitCode=$null;error='';startedAt=[DateTime]::UtcNow.ToString('o');staging='';log="$path.log";stdout="$path.stdout.log";stderr="$path.stderr.log"}
    "START candidate build $($job.startedAt)" | Set-Content -LiteralPath $job.log
    Write-ReleaseState $path $job
    $record.buildJobs += $path
    Write-ReleaseState $RecordPath $record
    $worker=$null
    try { $worker=Start-CandidateWorker $path $job }
    catch { "LAUNCH ERROR $($_.Exception.Message)" | Add-Content -LiteralPath $job.log; throw }
    try { $result=Wait-CandidateWorker $path $inputs }
    finally {if($worker){$worker.Dispose()}}
    if($result.status -ne 'passed'){throw "Candidate $($result.status): $($result.error). Journal: $path"}
    Assert-CandidateArtifactInputs $Context $RecordPath
}

function Wait-ExistingCandidateBuilds {
    foreach($file in @(Get-ChildItem -LiteralPath $DeploymentsRoot -Filter 'build-job-*.json' -File -ErrorAction SilentlyContinue)) {
        $job=Read-ReleaseState $file.FullName
        if($job.projectRoot -eq $ProjectRoot -and ($job.status -in @('launching','running') -or $job.nativeStatus -in @('launching','running'))) {
            $inputs=(Get-ReleaseInputs $ProjectRoot).identity+':'+(Get-ReleaseTreeHash (Join-Path $ProjectRoot 'frontend/dist'))
            [void](Wait-CandidateWorker $file.FullName $inputs)
        }
    }
}

function Start-BuildCompilerProcess {
    param([string[]]$Arguments)
    $info=[Diagnostics.ProcessStartInfo]::new((Get-Command go -CommandType Application | Select-Object -First 1).Source)
    $info.WorkingDirectory=$ProjectRoot; $info.UseShellExecute=$false; $info.CreateNoWindow=$true
    $info.RedirectStandardOutput=$true;$info.RedirectStandardError=$true
    foreach($argument in $Arguments){$info.ArgumentList.Add($argument)}
    return [Diagnostics.Process]::Start($info)
}

function Invoke-TrackedGoCompiler {
    param([string[]]$Arguments)
    $job=Read-ReleaseState $script:BuildJobPath
    $stdout=$null;$stderr=$null;$process=$null
    try {
        $stdout=[IO.File]::Open($job.stdout,[IO.FileMode]::Append,[IO.FileAccess]::Write,[IO.FileShare]::ReadWrite)
        $stderr=[IO.File]::Open($job.stderr,[IO.FileMode]::Append,[IO.FileAccess]::Write,[IO.FileShare]::ReadWrite)
        $job.nativeStatus='launching';Write-ReleaseState $script:BuildJobPath $job
        try { $process=Start-BuildCompilerProcess $Arguments }
        catch { $job.nativeStatus='start_failed';$job.error=$_.Exception.Message;Write-ReleaseState $script:BuildJobPath $job;throw }
        $outTask=$process.StandardOutput.BaseStream.CopyToAsync($stdout)
        $errTask=$process.StandardError.BaseStream.CopyToAsync($stderr)
        try { $job.nativeProcess=Get-BuildProcessIdentity $process }
        catch { if(-not $process.HasExited){throw} }
        if($job.nativeProcess){$job.nativeStatus='running';Write-ReleaseState $script:BuildJobPath $job}
        while(-not $process.WaitForExit(15000)){Write-Host "Go compiler running PID $($process.Id)"}
        $job.nativeStatus='exited';$job.nativeExitCode=$process.ExitCode
        Write-ReleaseState $script:BuildJobPath $job
        [void]$outTask.GetAwaiter().GetResult();[void]$errTask.GetAwaiter().GetResult()
        if($process.ExitCode -ne 0){throw "Go build failed with exit code $($process.ExitCode)"}
    } finally {if($stdout){$stdout.Dispose()};if($stderr){$stderr.Dispose()};if($process){$process.Dispose()}}
}

if ($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference='Stop'
    $workerRequestPath=$JobPath
    trap { [IO.File]::AppendAllText("$workerRequestPath.bootstrap.log",($_ | Out-String)); exit 1 }
    . (Join-Path $PSScriptRoot 'release-state.ps1')
    $request=Read-ReleaseState $JobPath
    . (Join-Path $PSScriptRoot 'release.ps1') -RuntimeRootOverride $request.runtimeRoot
    $script:BuildJobPath=Assert-ChildPath $workerRequestPath $DeploymentsRoot
    $request=Read-ReleaseState $script:BuildJobPath
    if($request.projectRoot -ne $ProjectRoot -or $request.status -ne 'launching'){throw 'Invalid candidate worker request'}
    [void](Assert-ChildPath $request.recordPath $DeploymentsRoot)
    $request.process=Get-BuildProcessIdentity ([Diagnostics.Process]::GetCurrentProcess())
    $request.status='running';Write-ReleaseState $script:BuildJobPath $request
    try {
        $actual=(Get-ReleaseInputs $ProjectRoot).identity+':'+(Get-ReleaseTreeHash (Join-Path $ProjectRoot 'frontend/dist'))
        if($actual -ne $request.inputIdentity){throw 'Candidate worker inputs changed'}
        Invoke-ReleaseCandidateBuild (Get-Context) $request.recordPath -Worker *>> $request.log
        $request=Read-ReleaseState $script:BuildJobPath
        $request.status='passed';$request.exitCode=0
    } catch {
        $failure=$_.Exception.Message
        $request=Read-ReleaseState $script:BuildJobPath
        $request.status='failed';$request.exitCode=1;$request.error=$failure
        Write-Host "ERROR $failure"
    } finally {
        $request.endedAt=[DateTime]::UtcNow.ToString('o')
        Write-ReleaseState $script:BuildJobPath $request
        "END $($request.status) exit=$($request.exitCode) $($request.error)" | Add-Content -LiteralPath $request.log
    }
    exit $request.exitCode
}
