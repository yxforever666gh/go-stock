# Git/version orchestration for the single publish entry point. Deployment and
# artifact handling remain in release.ps1; validation remains in verify.ps1.
function Invoke-PublishGit {
    param([string[]]$Arguments)
    $watch = [Diagnostics.Stopwatch]::StartNew()
    try {
    $gitArguments = @('-C',$ProjectRoot)
    if ($Arguments[0] -in @('fetch','push','ls-remote')) { $gitArguments += @('-c','core.sshCommand=ssh -o BatchMode=yes') }
    $output = @(& git @gitArguments @Arguments 2>&1)
    if ($LASTEXITCODE -ne 0) {
        $exitCode=$LASTEXITCODE
        $output | Write-Host
        $summary=($output | Select-Object -Last 1) -join ''
        if ($summary.Length -gt 300) { $summary=$summary.Substring(0,300) }
        throw "git $($Arguments[0]) failed (exit $exitCode): $summary"
    }
    return ($output | ForEach-Object { [string]$_ })
    } finally {
        $watch.Stop()
        if ($Arguments[0] -in @('fetch','push','ls-remote')) { $script:PublishGitNetworkSeconds += $watch.Elapsed.TotalSeconds }
    }
}

$script:PublishGitNetworkSeconds = 0

function Restore-PublishBuildInputs {
    # Sparse checkout can omit tracked compiler inputs (notably backup.go).
    # Materialize only missing skip-worktree files, never user deletions/edits.
    foreach ($line in (Invoke-PublishGit @('-c','core.quotepath=false','ls-files','-t','--','*.go','scripts/*.ps1','frontend/src/*','frontend/package*.json','go.mod','go.sum'))) {
        if ($line.StartsWith('S ')) {
            $relative = $line.Substring(2)
            if (-not (Test-Path -LiteralPath (Join-Path $ProjectRoot $relative))) {
                [void](Invoke-PublishGit @('restore','--ignore-skip-worktree-bits','--worktree','--',$relative))
            }
        }
    }
}

function Get-PublishHead { return ((Invoke-PublishGit @('rev-parse','HEAD')) -join '').Trim() }

function Assert-PublishClean {
    if (@(Invoke-PublishGit @('status','--porcelain')).Count) { throw 'Publish requires a clean checkout; commit development changes first' }
}

function Assert-GitHubSSHSettings {
    param([string]$Remote, [string[]]$Settings, [string]$Identity, [string]$Bridge)
    if ($Remote -notmatch '^(git@github\.com:|ssh://git@github\.com/)') { throw 'origin must use GitHub SSH' }
    $values = @{}
    foreach ($line in $Settings) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -eq 2) { $values[$parts[0]] = @($values[$parts[0]]) + $parts[1] }
    }
    $identityValues = @($values['identityfile'] | Where-Object { $_ })
    if (($values['hostname'] -join '') -ne 'ssh.github.com' -or ($values['port'] -join '') -ne '443' -or
        ($values['identitiesonly'] -join '') -ne 'yes' -or ($values['stricthostkeychecking'] -join '') -notin @('yes','true') -or
        $identityValues.Count -ne 1 -or $identityValues[0].Replace('\','/') -ne $Identity.Replace('\','/') -or
        -not ($values['proxycommand'] -join '').Replace('\','/').Contains($Bridge.Replace('\','/'))) {
        throw 'SSH must use the configured account key, ssh.github.com:443 and fail-closed GitHub proxy bridge'
    }
}

function Assert-GitHubRemoteURLs {
    param([string[]]$FetchURLs,[string[]]$PushURLs)
    if ($FetchURLs.Count -ne 1 -or $PushURLs.Count -ne 1 -or $FetchURLs[0] -cne $PushURLs[0]) { throw 'origin must have one identical controlled SSH fetch/push URL' }
    if ($FetchURLs[0] -notmatch '^(git@github\.com:|ssh://git@github\.com/)') { throw 'All GitHub fetch and push traffic must use controlled SSH' }
}

function Assert-PublishTransport {
    if ($env:GIT_SSH -or $env:GIT_SSH_COMMAND) { throw 'Unexpected Git SSH environment override' }
    $sshOverride = @(& git -C $ProjectRoot config --get core.sshCommand)
    if ($sshOverride.Count) { throw 'Unexpected repository Git SSH override' }
    $identity = Join-Path $env:USERPROFILE '.codex/secrets/github/codex_github_ed25519'
    $bridge = Join-Path $env:USERPROFILE '.ssh/github_proxy.py'
    if (-not (Test-Path -LiteralPath $identity -PathType Leaf) -or -not (Test-Path -LiteralPath $bridge -PathType Leaf)) { throw 'Required GitHub identity/proxy bridge is missing' }
    $settings = @(& ssh -G git@github.com 2>$null)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect SSH configuration' }
    $fetchURLs=@(Invoke-PublishGit @('remote','get-url','--all','origin'))
    $pushURLs=@(Invoke-PublishGit @('remote','get-url','--push','--all','origin'))
    Assert-GitHubRemoteURLs $fetchURLs $pushURLs
    Assert-GitHubSSHSettings $fetchURLs[0] $settings $identity $bridge
    $bridgeText = Get-Content -LiteralPath $bridge -Raw
    if ($bridgeText -notmatch 'PROXY\s*=\s*\(["'']127\.0\.0\.1["''],\s*7890\)' -or $bridgeText -notmatch 'socket\.create_connection\(PROXY,') { throw 'GitHub bridge does not target the required local proxy' }
    $owner = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    foreach ($access in (Get-Acl -LiteralPath $identity).Access) {
        $sid = $access.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
        if ($access.AccessControlType -eq 'Allow' -and $sid -notin @($owner,'S-1-5-18','S-1-5-32-544')) { throw 'GitHub private key permissions are too broad' }
    }
    $client = [Net.Sockets.TcpClient]::new()
    try { if (-not $client.ConnectAsync('127.0.0.1',7890).Wait(1500)) { throw 'Required proxy is unavailable' } }
    finally { $client.Dispose() }
}

function Get-PublishRemoteRefs {
    param([string]$Version)
    Assert-PublishTransport
    $refs = @{}
    foreach ($line in (Invoke-PublishGit @('ls-remote','origin','refs/heads/main',"refs/tags/$Version","refs/tags/$Version^{}"))) {
        $parts = $line -split '\s+'
        if ($parts.Count -eq 2) { $refs[$parts[1]] = $parts[0] }
    }
    return $refs
}

function Get-PublishLocalTag {
    param([string]$Version)
    $tag = @(& git -C $ProjectRoot rev-parse -q --verify "refs/tags/$Version" 2>$null)
    if ($LASTEXITCODE -ne 0) { return '' }
    return ($tag -join '').Trim()
}

function Assert-PublishBranch {
    Assert-PublishTransport
    [void](Invoke-PublishGit @('fetch','--no-tags','origin','main'))
    $head = Get-PublishHead
    foreach ($base in @('refs/heads/main','refs/remotes/origin/main')) {
        & git -C $ProjectRoot merge-base --is-ancestor $base $head
        if ($LASTEXITCODE -ne 0) { throw "$base cannot fast-forward to this checkout; integrate branches before publishing" }
    }
    return $head
}

function Assert-PublishRecord {
    param([string]$Path, $Record)
    [void](Assert-ChildPath $Path $DeploymentsRoot)
    if ($Record.formatVersion -ne 1 -or $Record.projectRoot -ne $ProjectRoot -or $Record.version -notmatch '^\d+\.\d+\.\d+$' -or
        $Record.sourceCommit -notmatch '^[0-9a-f]{40}$' -or ($Record.commit -and $Record.commit -notmatch '^[0-9a-f]{40}$')) { throw 'Invalid publish receipt' }
}

function Test-PublishText {
    param([string]$Path, [string]$Text)
    return (Test-Path -LiteralPath $Path -PathType Leaf) -and (Get-Content -LiteralPath $Path -Raw).Replace("`r`n","`n") -ceq $Text.Replace("`r`n","`n")
}

function Test-PublishPreparedCommit {
    param($Record)
    $parent = @(& git -C $ProjectRoot rev-parse --verify HEAD^ 2>$null)
    if ($LASTEXITCODE -ne 0 -or ($parent -join '') -ne $Record.sourceCommit) { return $false }
    $files = @('internal/releaseinfo/release_manifest.json','RELEASE_NOTES.md')
    $changed = @(Invoke-PublishGit @('diff-tree','--no-commit-id','--name-only','-r','HEAD'))
    if (@($changed | Where-Object { $_ -notin $files }).Count) { return $false }
    foreach ($file in $files) { if (-not (Test-PublishText (Join-Path $ProjectRoot $file) $Record.expectedFiles[$file])) { return $false } }
    return $true
}

function Complete-PublishVersion {
    param([string]$RecordPath)
    $record = Read-ReleaseState $RecordPath
    $head = Get-PublishHead
    $files = @('internal/releaseinfo/release_manifest.json','RELEASE_NOTES.md')
    if ($record.commit) {
        if ($record.commit -ne $head) { throw 'Resume commit differs from HEAD; start a new release for changed code' }
        Assert-PublishClean
        foreach ($file in $files) { if (-not (Test-PublishText (Join-Path $ProjectRoot $file) $record.expectedFiles[$file])) { throw "Resume metadata differs: $file" } }
        if ((Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json).appVersion -ne $record.version) { throw 'Resume version differs from manifest' }
        return
    }
    if ($head -ne $record.sourceCommit) {
        # Recover a successful version commit even if the process died before
        # recording its SHA. Both its parent and its exact file changes matter.
        if (-not (Test-PublishPreparedCommit $record)) { throw 'Unexpected commit or unrelated changes while preparing version' }
        Assert-PublishClean
    } else {
        $changed = @((Invoke-PublishGit @('diff','--name-only')); (Invoke-PublishGit @('diff','--cached','--name-only')); (Invoke-PublishGit @('ls-files','--others','--exclude-standard')))
        if (@($changed | Where-Object { $_ -notin $files }).Count) { throw 'Unrelated dirty files block version recovery' }
        foreach ($file in $files) {
            $path = Join-Path $ProjectRoot $file
            if (-not (Test-PublishText $path $record.originalFiles[$file]) -and -not (Test-PublishText $path $record.expectedFiles[$file])) { throw "User changes conflict with prepared version: $file" }
            [IO.File]::WriteAllText($path, $record.expectedFiles[$file], [Text.UTF8Encoding]::new($false))
        }
        [void](Invoke-PublishGit (@('add','--') + $files))
        [void](Invoke-PublishGit @('commit','-m',"release: prepare $($record.version)"))
        Complete-PublishVersion $RecordPath
        return
    }
    $record.commit, $record.status = $head, 'running'
    Write-ReleaseState $RecordPath $record
}

function New-PublishRecord {
    param([string]$SourceCommit, [string]$NotesPath)
    if (-not $NotesPath -or -not (Test-Path -LiteralPath $NotesPath -PathType Leaf)) { throw 'A new release requires NotesFile containing the release-note body' }
    $notes = (Get-Content -LiteralPath $NotesPath -Raw).Trim()
    if (-not $notes) { throw 'Release notes are empty' }
    $manifestText = Get-Content -LiteralPath $ManifestPath -Raw
    $manifest = $manifestText | ConvertFrom-Json
    if ($manifest.appVersion -notmatch '^(\d+)\.(\d+)\.(\d+)$') { throw 'Patch increment requires a stable semantic version' }
    $version = "$($Matches[1]).$($Matches[2]).$([int]$Matches[3]+1)"
    $remote = Get-PublishRemoteRefs $version
    if ((Get-PublishLocalTag $version) -or $remote["refs/tags/$version"]) { throw "Release tag $version already exists; it cannot be overwritten" }
    $manifest.appVersion = $version
    $notesPathInRepo = Join-Path $ProjectRoot 'RELEASE_NOTES.md'
    $oldNotes = (Get-Content -LiteralPath $notesPathInRepo -Raw).Replace("`r`n","`n")
    $headingEnd = $oldNotes.IndexOf("`n")
    if ($headingEnd -lt 0 -or -not $oldNotes.StartsWith('# ')) { throw 'Release notes must begin with their document heading' }
    $newNotes = $oldNotes.Substring(0,$headingEnd) + "`n`n## $version - $(Get-Date -Format yyyy-MM-dd)`n`n$notes`n`n" + $oldNotes.Substring($headingEnd).TrimStart()
    $path = Join-Path $DeploymentsRoot ('publish-' + [Guid]::NewGuid().ToString('N') + '.json')
    $record = @{formatVersion=1; projectRoot=$ProjectRoot; sourceCommit=$SourceCommit; commit=''; version=$version; status='preparing'; steps=@{};
        verificationIdentity=''; frontendHash=''; createdAt=[DateTime]::UtcNow.ToString('o'); lastError='';
        originalFiles=@{'internal/releaseinfo/release_manifest.json'=$manifestText; 'RELEASE_NOTES.md'=$oldNotes};
        expectedFiles=@{'internal/releaseinfo/release_manifest.json'=($manifest|ConvertTo-Json)+"`n"; 'RELEASE_NOTES.md'=$newNotes}}
    Write-ReleaseState $path $record
    return $path
}

function Ensure-PublishTag {
    param($Context)
    $version = [string]$Context.Manifest.appVersion
    if (-not (Get-PublishLocalTag $version)) { [void](Invoke-PublishGit @('tag','-a',$version,'-m',"Go-Stock $version")) }
    if (((Invoke-PublishGit @('cat-file','-t',"refs/tags/$version")) -join '') -ne 'tag') { throw 'Release tag must be annotated' }
    Assert-VersionTagMatchesCommit $Context
}

function Confirm-PublishRemote {
    param($Context, [switch]$Push)
    $version = [string]$Context.Manifest.appVersion
    $tag = Get-PublishLocalTag $version
    $refs = Get-PublishRemoteRefs $version
    $tagRef = "refs/tags/$version"
    if ($refs[$tagRef] -and ($refs[$tagRef] -ne $tag -or $refs["$tagRef^{}"] -ne $Context.Commit)) { throw 'Remote tag conflicts with the immutable release' }
    if ($refs['refs/heads/main'] -eq $Context.Commit -and $refs[$tagRef] -eq $tag -and $refs["$tagRef^{}"] -eq $Context.Commit) { $script:LastPublishRemoteRefs=$refs; Write-Host 'Remote main and annotated tag already match'; return }
    if (-not $Push) { throw 'Remote main/tag verification failed' }
    if ((Get-PublishHead) -ne $Context.Commit) { throw 'HEAD changed before push' }
    $pushError = $null
    try { [void](Invoke-PublishGit @('push','--atomic','origin','refs/heads/main',"refs/tags/$version")) } catch { $pushError = $_ }
    # A connection can fail after GitHub accepted the atomic push. Reconcile
    # before deciding to retry rather than creating another version or tag.
    try { Confirm-PublishRemote $Context } catch { if ($pushError) { throw $pushError }; throw }
}

function Invoke-Publish {
    $watch = [Diagnostics.Stopwatch]::StartNew()
    $script:PublishGitNetworkSeconds = 0
    $script:DeploymentReceiptPath = ''
    $recordPath = ''
    try {
        if ($Resume -and $NotesFile) { throw 'Resume reuses recorded notes; do not supply NotesFile' }
        Assert-PublishTransport
        [void](Get-ReleaseInputs $ProjectRoot)
        $head = Get-PublishHead
        if ($Resume) {
            $recordPath = Assert-ChildPath ([IO.Path]::GetFullPath($Resume)) $DeploymentsRoot
            Assert-PublishRecord $recordPath (Read-ReleaseState $recordPath)
        } else {
            foreach ($file in @(Get-ChildItem -LiteralPath $DeploymentsRoot -Filter 'publish-*.json' -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending)) {
                $record = Read-ReleaseState $file.FullName
                if ($record.projectRoot -eq $ProjectRoot -and ($record.commit -eq $head -or (-not $record.commit -and $record.sourceCommit -eq $head))) { $recordPath=$file.FullName; break }
                if (-not $record.commit -and $record.projectRoot -eq $ProjectRoot -and (Test-PublishPreparedCommit $record)) { $recordPath=$file.FullName; break }
            }
        }
        if (-not $recordPath) { Assert-PublishClean }
        [void](Assert-PublishBranch)
        if ($recordPath) { Complete-PublishVersion $recordPath }
        $head = Get-PublishHead
        $context = Get-Context
        if (-not $recordPath -or (Read-ReleaseState $recordPath).status -eq 'complete') {
            $remote = Get-PublishRemoteRefs $context.Manifest.appVersion
            if ($remote['refs/heads/main'] -eq $head -and $remote["refs/tags/$($context.Manifest.appVersion)^{}"] -eq $head) {
                if ($remote["refs/tags/$($context.Manifest.appVersion)"] -ne (Get-PublishLocalTag $context.Manifest.appVersion)) { throw 'Local and remote annotated tag objects differ' }
                Assert-VersionTagMatchesCommit $context
                [void](Read-BuildArtifact $context)
                Invoke-Deploy
                [void](Get-ExactReleaseStatus (Read-BuildArtifact $context))
                Write-Host "Already published $($context.Manifest.appVersion); no version increment"
                return
            }
        }
        [void](Invoke-PublishGit @('switch','main'))
        [void](Invoke-PublishGit @('merge','--ff-only',$head))
        Restore-PublishBuildInputs
        if (-not $recordPath) { $recordPath = New-PublishRecord $head $NotesFile; Complete-PublishVersion $recordPath }
        $record = Read-ReleaseState $recordPath
        Assert-PublishRecord $recordPath $record
        foreach ($step in $record.steps.Values) { $step.lastRunSeconds=0 }
        $record.status, $record.lastError = 'running',''
        $inputs = Get-ReleaseInputs $ProjectRoot
        $record.inputs = $inputs.values
        Write-ReleaseState $recordPath $record
        $context = Get-Context
        Invoke-ReleaseValidation $recordPath
        $buildInput = $inputs.identity + ':' + (Get-ReleaseTreeHash (Join-Path $ProjectRoot 'frontend/dist'))
        Invoke-ReleaseStage $recordPath 'candidate-build' $buildInput { Invoke-ReleaseCandidateBuild $context $recordPath } { if (Test-Path -LiteralPath (Join-Path $context.ReleaseDir 'build.json')) { Get-SHA256 (Join-Path $context.ReleaseDir 'build.json') } else { '' } }
        [void](Read-BuildArtifact $context)
        Invoke-ReleaseStage $recordPath 'tag' $context.Commit { Ensure-PublishTag $context } { Get-PublishLocalTag $context.Manifest.appVersion }
        Invoke-ReleaseStage $recordPath 'git-publish' $context.Commit { Confirm-PublishRemote $context -Push } -Always
        Invoke-ReleaseStage $recordPath 'deploy' $context.Commit { Invoke-Deploy } -Always
        $pointer = Read-BuildArtifact $context
        $status = Get-ExactReleaseStatus $pointer
        $record = Read-ReleaseState $recordPath
        $record.status, $record.completedAt = 'complete', [DateTime]::UtcNow.ToString('o')
        $record.artifact = $pointer
        $record.runtime = $status
        $record.remoteRefs = $script:LastPublishRemoteRefs
        $record.processId = [int](Get-ReleaseListener).ProcessId
        if ($script:DeploymentReceiptPath -and (Test-Path -LiteralPath $script:DeploymentReceiptPath)) { $record.rollbackReceipt = $script:DeploymentReceiptPath }
        $record.totalSeconds = [math]::Round($watch.Elapsed.TotalSeconds,3)
        $record.gitNetworkSeconds = [math]::Round($script:PublishGitNetworkSeconds,3)
        Write-ReleaseState $recordPath $record
        $verifySeconds = ($record.steps.GetEnumerator() | Where-Object { $_.Key -like 'verify-*' -and $_.Key -ne 'verify-frontend production build' } | ForEach-Object { $_.Value.lastRunSeconds } | Measure-Object -Sum).Sum
        Write-Host "Published $($record.version), commit $($record.commit)"
        Write-Host "Verification ${verifySeconds}s; frontend build $($record.steps['verify-frontend production build'].lastRunSeconds)s; Go build $($record.steps['candidate-build'].lastRunSeconds)s; Git network $($record.gitNetworkSeconds)s; deploy $($record.steps['deploy'].lastRunSeconds)s; total $($record.totalSeconds)s"
        Write-Host "Receipt: $recordPath"
    } catch {
        if ($recordPath -and (Test-Path -LiteralPath $recordPath)) {
            $record = Read-ReleaseState $recordPath
            $record.status, $record.lastError = 'failed', $_.Exception.Message
            $record.totalSeconds = [math]::Round($watch.Elapsed.TotalSeconds,3)
            $record.gitNetworkSeconds = [math]::Round($script:PublishGitNetworkSeconds,3)
            if ($script:DeploymentReceiptPath -and (Test-Path -LiteralPath $script:DeploymentReceiptPath)) { $record.rollbackReceipt = $script:DeploymentReceiptPath }
            Write-ReleaseState $recordPath $record
            Write-Host "Resume: pwsh -File scripts/release.ps1 -Command publish -Resume `"$recordPath`""
        }
        throw
    } finally { $watch.Stop() }
}
