package releaseinfo

import "testing"

func TestReleaseIdentity170(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "1.7.1" || manifest.MainSchemaVersion != 10 || manifest.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 10 || status.MinuteSchemaVersion != 2 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
