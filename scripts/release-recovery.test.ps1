$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'release-state.ps1')
. (Join-Path $PSScriptRoot 'release-build-process.ps1')
$realMatcher=${function:Get-MatchingBuildProcess}
$realIdentity=${function:Get-BuildProcessIdentity}
function Assert-True {param($Value,$Message);if(-not $Value){throw $Message}}
function Assert-Fails {param([scriptblock]$Action,[string]$Message);try{& $Action}catch{if($_.Exception.Message -notlike "*$Message*"){throw};return};throw "Expected failure: $Message"}
$base='H:\Download\go-stock-recovery-tests'
$root=Join-Path $base ([Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $root | Out-Null
try {
    $a=Join-Path $root 'a.json';$b=Join-Path $root 'b.json'
    Write-ReleaseState $a @{startedAt='2026-01-01T00:00:00Z';endedAt='2026-01-01T00:00:10Z';seconds=10;status='failed'}
    Write-ReleaseState $b @{startedAt='2026-01-01T00:00:40Z';endedAt='2026-01-01T00:01:00Z';seconds=20;status='complete'}
    $record=@{attempts=@($a,$b);legacyTimingUnknown=$false;createdAt='2026-01-01T00:00:00Z'}
    $timing=Get-ReleaseTiming $record
    Assert-True ($timing.elapsedSeconds -eq 60 -and $timing.executionSeconds -eq 30 -and $timing.interruptionSeconds -eq 30 -and $timing.currentSeconds -eq 20) 'Attempt totals double counted nested stages or lost intervals'
    $old=Read-ReleaseState $a;$old.seconds=$null;$old.endedAt=$null;Write-ReleaseState $a $old
    $timing=Get-ReleaseTiming $record
    Assert-True ($null -eq $timing.executionSeconds -and $null -eq $timing.interruptionSeconds -and $timing.knownExecutionSeconds -eq 20) 'Killed attempt invented execution time'
    $record.legacyTimingUnknown=$true
    Assert-True ($null -eq (Get-ReleaseTiming $record).executionSeconds) 'Legacy timing was invented'

    $path=Join-Path $root 'publish.json'
    Write-ReleaseState $path @{createdAt='2026-01-01T00:00:00Z';steps=@{}}
    Start-ReleaseAttempt $root;Connect-ReleaseAttempt $path
    $firstAttempt=$script:ReleaseAttemptPath
    Start-ReleaseAttempt $root;Connect-ReleaseAttempt $path
    Assert-True ((Read-ReleaseState $firstAttempt).status -eq 'interrupted') 'Orphan attempt not marked interrupted'
    Invoke-ReleaseDiagnostic 'parent' {Invoke-ReleaseDiagnostic 'child' {}}
    Complete-ReleaseAttempt 'complete' '' 2
    $result=Read-ReleaseState $path
    Assert-True ($result.attempts.Count -eq 2 -and $null -eq $result.timing.executionSeconds) 'Attempts overwritten or old execution guessed'
    $script:ReleaseAttemptPath=''
    Invoke-ReleaseStage $path 'silent' 'same' {}
    Invoke-ReleaseStage $path 'silent' 'same' {}
    Assert-Fails {Invoke-ReleaseStage $path 'silent' 'different' {throw 'simulated exit 7'}} 'simulated exit 7'
    $state=Read-ReleaseState $path
    $log=Get-Content $state.steps.silent.log -Raw
    Assert-True ($state.stepHistory.Count -eq 3 -and $log.Contains('START') -and $log.Contains('ERROR') -and $log.Contains('END')) 'Empty output/failure lost lifecycle history'
    $state.steps.silent.status='running';Write-ReleaseState $path $state
    Invoke-ReleaseStage $path 'silent' 'different' {}
    Assert-True ((Read-ReleaseState $path).stepHistory[-1].status -eq 'interrupted') 'Unfinished stage was overwritten'

    $self=[Diagnostics.Process]::GetCurrentProcess()
    $identity=Get-BuildProcessIdentity $self
    $same=Get-MatchingBuildProcess $identity;$same.Dispose()
    $bad=$identity.Clone();$bad.startedAt='2000-01-01T00:00:00Z'
    Assert-Fails {Get-MatchingBuildProcess $bad} 'PID identity mismatch'
    $bad=$identity.Clone();$bad.executable='wrong.exe'
    Assert-Fails {Get-MatchingBuildProcess $bad} 'PID identity mismatch'

    # Simulated process boundaries: no compiler, network, service or database.
    $script:polls=0
    function Get-MatchingBuildProcess {
        param($Identity)
        if(-not $Identity){return $null}
        $script:polls++
        if($script:polls -eq 1){return [Diagnostics.Process]::GetCurrentProcess()}
        return $null
    }
    function Start-Sleep {param($Milliseconds)}
    $jobPath=Join-Path $root 'job.json'
    $job=@{status='running';nativeStatus='not_started';process=@{pid=1};nativeProcess=$null;inputIdentity='inputs';error='';log=(Join-Path $root 'job.log')}
    Write-ReleaseState $jobPath $job
    $result=Wait-CandidateWorker $jobPath 'inputs'
    Assert-True ($polls -eq 2 -and $result.status -eq 'interrupted') 'Live process was not awaited before interruption recovery'
    Assert-Fails {Wait-CandidateWorker $jobPath 'changed'} 'inputs changed'
    $job.status='passed';$job.nativeStatus='exited';$job.exitCode=0;Write-ReleaseState $jobPath $job
    Assert-True ((Wait-CandidateWorker $jobPath 'inputs').exitCode -eq 0) 'Completed child result was not reusable'
    $job.status='failed';$job.exitCode=7;$job.error='compiler exit 7';Write-ReleaseState $jobPath $job
    Assert-True ((Wait-CandidateWorker $jobPath 'inputs').exitCode -eq 7) 'Child exit code was lost'
    function Start-BuildCompilerProcess {
        param($Arguments)
        if($script:fakeStartFailure){throw 'simulated launch failure'}
        $fake=[pscustomobject]@{Id=42;ExitCode=$script:fakeExit;HasExited=$true;StandardOutput=@{BaseStream=[IO.MemoryStream]::new([Text.Encoding]::UTF8.GetBytes('stdout marker'))};StandardError=@{BaseStream=[IO.MemoryStream]::new([Text.Encoding]::UTF8.GetBytes('stderr marker'))}}
        $fake | Add-Member ScriptMethod WaitForExit {param($Milliseconds);return $true}
        $fake | Add-Member ScriptMethod Dispose {}
        return $fake
    }
    function Get-BuildProcessIdentity {param($Process);if($script:fakeFastExit){throw 'Process already exited'};return @{pid=$Process.Id;startedAt='2026-01-01T00:00:00Z';executable='fixture-go.exe'}}
    $script:BuildJobPath=$jobPath
    $job=Read-ReleaseState $jobPath;$job.stdout="$jobPath.stdout";$job.stderr="$jobPath.stderr";Write-ReleaseState $jobPath $job
    $script:fakeExit=0
    $script:fakeFastExit=$false;$script:fakeStartFailure=$false
    Invoke-TrackedGoCompiler @('build')
    Assert-True ((Read-ReleaseState $jobPath).nativeExitCode -eq 0) 'Silent compiler completion was not saved'
    Assert-True ((Get-Content $job.stdout -Raw) -eq 'stdout marker' -and (Get-Content $job.stderr -Raw) -eq 'stderr marker') 'Native streams were not written to durable files'
    $script:fakeExit=7
    Assert-Fails {Invoke-TrackedGoCompiler @('build')} 'exit code 7'
    Assert-True ((Read-ReleaseState $jobPath).nativeExitCode -eq 7) 'Failed compiler exit code was not saved'
    $job=Read-ReleaseState $jobPath;$job.nativeProcess=$null;Write-ReleaseState $jobPath $job
    $script:fakeFastExit=$true
    Assert-Fails {Invoke-TrackedGoCompiler @('build')} 'exit code 7'
    Assert-True ((Read-ReleaseState $jobPath).nativeStatus -eq 'exited') 'Fast exit became an unknown launch'
    $script:fakeStartFailure=$true
    Assert-Fails {Invoke-TrackedGoCompiler @('build')} 'launch failure'
    Assert-True ((Read-ReleaseState $jobPath).nativeStatus -eq 'start_failed') 'Proven start failure became permanently unknown'

    # Exercise the real one-shot PowerShell worker against a mocked release
    # boundary, including paths with spaces and persistent stdout/stderr.
    Set-Item Function:Get-MatchingBuildProcess $realMatcher
    Set-Item Function:Get-BuildProcessIdentity $realIdentity
    $ProjectRoot=Join-Path $root 'worker fixture';$ScriptDir=Join-Path $ProjectRoot 'scripts'
    $DeploymentsRoot=Join-Path $ProjectRoot 'runtime/deployments'
    New-Item -ItemType Directory -Force -Path $ScriptDir,$DeploymentsRoot | Out-Null
    Copy-Item (Join-Path $PSScriptRoot 'release-state.ps1'),(Join-Path $PSScriptRoot 'release-build-process.ps1') $ScriptDir
    @'
param($RuntimeRootOverride)
$ProjectRoot=Split-Path $PSScriptRoot -Parent
$DeploymentsRoot=Join-Path $RuntimeRootOverride 'deployments'
function Assert-ChildPath {param($Path,$Root);if(-not $Path.StartsWith($Root)){throw 'fixture path escaped'};return $Path}
function Get-ReleaseInputs {param($Root);return @{identity='fixture-input'}}
function Get-ReleaseTreeHash {param($Path);return 'fixture-front'}
function Get-Context {return @{}}
function Invoke-ReleaseCandidateBuild {
    param($Context,$RecordPath,[switch]$Worker)
    if(-not $Worker){throw 'Worker flag was lost'}
    Write-Output 'fixture compiler stdout'
    Write-Error 'fixture compiler stderr' -ErrorAction Continue
    if((Get-Content $RecordPath -Raw).Trim() -eq 'fail'){throw 'fixture compile exit failure'}
}
'@ | Set-Content (Join-Path $ScriptDir 'release.ps1')
    foreach($mode in @('pass','fail')) {
        $requestPath=Join-Path $DeploymentsRoot "$mode.json"
        $recordPath=Join-Path $DeploymentsRoot 'record.json';$mode | Set-Content $recordPath
        $request=@{projectRoot=$ProjectRoot;runtimeRoot=(Join-Path $ProjectRoot 'runtime');recordPath=$recordPath;status='launching';nativeStatus='not_started';process=$null;nativeProcess=$null;inputIdentity='fixture-input:fixture-front';exitCode=$null;error='';stdout="$requestPath.stdout";stderr="$requestPath.stderr";log="$requestPath.log"}
        'START fixture' | Set-Content $request.log
        Write-ReleaseState $requestPath $request
        $worker=Start-CandidateWorker $requestPath $request
        try {Assert-True ($worker.WaitForExit(15000)) 'Fixture worker did not exit'}finally{$worker.Dispose()}
        $result=Wait-CandidateWorker $requestPath $request.inputIdentity
        $expected=if($mode -eq 'pass'){'passed'}else{'failed'}
        Assert-True ($result.status -eq $expected -and $null -ne $result.exitCode) ("Real worker lost completion status: " + ($result|ConvertTo-Json -Compress) + (Get-Content $request.stderr -Raw))
        $workerLog=Get-Content $request.log -Raw
        Assert-True ($workerLog.Contains('fixture compiler stdout') -and $workerLog.Contains('fixture compiler stderr') -and $workerLog.Contains('END')) 'Worker lifecycle logs were lost'
    }
    $requestPath=Join-Path $DeploymentsRoot 'start-failure.json'
    $request.status='launching';$request.process=$null;$request.error='';$request.stdout=$root
    Write-ReleaseState $requestPath $request
    Assert-Fails {Start-CandidateWorker $requestPath $request} ''
    Assert-True ((Read-ReleaseState $requestPath).status -eq 'failed') 'Definite worker launch failure remained unknown'
    Write-Host 'Release timing and recovery tests passed'
} finally {
    $resolved=[IO.Path]::GetFullPath($root)
    if(-not $resolved.StartsWith([IO.Path]::GetFullPath($base)+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'Cleanup escaped fixture root'}
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
