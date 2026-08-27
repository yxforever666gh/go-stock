package releaseinfo

import "testing"

func TestReleaseIdentity186Schema14(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "1.8.6" || manifest.MainSchemaVersion != 14 || manifest.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 14 || status.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
