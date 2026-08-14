package releaseinfo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAppVersionIsConsistentAcrossReleaseArtifacts(t *testing.T) {
	manifest := Manifest()
	version := strings.TrimSpace(manifest.AppVersion)
	if version == "" {
		t.Fatal("release manifest appVersion is required")
	}
	root := releaseinfoRepositoryRoot(t)

	var frontendPackage struct {
		Version string `json:"version"`
	}
	readJSONFile(t, filepath.Join(root, "frontend", "package.json"), &frontendPackage)
	assertReleaseVersion(t, "frontend/package.json version", frontendPackage.Version, version)

	var lockfile struct {
		Version  string `json:"version"`
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	readJSONFile(t, filepath.Join(root, "frontend", "package-lock.json"), &lockfile)
	assertReleaseVersion(t, "frontend/package-lock.json root version", lockfile.Version, version)
	rootPackage, ok := lockfile.Packages[""]
	if !ok {
		t.Fatal("frontend/package-lock.json packages[\"\"] is required")
	}
	assertReleaseVersion(t, "frontend/package-lock.json packages[\"\"].version", rootPackage.Version, version)

	var contract struct {
		Info struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
	}
	readYAMLFile(t, filepath.Join(root, "api", "openapi.yaml"), &contract)
	assertReleaseVersion(t, "api/openapi.yaml info.version", contract.Info.Version, version)

	readme := readTextFile(t, filepath.Join(root, "README.md"))
	if !strings.Contains(readme, "App "+version) {
		t.Fatalf("README.md does not identify current App version %q", version)
	}
	changelog := readTextFile(t, filepath.Join(root, "CHANGELOG.md"))
	if got := firstChangelogVersion(changelog); got != version {
		t.Fatalf("CHANGELOG.md first release = %q, want %q", got, version)
	}

}

func releaseinfoRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve releaseinfo test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func readYAMLFile(t *testing.T, path string, target any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(content, target); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func assertReleaseVersion(t *testing.T, artifact, got, want string) {
	t.Helper()
	if strings.TrimSpace(got) != want {
		t.Fatalf("%s = %q, want %q", artifact, got, want)
	}
}

func firstChangelogVersion(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		return strings.TrimSpace(strings.SplitN(strings.TrimPrefix(line, "## "), " - ", 2)[0])
	}
	return ""
}
