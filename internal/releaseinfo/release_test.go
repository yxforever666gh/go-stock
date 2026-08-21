package releaseinfo

import "testing"

func TestReleaseIdentity173Schema11(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "1.7.3" || manifest.MainSchemaVersion != 11 || manifest.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 11 || status.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
