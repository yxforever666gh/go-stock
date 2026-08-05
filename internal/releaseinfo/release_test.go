package releaseinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"go-stock/backend/strategy/v150"
)

func TestManifestPinsAppSchemaAndFrozenStrategyIdentity(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "1.5.1" || manifest.CurrentStrategyVersion != v150.StrategyVersion {
		t.Fatalf("unexpected release identity: %+v", manifest)
	}
	if manifest.MainSchemaVersion != 1 || manifest.MinuteSchemaVersion != 1 {
		t.Fatalf("unexpected schema identity: %+v", manifest)
	}
	if manifest.StrategyConfigHash != v150.FixedStrategyV150ConfigHash() {
		t.Fatalf("manifest config hash = %s, strategy = %s", manifest.StrategyConfigHash, v150.FixedStrategyV150ConfigHash())
	}
	status := SystemVersion("paused")
	if status.ConfigHash != manifest.StrategyConfigHash || status.StrategyConfigHash != manifest.StrategyConfigHash {
		t.Fatalf("version status does not match manifest: %+v", status)
	}
}

func TestInitializeBuildInfoHashesExactArtifact(t *testing.T) {
	payload := []byte("go-stock-release-artifact")
	path := filepath.Join(t.TempDir(), "go-stock-web.exe")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InitializeBuildInfo(path); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(payload)
	if got := Build().ArtifactSHA256; got != hex.EncodeToString(want[:]) {
		t.Fatalf("artifact hash = %s, want %s", got, hex.EncodeToString(want[:]))
	}
}
