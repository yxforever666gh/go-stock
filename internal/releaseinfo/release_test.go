package releaseinfo

import "testing"

func TestReleaseIdentity2712Schema26(t *testing.T) {
	manifest := Manifest()
	if manifest.AppVersion != "2.8.0" || manifest.MainSchemaVersion != 26 || manifest.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != 26 || status.MinuteSchemaVersion != 3 {
		t.Fatalf("unexpected version status: %+v", status)
	}
}
