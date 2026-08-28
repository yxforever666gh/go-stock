package migrations

import (
	"bytes"
	"testing"
	"time"

	"go-stock/backend/research"
	"go-stock/backend/research2"
)

func TestSchema17ThemeCatalystUniquenessChecksAndImmutability(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := applyThemeCatalystSchema(mainDB); err != nil {
		t.Fatalf("repeat schema 17: %v", err)
	}
	if err := verifyMainSchema17Runtime(mainDB); err != nil {
		t.Fatal(err)
	}

	now := "2026-08-28T15:00:00+08:00"
	themeInsert := `INSERT INTO market_themes(theme_id,canonical_name,normalized_name,description,status,created_at,updated_at)
VALUES (?,?,?,?,?,?,?)`
	if err := mainDB.Exec(themeInsert, "theme-ai", "人工智能", "人工智能", "fixture", "active", now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(themeInsert, "theme-ai", "机器人", "机器人", "fixture", "active", now, now).Error; err == nil {
		t.Fatal("theme_id uniqueness was not enforced")
	}
	if err := mainDB.Exec(themeInsert, "theme-duplicate-name", "AI", "人工智能", "fixture", "active", now, now).Error; err == nil {
		t.Fatal("normalized theme name uniqueness was not enforced")
	}
	if err := mainDB.Exec(themeInsert, "theme-invalid", "无效", "无效", "fixture", "deleted", now, now).Error; err == nil {
		t.Fatal("theme status CHECK was not enforced")
	}

	aliasInsert := `INSERT INTO market_theme_aliases(alias_id,theme_id,alias,normalized_alias,source,created_at) VALUES (?,?,?,?,?,?)`
	if err := mainDB.Exec(aliasInsert, "alias-ai", "theme-ai", "AI", "ai", "fixture", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(aliasInsert, "alias-ai-2", "theme-ai", "人工智能产业", "ai", "fixture", now).Error; err == nil {
		t.Fatal("normalized alias uniqueness was not enforced")
	}

	snapshotInsert := `INSERT INTO market_theme_daily_snapshots(
snapshot_id,theme_id,trade_date,cycle_no,lifecycle_stage,rank,heat_score,summary,observed_at,frozen_at,content_hash,constituent_count,catalyst_count,conflicting_catalyst_count,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if err := mainDB.Exec(snapshotInsert, "snapshot-ai-1", "theme-ai", "2026-08-28", 1, "观察", 1, 72.5, "fixture", now, now, "snapshot-hash", 1, 1, 1, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(snapshotInsert, "snapshot-ai-2", "theme-ai", "2026-08-28", 1, "发酵", 1, 80, "duplicate", now, now, "snapshot-hash-2", 0, 0, 0, now).Error; err == nil {
		t.Fatal("theme/trade-date snapshot uniqueness was not enforced")
	}
	if err := mainDB.Exec(snapshotInsert, "snapshot-invalid-stage", "theme-ai", "2026-08-29", 1, "高潮", 1, 80, "invalid", now, now, "snapshot-invalid", 0, 0, 0, now).Error; err == nil {
		t.Fatal("five-stage lifecycle CHECK was not enforced")
	}
	if err := mainDB.Exec(snapshotInsert, "snapshot-invalid-rank", "theme-ai", "2026-08-30", 1, "观察", 0, 80, "invalid rank", now, now, "snapshot-invalid-rank", 0, 0, 0, now).Error; err == nil {
		t.Fatal("positive snapshot rank CHECK was not enforced")
	}
	if err := mainDB.Exec(snapshotInsert, "snapshot-constituent-rank", "theme-ai", "2026-08-30", 1, "观察", 1, 80, "rank fixture", now, now, "snapshot-rank-fixture", 1, 0, 0, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec("UPDATE market_theme_daily_snapshots SET summary = 'changed' WHERE snapshot_id = ?", "snapshot-ai-1").Error; err == nil {
		t.Fatal("immutable daily snapshot accepted an update")
	}
	if err := mainDB.Exec("DELETE FROM market_theme_daily_snapshots WHERE snapshot_id = ?", "snapshot-ai-1").Error; err == nil {
		t.Fatal("immutable daily snapshot accepted a delete")
	}

	eventInsert := `INSERT INTO market_catalyst_events(
catalyst_event_id,theme_id,event_fingerprint,event_type,title,summary,event_at,first_available_at,credibility_score,status,entity_keys_json,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`
	if err := mainDB.Exec(eventInsert, "event-ai-1", "theme-ai", "event-fingerprint", "policy", "政策事件", "fixture", now, now, 80, "active", `[]`, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(eventInsert, "event-ai-2", "theme-ai", "event-fingerprint", "news", "重复事件", "fixture", now, now, 70, "active", `[]`, now).Error; err == nil {
		t.Fatal("theme/event-fingerprint uniqueness was not enforced")
	}
	if err := mainDB.Exec(eventInsert, "event-invalid-score", "theme-ai", "event-invalid-score", "news", "无效可信度", "fixture", now, now, 101, "active", `[]`, now).Error; err == nil {
		t.Fatal("event credibility CHECK was not enforced")
	}
	if err := mainDB.Exec(eventInsert, "event-invalid-status", "theme-ai", "event-invalid-status", "news", "无效状态", "fixture", now, now, 50, "deleted", `[]`, now).Error; err == nil {
		t.Fatal("event status CHECK was not enforced")
	}

	claimInsert := `INSERT INTO market_catalyst_source_claims(
source_claim_id,catalyst_event_id,source_name,source_ref,source_ref_hash,stance,source_credibility_score,summary,claim_fingerprint,published_at,available_at,collected_at,raw_payload_hash,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if err := mainDB.Exec(claimInsert, "claim-support", "event-ai-1", "source-a", "https://a.test/1", "source-a-hash", "supports", 85, "支持", "same-claim", now, now, now, "raw-a", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(claimInsert, "claim-contradict", "event-ai-1", "source-b", "https://b.test/1", "source-b-hash", "contradicts", 65, "反驳", "same-claim", now, now, now, "raw-b", now).Error; err != nil {
		t.Fatalf("conflicting independent sources must coexist: %v", err)
	}
	if err := mainDB.Exec(claimInsert, "claim-duplicate-link", "event-ai-1", "source-a-copy", "https://a.test/1", "source-a-hash", "neutral", 50, "duplicate", "other-claim", now, now, now, "raw-c", now).Error; err == nil {
		t.Fatal("event/source-ref uniqueness was not enforced")
	}
	if err := mainDB.Exec(claimInsert, "claim-invalid-stance", "event-ai-1", "source-c", "https://c.test/1", "source-c-hash", "unknown", 50, "invalid", "claim-c", now, now, now, "raw-c", now).Error; err == nil {
		t.Fatal("claim stance CHECK was not enforced")
	}
	if err := mainDB.Exec("UPDATE market_catalyst_source_claims SET summary = 'changed' WHERE source_claim_id = ?", "claim-support").Error; err == nil {
		t.Fatal("immutable source claim accepted an update")
	}
	if err := mainDB.Exec("DELETE FROM market_catalyst_source_claims WHERE source_claim_id = ?", "claim-support").Error; err == nil {
		t.Fatal("immutable source claim accepted a delete")
	}

	if err := mainDB.Exec("INSERT INTO market_theme_snapshot_catalysts(snapshot_id,catalyst_event_id,created_at) VALUES (?,?,?)", "snapshot-ai-1", "event-ai-1", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec("INSERT INTO market_theme_snapshot_catalysts(snapshot_id,catalyst_event_id,created_at) VALUES (?,?,?)", "snapshot-ai-1", "event-ai-1", now).Error; err == nil {
		t.Fatal("snapshot/catalyst association uniqueness was not enforced")
	}
	if err := mainDB.Exec("INSERT INTO market_theme_snapshot_catalysts(snapshot_id,catalyst_event_id,created_at) VALUES (?,?,?)", "snapshot-ai-1", "event-ai-different", now).Error; err == nil {
		t.Fatal("complete snapshot accepted a different catalyst beyond declared capacity")
	}
	if err := mainDB.Exec("UPDATE market_theme_snapshot_catalysts SET catalyst_event_id = 'changed' WHERE snapshot_id = ?", "snapshot-ai-1").Error; err == nil {
		t.Fatal("frozen snapshot catalyst accepted an update")
	}
	if err := mainDB.Exec("DELETE FROM market_theme_snapshot_catalysts WHERE snapshot_id = ?", "snapshot-ai-1").Error; err == nil {
		t.Fatal("frozen snapshot catalyst accepted a delete")
	}

	constituentInsert := `INSERT INTO market_theme_snapshot_constituents(
constituent_id,snapshot_id,asset_type,market,code,name,role,rank,contribution_score,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`
	if err := mainDB.Exec(constituentInsert, "constituent-ai-1", "snapshot-ai-1", "stock", "SH", "sh600000", "浦发银行", "leader", 1, 88, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(constituentInsert, "constituent-ai-2", "snapshot-ai-1", "stock", "SH", "sh600000", "浦发银行", "follower", 1, 50, now).Error; err == nil {
		t.Fatal("snapshot/asset constituent uniqueness was not enforced")
	}
	if err := mainDB.Exec(constituentInsert, "constituent-invalid", "snapshot-ai-1", "bond", "SH", "sh000000", "无效", "", 1, 0, now).Error; err == nil {
		t.Fatal("constituent asset-type CHECK was not enforced")
	}
	if err := mainDB.Exec(constituentInsert, "constituent-invalid-rank", "snapshot-constituent-rank", "stock", "SH", "sh600001", "无效排名", "", 0, 0, now).Error; err == nil {
		t.Fatal("positive constituent rank CHECK was not enforced")
	}
	if err := mainDB.Exec(constituentInsert, "constituent-over-capacity", "snapshot-ai-1", "stock", "SH", "sh600001", "追加成分", "follower", 2, 50, now).Error; err == nil {
		t.Fatal("complete snapshot accepted a different constituent beyond declared capacity")
	}
	if err := mainDB.Exec("UPDATE market_theme_snapshot_constituents SET name = 'changed' WHERE constituent_id = ?", "constituent-ai-1").Error; err == nil {
		t.Fatal("frozen snapshot constituent accepted an update")
	}
	if err := mainDB.Exec("DELETE FROM market_theme_snapshot_constituents WHERE constituent_id = ?", "constituent-ai-1").Error; err == nil {
		t.Fatal("frozen snapshot constituent accepted a delete")
	}

	evidenceLinkInsert := `INSERT INTO market_theme_evidence_links(
link_id,theme_id,snapshot_id,catalyst_event_id,source_claim_id,evidence_item_id,link_type,created_at) VALUES (?,?,?,?,?,?,?,?)`
	if err := mainDB.Exec(evidenceLinkInsert, "link-ai-1", "theme-ai", nil, "event-ai-1", nil, "evidence-1", "supports", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(evidenceLinkInsert, "link-ai-2", "theme-ai", nil, "event-ai-1", nil, "evidence-1", "supports", now).Error; err == nil {
		t.Fatal("NULL-safe evidence association uniqueness was not enforced")
	}
}

func TestSchema17VerifierRejectsMissingSnapshotImmutabilityGuard(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec("DROP TRIGGER immutable_market_theme_daily_snapshots_update").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyMain(mainDB); err == nil {
		t.Fatal("schema 17 verification accepted a missing snapshot immutability trigger")
	}
}

func TestSchema17VerifierRejectsMissingSnapshotCapacityGuard(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec("DROP TRIGGER bounded_market_theme_snapshot_catalysts_insert").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyMain(mainDB); err == nil {
		t.Fatal("schema 17 verification accepted a missing snapshot capacity trigger")
	}
}

func TestSchema16To17PreservesResearchAccountingDrawingsAndMinuteSchema3(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	applyPublishedMigrationPrefix(t, mainDB, mainMigrations, 16, "2.1.0")
	applyPublishedMigrationPrefix(t, minuteDB, minuteMigrations, 3, "2.1.0")
	if mainDB.Migrator().HasTable("market_themes") {
		t.Fatal("schema 16 fixture unexpectedly contains schema 17 tables")
	}

	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	if err := mainDB.Model(&research.SimulatedAccount{}).Where("id = ?", 1).Update("cash", 321098.76).Error; err != nil {
		t.Fatal(err)
	}
	r1Trade := research.SimulatedTrade{TradeID: "schema17-r1-trade", RecommendationID: "schema17-r1-rec", StockCode: "sh600000", Side: "sell", TradedAt: now, MarketPrice: 10.25, ExecutionPrice: 10.23, Quantity: 100, Notional: 1023, Commission: 5, StampDuty: 0.5115, TransferFee: 0.01023, SlippageAmount: 2, TotalFees: 7.52173, NetCashFlow: 1015.47827}
	if err := mainDB.Create(&r1Trade).Error; err != nil {
		t.Fatal(err)
	}
	r1Position := research.Position{RecommendationID: "schema17-r1-position", StockCode: "sh600001", StockName: "测试", Market: "SH", Quantity: 100, EntryAt: now.Add(-time.Hour), EntryPrice: 10.01, BuyFees: 5.01, Status: "closed", ExitAt: &now, ExitPrice: 10.23, SellFees: 5.52173, NetPnL: 9.46827}
	if err := mainDB.Create(&r1Position).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Model(&research2.Account{}).Where("id = ?", 1).Update("cash", 8765.43).Error; err != nil {
		t.Fatal(err)
	}
	r2Trade := research2.Trade{TradeID: "schema17-r2-trade", RecommendationID: "schema17-r2-rec", Side: "sell", TradedAt: now, MarketPrice: 10.22, ExecutionPrice: 10.20, Quantity: 100, Commission: 5, StampDuty: 0.51, TransferFee: 0.01, SlippageAmount: 2, NetCashFlow: 1014.48}
	if err := mainDB.Create(&r2Trade).Error; err != nil {
		t.Fatal(err)
	}
	r2Snapshot := research2.AccountSnapshot{SnapshotID: "schema17-r2-snapshot", ValuedAt: now, TradingDate: "2026-08-28", SnapshotType: "sell", Cash: 8765.43, PositionValue: 0, NetAssetValue: 8765.43, NetProfit: -3234.57, ReturnRate: -0.2695475}
	if err := mainDB.Create(&r2Snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO chart_drawing_documents(
drawing_document_id,scope_type,scope_id,asset_type,market,code,period,adjustment,revision,drawings_json,created_at,updated_at)
VALUES ('schema17-drawing','research_run','run-17','stock','SH','sh600000','1m','qfq',1,'[{"type":"line"}]',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO chart_drawing_revisions(document_id,revision,drawings_json,created_at)
VALUES ('schema17-drawing',1,'[{"type":"line"}]',?)`, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO research_evidence_sets(
evidence_set_id,owner_type,owner_id,cutoff_at,collector_version,evidence_profile_version,status,content_hash,frozen_at,created_at)
VALUES ('schema17-evidence-set','research1','schema17-run',?,'collector-v1','profile-v1','frozen','set-content-hash',?,?)`, now, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO research_evidence_items(
evidence_item_id,evidence_set_id,source_id,source_name,source_ref,category,available_at,collected_at,status,summary,payload,content_hash,created_at)
VALUES ('schema17-evidence-item','schema17-evidence-set','source-1','fixture','https://fixture.test/evidence','theme',?,?,'available','fixture',X'0102','item-content-hash',?)`, now, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := minuteDB.Exec(`INSERT INTO market_bar_cache(asset_type,symbol,period,adjustment,bar_time,open,high,low,close,source,updated_at)
VALUES ('stock','sh600000','1m','qfq',1,10,11,9,10.5,'fixture',1)`).Error; err != nil {
		t.Fatal(err)
	}

	before := captureResearchHistory(t, mainDB)
	var minuteMigrationCountBefore int64
	if err := minuteDB.Model(&MigrationRecord{}).Count(&minuteMigrationCountBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatalf("repeat schema 17 migration: %v", err)
	}
	after := captureResearchHistory(t, mainDB)
	for name, expected := range before {
		if !bytes.Equal(expected, after[name]) {
			t.Fatalf("schema 17 rewrote %s", name)
		}
	}

	var storedR1Trade research.SimulatedTrade
	if err := mainDB.Where("trade_id = ?", r1Trade.TradeID).First(&storedR1Trade).Error; err != nil || storedR1Trade.TotalFees != r1Trade.TotalFees || storedR1Trade.NetCashFlow != r1Trade.NetCashFlow {
		t.Fatalf("research1 fees changed: %+v err=%v", storedR1Trade, err)
	}
	var storedR1Position research.Position
	if err := mainDB.Where("recommendation_id = ?", r1Position.RecommendationID).First(&storedR1Position).Error; err != nil || storedR1Position.NetPnL != r1Position.NetPnL {
		t.Fatalf("research1 pnl changed: %+v err=%v", storedR1Position, err)
	}
	var storedR2Trade research2.Trade
	if err := mainDB.Where("trade_id = ?", r2Trade.TradeID).First(&storedR2Trade).Error; err != nil || storedR2Trade.Commission != r2Trade.Commission || storedR2Trade.StampDuty != r2Trade.StampDuty || storedR2Trade.TransferFee != r2Trade.TransferFee || storedR2Trade.NetCashFlow != r2Trade.NetCashFlow {
		t.Fatalf("research2 fees changed: %+v err=%v", storedR2Trade, err)
	}
	var storedR2Snapshot research2.AccountSnapshot
	if err := mainDB.Where("snapshot_id = ?", r2Snapshot.SnapshotID).First(&storedR2Snapshot).Error; err != nil || storedR2Snapshot.NetProfit != r2Snapshot.NetProfit || storedR2Snapshot.ReturnRate != r2Snapshot.ReturnRate {
		t.Fatalf("research2 pnl changed: %+v err=%v", storedR2Snapshot, err)
	}
	var drawingRevision, drawingHistoryCount int64
	if err := mainDB.Raw("SELECT revision FROM chart_drawing_documents WHERE drawing_document_id = ?", "schema17-drawing").Scan(&drawingRevision).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Table("chart_drawing_revisions").Where("document_id = ?", "schema17-drawing").Count(&drawingHistoryCount).Error; err != nil {
		t.Fatal(err)
	}
	if drawingRevision != 1 || drawingHistoryCount != 1 {
		t.Fatalf("schema 16 drawing history changed: revision=%d history=%d", drawingRevision, drawingHistoryCount)
	}
	var storedSetHash, storedItemHash string
	if err := mainDB.Raw("SELECT content_hash FROM research_evidence_sets WHERE evidence_set_id = ?", "schema17-evidence-set").Scan(&storedSetHash).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Raw("SELECT content_hash FROM research_evidence_items WHERE evidence_item_id = ?", "schema17-evidence-item").Scan(&storedItemHash).Error; err != nil {
		t.Fatal(err)
	}
	if storedSetHash != "set-content-hash" || storedItemHash != "item-content-hash" {
		t.Fatalf("evidence hashes changed: set=%q item=%q", storedSetHash, storedItemHash)
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
	mainStatus, err := VerifyMain(mainDB)
	if err != nil {
		t.Fatal(err)
	}
	minuteStatus, err := VerifyMinute(minuteDB)
	if err != nil {
		t.Fatal(err)
	}
	if mainStatus.CurrentVersion != 20 || minuteStatus.CurrentVersion != 3 {
		t.Fatalf("schema versions main=%d minute=%d", mainStatus.CurrentVersion, minuteStatus.CurrentVersion)
	}
}
