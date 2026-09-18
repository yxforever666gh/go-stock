//go:build integration

package data_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/research2"
	"go-stock/backend/research2app"
	"go-stock/backend/researchconfig"
	"go-stock/internal/migrations"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Explicitly opt-in only. Configuration is read from a query-only connection;
// ALL evidence, audit, reports and provider caches use disposable databases.
func TestResearch2LiveIsolatedChain(t *testing.T) {
	if os.Getenv("GO_STOCK_LIVE_RESEARCH2_CHAIN") != "1" {
		t.Skip("explicit live-chain opt-in required")
	}
	sourcePath := os.Getenv("GO_STOCK_SOURCE_DB_PATH")
	if sourcePath == "" {
		t.Fatal("source path required")
	}
	source, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(sourcePath)+"?mode=ro&_pragma=query_only(1)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("read-only source unavailable")
	}
	sourceSQL, err := source.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sourceSQL.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	saved, err := researchconfig.New(source).Load(ctx, researchconfig.Research2)
	if err != nil {
		t.Fatal("research2 configuration unavailable")
	}
	temp := t.TempDir()
	main, err := gorm.Open(sqlite.Open(filepath.Join(temp, "main.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	minute, err := gorm.Open(sqlite.Open(filepath.Join(temp, "minute.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	for _, database := range []*gorm.DB{main, minute} {
		sql, err := database.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sql.Close() })
	}
	if err = migrations.MigrateAll(main, minute); err != nil {
		t.Fatal(err)
	}
	var stocks []models.StockBasic
	if err = source.Find(&stocks).Error; err != nil {
		t.Fatal("source stock master unavailable")
	}
	if len(stocks) > 0 {
		if err = main.CreateInBatches(stocks, 200).Error; err != nil {
			t.Fatal("isolated stock master copy failed")
		}
	}
	cfg := researchconfig.Clone(saved.Settings)
	for _, model := range cfg.AiConfigs {
		model.ID = 0
	}
	local, err := researchconfig.New(main).Save(ctx, researchconfig.Research2, 1, cfg)
	if err != nil {
		t.Fatal("isolated configuration copy failed")
	}
	oldMain, oldMinute := db.Dao, db.MinuteDao
	db.Dao, db.MinuteDao = main, minute
	defer func() { db.Dao, db.MinuteDao = oldMain, oldMinute }()
	data.InitAnalyzeSentiment()
	deps, err := data.NewResearch2Dependencies(int(local.Settings.AIAnalysisConfigID), main, minute, local.Settings)
	if err != nil {
		t.Fatal("provider assembly failed")
	}
	observed := &observedLiveEvidence{provider: deps.Evidence}
	deps.Evidence = observed
	runtime, err := research2app.NewRuntime(main, deps)
	if err != nil {
		t.Fatal(err)
	}
	run, runErr := runtime.Runner.RunDiagnostic(ctx, time.Now())
	var trades, recommendations int64
	main.Model(&research2.Trade{}).Count(&trades)
	main.Model(&research2.Recommendation{}).Count(&recommendations)
	var attempts []json.RawMessage
	_ = json.Unmarshal([]byte(run.ModelAttemptLogJSON), &attempts)
	summary := map[string]any{"status": run.Status, "model": run.ModelName, "provider": run.ProviderName, "modelAttempts": len(attempts), "candidateCount": run.RecommendationCount, "reportCharacters": len([]rune(run.ReportMarkdown)), "evidenceSetId": run.EvidenceSetID, "published": run.Published, "trades": trades, "recommendationRows": recommendations, "productionDatabaseWritten": false, "error": runErr != nil}
	summary["degradedReasons"] = observed.evidence.DegradedReasons
	summary["evidenceCutoff"] = observed.evidence.CutoffAt
	summary["stockMasterRows"] = len(stocks)
	summary["failureReason"] = run.FailureReason
	var compact struct {
		Candidates []struct {
			Code           string   `json:"code"`
			CoreEligible   bool     `json:"coreEligible"`
			MinuteBarCount int      `json:"minuteBarCount"`
			Missing        []string `json:"missing"`
		} `json:"candidates"`
	}
	_ = json.Unmarshal([]byte(observed.evidence.Prompt), &compact)
	summary["candidateEvidence"] = compact.Candidates
	if path := os.Getenv("GO_STOCK_CHAIN_RESULT_PATH"); path != "" {
		body, _ := json.MarshalIndent(summary, "", "  ")
		if err = os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if runErr != nil {
		t.Fatalf("live chain did not complete: status=%s; inspect isolated audit without exposing provider credentials", run.Status)
	}
	if run.Status != "success" || len(attempts) == 0 || strings.TrimSpace(run.ReportMarkdown) == "" || run.EvidenceSetID == "" {
		t.Fatalf("incomplete live chain: status=%s attempts=%d candidates=%d", run.Status, len(attempts), run.RecommendationCount)
	}
	if run.Published || trades != 0 || recommendations != 0 {
		t.Fatal("diagnostic emitted executable recommendations")
	}
}

type observedLiveEvidence struct {
	provider research2app.EvidenceProvider
	evidence research2.Evidence
}

func (o *observedLiveEvidence) Collect(ctx context.Context, at time.Time, cash float64) (research2.Evidence, error) {
	value, err := o.provider.Collect(ctx, at, cash)
	o.evidence = value
	return value, err
}
func (o *observedLiveEvidence) CollectWithExclusions(ctx context.Context, at time.Time, excluded map[string]struct{}, cash float64) (research2.Evidence, error) {
	value, err := o.provider.CollectWithExclusions(ctx, at, excluded, cash)
	o.evidence = value
	return value, err
}
