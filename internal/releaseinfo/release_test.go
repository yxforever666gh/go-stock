package releaseinfo

import "testing"

func TestReleaseIdentity272Schema23(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.7.3" || manifest.MainSchemaVersion != 23 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 23 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
