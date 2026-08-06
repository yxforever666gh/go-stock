package releaseinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseRollbackRequiresExactPreviousReadiness(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	scriptPath := filepath.Join(filepath.Dir(filename), "..", "..", "scripts", "release.ps1")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(content), "\r\n", "\n")

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
