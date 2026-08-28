package releaseinfo

import "testing"

func TestReleaseIdentity230Schema18(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.3.0" || manifest.MainSchemaVersion != 18 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 18 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
