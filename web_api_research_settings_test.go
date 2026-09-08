package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go-stock/backend/models"
	"go-stock/backend/research2"
	"go-stock/backend/research2app"
	"go-stock/backend/researchapp"
	"go-stock/backend/researchconfig"
	"go-stock/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newResearchSettingsTestApp(t *testing.T) *App {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "centers.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := database.AutoMigrate(&researchconfig.Record{}, &models.AIConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, center := range []string{researchconfig.Research1, researchconfig.Research2} {
		cfg := &models.SettingConfig{Settings: &models.Settings{TushareToken: center, TencentMinuteEnabled: true, Research2AutoEnabled: true, AICapitalDeploymentEnabled: true, AITargetCapitalUtilization: 0.9, AIMaxImmediateBuysPerRun: 2, AIReanalysisIntervalMinutes: 30, AIReviewStartTime: "09:50", AIReviewIntervalMinutes: 15, PrivateMinuteLevel: "1min", PrivateMinuteProxyMode: "disable"}, MinuteProviderOrder: []string{"tencent", "sina", "akshare", "private"}}
		raw, err := researchconfig.ConfigJSON(center, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err = database.Create(&researchconfig.Record{Center: center, Revision: 1, ConfigJSON: string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
		if err = database.Create(&models.AIConfig{Owner: center, Name: center, ApiKey: center + "-key", ModelName: "fixture", Sort: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	app := NewAppWithServices(service.AppServices{})
	app.researchConfigStore = researchconfig.New(database)
	app.researchDatabase = database
	return app
}

func researchSettingsRequest(t *testing.T, mux *http.ServeMux, method, center string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(method, "/api/v1/research-centers/"+center+"/settings", bytes.NewReader(body)))
	return recorder
}

func TestResearchSettingsAPIIsolatesOwnersAndRejectsStalePages(t *testing.T) {
	app := newResearchSettingsTestApp(t)
	mux := http.NewServeMux()
	registerResearchSettingsRoutes(mux, app)
	read := func(center string) researchSettingsPayload {
		recorder := researchSettingsRequest(t, mux, http.MethodGet, center, nil)
		if recorder.Code != http.StatusOK {
			t.Fatal(recorder.Body.String())
		}
		var payload researchSettingsPayload
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	r1, r2 := read(researchconfig.Research1), read(researchconfig.Research2)
	r1.AIConfigs[0].ApiKey = "new-r1-key"
	if got := researchSettingsRequest(t, mux, http.MethodPut, researchconfig.Research1, r1); got.Code != http.StatusOK {
		t.Fatal(got.Body.String())
	}
	if got := researchSettingsRequest(t, mux, http.MethodPut, researchconfig.Research1, r1); got.Code != http.StatusConflict {
		t.Fatalf("stale status=%d %s", got.Code, got.Body.String())
	}
	if got := read(researchconfig.Research2); !reflect.DeepEqual(got, r2) {
		t.Fatal("R1 save changed R2")
	}
	// Another center's revision is independent, but its model IDs cannot cross.
	r2.AIConfigs = r1.AIConfigs
	if got := researchSettingsRequest(t, mux, http.MethodPut, researchconfig.Research2, r2); got.Code != http.StatusBadRequest {
		t.Fatalf("owner status=%d", got.Code)
	}
	r2 = read(researchconfig.Research2)
	if r2.Revision != 1 {
		t.Fatal("rejected save advanced revision")
	}
	r2.Config = json.RawMessage(`{"aiCapitalDeploymentEnabled":false}`)
	if got := researchSettingsRequest(t, mux, http.MethodPut, researchconfig.Research2, r2); got.Code != http.StatusBadRequest {
		t.Fatalf("cross-field status=%d", got.Code)
	}
}

func TestResearchRuntimeKeepsTaskSnapshotAndDoesNotReloadOtherCenter(t *testing.T) {
	app := newResearchSettingsTestApp(t)
	var r1Inputs []*models.SettingConfig
	r2Created := 0
	app.researchFactory = func(cfg *models.SettingConfig) (*researchapp.Runtime, error) {
		r1Inputs = append(r1Inputs, cfg)
		return &researchapp.Runtime{}, nil
	}
	app.research2Factory = func(*models.SettingConfig) (*research2app.Runtime, error) {
		r2Created++
		return &research2app.Runtime{}, nil
	}
	r1, _ := app.researchConfiguration(t.Context(), researchconfig.Research1)
	r2, _ := app.researchConfiguration(t.Context(), researchconfig.Research2)
	old, err := app.researchRuntimeForSettings(r1.Settings)
	if err != nil {
		t.Fatal(err)
	}
	other, err := app.ensureResearch2Runtime(r2.Settings)
	if err != nil {
		t.Fatal(err)
	}
	r1.Settings.AiConfigs[0].ApiKey = "next-round"
	if _, err = app.saveResearchConfiguration(t.Context(), researchconfig.Research1, r1.Revision, r1.Settings); err != nil {
		t.Fatal(err)
	}
	if len(r1Inputs) != 1 || r1Inputs[0].AiConfigs[0].ApiKey != "research1-key" {
		t.Fatal("save modified current task")
	}
	next, err := app.getResearchRuntime()
	if err != nil || next == old || len(r1Inputs) != 2 || r1Inputs[1].AiConfigs[0].ApiKey != "next-round" {
		t.Fatalf("next task err=%v inputs=%d", err, len(r1Inputs))
	}
	same, err := app.ensureResearch2Runtime(r2.Settings)
	if err != nil || same != other || r2Created != 1 {
		t.Fatal("R1 save replaced R2 runtime")
	}
	// A mail-only edit must retain the same analysis runtime and collector state.
	r2.Settings.Research2EmailTo = "new@example.test"
	same, err = app.ensureResearch2Runtime(r2.Settings)
	if err != nil || same != other {
		t.Fatal("mail-only settings replaced analysis runtime")
	}
}

func TestGlobalSettingsRejectOldResearchPayloadAndPreserveUnchangedModels(t *testing.T) {
	global := &models.SettingConfig{Settings: &models.Settings{DarkTheme: false, TushareToken: "global-token", AICapitalDeploymentEnabled: true}, AiConfigs: []*models.AIConfig{{ID: 1, ApiKey: "global-key"}}}
	if _, err := mergeGlobalSettings(global, map[string]json.RawMessage{"research2AutoEnabled": json.RawMessage(`false`)}); err == nil {
		t.Fatal("old global page can override research")
	}
	merged, err := mergeGlobalSettings(global, map[string]json.RawMessage{"darkTheme": json.RawMessage(`true`)})
	if err != nil || !merged.DarkTheme || merged.TushareToken != "global-token" || merged.AiConfigs != nil || global.DarkTheme {
		t.Fatalf("partial global save err=%v", err)
	}
}

func TestResearchModelSelectionRejectsAnotherCenter(t *testing.T) {
	app := newResearchSettingsTestApp(t)
	r1, _ := app.researchConfiguration(context.Background(), researchconfig.Research1)
	r2, _ := app.researchConfiguration(context.Background(), researchconfig.Research2)
	if _, err := researchModel(r1, int(r2.Settings.AiConfigs[0].ID)); err == nil {
		t.Fatal("cross-center model was accepted")
	}
}

func TestResearch2DisableBeforeRuntimeCreationKeepsExitsAndDoesNotInterruptAnalysis(t *testing.T) {
	app := newResearchSettingsTestApp(t)
	database := app.researchDatabase
	if err := database.AutoMigrate(&research2.ExecutionChain{}, &research2.AnalysisRun{}, &research2.Recommendation{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(research2Location())
	chain := research2.ExecutionChain{ChainID: "chain", TradingDate: now.Format("2006-01-02"), Status: "running", TargetSlots: 3, StartedAt: now, ScheduledFor: now}
	if err := database.Create(&chain).Error; err != nil {
		t.Fatal(err)
	}
	run := research2.AnalysisRun{RunID: "active-model", ChainID: chain.ChainID, TradingDate: chain.TradingDate, AttemptNo: 1, Status: "running", StartedAt: now, ScheduledFor: now, EvidenceCutoffAt: now, SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	if err := database.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	// No mail delivery is part of this fixture. Keep its entrypoint from running.
	app.research2EmailMu.Lock()
	defer app.research2EmailMu.Unlock()
	snapshot, _ := app.researchConfiguration(t.Context(), researchconfig.Research2)
	snapshot.Settings.Research2AutoEnabled = false
	if _, err := app.saveResearchConfiguration(t.Context(), researchconfig.Research2, snapshot.Revision, snapshot.Settings); err != nil {
		t.Fatal(err)
	}
	if !app.runtime.Shutdown(time.Second) {
		t.Fatal("config tasks did not finish")
	}
	var stored research2.AnalysisRun
	if err := database.Where("run_id = ?", run.RunID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "running" {
		t.Fatalf("ordinary save mislabeled analysis: %s", stored.Status)
	}
	var stopped research2.ExecutionChain
	if err := database.Where("chain_id = ?", chain.ChainID).First(&stopped).Error; err != nil {
		t.Fatal(err)
	}
	if stopped.Status != "disabled" || app.research2Runtime != nil {
		t.Fatal("disable required an analysis runtime")
	}
	if _, exists := app.getCronEntry(research2AnalysisEntryKey); exists {
		t.Fatal("disabled center scheduled new analyses")
	}
	for _, key := range []string{research2TradingEntryKey, research2MetricsEntryKey} {
		if _, exists := app.getCronEntry(key); !exists {
			t.Fatalf("disabled center lost exit/valuation task %s", key)
		}
	}
	restarted := &App{ctx: context.Background(), researchDatabase: database, researchConfigStore: app.researchConfigStore}
	if err := restarted.recoverResearch2RunsOnStartup(now); err != nil {
		t.Fatal(err)
	}
	if err := database.Where("run_id = ?", run.RunID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "failed" {
		t.Fatalf("real cold-start recovery did not close interrupted run: %s", stored.Status)
	}
}
