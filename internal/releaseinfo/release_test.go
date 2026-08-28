package releaseinfo

import "testing"

func TestReleaseIdentity240Schema19(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.4.0" || manifest.MainSchemaVersion != 19 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 19 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
