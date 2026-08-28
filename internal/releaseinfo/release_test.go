package releaseinfo

import "testing"

func TestReleaseIdentity260Schema20(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.6.0" || manifest.MainSchemaVersion != 20 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 20 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
