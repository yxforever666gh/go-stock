package releaseinfo

import "testing"

func TestReleaseIdentity176Schema11(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "1.7.6" || manifest.MainSchemaVersion != 11 || manifest.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 11 || status.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
