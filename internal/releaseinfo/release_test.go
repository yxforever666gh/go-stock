package releaseinfo

import "testing"

func TestReleaseIdentity185Schema13(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "1.8.5" || manifest.MainSchemaVersion != 13 || manifest.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 13 || status.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
