package migrations

import (
	"bytes"
	"compress/gzip"
	"context"
	"testing"
	"time"

	"go-stock/backend/research"
	"go-stock/backend/research2"
	"go-stock/backend/researchaudit"
)

func auditGzipFixture(t *testing.T, value string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

type schema18ReplayExecutor struct{}

func (schema18ReplayExecutor) CompleteReplay(_ context.Context, call researchaudit.ReplayCall) (researchaudit.ReplayCallResult, error) {
	return researchaudit.ReplayCallResult{Content: `{"replayed":true}`, ProviderName: "fixture", ModelName: "fixture-model", AttemptLog: map[string]any{"modelConfigId": call.ModelConfigID}}, nil
}

func TestSchema18IsIdempotentAndEnforcesAuditContracts(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := applyResearchAuditSchema(mainDB); err != nil {
		t.Fatalf("repeat schema 18: %v", err)
	}
	if err := verifyMainSchema18Runtime(mainDB); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	hashA := "a" + string(bytes.Repeat([]byte{'0'}, 63))
	hashB := "b" + string(bytes.Repeat([]byte{'1'}, 63))
	template := auditGzipFixture(t, "prompt template")
	if err := mainDB.Exec(`INSERT INTO research_audit_prompt_versions(
prompt_version_id,research_scope,phase,version,template_codec,template_blob,template_sha256,created_at)
VALUES (?,?,?,?,?,?,?,?)`, "prompt-r1-market-v1", "research1", "market", "v1", "gzip", template, hashA, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO research_audit_prompt_versions(
prompt_version_id,research_scope,phase,version,template_codec,template_blob,template_sha256,created_at)
VALUES (?,?,?,?,?,?,?,?)`, "prompt-duplicate", "research1", "market", "v1", "gzip", template, hashA, now).Error; err == nil {
		t.Fatal("duplicate prompt scope/phase/version was accepted")
	}
	if err := mainDB.Exec("UPDATE research_audit_prompt_versions SET version='v2' WHERE prompt_version_id=?", "prompt-r1-market-v1").Error; err == nil {
		t.Fatal("immutable prompt version accepted an update")
	}
	if err := mainDB.Exec("DELETE FROM research_audit_prompt_versions WHERE prompt_version_id=?", "prompt-r1-market-v1").Error; err == nil {
		t.Fatal("immutable prompt version accepted a delete")
	}

	promptPayload := auditGzipFixture(t, "full final prompt")
	evidencePayload := auditGzipFixture(t, `[{"source":"fixture"}]`)
	responsePayload := auditGzipFixture(t, `{"answer":"ok"}`)
	payloadInsert := `INSERT INTO research_audit_payloads(
payload_id,owner_type,owner_id,prompt_version_id,phase,call_sequence,attempt,provider_name,model_name,model_parameters_json,cutoff_at,
final_prompt_codec,final_prompt_blob,final_prompt_sha256,evidence_codec,evidence_blob,evidence_sha256,tools_json,
raw_response_codec,raw_response_blob,raw_response_sha256,redaction_manifest_json,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if err := mainDB.Exec(payloadInsert,
		"payload-r1-1", "research1", "run-r1", "prompt-r1-market-v1", "market", 1, 1, "fixture", "fixture-model", `{"temperature":0}`, now,
		"gzip", promptPayload, hashA, "gzip", evidencePayload, hashB, `[]`, "gzip", responsePayload, hashA, `{"secrets":["api_key"]}`, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(payloadInsert,
		"payload-r1-duplicate", "research1", "run-r1", nil, "market", 1, 1, "fixture", "fixture-model", `{}`, now,
		"gzip", promptPayload, hashA, "gzip", evidencePayload, hashB, `[]`, nil, nil, nil, `{}`, now).Error; err == nil {
		t.Fatal("duplicate payload owner/call/attempt was accepted")
	}
	if err := mainDB.Exec(payloadInsert,
		"payload-invalid-owner", "other", "run-r1", nil, "market", 2, 1, "fixture", "fixture-model", `{}`, now,
		"gzip", promptPayload, hashA, "gzip", evidencePayload, hashB, `[]`, nil, nil, nil, `{}`, now).Error; err == nil {
		t.Fatal("payload outside the audit owner domains was accepted")
	}
	if err := mainDB.Exec("UPDATE research_audit_payloads SET model_name='changed' WHERE payload_id=?", "payload-r1-1").Error; err == nil {
		t.Fatal("immutable audit payload accepted an update")
	}
	if err := mainDB.Exec("DELETE FROM research_audit_payloads WHERE payload_id=?", "payload-r1-1").Error; err == nil {
		t.Fatal("immutable audit payload accepted a delete")
	}

	if err := mainDB.Exec(`INSERT INTO research_audit_run_states(owner_type,owner_id,status,payload_count,last_error,created_at,updated_at)
VALUES ('research1','run-r1','capturing',0,NULL,?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`UPDATE research_audit_run_states SET status='complete',payload_count=1,updated_at=?
WHERE owner_type='research1' AND owner_id='run-r1'`, now.Add(time.Minute)).Error; err != nil {
		t.Fatalf("mutable audit run state fields were rejected: %v", err)
	}
	if err := mainDB.Exec(`UPDATE research_audit_run_states SET status='capturing'
WHERE owner_type='research1' AND owner_id='run-r1'`).Error; err == nil {
		t.Fatal("terminal audit run state was reopened")
	}
	if err := mainDB.Exec(`UPDATE research_audit_run_states SET payload_count=2
WHERE owner_type='research1' AND owner_id='run-r1'`).Error; err == nil {
		t.Fatal("terminal audit payload count accepted an update")
	}
	if err := mainDB.Exec(`UPDATE research_audit_run_states SET owner_id='changed'
WHERE owner_type='research1' AND owner_id='run-r1'`).Error; err == nil {
		t.Fatal("audit run state identity accepted an update")
	}
	if err := mainDB.Exec(`DELETE FROM research_audit_run_states WHERE owner_type='research1' AND owner_id='run-r1'`).Error; err == nil {
		t.Fatal("audit run state identity accepted a delete")
	}
	if err := mainDB.Exec(`INSERT INTO research_audit_run_states(owner_type,owner_id,status,payload_count,created_at,updated_at)
VALUES ('research2','run-failed','capturing',0,?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`UPDATE research_audit_run_states SET status='failed',last_error='fixture',updated_at=?
WHERE owner_type='research2' AND owner_id='run-failed'`, now.Add(time.Minute)).Error; err != nil {
		t.Fatalf("valid capturing-to-failed transition was rejected: %v", err)
	}

	if err := mainDB.Exec(`INSERT INTO research_replays(
replay_id,source_owner_type,source_owner_id,model_config_id,status,cutoff_at,created_at)
VALUES ('replay-1','research1','run-r1',1,'queued',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`UPDATE research_replays SET status='running',started_at=? WHERE replay_id='replay-1'`, now.Add(time.Minute)).Error; err != nil {
		t.Fatalf("mutable replay status/time fields were rejected: %v", err)
	}
	if err := mainDB.Exec(`UPDATE research_replays SET status='completed',completed_at=? WHERE replay_id='replay-1'`, now.Add(2*time.Minute)).Error; err != nil {
		t.Fatalf("valid replay terminal transition was rejected: %v", err)
	}
	if err := mainDB.Exec(`UPDATE research_replays SET status='running',completed_at=NULL WHERE replay_id='replay-1'`).Error; err == nil {
		t.Fatal("terminal replay was reopened")
	}
	if err := mainDB.Exec(`UPDATE research_replays SET started_at=? WHERE replay_id='replay-1'`, now.Add(3*time.Minute)).Error; err == nil {
		t.Fatal("established replay start time accepted an update")
	}
	if err := mainDB.Exec(`UPDATE research_replays SET model_config_id=2 WHERE replay_id='replay-1'`).Error; err == nil {
		t.Fatal("replay model identity accepted an update")
	}
	if err := mainDB.Exec(`UPDATE research_replays SET cutoff_at=? WHERE replay_id='replay-1'`, now.Add(time.Minute)).Error; err == nil {
		t.Fatal("replay cutoff accepted an update")
	}
	if err := mainDB.Exec(`DELETE FROM research_replays WHERE replay_id='replay-1'`).Error; err == nil {
		t.Fatal("replay accepted a delete")
	}
	if err := mainDB.Exec(`INSERT INTO research_replays(
replay_id,source_owner_type,source_owner_id,model_config_id,status,cutoff_at,created_at)
VALUES ('replay-invalid','research1','run-r1',0,'queued',?,?)`, now, now).Error; err == nil {
		t.Fatal("non-positive replay model_config_id was accepted")
	}
	if err := mainDB.Exec(`INSERT INTO research_replays(
replay_id,source_owner_type,source_owner_id,model_config_id,status,cutoff_at,created_at)
VALUES ('replay-failed','research2','run-failed',1,'queued',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`UPDATE research_replays SET status='running',started_at=? WHERE replay_id='replay-failed'`, now.Add(time.Minute)).Error; err != nil {
		t.Fatalf("valid queued-to-running failure path was rejected: %v", err)
	}
	if err := mainDB.Exec(`UPDATE research_replays SET status='failed',completed_at=?,last_error='fixture' WHERE replay_id='replay-failed'`, now.Add(2*time.Minute)).Error; err != nil {
		t.Fatalf("valid running-to-failed transition was rejected: %v", err)
	}

	resultPayload := auditGzipFixture(t, `{"decision":"empty"}`)
	if err := mainDB.Exec(`INSERT INTO research_replay_results(
replay_result_id,replay_id,result_codec,result_blob,result_sha256,diff_summary_json,created_at)
VALUES ('result-1','replay-1','gzip',?,?,?,?)`, resultPayload, hashA, `{"changed":false}`, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO research_replay_results(
replay_result_id,replay_id,result_codec,result_blob,result_sha256,diff_summary_json,created_at)
VALUES ('result-duplicate','replay-1','gzip',?,?,?,?)`, resultPayload, hashA, `{}`, now).Error; err == nil {
		t.Fatal("second immutable result for one replay was accepted")
	}
	if err := mainDB.Exec("UPDATE research_replay_results SET diff_summary_json='{}' WHERE replay_result_id='result-1'").Error; err == nil {
		t.Fatal("immutable replay result accepted an update")
	}
	if err := mainDB.Exec("DELETE FROM research_replay_results WHERE replay_result_id='result-1'").Error; err == nil {
		t.Fatal("immutable replay result accepted a delete")
	}
}

func TestSchema18VerifierRejectsMissingAuditTrigger(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec("DROP TRIGGER immutable_research_audit_payloads_update").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyMain(mainDB); err == nil {
		t.Fatal("schema 18 verification accepted a missing payload immutability trigger")
	}
}

func TestSchema18RepositoryReplayUsesProductionDDLAndDoesNotTouchFormalResearch(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	before := captureResearchHistory(t, mainDB)
	repository := researchaudit.NewRepository(mainDB)
	recorder := researchaudit.NewRecorder(repository)
	ctx := context.Background()
	if err := recorder.Begin(ctx, researchaudit.OwnerResearch1, "schema18-replay-source"); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 8, 28, 1, 55, 0, 0, time.UTC)
	prepared, err := recorder.Prepare(ctx, researchaudit.CallInput{
		OwnerType: researchaudit.OwnerResearch1, OwnerID: "schema18-replay-source", Phase: "final", CallSequence: 1, Attempt: 1,
		Prompt: "frozen prompt", Evidence: map[string]any{"evidenceSetId": "set-1"}, CutoffAt: &cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = recorder.Record(ctx, prepared, researchaudit.CallResult{RawResponse: `{"original":true}`, ProviderName: "fixture", ModelName: "fixture-model", ActualConfigID: 1}); err != nil {
		t.Fatal(err)
	}
	if err = recorder.Complete(ctx, researchaudit.OwnerResearch1, "schema18-replay-source"); err != nil {
		t.Fatal(err)
	}
	service := researchaudit.NewService(repository)
	replay, err := service.CreateReplay(ctx, researchaudit.CreateReplayRequest{SourceOwnerType: researchaudit.OwnerResearch1, SourceOwnerID: "schema18-replay-source", ModelConfigID: 1})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.ExecuteReplay(ctx, replay.ReplayID, schema18ReplayExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Replay.Status != "completed" || view.Result == nil {
		t.Fatalf("replay did not complete against production schema18 DDL: %+v", view)
	}
	after := captureResearchHistory(t, mainDB)
	for name, expected := range before {
		if !bytes.Equal(expected, after[name]) {
			t.Fatalf("isolated replay rewrote formal %s", name)
		}
	}
}

func TestSchema17To18PreservesHistoryAndMinuteSchema3(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	applyPublishedMigrationPrefix(t, mainDB, mainMigrations, 17, "2.2.0")
	applyPublishedMigrationPrefix(t, minuteDB, minuteMigrations, 3, "2.2.0")
	if mainDB.Migrator().HasTable("research_audit_payloads") {
		t.Fatal("schema 17 fixture unexpectedly contains schema 18 audit tables")
	}

	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	if err := mainDB.Model(&research.SimulatedAccount{}).Where("id = ?", 1).Update("cash", 310000.25).Error; err != nil {
		t.Fatal(err)
	}
	r1Trade := research.SimulatedTrade{TradeID: "schema18-r1-trade", RecommendationID: "schema18-r1-rec", StockCode: "sh600000", Side: "sell", TradedAt: now, MarketPrice: 10.25, ExecutionPrice: 10.23, Quantity: 100, Notional: 1023, Commission: 5, StampDuty: 0.5115, TransferFee: 0.01023, SlippageAmount: 2, TotalFees: 7.52173, NetCashFlow: 1015.47827}
	if err := mainDB.Create(&r1Trade).Error; err != nil {
		t.Fatal(err)
	}
	r1Position := research.Position{RecommendationID: "schema18-r1-position", StockCode: "sh600001", StockName: "测试", Market: "SH", Quantity: 100, EntryAt: now.Add(-time.Hour), EntryPrice: 10.01, BuyFees: 5.01, Status: "closed", ExitAt: &now, ExitPrice: 10.23, SellFees: 5.52173, NetPnL: 9.46827}
	if err := mainDB.Create(&r1Position).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Model(&research2.Account{}).Where("id = ?", 1).Update("cash", 8765.43).Error; err != nil {
		t.Fatal(err)
	}
	r2Trade := research2.Trade{TradeID: "schema18-r2-trade", RecommendationID: "schema18-r2-rec", Side: "sell", TradedAt: now, MarketPrice: 10.22, ExecutionPrice: 10.20, Quantity: 100, Commission: 5, StampDuty: 0.51, TransferFee: 0.01, SlippageAmount: 2, NetCashFlow: 1014.48}
	if err := mainDB.Create(&r2Trade).Error; err != nil {
		t.Fatal(err)
	}
	r2Snapshot := research2.AccountSnapshot{SnapshotID: "schema18-r2-snapshot", ValuedAt: now, TradingDate: "2026-08-28", SnapshotType: "sell", Cash: 8765.43, PositionValue: 0, NetAssetValue: 8765.43, NetProfit: -3234.57, ReturnRate: -0.2695475}
	if err := mainDB.Create(&r2Snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO research_evidence_sets(
evidence_set_id,owner_type,owner_id,cutoff_at,collector_version,evidence_profile_version,status,content_hash,frozen_at,created_at)
VALUES ('schema18-evidence-set','research1','schema18-run',?,'collector-v1','profile-v2','frozen','evidence-set-hash',?,?)`, now, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO chart_drawing_documents(
drawing_document_id,scope_type,scope_id,asset_type,market,code,period,adjustment,revision,drawings_json,created_at,updated_at)
VALUES ('schema18-drawing','research_run','run-18','stock','SH','sh600000','1m','qfq',1,'[{"type":"line"}]',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO market_themes(theme_id,canonical_name,normalized_name,description,status,created_at,updated_at)
VALUES ('schema18-theme','AI算力','ai算力','fixture','active',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec(`INSERT INTO market_theme_daily_snapshots(
snapshot_id,theme_id,trade_date,cycle_no,lifecycle_stage,rank,heat_score,summary,observed_at,frozen_at,content_hash,constituent_count,catalyst_count,conflicting_catalyst_count,created_at)
VALUES ('schema18-theme-snapshot','schema18-theme','2026-08-28',1,'观察',1,88.5,'fixture',?,?,'theme-content-hash',0,0,0,?)`, now, now, now).Error; err != nil {
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
		t.Fatalf("repeat schema 18 migration: %v", err)
	}
	after := captureResearchHistory(t, mainDB)
	for name, expected := range before {
		if !bytes.Equal(expected, after[name]) {
			t.Fatalf("schema 18 rewrote %s", name)
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
	if err := mainDB.Where("trade_id = ?", r2Trade.TradeID).First(&storedR2Trade).Error; err != nil || storedR2Trade.Commission != r2Trade.Commission || storedR2Trade.NetCashFlow != r2Trade.NetCashFlow {
		t.Fatalf("research2 fees changed: %+v err=%v", storedR2Trade, err)
	}
	var storedR2Snapshot research2.AccountSnapshot
	if err := mainDB.Where("snapshot_id = ?", r2Snapshot.SnapshotID).First(&storedR2Snapshot).Error; err != nil || storedR2Snapshot.NetProfit != r2Snapshot.NetProfit || storedR2Snapshot.ReturnRate != r2Snapshot.ReturnRate {
		t.Fatalf("research2 pnl changed: %+v err=%v", storedR2Snapshot, err)
	}
	var evidenceHash, themeHash string
	if err := mainDB.Raw("SELECT content_hash FROM research_evidence_sets WHERE evidence_set_id='schema18-evidence-set'").Scan(&evidenceHash).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Raw("SELECT content_hash FROM market_theme_daily_snapshots WHERE snapshot_id='schema18-theme-snapshot'").Scan(&themeHash).Error; err != nil {
		t.Fatal(err)
	}
	var drawingCount, barCount, minuteMigrationCountAfter int64
	if err := mainDB.Table("chart_drawing_documents").Where("drawing_document_id='schema18-drawing'").Count(&drawingCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := minuteDB.Table("market_bar_cache").Where("symbol='sh600000'").Count(&barCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := minuteDB.Model(&MigrationRecord{}).Count(&minuteMigrationCountAfter).Error; err != nil {
		t.Fatal(err)
	}
	if evidenceHash != "evidence-set-hash" || themeHash != "theme-content-hash" || drawingCount != 1 {
		t.Fatalf("pre-schema18 artifacts changed: evidence=%q theme=%q drawings=%d", evidenceHash, themeHash, drawingCount)
	}
	if minuteMigrationCountBefore != 3 || minuteMigrationCountAfter != 3 || barCount != 1 {
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
	assertCurrentSchemaVersions(t, mainStatus, minuteStatus)
}
