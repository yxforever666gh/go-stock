package migrations

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"testing"
	"time"

	"go-stock/backend/research"
	"go-stock/backend/research2"
)

func TestSchema16DrawingIsolationRevisionChecksAndImmutability(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := applyChartDrawingSchema(mainDB); err != nil {
		t.Fatalf("repeat schema 16: %v", err)
	}
	if err := verifyMainSchema16Runtime(mainDB); err != nil {
		t.Fatal(err)
	}

	now := "2026-08-28T14:30:00+08:00"
	documentInsert := `INSERT INTO chart_drawing_documents(
drawing_document_id,scope_type,scope_id,asset_type,market,code,period,adjustment,revision,drawings_json,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`
	if err := mainDB.Exec(documentInsert, "drawing-doc-1", "research_run", "run-1", "stock", "SH", "sh600000", "1m", "qfq", 0, `[]`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(documentInsert, "drawing-doc-1", "research_run", "run-2", "stock", "SH", "sh600001", "1m", "qfq", 0, `[]`, now, now).Error; err == nil {
		t.Fatal("drawing_document_id uniqueness was not enforced")
	}
	if err := mainDB.Exec(documentInsert, "drawing-doc-2", "research_run", "run-1", "stock", "SH", "sh600000", "1m", "qfq", 0, `[]`, now, now).Error; err == nil {
		t.Fatal("scope/asset isolation key accepted a duplicate current document")
	}
	if err := mainDB.Exec(documentInsert, "drawing-doc-negative", "research_run", "run-negative", "stock", "SH", "sh600000", "1m", "qfq", -1, `[]`, now, now).Error; err == nil {
		t.Fatal("current drawing document accepted a negative revision")
	}

	revisionInsert := `INSERT INTO chart_drawing_revisions(document_id,revision,drawings_json,created_at) VALUES (?,?,?,?)`
	if err := mainDB.Exec(revisionInsert, "drawing-doc-1", 0, `[{"type":"line"}]`, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(revisionInsert, "drawing-doc-1", 0, `[]`, now).Error; err == nil {
		t.Fatal("document/revision uniqueness was not enforced")
	}
	if err := mainDB.Exec(revisionInsert, "drawing-doc-1", -1, `[]`, now).Error; err == nil {
		t.Fatal("drawing history accepted a negative revision")
	}
	if err := mainDB.Exec("UPDATE chart_drawing_revisions SET drawings_json = '[]' WHERE document_id = ? AND revision = 0", "drawing-doc-1").Error; err == nil {
		t.Fatal("immutable drawing revision accepted an update")
	}
	if err := mainDB.Exec("DELETE FROM chart_drawing_revisions WHERE document_id = ? AND revision = 0", "drawing-doc-1").Error; err == nil {
		t.Fatal("immutable drawing revision accepted a delete")
	}
	if err := mainDB.Exec("UPDATE chart_drawing_documents SET revision = 1, drawings_json = ?, updated_at = ? WHERE drawing_document_id = ?", `[{"type":"ray"}]`, now, "drawing-doc-1").Error; err != nil {
		t.Fatalf("current drawing document must remain mutable: %v", err)
	}
	var revision int
	if err := mainDB.Raw("SELECT revision FROM chart_drawing_documents WHERE drawing_document_id = ?", "drawing-doc-1").Scan(&revision).Error; err != nil || revision != 1 {
		t.Fatalf("current drawing revision=%d err=%v", revision, err)
	}
}

func TestSchema16VerifierRejectsMissingImmutabilityGuard(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec("DROP TRIGGER immutable_chart_drawing_revisions_update").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyMain(mainDB); err == nil {
		t.Fatal("schema 16 verification accepted a missing revision immutability trigger")
	}
}

func TestSchema15To17PreservesResearchAccountingAndLeavesMinuteSchema3Unchanged(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	applyPublishedMigrationPrefix(t, mainDB, mainMigrations, 15, "2.0.0")
	applyPublishedMigrationPrefix(t, minuteDB, minuteMigrations, 3, "2.0.0")
	if mainDB.Migrator().HasTable("chart_drawing_documents") || mainDB.Migrator().HasTable("chart_drawing_revisions") {
		t.Fatal("schema 15 fixture unexpectedly contains schema 16 drawing tables")
	}

	now := time.Date(2026, 8, 28, 14, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	completed := now.Add(time.Minute)
	if err := mainDB.Model(&research.SimulatedAccount{}).Where("id = ?", 1).Update("cash", 432109.87).Error; err != nil {
		t.Fatal(err)
	}
	run1 := research.AnalysisRun{RunID: "schema16-r1-run", ScheduledFor: now, StartedAt: now, CompletedAt: &completed, Status: "success", ModelAttemptLogJSON: "[]", RecommendationCount: 1}
	if err := mainDB.Create(&run1).Error; err != nil {
		t.Fatal(err)
	}
	rec1 := research.Recommendation{RecommendationID: "schema16-r1-rec", AnalysisRunID: run1.RunID, StockCode: "sh600000", StockName: "浦发银行", SignalAt: now, Status: "closed"}
	if err := mainDB.Create(&rec1).Error; err != nil {
		t.Fatal(err)
	}
	trade1 := research.SimulatedTrade{TradeID: "schema16-r1-trade", RecommendationID: rec1.RecommendationID, StockCode: rec1.StockCode, Side: "sell", TradedAt: completed, MarketPrice: 10.25, ExecutionPrice: 10.23, Quantity: 100, Notional: 1023, Commission: 5, StampDuty: 0.5115, TransferFee: 0.01023, SlippageAmount: 2, TotalFees: 7.52173, NetCashFlow: 1015.47827}
	if err := mainDB.Create(&trade1).Error; err != nil {
		t.Fatal(err)
	}
	position1 := research.Position{RecommendationID: rec1.RecommendationID, StockCode: rec1.StockCode, StockName: rec1.StockName, Market: "SH", Quantity: 100, EntryAt: now, EntryPrice: 10.01, BuyFees: 5.01, Status: "closed", ExitAt: &completed, ExitPrice: 10.23, SellFees: 5.52173, NetPnL: 9.46827}
	if err := mainDB.Create(&position1).Error; err != nil {
		t.Fatal(err)
	}
	snapshot1 := research.AccountValuationSnapshot{SnapshotID: "schema16-r1-snapshot", SnapshotType: "trade", TradingDate: "2026-08-28", ValuedAt: completed, Cash: 432109.87, PositionValue: 10000, NetAssetValue: 442109.87, CumulativeNetContribution: 500000, UnitValue: 0.88421974, TimeWeightedReturn: -0.11578026, ValuationStatus: "complete"}
	if err := mainDB.Create(&snapshot1).Error; err != nil {
		t.Fatal(err)
	}

	if err := mainDB.Model(&research2.Account{}).Where("id = ?", 1).Update("cash", 9876.54).Error; err != nil {
		t.Fatal(err)
	}
	run2 := research2.AnalysisRun{RunID: "schema16-r2-run", TradingDate: "2026-08-28", ScheduledFor: now, StartedAt: now, EvidenceCutoffAt: now, GeneratedAt: &completed, Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 1, OnTime: true}
	if err := mainDB.Create(&run2).Error; err != nil {
		t.Fatal(err)
	}
	rec2 := research2.Recommendation{RecommendationID: "schema16-r2-rec", AnalysisRunID: run2.RunID, StockCode: "sz000001", StockName: "平安银行", SignalAt: now, FinalScore: 61, ReferencePrice: 10, BuyLower: 9.8, BuyUpper: 10.2, Status: "closed", TargetBuyAt: now, BuyAt: &now, BuyPrice: 10.01, Quantity: 100, BuyFees: 5.01, TargetSellAt: &completed, SellAt: &completed, SellPrice: 10.20, SellFees: 5.52, NetPnL: 8.47}
	if err := mainDB.Create(&rec2).Error; err != nil {
		t.Fatal(err)
	}
	trade2 := research2.Trade{TradeID: "schema16-r2-trade", RecommendationID: rec2.RecommendationID, Side: "sell", TradedAt: completed, MarketPrice: 10.22, ExecutionPrice: 10.20, Quantity: 100, Commission: 5, StampDuty: 0.51, TransferFee: 0.01, SlippageAmount: 2, NetCashFlow: 1014.48}
	if err := mainDB.Create(&trade2).Error; err != nil {
		t.Fatal(err)
	}
	snapshot2 := research2.AccountSnapshot{SnapshotID: "schema16-r2-snapshot", ValuedAt: completed, TradingDate: "2026-08-28", SnapshotType: "sell", Cash: 9876.54, PositionValue: 0, NetAssetValue: 9876.54, NetProfit: -2123.46, ReturnRate: -0.176955}
	if err := mainDB.Create(&snapshot2).Error; err != nil {
		t.Fatal(err)
	}

	if err := minuteDB.Exec(`INSERT INTO market_bar_cache(asset_type,symbol,period,adjustment,bar_time,open,high,low,close,source,updated_at)
VALUES ('stock','sh600000','1m','qfq',1,10,11,9,10.5,'fixture',1)`).Error; err != nil {
		t.Fatal(err)
	}
	beforeHistory := captureResearchHistory(t, mainDB)
	beforeDigest := researchHistoryDigest(beforeHistory)
	var minuteMigrationCountBefore int64
	if err := minuteDB.Model(&MigrationRecord{}).Count(&minuteMigrationCountBefore).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatalf("repeat schema 16 migration: %v", err)
	}
	afterHistory := captureResearchHistory(t, mainDB)
	if afterDigest := researchHistoryDigest(afterHistory); afterDigest != beforeDigest {
		t.Fatalf("schema 16 rewrote research history: before=%s after=%s", beforeDigest, afterDigest)
	}
	for name, before := range beforeHistory {
		if !bytes.Equal(before, afterHistory[name]) {
			t.Fatalf("schema 16 rewrote %s", name)
		}
	}

	var storedR1Trade research.SimulatedTrade
	if err := mainDB.Where("trade_id = ?", trade1.TradeID).First(&storedR1Trade).Error; err != nil || storedR1Trade.TotalFees != trade1.TotalFees || storedR1Trade.NetCashFlow != trade1.NetCashFlow {
		t.Fatalf("research1 trade changed: %+v err=%v", storedR1Trade, err)
	}
	var storedR1Position research.Position
	if err := mainDB.Where("recommendation_id = ?", position1.RecommendationID).First(&storedR1Position).Error; err != nil || storedR1Position.NetPnL != position1.NetPnL {
		t.Fatalf("research1 pnl changed: %+v err=%v", storedR1Position, err)
	}
	var storedR2Trade research2.Trade
	if err := mainDB.Where("trade_id = ?", trade2.TradeID).First(&storedR2Trade).Error; err != nil || storedR2Trade.Commission != trade2.Commission || storedR2Trade.StampDuty != trade2.StampDuty || storedR2Trade.TransferFee != trade2.TransferFee || storedR2Trade.NetCashFlow != trade2.NetCashFlow {
		t.Fatalf("research2 trade changed: %+v err=%v", storedR2Trade, err)
	}
	var storedR2Snapshot research2.AccountSnapshot
	if err := mainDB.Where("snapshot_id = ?", snapshot2.SnapshotID).First(&storedR2Snapshot).Error; err != nil || storedR2Snapshot.NetProfit != snapshot2.NetProfit || storedR2Snapshot.ReturnRate != snapshot2.ReturnRate {
		t.Fatalf("research2 pnl changed: %+v err=%v", storedR2Snapshot, err)
	}

	mainStatus, err := VerifyMain(mainDB)
	if err != nil {
		t.Fatal(err)
	}
	minuteStatus, err := VerifyMinute(minuteDB)
	if err != nil {
		t.Fatal(err)
	}
	if mainStatus.CurrentVersion != 24 || minuteStatus.CurrentVersion != 3 {
		t.Fatalf("schema versions main=%d minute=%d", mainStatus.CurrentVersion, minuteStatus.CurrentVersion)
	}
	var minuteMigrationCountAfter, barCount int64
	if err := minuteDB.Model(&MigrationRecord{}).Count(&minuteMigrationCountAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := minuteDB.Table("market_bar_cache").Where("symbol = ?", "sh600000").Count(&barCount).Error; err != nil {
		t.Fatal(err)
	}
	if minuteMigrationCountBefore != 3 || minuteMigrationCountAfter != minuteMigrationCountBefore || barCount != 1 {
		t.Fatalf("minute schema changed: migrations=%d/%d bars=%d", minuteMigrationCountBefore, minuteMigrationCountAfter, barCount)
	}
}

func researchHistoryDigest(values map[string][]byte) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		_, _ = hash.Write([]byte(key))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(values[key])
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
