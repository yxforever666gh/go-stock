$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$SourceScripts = $PSScriptRoot
. (Join-Path $SourceScripts 'release.ps1')
. (Join-Path $SourceScripts 'verify.ps1') -Tier release -SkipGoBuild
$RealPublishGit = ${function:Invoke-PublishGit}
$RealWriteState = ${function:Write-ReleaseState}
$RealValidation = ${function:Invoke-ReleaseValidation}
$RealVerificationCommand = ${function:Invoke-VerificationCommand}
$RealChecked = ${function:Invoke-Checked}

function Write-ReleaseState {
    param([string]$Path,[System.Collections.IDictionary]$State)
    if ($script:FailMaintenanceWrite -and (Split-Path -Leaf $Path) -like 'maintenance-*' -and $State.status -eq $script:FailMaintenanceWrite) {
        $script:FailMaintenanceWrite=''
        throw 'fixture maintenance receipt failure'
    }
    if ($script:FailReceiptAfterDeploy -and $State.steps['deploy'] -and $State.steps['deploy'].status -eq 'passed') {
        $script:FailReceiptAfterDeploy=$false
        throw 'fixture receipt write failed after deployment'
    }
    & $RealWriteState $Path $State
}

function Assert-True { param([bool]$Value,[string]$Message); if (-not $Value) { throw $Message } }
function Assert-Fails { param([scriptblock]$Action,[string]$Match); try { & $Action } catch { if ($_.Exception.Message -notlike "*$Match*") { throw }; return }; throw "Expected failure: $Match" }

# All process, HTTP, compiler and GitHub transport boundaries are replaced.
# Git itself operates only against the disposable local bare origin below.
function Assert-PublishTransport { if ($script:RejectProxy) { throw 'Required proxy is unavailable' } }
function Get-ReleaseInputs {
    param([string]$Root)
    if ($script:Toolchain -eq 'missing') { throw 'fixture toolchain unavailable' }
    $commit=(& git -C $Root rev-parse HEAD).Trim()
    return @{identity=(Get-ReleaseTextHash ($commit+':'+$script:Toolchain));values=@{commit=$commit;go=$script:Toolchain;node='fixture';npm='fixture';target=@('windows','amd64')}}
}
function Invoke-PublishGit {
    param([string[]]$Arguments)
    if ($Arguments[0] -eq 'push') {
        $script:PushCalls++
        $script:Events.Add('push')
        if ($script:FailPush -eq 'before') { throw 'fixture push failure' }
        $result = & $RealPublishGit $Arguments
        if ($script:FailPush -eq 'after') { $script:FailPush=''; throw 'fixture push response lost' }
        return $result
    }
    if ($Arguments[0] -eq 'tag' -and $Arguments -contains '-a') { $script:TagCalls++;$script:Events.Add('tag') }
    return & $RealPublishGit $Arguments
}
function Invoke-VerificationCommand {
    param([string]$Program,[string[]]$Arguments,[string]$WorkingDirectory)
    $key=$Program+' '+($Arguments -join ' ')
    $script:Events.Add($key)
    if ($script:FailValidation -and $key -like $script:FailValidation) { throw 'fixture verification failure' }
    if ($Program -eq $NpmProgram -and ($Arguments -join ' ') -eq 'run build') {
        $script:FrontendBuilds++
        New-Item -ItemType Directory -Force -Path (Join-Path $FrontendRoot 'dist') | Out-Null
        [IO.File]::WriteAllText((Join-Path $FrontendRoot 'dist/index.html'), '<html>'+ (Get-Content (Join-Path $ProjectRoot 'source.txt') -Raw) + $script:FrontendSuffix + '</html>')
    }
    if ($Program -eq 'go') { Assert-True (Test-Path (Join-Path $FrontendRoot 'dist/index.html')) 'Go ran before frontend build' }
}
function Invoke-ReleaseValidation {
    param([string]$RecordPath)
    $script:ReleaseRecord=$RecordPath
    $script:VerificationInputs=Get-ReleaseInputs $ProjectRoot
    $script:SkipGoBuild=$true
    Invoke-ReleaseVerification
    $record=Read-ReleaseState $RecordPath
    $record.verificationIdentity=$VerificationInputs.identity
    $record.frontendHash=Get-ReleaseTreeHash (Join-Path $FrontendRoot 'dist')
    Write-ReleaseState $RecordPath $record
}
function Invoke-CandidateCompiler {
    param($Context,[string]$Binary)
    $script:GoBuilds++;$script:Events.Add('candidate-build')
    if ($script:FailBuild) { throw 'fixture compiler failure' }
    @{manifest=$Context.Manifest;build=@{commit=$Context.Commit;dirty=$false;buildTime='fixture'}} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $Binary
}
function Get-ZoneInfoSource { return (Join-Path $FixtureRoot 'fixture-zone.zip') }
function Get-ArtifactInspection {
    param([string]$Binary)
    $inspect=Get-Content -LiteralPath $Binary -Raw | ConvertFrom-Json
    $inspect.build | Add-Member -NotePropertyName artifactSHA256 -NotePropertyValue (Get-SHA256 $Binary)
    if ($script:BadInspection) { $inspect.build.commit='wrong-embedded-commit' }
    return $inspect
}
function Get-ReleaseListener { return $script:Listener }
function Get-CimInstance { param($ClassName,$Filter,$ErrorAction); $id=[int]($Filter -replace '\D',''); if ($script:FakeProcesses.ContainsKey($id)) {return $script:FakeProcesses[$id]} }
function Get-Process { param([int]$Id,$ErrorAction); if ($script:FakeProcesses.ContainsKey($Id)) {return $script:FakeProcesses[$Id]} }
function Stop-Process {
    param([int]$Id,[switch]$Force)
    $script:Stops++;$script:Events.Add('stop')
    [void]$script:FakeProcesses.Remove($Id)
    if ($script:Listener -and $script:Listener.ProcessId -eq $Id) {$script:Listener=$null}
}
function Wait-Process { param($Id,$Timeout,$ErrorAction) }
function Start-Sleep { param($Seconds,$Milliseconds) }
function Start-Process {
    param($FilePath,$WorkingDirectory,$WindowStyle,$RedirectStandardOutput,$RedirectStandardError,[switch]$PassThru)
    $script:Starts++;$script:NextPID++;$script:Events.Add('start')
    $process=[pscustomobject]@{Id=$script:NextPID;ProcessId=$script:NextPID;ExecutablePath=$FilePath}
    $script:FakeProcesses[$process.Id]=$process;$script:Listener=$process
    return $process
}
function Invoke-RestMethod {
    param($Uri,$TimeoutSec)
    $inspect=Get-ArtifactInspection $script:Listener.ExecutablePath
    $result=[pscustomobject]@{appVersion=$inspect.manifest.appVersion;commit=$inspect.build.commit;artifactSHA256=$inspect.build.artifactSHA256;dirty=$false;
        mainSchemaVersion=$inspect.manifest.mainSchemaVersion;minuteSchemaVersion=$inspect.manifest.minuteSchemaVersion;
        readiness=[pscustomobject]@{migrations=$true;database=$true;services=$true;scheduler=$true;ready=$true}}
    if ($script:FailRuntimeVersion -eq $result.appVersion) {$result.commit='wrong-runtime-commit'}
    switch ($script:RuntimeFault) { 'hash' {$result.artifactSHA256='wrong'} 'dirty' {$result.dirty=$true} 'scheduler' {$result.readiness.scheduler=$false} }
    return $result
}

function New-DatabaseArchive {
    param($PreviousPointer,$NewPointer)
    $script:Events.Add('archive')
    $path=Join-Path $FixtureRoot 'archive.zip'; 'fixture archive' | Set-Content $path
    return @{Path=$path;SHA256=(Get-SHA256 $path)}
}
function Invoke-DatabaseJSON {
    param([string]$Binary,[string[]]$Arguments)
    $script:Events.Add('database-'+$Arguments[1])
    if ($Arguments[1] -eq 'migrate') {
        'partial migration' | Set-Content $MainDB
        'partial migration' | Set-Content $MinuteDB
        if ($script:FailMigration) { throw 'fixture migration failed' }
    }
    return @{}
}
function Restore-FromReceipt {
    param($Receipt)
    $script:Events.Add('restore')
    Assert-True ($Receipt.archivedMainDB -eq $MainDB -and $Receipt.archivedMinuteDB -eq $MinuteDB) 'Archive receipt lost database paths'
    'original main' | Set-Content $MainDB; 'original minute' | Set-Content $MinuteDB
    return ''
}
function Remove-RestorationSafetyCopy { param($Path) }

function New-ReleaseFixture {
    $script:FixtureRoot=Join-Path $TestRoot ([Guid]::NewGuid().ToString('N'))
    $script:ProjectRoot=Join-Path $FixtureRoot 'repo'
    $script:ScriptDir=Join-Path $ProjectRoot 'scripts'
    $script:FrontendRoot=Join-Path $ProjectRoot 'frontend'
    $script:RuntimeRoot=Join-Path $ProjectRoot 'runtime'
    $script:ReleaseRoot=Join-Path $RuntimeRoot 'releases'
    $script:DeploymentsRoot=Join-Path $RuntimeRoot 'deployments'
    $script:ArchivesRoot=Join-Path $RuntimeRoot 'archives'
    $script:RestoreRoot=Join-Path $RuntimeRoot 'restore-staging'
    $script:FailedMigrationsRoot=Join-Path $RuntimeRoot 'failed-migrations'
    $script:CurrentPointer=Join-Path $RuntimeRoot 'current.json'
    $script:PidFile=Join-Path $RuntimeRoot 'go-stock-web.pid'
    $script:ManifestPath=Join-Path $ProjectRoot 'internal/releaseinfo/release_manifest.json'
    $script:MainDB=Join-Path $FixtureRoot 'stock.db';$script:MinuteDB=Join-Path $FixtureRoot 'minute.db'
    $script:WebAddr='127.0.0.1:34115'
    $script:NotesFile=Join-Path $FixtureRoot 'release-notes.md';$script:Resume=''
    $script:RejectProxy=$false;$script:Toolchain='fixture-v1';$script:FailPush='';$script:FailValidation='';$script:FailBuild=$false;$script:BadInspection=$false;$script:FailRuntimeVersion=''
    $script:FailReceiptAfterDeploy=$false;$script:RuntimeFault='';$script:FailMigration=$false
    $script:FrontendSuffix=''
    $script:FailMaintenanceWrite=''
    $script:PushCalls=0;$script:TagCalls=0;$script:FrontendBuilds=0;$script:GoBuilds=0;$script:Starts=0;$script:Stops=0;$script:NextPID=100
    $script:Events=[Collections.Generic.List[string]]::new();$script:FakeProcesses=@{};$script:Listener=$null;$script:DeploymentReceiptPath=''
    foreach($dir in @($ScriptDir,(Join-Path $FrontendRoot 'src'),(Split-Path $ManifestPath),$DeploymentsRoot)) {New-Item -ItemType Directory -Force -Path $dir | Out-Null}
    'zoneinfo fixture' | Set-Content (Join-Path $FixtureRoot 'fixture-zone.zip')
    '{"appVersion":"1.0.0","mainSchemaVersion":26,"minuteSchemaVersion":3}' | Set-Content $ManifestPath
    "# Release notes`n`n## 1.0.0 - fixture`n`n- initial`n" | Set-Content (Join-Path $ProjectRoot 'RELEASE_NOTES.md')
    "/runtime/`n/frontend/dist/`n" | Set-Content (Join-Path $ProjectRoot '.gitignore')
    'base' | Set-Content (Join-Path $ProjectRoot 'source.txt')
    '- fixture release' | Set-Content $NotesFile
    & git init --quiet --initial-branch=main $ProjectRoot; if($LASTEXITCODE){throw 'fixture git init failed'}
    & git -C $ProjectRoot config user.name 'Release Fixture'; & git -C $ProjectRoot config user.email 'release@example.invalid'
    & git -C $ProjectRoot config core.autocrlf false
    & git -C $ProjectRoot add .; & git -C $ProjectRoot commit -qm initial
    & git -C $ProjectRoot tag -a 1.0.0 -m initial
    $origin=Join-Path $FixtureRoot 'origin.git'
    & git init --bare --quiet --initial-branch=main $origin
    & git -C $ProjectRoot remote add origin $origin
    & git -C $ProjectRoot push --quiet origin main refs/tags/1.0.0 2>$null
    $context=Get-Context
    New-Item -ItemType Directory -Force -Path $context.ReleaseDir | Out-Null
    @{manifest=$context.Manifest;build=@{commit=$context.Commit;dirty=$false;buildTime='fixture'}} | ConvertTo-Json -Depth 5 | Set-Content $context.Binary
    Copy-Item (Get-ZoneInfoSource) $context.ZoneInfo
    $pointer=New-Pointer $context
    Write-JSONAtomic (Join-Path $context.ReleaseDir 'build.json') $pointer
    Write-JSONAtomic $CurrentPointer $pointer
    $script:Listener=[pscustomobject]@{Id=100;ProcessId=100;ExecutablePath=$pointer.binary}
    $script:FakeProcesses[100]=$script:Listener;100 | Set-Content $PidFile
    'original main' | Set-Content $MainDB; 'original minute' | Set-Content $MinuteDB
    $script:InitialPointer=$pointer
}

function Add-DevelopmentCommit {
    'changed' | Add-Content (Join-Path $ProjectRoot 'source.txt')
    [void](Invoke-PublishGit @('add','source.txt'))
    [void](Invoke-PublishGit @('commit','-m','fixture feature'))
}
function Get-FixtureReceipt { return (Get-ChildItem -LiteralPath $DeploymentsRoot -Filter 'publish-*.json' | Select-Object -First 1).FullName }
function Resume-Fixture { $script:Resume=Get-FixtureReceipt;$script:NotesFile='';Invoke-Publish }

$testBase=if(Test-Path -LiteralPath 'H:\Download'){ 'H:\Download\go-stock-release-tests' }else{Join-Path ([IO.Path]::GetTempPath()) 'go-stock-release-tests'}
$TestRoot=[IO.Path]::GetFullPath((Join-Path $testBase ([Guid]::NewGuid().ToString('N'))))
New-Item -ItemType Directory -Force -Path $TestRoot | Out-Null
$passed=0
try {
    New-ReleaseFixture
    Invoke-Publish
    Assert-True ($GoBuilds -eq 0 -and $Starts -eq 0 -and (Get-Content $ManifestPath -Raw | ConvertFrom-Json).appVersion -eq '1.0.0') 'No-op bumped or rebuilt'
    Add-DevelopmentCommit
    Invoke-Publish
    Assert-True ($FrontendBuilds -eq 1 -and $GoBuilds -eq 1 -and $Starts -eq 1 -and $Stops -eq 1 -and $TagCalls -eq 1 -and $PushCalls -eq 1) 'Normal publish duplicated work'
    Assert-True ($Events.IndexOf('candidate-build') -lt $Events.IndexOf('tag') -and $Events.IndexOf('tag') -lt $Events.IndexOf('push') -and $Events.IndexOf('push') -lt $Events.IndexOf('stop')) 'Publish order is wrong'
    $publishedHead=Get-PublishHead
    Invoke-Publish
    Assert-True ((Get-PublishHead) -eq $publishedHead -and $GoBuilds -eq 1 -and $Starts -eq 1) 'Repeated successful publish created an empty version'
    $savedChecked=${function:Invoke-Checked}
    $script:VerifierChildren=0
    function Invoke-Checked {
        param($Program,$Arguments,$Failure)
        Assert-True ($Program -eq 'pwsh' -and $Arguments -contains '-ReleaseRecord') 'Unexpected verification child'
        Assert-True ($env:HTTPS_PROXY -eq 'http://127.0.0.1:7890') 'Verifier child lost proxy'
        $script:VerifierChildren++
    }
    & $RealValidation (Get-FixtureReceipt)
    Assert-True ($VerifierChildren -eq 0) 'Complete verified inputs spawned another verifier'
    'tampered generated assets' | Add-Content (Join-Path $FrontendRoot 'dist/index.html')
    & $RealValidation (Get-FixtureReceipt)
    Assert-True ($VerifierChildren -eq 1) 'Changed frontend output did not invalidate verification'
    Set-Item -Path Function:Invoke-Checked -Value $savedChecked
    $passed++

    foreach($failure in @('verification','compiler','push','push-response','deploy')) {
        New-ReleaseFixture;Add-DevelopmentCommit
        switch($failure) {'verification'{$script:FailValidation='*run lint'} 'compiler'{$script:FailBuild=$true} 'push'{$script:FailPush='before'} 'push-response'{$script:FailPush='after'} 'deploy'{$script:FailRuntimeVersion='1.0.1'}}
        if($failure -eq 'push-response'){Invoke-Publish}else{Assert-Fails {Invoke-Publish} 'failed'}
        if($failure -in @('verification','compiler')){Assert-True ($PushCalls -eq 0 -and $Stops -eq 0) 'Preflight failure touched remote/service'}
        if($failure -eq 'deploy'){Assert-True ((Get-Content $CurrentPointer -Raw|ConvertFrom-Json).appVersion -eq '1.0.0') 'Rollback did not restore prior pointer'}
        $beforeHead=Get-PublishHead;$beforeFront=$FrontendBuilds;$beforeBuild=$GoBuilds
        $script:FailValidation='';$script:FailBuild=$false;$script:FailPush='';$script:FailRuntimeVersion=''
        Resume-Fixture
        Assert-True ((Get-PublishHead) -eq $beforeHead -and (Get-Content $ManifestPath -Raw|ConvertFrom-Json).appVersion -eq '1.0.1' -and $TagCalls -eq 1) 'Resume repeated version/tag'
        if($failure -in @('push','push-response','deploy')){Assert-True ($FrontendBuilds -eq $beforeFront -and $GoBuilds -eq $beforeBuild) 'Resume reran completed builds'}
        $passed++
    }

    foreach($point in @('manifest','both','commit')) {
        New-ReleaseFixture;Add-DevelopmentCommit
        $base=Get-PublishHead;$path=New-PublishRecord $base $NotesFile;$state=Read-ReleaseState $path
        [IO.File]::WriteAllText($ManifestPath,$state.expectedFiles['internal/releaseinfo/release_manifest.json'])
        if($point -ne 'manifest'){[IO.File]::WriteAllText((Join-Path $ProjectRoot 'RELEASE_NOTES.md'),$state.expectedFiles['RELEASE_NOTES.md'])}
        if($point -eq 'commit'){[void](Invoke-PublishGit @('add','internal/releaseinfo/release_manifest.json','RELEASE_NOTES.md'));[void](Invoke-PublishGit @('commit','-m','release: prepare 1.0.1'))}
        Resume-Fixture
        Assert-True (@(Invoke-PublishGit @('log','--format=%H','--grep=release: prepare')).Count -eq 1) 'Interrupted version preparation committed twice'
        $passed++
    }

    New-ReleaseFixture;Add-DevelopmentCommit;$script:Toolchain='missing'
    $beforeHead=Get-PublishHead
    Assert-Fails {Invoke-Publish} 'toolchain unavailable'
    Assert-True ((Get-PublishHead) -eq $beforeHead -and $GoBuilds -eq 0 -and $PushCalls -eq 0) 'Unavailable toolchain created a release commit'
    $passed++

    New-ReleaseFixture;Add-DevelopmentCommit;$script:RejectProxy=$true
    Assert-Fails {Invoke-Publish} 'proxy'
    Assert-True ($GoBuilds -eq 0 -and $Stops -eq 0) 'Proxy failure did not fail closed'
    $passed++

    New-ReleaseFixture;Add-DevelopmentCommit;$script:FailPush='before'
    Assert-Fails {Invoke-Publish} 'failed'
    $context=Get-Context;'corrupt' | Add-Content $context.Binary
    $script:FailPush=''
    Assert-Fails {Resume-Fixture} 'hash mismatch'
    Assert-True ($Stops -eq 0) 'Corrupt artifact stopped the service'
    $passed++

    New-ReleaseFixture;Add-DevelopmentCommit;$script:FailBuild=$true
    Assert-Fails {Invoke-Publish} 'failed'
    $script:FailBuild=$false;$script:Toolchain='fixture-v2'
    Resume-Fixture
    Assert-True ($FrontendBuilds -eq 2) 'Toolchain change reused verification'
    $passed++

    New-ReleaseFixture;Add-DevelopmentCommit;$script:FailPush='before'
    Assert-Fails {Invoke-Publish} 'failed'
    $artifactHash=(Get-SHA256 (Get-Context).Binary)
    $script:FailPush='';$script:Toolchain='fixture-v2'
    Assert-Fails {Resume-Fixture} 'different build inputs'
    Assert-True ((Get-SHA256 (Get-Context).Binary) -eq $artifactHash -and $GoBuilds -eq 1 -and $Stops -eq 0) 'Changed tools overwrote immutable artifact'
    $passed++

    New-ReleaseFixture;Add-DevelopmentCommit;$script:FailPush='before'
    Assert-Fails {Invoke-Publish} 'failed'
    $artifactHash=Get-SHA256 (Get-Context).Binary
    $script:FailPush='';$script:FrontendSuffix='new generated assets'
    'invalidate cached output' | Add-Content (Join-Path $FrontendRoot 'dist/index.html')
    Assert-Fails {Resume-Fixture} 'different build inputs'
    Assert-True ((Get-SHA256 (Get-Context).Binary) -eq $artifactHash -and $GoBuilds -eq 1 -and $Stops -eq 0) 'Changed frontend output reused or overwrote immutable artifact'
    $passed++

    New-ReleaseFixture
    $lock=Enter-ReleaseLock
    try{Assert-Fails {Enter-ReleaseLock} 'lock'}finally{$lock.Dispose()}
    $badProcess=[pscustomobject]@{ProcessId=100;ExecutablePath=(Join-Path $ReleaseRoot 'another.exe')}
    Assert-Fails {Assert-ReleaseProcess $InitialPointer $badProcess 100} 'process'
    Assert-Fails {Assert-ReleaseProcess $InitialPointer $Listener 999} 'PID'
    $script:BadInspection=$true
    Assert-Fails {Read-BuildArtifact (Get-Context)} 'embedded identity'
    $script:BadInspection=$false
    $passed++

    foreach($fault in @('hash','dirty','scheduler')) {
        $script:RuntimeFault=$fault
        Assert-Fails {Get-ExactReleaseStatus $InitialPointer 100} $(if($fault -eq 'scheduler'){'Readiness'}else{'identity'})
    }
    $script:RuntimeFault=''
    $script:FakeProcesses[999]=[pscustomobject]@{ProcessId=999;ExecutablePath=(Join-Path $ReleaseRoot 'another.exe')}
    999 | Set-Content $PidFile
    Assert-Fails {Stop-Current} 'process'
    Assert-True ($Stops -eq 0) 'Stale PID killed a process'
    $passed++

    New-ReleaseFixture;Add-DevelopmentCommit;$script:FailReceiptAfterDeploy=$true
    Assert-Fails {Invoke-Publish} 'receipt'
    Resume-Fixture
    Assert-True ($Starts -eq 1 -and $Stops -eq 1 -and $GoBuilds -eq 1 -and $TagCalls -eq 1) 'Lost completion receipt restarted or rebuilt'
    Assert-True (Test-Path -LiteralPath (Read-ReleaseState (Get-FixtureReceipt)).rollbackReceipt) 'Resume discarded the original rollback receipt'
    $passed++

    New-ReleaseFixture;Add-DevelopmentCommit;$script:FailBuild=$true
    Assert-Fails {Invoke-Publish} 'failed'
    Add-DevelopmentCommit
    Assert-Fails {Resume-Fixture} 'Resume commit'
    Assert-True ($GoBuilds -eq 1 -and $Stops -eq 0) 'Changed commit reused a receipt'
    $passed++

    New-ReleaseFixture;Add-DevelopmentCommit
    [void](Invoke-PublishGit @('tag','-a','1.0.1','-m','conflicting tag'))
    Assert-Fails {Invoke-Publish} 'already exists'
    Assert-True ($GoBuilds -eq 0 -and $Stops -eq 0) 'Tag conflict touched build/runtime'
    $passed++

    New-ReleaseFixture;Add-DevelopmentCommit
    [void](Invoke-PublishGit @('switch','-c','rival','1.0.0'))
    'rival change' | Set-Content (Join-Path $ProjectRoot 'rival.txt')
    [void](Invoke-PublishGit @('add','rival.txt'));[void](Invoke-PublishGit @('commit','-m','rival change'))
    [void](Invoke-PublishGit @('push','origin','rival:main'))
    [void](Invoke-PublishGit @('switch','main'))
    Assert-Fails {Invoke-Publish} 'cannot fast-forward'
    Assert-True ($GoBuilds -eq 0 -and $Stops -eq 0) 'Remote divergence touched runtime'
    $passed++

    New-ReleaseFixture
    Remove-Item -LiteralPath (Join-Path (Get-Context).ReleaseDir 'build.json')
    Assert-Fails {Invoke-Deploy} 'Missing release pointer'
    Assert-True ($GoBuilds -eq 0 -and $Stops -eq 0) 'Deploy implicitly rebuilt an incomplete artifact'
    $passed++

    foreach($failure in @('migration','startup')) {
        New-ReleaseFixture
        $manifest=Get-Content $ManifestPath -Raw|ConvertFrom-Json;$manifest.mainSchemaVersion=27
        $manifest|ConvertTo-Json|Set-Content $ManifestPath
        [void](Invoke-PublishGit @('add','internal/releaseinfo/release_manifest.json'));[void](Invoke-PublishGit @('commit','-m','schema fixture change'))
        if($failure -eq 'migration'){$script:FailMigration=$true}else{$script:FailRuntimeVersion='1.0.1'}
        Assert-Fails {Invoke-Publish} 'failed'
        Assert-True ($Events.IndexOf('archive') -ge 0 -and $Events.IndexOf('archive') -lt $Events.IndexOf('database-migrate') -and $Events.Contains('restore')) 'Schema rollback order is wrong'
        Assert-True ((Get-Content $MainDB -Raw).Trim() -eq 'original main' -and (Get-Content $MinuteDB -Raw).Trim() -eq 'original minute') 'Rollback did not restore both fixture databases'
        Assert-True ((Get-Content $CurrentPointer -Raw|ConvertFrom-Json).appVersion -eq '1.0.0') 'Schema rollback lost prior pointer'
        $passed++
    }

    foreach ($point in @('started','complete')) {
        New-ReleaseFixture
        $manifest=Get-Content $ManifestPath -Raw|ConvertFrom-Json;$manifest.mainSchemaVersion=27
        $manifest|ConvertTo-Json|Set-Content $ManifestPath
        [void](Invoke-PublishGit @('add','internal/releaseinfo/release_manifest.json'));[void](Invoke-PublishGit @('commit','-m','schema fixture change'))
        $script:FailMaintenanceWrite=$point
        Assert-Fails {Invoke-Publish} 'maintenance receipt failure'
        if ($point -eq 'started') {
            Assert-True (-not $Events.Contains('database-migrate') -and -not $Events.Contains('restore')) 'Migration ran without a durable original receipt'
        } else {
            Assert-True ((Get-Content $CurrentPointer -Raw|ConvertFrom-Json).appVersion -eq '1.0.1') 'Ready new release rolled back after completion journal failure'
            Resume-Fixture
            Assert-True ($Starts -eq 1 -and -not $Events.Contains('restore') -and @(Get-PendingSchemaMaintenance).Count -eq 0) 'Ready release with pending journal restarted or restored old databases'
        }
        $passed++
    }

    New-ReleaseFixture
    $nativeRecord=Join-Path $DeploymentsRoot 'native-output.json'
    Write-ReleaseState $nativeRecord @{steps=@{}}
    Invoke-ReleaseStage $nativeRecord 'native-output' 'fixture' {
        & $RealVerificationCommand 'pwsh' @('-NoProfile','-Command',"'native-verifier-output'") $ProjectRoot
        & $RealChecked 'pwsh' @('-NoProfile','-Command',"'native-compiler-output'") 'fixture failure'
    }
    $nativeLog=Get-Content (Read-ReleaseState $nativeRecord).steps['native-output'].log -Raw
    Assert-True ($nativeLog.Contains('native-verifier-output') -and $nativeLog.Contains('native-compiler-output')) 'Native output bypassed the release log'
    $passed++

    New-ReleaseFixture
    $archive=New-DatabaseArchive $InitialPointer $InitialPointer
    $script:RollbackReceipt=Join-Path $DeploymentsRoot 'original-rollback.json'
    Write-JSONAtomic $RollbackReceipt (New-RollbackReceipt $InitialPointer $archive.Path $archive.SHA256)
    $maintenancePath=Join-Path $DeploymentsRoot 'maintenance-interrupted.json'
    Write-ReleaseState $maintenancePath @{status='started';commit='interrupted-new-commit';artifactSHA256='new-artifact';rollbackReceipt=$RollbackReceipt}
    'upgraded main' | Set-Content $MainDB; 'upgraded minute' | Set-Content $MinuteDB
    $eventCount=$Events.Count
    Assert-Fails {Invoke-Deploy} 'Interrupted schema maintenance'
    Assert-True ($Events.Count -eq $eventCount -and $Stops -eq 0 -and (Get-Content $MainDB -Raw).Trim() -eq 'upgraded main') 'Interrupted migration was archived or restarted blindly'
    Invoke-Rollback
    Assert-True ((Read-ReleaseState $maintenancePath).status -eq 'rolled_back' -and (Get-Content $MainDB -Raw).Trim() -eq 'original main') 'Explicit rollback failed to recover interrupted migration'
    Invoke-Deploy
    Assert-True ($Starts -eq 1) 'Recovered migration restarted twice'
    Write-ReleaseState $maintenancePath @{status='started';commit=$InitialPointer.commit;artifactSHA256=$InitialPointer.artifactSHA256;rollbackReceipt=$RollbackReceipt}
    Invoke-Deploy
    Assert-True ((Read-ReleaseState $maintenancePath).status -eq 'complete' -and $Starts -eq 1) 'Running exact release did not reconcile maintenance journal'
    $passed++

    $url='git@github.com:owner/repo.git'
    Assert-GitHubRemoteURLs @($url) @($url)
    Assert-Fails {Assert-GitHubRemoteURLs @('https://github.com/owner/repo.git') @($url)} 'URL'
    Assert-Fails {Assert-GitHubRemoteURLs @($url) @($url,$url)} 'URL'
    $identity='C:/fixture/key';$bridge='C:/fixture/github_proxy.py'
    $settings=@('hostname ssh.github.com','port 443','identitiesonly yes','stricthostkeychecking true',"identityfile $identity","proxycommand python $bridge %h %p")
    Assert-GitHubSSHSettings 'git@github.com:owner/repo.git' $settings $identity $bridge
    Assert-Fails {Assert-GitHubSSHSettings 'https://github.com/owner/repo' $settings $identity $bridge} 'SSH'
    Assert-Fails {Assert-GitHubSSHSettings 'git@github.com:owner/repo.git' ($settings -replace 'port 443','port 22') $identity $bridge} 'SSH'
    Assert-Fails {Assert-GitHubSSHSettings 'git@github.com:owner/repo.git' ($settings -replace 'proxycommand.*','proxycommand none') $identity $bridge} 'proxy'
    $passed++
    Write-Host "Release pipeline offline scenarios passed: $passed"
} finally {
    $resolved=[IO.Path]::GetFullPath($TestRoot)
    $prefix=[IO.Path]::GetFullPath($testBase).TrimEnd('\','/')+[IO.Path]::DirectorySeparatorChar
    if(-not $resolved.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Test cleanup escaped its temporary root'}
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
