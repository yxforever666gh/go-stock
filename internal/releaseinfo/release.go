package releaseinfo

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/strategy/v150"
)

// These values are replaced by the Windows release build through -ldflags.
var (
	Commit    = "dev"
	BuildTime = "unknown"
	Dirty     = "true"
)

//go:embed release_manifest.json
var manifestJSON []byte

type ReleaseManifest struct {
	AppVersion             string `json:"appVersion"`
	CurrentStrategyVersion string `json:"currentStrategyVersion"`
	StrategyConfigHash     string `json:"strategyConfigHash"`
	MainSchemaVersion      int    `json:"mainSchemaVersion"`
	MinuteSchemaVersion    int    `json:"minuteSchemaVersion"`
}

type BuildInfo struct {
	Commit         string `json:"commit"`
	BuildTime      string `json:"buildTime"`
	ArtifactSHA256 string `json:"artifactSHA256"`
	Dirty          bool   `json:"dirty"`
}

type ReadinessStatus struct {
	Migrations   bool   `json:"migrations"`
	Database     bool   `json:"database"`
	Services     bool   `json:"services"`
	Scheduler    bool   `json:"scheduler"`
	StrategyMode string `json:"strategyMode"`
	Ready        bool   `json:"ready"`
	Error        string `json:"error,omitempty"`
}

type VersionStatus struct {
	AppVersion             string          `json:"appVersion"`
	Commit                 string          `json:"commit"`
	BuildTime              string          `json:"buildTime"`
	ArtifactSHA256         string          `json:"artifactSHA256"`
	Dirty                  bool            `json:"dirty"`
	MainSchemaVersion      int             `json:"mainSchemaVersion"`
	MinuteSchemaVersion    int             `json:"minuteSchemaVersion"`
	CurrentStrategyVersion string          `json:"currentStrategyVersion"`
	StrategyConfigHash     string          `json:"strategyConfigHash"`
	ConfigHash             string          `json:"configHash"`
	StartedAt              time.Time       `json:"startedAt"`
	StrategyMode           string          `json:"strategyMode"`
	Readiness              ReadinessStatus `json:"readiness"`
}

var state = struct {
	sync.RWMutex
	manifest  ReleaseManifest
	build     BuildInfo
	readiness ReadinessStatus
	startedAt time.Time
}{startedAt: time.Now().UTC()}

func init() {
	if err := json.Unmarshal(manifestJSON, &state.manifest); err != nil {
		panic(fmt.Sprintf("invalid embedded release manifest: %v", err))
	}
	if state.manifest.CurrentStrategyVersion != v150.StrategyVersion {
		panic(fmt.Sprintf("release manifest strategy %s does not match strategy package %s", state.manifest.CurrentStrategyVersion, v150.StrategyVersion))
	}
	if state.manifest.StrategyConfigHash != v150.FixedStrategyV150ConfigHash() {
		panic(fmt.Sprintf("release manifest strategy config hash %s does not match strategy package %s", state.manifest.StrategyConfigHash, v150.FixedStrategyV150ConfigHash()))
	}
	state.build = BuildInfo{
		Commit:    strings.TrimSpace(Commit),
		BuildTime: strings.TrimSpace(BuildTime),
		Dirty:     parseBoolDefault(Dirty, true),
	}
}

func Manifest() ReleaseManifest {
	state.RLock()
	defer state.RUnlock()
	return state.manifest
}

func ManifestJSON() []byte {
	return append([]byte(nil), manifestJSON...)
}

func Build() BuildInfo {
	state.RLock()
	defer state.RUnlock()
	return state.build
}

// InitializeBuildInfo hashes the exact executable that is running. It should
// be called once, before storage and services are initialized.
func InitializeBuildInfo(executablePath string) error {
	if strings.TrimSpace(executablePath) == "" {
		var err error
		executablePath, err = os.Executable()
		if err != nil {
			return err
		}
	}
	f, err := os.Open(executablePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	state.Lock()
	state.build = BuildInfo{
		Commit:         strings.TrimSpace(Commit),
		BuildTime:      strings.TrimSpace(BuildTime),
		ArtifactSHA256: hex.EncodeToString(h.Sum(nil)),
		Dirty:          parseBoolDefault(Dirty, true),
	}
	state.Unlock()
	return nil
}

func MarkStorageReady() {
	state.Lock()
	state.readiness.Migrations = true
	state.readiness.Database = true
	state.readiness.Error = ""
	state.Unlock()
}

func MarkServicesReady() {
	state.Lock()
	state.readiness.Services = true
	state.Unlock()
}

func MarkSchedulerReady(ready bool) {
	state.Lock()
	state.readiness.Scheduler = ready
	state.Unlock()
}

func MarkNotReady(err error) {
	state.Lock()
	defer state.Unlock()
	if err != nil {
		state.readiness.Error = err.Error()
	}
}

func Readiness() ReadinessStatus {
	return readinessForMode("")
}

func SystemVersion(strategyMode string) VersionStatus {
	state.RLock()
	manifest := state.manifest
	build := state.build
	startedAt := state.startedAt
	state.RUnlock()
	readiness := readinessForMode(strategyMode)
	return VersionStatus{
		AppVersion:             manifest.AppVersion,
		Commit:                 build.Commit,
		BuildTime:              build.BuildTime,
		ArtifactSHA256:         build.ArtifactSHA256,
		Dirty:                  build.Dirty,
		MainSchemaVersion:      manifest.MainSchemaVersion,
		MinuteSchemaVersion:    manifest.MinuteSchemaVersion,
		CurrentStrategyVersion: manifest.CurrentStrategyVersion,
		StrategyConfigHash:     manifest.StrategyConfigHash,
		ConfigHash:             manifest.StrategyConfigHash,
		StartedAt:              startedAt,
		StrategyMode:           normalizeMode(strategyMode),
		Readiness:              readiness,
	}
}

func readinessForMode(strategyMode string) ReadinessStatus {
	state.RLock()
	status := state.readiness
	state.RUnlock()
	status.StrategyMode = normalizeMode(strategyMode)
	status.Ready = status.Migrations && status.Database && status.Services && status.Scheduler && status.Error == ""
	return status
}

func normalizeMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "live" {
		return "paused"
	}
	return mode
}

func parseBoolDefault(raw string, fallback bool) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}
