package releaseinfo

import "testing"

func TestReleaseIdentity278Schema24(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.7.8" || manifest.MainSchemaVersion != 24 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 24 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
