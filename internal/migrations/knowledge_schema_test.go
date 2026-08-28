package migrations

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go-stock/backend/research"
	"go-stock/backend/research2"
)

func TestSchema19KnowledgeApprovalFTSAndImmutabilityContracts(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyKnowledgeSchema(database); err != nil {
		t.Fatalf("apply schema 19: %v", err)
	}
	if err := applyKnowledgeSchema(database); err != nil {
		t.Fatalf("repeat schema 19: %v", err)
	}
	if err := verifyMainSchema19Runtime(database); err != nil {
		t.Fatal(err)
	}

	now := "2026-08-28T16:00:00+08:00"
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	if err := database.Exec(`INSERT INTO knowledge_documents(
document_id,title,description,tags_json,document_type,origin_type,source_owner_type,source_owner_id,created_by_user_id,created_at,updated_at)
VALUES ('doc-1','安全知识','fixture','["审计"]','research_report','research_report','research1','run-1','user-1',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO knowledge_documents(
document_id,title,tags_json,document_type,origin_type,source_owner_type,source_owner_id,created_by_user_id,created_at,updated_at)
VALUES ('bad-owner','bad','[]','text','upload','research1',NULL,'user-1',?,?)`, now, now).Error; err == nil {
		t.Fatal("document accepted a partial source owner identity")
	}
	if err := database.Exec(`CREATE TABLE knowledge_injection_guard(id INTEGER PRIMARY KEY)`).Error; err != nil {
		t.Fatal(err)
	}
	injectionText := `alpha knowledge'; DROP TABLE knowledge_injection_guard; -- is inert text`
	if err := database.Exec(`INSERT INTO knowledge_document_versions(
version_id,document_id,version_no,content_text,content_sha256,mime_type,source_filename,extraction_status,created_by_user_id,created_at)
VALUES ('version-1','doc-1',1,?,?, 'text/markdown','report.md','complete','user-1',?)`, injectionText, hashA, now).Error; err != nil {
		t.Fatal(err)
	}
	if !database.Migrator().HasTable("knowledge_injection_guard") {
		t.Fatal("indexed document text was executed as SQL")
	}
	var ftsCount int64
	if err := database.Raw(`SELECT count(*) FROM knowledge_document_fts WHERE knowledge_document_fts MATCH ?`, "alpha").Scan(&ftsCount).Error; err != nil {
		t.Fatalf("FTS5 query failed: %v", err)
	}
	if ftsCount != 1 {
		t.Fatalf("FTS5 insert trigger rows=%d, want 1", ftsCount)
	}
	if err := database.Exec(`UPDATE knowledge_document_versions SET content_text='rewritten' WHERE version_id='version-1'`).Error; err == nil {
		t.Fatal("immutable knowledge version accepted update")
	}
	if err := database.Exec(`DELETE FROM knowledge_document_versions WHERE version_id='version-1'`).Error; err == nil {
		t.Fatal("immutable knowledge version accepted delete")
	}

	if err := database.Exec(`INSERT INTO knowledge_version_states(
state_id,version_id,status,approved_by_actor_type,approved_by_user_id,approved_at,created_at,updated_at)
VALUES ('state-direct','version-1','approved','user','user-1',?,?,?)`, now, now, now).Error; err == nil {
		t.Fatal("version state bypassed mandatory draft state")
	}
	if err := database.Exec(`INSERT INTO knowledge_version_states(state_id,version_id,status,created_at,updated_at)
VALUES ('state-1','version-1','draft',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	var approvedCount int64
	approvedQuery := `SELECT count(*)
FROM knowledge_document_fts
JOIN knowledge_version_states ON knowledge_version_states.version_id = knowledge_document_fts.version_id
WHERE knowledge_document_fts MATCH ? AND knowledge_version_states.status = 'approved'`
	if err := database.Raw(approvedQuery, "alpha").Scan(&approvedCount).Error; err != nil {
		t.Fatal(err)
	}
	if approvedCount != 0 {
		t.Fatalf("draft content escaped approved-state join: %d", approvedCount)
	}
	if err := database.Exec(`UPDATE knowledge_version_states
SET status='approved',approved_by_actor_type='system',approved_by_user_id='scheduler',approved_at=?,updated_at=?
WHERE state_id='state-1'`, now, now).Error; err == nil {
		t.Fatal("system directly approved a knowledge version")
	}
	if err := database.Exec(`UPDATE knowledge_version_states
SET status='approved',approved_by_actor_type='user',approved_by_user_id='user-approver',approval_reason='verified',approved_at=?,updated_at=?
WHERE state_id='state-1'`, now, now).Error; err != nil {
		t.Fatalf("draft to approved: %v", err)
	}
	if err := database.Raw(approvedQuery, "alpha").Scan(&approvedCount).Error; err != nil {
		t.Fatal(err)
	}
	if approvedCount != 1 {
		t.Fatalf("approved-state join rows=%d, want 1", approvedCount)
	}
	if err := database.Exec(`UPDATE knowledge_version_states
SET status='superseded',approved_by_user_id='different-user',superseded_by_actor_type='user',superseded_by_user_id='user-approver',superseded_at=?,updated_at=?
WHERE state_id='state-1'`, now, now).Error; err == nil {
		t.Fatal("superseding a version rewrote its original approval identity")
	}
	if err := database.Exec(`UPDATE knowledge_version_states
SET status='superseded',superseded_by_actor_type='user',superseded_by_user_id='user-approver',superseded_reason='new version',superseded_at=?,updated_at=?
WHERE state_id='state-1'`, now, now).Error; err != nil {
		t.Fatalf("approved to superseded: %v", err)
	}
	if err := database.Raw(approvedQuery, "alpha").Scan(&approvedCount).Error; err != nil {
		t.Fatal(err)
	}
	if approvedCount != 0 {
		t.Fatalf("superseded content escaped approved-state join: %d", approvedCount)
	}
	if err := database.Exec(`UPDATE knowledge_version_states SET status='approved' WHERE state_id='state-1'`).Error; err == nil {
		t.Fatal("superseded version state was reopened")
	}

	if err := database.Exec(`INSERT INTO knowledge_document_versions(
version_id,document_id,version_no,content_text,content_sha256,mime_type,source_filename,extraction_status,created_by_user_id,created_at)
VALUES ('version-2','doc-1',2,'beta',?,'text/plain','report-v2.txt','complete','user-1',?)`, hashB, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO knowledge_version_states(state_id,version_id,status,created_at,updated_at)
VALUES ('state-2','version-2','draft',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`UPDATE knowledge_version_states
SET status='rejected',rejected_by_actor_type='user',rejected_by_user_id='user-reviewer',rejection_reason='unverified',rejected_at=?,updated_at=?
WHERE state_id='state-2'`, now, now).Error; err != nil {
		t.Fatalf("draft to rejected: %v", err)
	}

	if err := database.Exec(`INSERT INTO knowledge_memory_candidates(
candidate_id,source_owner_type,source_owner_id,title,content_text,content_sha256,status,proposed_by_actor_type,proposed_by_actor_id,created_at,updated_at)
VALUES ('candidate-1','research1','run-1','AI memory','candidate body',?,'draft','ai','model-1',?,?)`, hashA, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`UPDATE knowledge_memory_candidates
SET status='approved',decision_actor_type='system',decision_by_user_id='scheduler',updated_at=?
WHERE candidate_id='candidate-1'`, now).Error; err == nil {
		t.Fatal("system directly approved an AI memory candidate")
	}
	if err := database.Exec(`UPDATE knowledge_memory_candidates
SET status='approved',decision_actor_type='user',decision_by_user_id='user-reviewer',decision_reason='accepted',approved_version_id='version-2',updated_at=?
WHERE candidate_id='candidate-1'`, now).Error; err != nil {
		t.Fatalf("user approve memory candidate: %v", err)
	}
	if err := database.Exec(`UPDATE knowledge_memory_candidates SET status='rejected' WHERE candidate_id='candidate-1'`).Error; err == nil {
		t.Fatal("approved memory candidate was changed")
	}
	if err := database.Exec(`INSERT INTO knowledge_memory_candidates(
candidate_id,source_owner_type,source_owner_id,title,content_text,content_sha256,status,proposed_by_actor_type,decision_actor_type,decision_by_user_id,created_at,updated_at)
VALUES ('candidate-direct','research2','run-2','bad','bad body',?,'approved','system','system','scheduler',?,?)`, hashB, now, now).Error; err == nil {
		t.Fatal("memory candidate bypassed draft/user review")
	}

	if err := database.Exec(`INSERT INTO knowledge_retrieval_runs(
retrieval_run_id,owner_type,owner_id,cutoff_at,query_text,experimental_enabled,created_at)
VALUES ('retrieval-1','research1','run-1',?,'alpha',1,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO knowledge_retrieval_hits(
retrieval_hit_id,retrieval_run_id,version_id,rank,score,adopted,adoption_reason,verification_status,verification_reason,evidence_set_id,evidence_item_id,evidence_refs_json,created_at)
VALUES ('hit-1','retrieval-1','version-1',1,0.9,1,'adopted','verified','approved at cutoff','set-1','item-1','["item-1","item-2"]',?)`, now).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE knowledge_retrieval_runs SET query_text='changed' WHERE retrieval_run_id='retrieval-1'`,
		`DELETE FROM knowledge_retrieval_runs WHERE retrieval_run_id='retrieval-1'`,
		`UPDATE knowledge_retrieval_hits SET adopted=0 WHERE retrieval_hit_id='hit-1'`,
		`DELETE FROM knowledge_retrieval_hits WHERE retrieval_hit_id='hit-1'`,
	} {
		if err := database.Exec(statement).Error; err == nil {
			t.Fatalf("immutable retrieval audit accepted %q", statement)
		}
	}
}

func TestSchema19VerifierRejectsMissingKnowledgeGuard(t *testing.T) {
	database := openMigrationTestDB(t)
	applyPublishedMigrationPrefix(t, database, mainMigrations, 19, "2.4.0")
	if _, err := VerifyMain(database); err != nil {
		t.Fatalf("valid schema 19 failed verification: %v", err)
	}
	if err := database.Exec(`DROP TRIGGER immutable_knowledge_retrieval_hits_update`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyMain(database); err == nil {
		t.Fatal("schema 19 verifier accepted a missing immutable retrieval-hit guard")
	}
}

func TestSchema18To19KnowledgeMigrationPreservesResearchAccountingAndTrading(t *testing.T) {
	database := openMigrationTestDB(t)
	applyPublishedMigrationPrefix(t, database, mainMigrations, 18, "2.3.0")
	if database.Migrator().HasTable("knowledge_documents") {
		t.Fatal("schema 18 fixture unexpectedly contains schema 19 tables")
	}

	now := time.Date(2026, 8, 28, 15, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	if err := database.Model(&research.SimulatedAccount{}).Where("id = ?", 1).Update("cash", 432100.25).Error; err != nil {
		t.Fatal(err)
	}
	r1Recommendation := research.Recommendation{RecommendationID: "schema19-r1-rec", AnalysisRunID: "schema19-r1-run", StockCode: "sh600000", StockName: "浦发银行", SignalAt: now, Status: "closed", TotalFees: 7.52, NetPnL: 91.48, CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&r1Recommendation).Error; err != nil {
		t.Fatal(err)
	}
	r1Trade := research.SimulatedTrade{TradeID: "schema19-r1-trade", RecommendationID: r1Recommendation.RecommendationID, StockCode: "sh600000", Side: "sell", TradedAt: now, MarketPrice: 10.25, ExecutionPrice: 10.23, Quantity: 100, Notional: 1023, Commission: 5, StampDuty: 0.5115, TransferFee: 0.01023, SlippageAmount: 2, TotalFees: 7.52173, NetCashFlow: 1015.47827}
	if err := database.Create(&r1Trade).Error; err != nil {
		t.Fatal(err)
	}
	r1Position := research.Position{RecommendationID: r1Recommendation.RecommendationID, StockCode: "sh600000", StockName: "浦发银行", Market: "SH", Quantity: 100, EntryAt: now.Add(-time.Hour), EntryPrice: 10.01, BuyFees: 5.01, Status: "closed", ExitAt: &now, ExitPrice: 10.23, SellFees: 5.52173, NetPnL: 9.46827}
	if err := database.Create(&r1Position).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&research2.Account{}).Where("id = ?", 1).Update("cash", 9988.75).Error; err != nil {
		t.Fatal(err)
	}
	r2Recommendation := research2.Recommendation{RecommendationID: "schema19-r2-rec", AnalysisRunID: "schema19-r2-run", Rank: 1, StockCode: "sz000001", StockName: "平安银行", SignalAt: now, Status: "closed", TargetBuyAt: now, NetPnL: 21.75, CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&r2Recommendation).Error; err != nil {
		t.Fatal(err)
	}
	r2Trade := research2.Trade{TradeID: "schema19-r2-trade", RecommendationID: r2Recommendation.RecommendationID, Side: "sell", TradedAt: now, MarketPrice: 10.22, ExecutionPrice: 10.20, Quantity: 100, Commission: 5, StampDuty: 0.51, TransferFee: 0.01, SlippageAmount: 2, NetCashFlow: 1014.48}
	if err := database.Create(&r2Trade).Error; err != nil {
		t.Fatal(err)
	}

	before := captureResearchHistory(t, database)
	if err := applyKnowledgeSchema(database); err != nil {
		t.Fatal(err)
	}
	if err := applyKnowledgeSchema(database); err != nil {
		t.Fatalf("repeat schema 19: %v", err)
	}
	after := captureResearchHistory(t, database)
	for table, expected := range before {
		if !bytes.Equal(expected, after[table]) {
			t.Fatalf("schema 19 rewrote historical %s", table)
		}
	}
}
