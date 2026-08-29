package releaseinfo

import "testing"

func TestReleaseIdentity270Schema21(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.7.0" || manifest.MainSchemaVersion != 21 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 21 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
