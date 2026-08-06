package releaseinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseRollbackRequiresExactPreviousReadiness(t *testing.T) {
	script := readReleaseScript(t)

	for _, forbidden := range []string{"function Wait-WebHealth", "Wait-WebHealth $"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("release rollback still uses legacy health-only verification %q", forbidden)
		}
	}

	required := []string{
		`& $Previous.binary "release" "inspect"`,
		`$status.appVersion -eq $Expected.AppVersion`,
		`$status.commit -eq $Expected.Commit`,
		`$status.artifactSHA256).ToLowerInvariant() -eq $Expected.ArtifactSHA256`,
		`$status.mainSchemaVersion -eq $Expected.MainSchemaVersion`,
		`$status.minuteSchemaVersion -eq $Expected.MinuteSchemaVersion`,
		`$status.currentStrategyVersion -eq $Expected.CurrentStrategyVersion`,
		`$status.strategyConfigHash -eq $Expected.StrategyConfigHash`,
		`$status.configHash -eq $Expected.StrategyConfigHash`,
		`$status.strategyMode -eq "paused"`,
		`$status.readiness.strategyMode -eq "paused"`,
		`[bool]$status.readiness.ready`,
	}
	for _, fragment := range required {
		if !strings.Contains(script, fragment) {
			t.Errorf("release rollback exact-readiness contract is missing %q", fragment)
		}
	}

	automaticRollback := strings.Index(script, `Wait-ExactPreviousReadiness $previousReadiness $previousProcess.Id`)
	manualRollback := strings.Index(script, `Wait-ExactPreviousReadiness $previousReadiness $process.Id`)
	if automaticRollback < 0 || manualRollback < 0 {
		t.Fatalf("both automatic and manual rollback paths must verify exact previous readiness")
	}
	if strings.Count(script, "Wait-ExactPreviousReadiness ") != 3 {
		t.Fatalf("exact previous readiness must have one declaration and two rollback callers")
	}
	if !strings.Contains(script[automaticRollback:], `Assert-SingleListener $previousProcess.Id`) {
		t.Fatal("automatic rollback must enforce a single listener after exact readiness")
	}
	if !strings.Contains(script[manualRollback:], `Assert-SingleListener $process.Id`) {
		t.Fatal("manual rollback must enforce a single listener after exact readiness")
	}
}

func TestReleaseBranchGateQueriesActualOriginHeads(t *testing.T) {
	script := readReleaseScript(t)
	if strings.Contains(script, "refs/remotes/origin") {
		t.Fatal("release branch gate must not trust cached refs/remotes/origin")
	}
	for _, fragment := range []string{
		`function Get-GitHubOriginRepository`,
		`git -C $ProjectRoot remote get-url origin`,
		`Origin is not a supported GitHub URL`,
		`for ($remoteAttempt = 1; $remoteAttempt -le 3; $remoteAttempt++)`,
		`try {`,
		`& git -C $ProjectRoot -c http.version=HTTP/1.1 -c http.lowSpeedLimit=1 -c http.lowSpeedTime=20 ls-remote --heads origin`,
		`catch {`,
		`if ($remoteAttempt -lt 3) { Start-Sleep -Seconds $remoteAttempt }`,
		`$attemptExitCode = $LASTEXITCODE`,
		`$endpoint = "repos/$repository/git/matching-refs/heads/?per_page=100"`,
		`& gh api --paginate $endpoint --jq ".[].ref"`,
		`Cannot query origin branch heads through git`,
		`$remoteBranches.Count -ne 1`,
		`$remoteBranches[0] -ne "refs/heads/main"`,
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("release branch gate is missing %q", fragment)
		}
	}
}

func TestReleaseStartupUsesVersionedTimezoneData(t *testing.T) {
	script := readReleaseScript(t)
	for _, fragment := range []string{
		`function Resolve-ReleaseZoneInfo`,
		`Get-Command go.exe`,
		`$env:ZONEINFO = Resolve-ReleaseZoneInfo`,
	} {
		if strings.Contains(script, fragment) {
			t.Errorf("release startup must not discover host timezone data through %q", fragment)
		}
	}
	for _, fragment := range []string{
		`function Get-BuildZoneInfoSource`,
		`& go env GOROOT`,
		`lib\time\zoneinfo.zip`,
		`Copy-Item -LiteralPath (Get-BuildZoneInfoSource) -Destination $zoneInfoPath`,
		`zoneInfoSHA256 = $zoneInfoHash`,
		`Assert-ZoneInfoArchive $Context.Binary $zoneInfoPath $zoneInfoHash`,
		`param([string]$Binary, [string]$ZoneInfo, [string]$ZoneInfoSHA256)`,
		`$oldZoneInfo = $env:ZONEINFO`,
		`$env:ZONEINFO = (Resolve-Path -LiteralPath $ZoneInfo).Path`,
		`$env:ZONEINFO = $oldZoneInfo`,
		`$properties["zoneInfo"]`,
		`$properties["zoneInfoSHA256"]`,
		`Assert-PathHasNoLinks $ReleasesRoot $ZoneInfo "Release timezone sidecar"`,
		`Assert-PathHasNoLinks $ReleasesRoot ([string]$Pointer.zoneInfo) "Release timezone sidecar"`,
		`$previous = Set-ReleasePointerZoneInfo $previous $candidateMetadata.ZoneInfo $candidateMetadata.ZoneInfoSHA256`,
		`Deployments require a prebuilt candidate; run release.ps1 build first`,
		`Start-Candidate $Context.Binary $candidateMetadata.ZoneInfo $candidateMetadata.ZoneInfoSHA256`,
		`Start-Candidate $previous.binary $previous.zoneInfo $previous.zoneInfoSHA256`,
		`Start-Candidate $receipt.previous.binary $receipt.previous.zoneInfo $receipt.previous.zoneInfoSHA256`,
		`Split-Path -Parent $previous.zoneInfo`,
		`$receipt.failedAt -and $receipt.candidate`,
		`Get-PreviousReadinessExpectation $current`,
		`Get-RollbackListenerExpectation $current $receipt.previous`,
		`candidate = $candidatePointer`,
		`current = $candidatePointer`,
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("release startup timezone contract is missing %q", fragment)
		}
	}

	deploy := strings.Index(script, `function Deploy-Candidate`)
	if deploy < 0 {
		t.Fatal("release deploy function is missing")
	}
	if strings.Contains(script[deploy:], `go env GOROOT`) || strings.Contains(script[deploy:], `Get-BuildZoneInfoSource`) {
		t.Fatal("deploy and rollback must use recorded timezone sidecars without querying the Go installation")
	}
	rollback := strings.Index(script[deploy:], `function Invoke-Rollback`)
	if rollback < 0 {
		t.Fatal("release rollback function is missing")
	}
	if strings.Contains(script[deploy:deploy+rollback], `Build-Candidate`) {
		t.Fatal("deploy must consume the unique prebuilt candidate rather than building another artifact")
	}
	manualRollback := script[deploy+rollback:]
	currentIdentity := strings.Index(manualRollback, `Get-PreviousReadinessExpectation $current`)
	currentExecution := strings.Index(manualRollback, `Assert-ZoneInfoArchive $current.binary`)
	if currentIdentity < 0 || currentExecution < 0 || currentIdentity > currentExecution {
		t.Fatal("manual rollback must verify current pointer identity before executing its binary")
	}
}

func TestWindowsWebEntryPointEmbedsTimezoneData(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	projectRoot := filepath.Join(filepath.Dir(filename), "..", "..")
	command := exec.Command("go", "list", "-tags", "webonly", "-deps", ".")
	command.Dir = projectRoot
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list Windows Web dependencies: %v", err)
	}
	if !strings.Contains(strings.ReplaceAll(string(output), "\r\n", "\n"), "\ntime/tzdata\n") {
		t.Fatal("Windows Web entry point must embed IANA timezone data")
	}
}

func readReleaseScript(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	scriptPath := filepath.Join(filepath.Dir(filename), "..", "..", "scripts", "release.ps1")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}
