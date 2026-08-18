package releaseinfo

import "testing"

func TestReleaseIdentity160(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "1.6.5" || manifest.MainSchemaVersion != 8 || manifest.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 8 || status.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
