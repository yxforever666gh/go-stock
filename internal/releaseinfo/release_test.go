package releaseinfo

import "testing"

func TestReleaseIdentity200Schema15(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.0.0" || manifest.MainSchemaVersion != 15 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 15 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
