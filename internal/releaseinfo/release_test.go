package releaseinfo

import "testing"

func TestReleaseIdentity272Schema22(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.7.2" || manifest.MainSchemaVersion != 22 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 22 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
