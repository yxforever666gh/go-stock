package releaseinfo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func TestReleaseManifestAndVersionStatusAgree(t *testing.T) {
	manifest := Manifest()
	if !semanticVersionPattern.MatchString(manifest.AppVersion) || manifest.MainSchemaVersion < 1 || manifest.MinuteSchemaVersion < 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	status := SystemVersion()
	if status.AppVersion != manifest.AppVersion || status.MainSchemaVersion != manifest.MainSchemaVersion || status.MinuteSchemaVersion != manifest.MinuteSchemaVersion {
		t.Fatalf("unexpected version status: %+v", status)
	}
	notes, err := os.ReadFile(filepath.Join("..", "..", "RELEASE_NOTES.md"))
	if err != nil {
		t.Fatal(err)
	}
	topVersion := ""
	for _, line := range strings.Split(string(notes), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "##" && semanticVersionPattern.MatchString(fields[1]) {
			topVersion = fields[1]
			break
		}
	}
	if topVersion != manifest.AppVersion {
		t.Fatalf("top release notes version = %q, want %q", topVersion, manifest.AppVersion)
	}
}
