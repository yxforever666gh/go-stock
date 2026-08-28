package releaseinfo

import "testing"

func TestReleaseIdentity220Schema17(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.2.0" || manifest.MainSchemaVersion != 17 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 17 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
