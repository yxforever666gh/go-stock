package releaseinfo

import "testing"

func TestReleaseIdentity210Schema16(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.1.0" || manifest.MainSchemaVersion != 16 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 16 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
