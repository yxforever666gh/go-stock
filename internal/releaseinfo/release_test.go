package releaseinfo

import "testing"

func TestReleaseIdentity2710Schema25(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.7.11" || manifest.MainSchemaVersion != 25 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 25 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
