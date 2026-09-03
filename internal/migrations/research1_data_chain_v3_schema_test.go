package migrations

import (
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestSchema25BackfillsResearch1ProvenanceWithoutChangingFinancialHistory(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research_v160_analysis_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  strategy_version TEXT,
  evidence_profile_version TEXT,
  final_report TEXT,
  updated_at DATETIME
);
CREATE TABLE research_v270_buy_opportunities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  opportunity_id TEXT NOT NULL UNIQUE,
  action TEXT NOT NULL,
  quote_price REAL,
  quote_at DATETIME,
  status TEXT NOT NULL,
  updated_at DATETIME
);
CREATE TABLE research_v160_lifecycle_observations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  observation_id TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  model_invoked NUMERIC NOT NULL DEFAULT 0,
  created_at DATETIME
);
CREATE TABLE research_v160_decision_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id TEXT NOT NULL UNIQUE,
  decision_type TEXT NOT NULL,
  reason TEXT,
  created_at DATETIME
);
CREATE TABLE research_v160_simulated_accounts (
  id INTEGER PRIMARY KEY,
  initial_cash REAL NOT NULL,
  cash REAL NOT NULL,
  updated_at DATETIME
);
CREATE TABLE research_v160_simulated_trades (
  id INTEGER PRIMARY KEY,
  trade_id TEXT NOT NULL UNIQUE,
  total_fees REAL NOT NULL,
  net_cash_flow REAL NOT NULL
);
CREATE TABLE research_v160_positions (
  id INTEGER PRIMARY KEY,
  recommendation_id TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  net_pnl REAL NOT NULL,
  updated_at DATETIME
);
INSERT INTO research_v160_analysis_runs(run_id,status,strategy_version,evidence_profile_version,final_report,updated_at)
VALUES ('legacy-run','success','','','unchanged','2026-09-03 10:00:00');
INSERT INTO research_v270_buy_opportunities(opportunity_id,action,quote_price,quote_at,status,updated_at) VALUES
('legacy-recorded','wait',10.25,'2026-09-03 10:01:00','active','2026-09-03 10:02:00'),
('legacy-unavailable','reject',0,NULL,'closed','2026-09-03 10:03:00');
INSERT INTO research_v160_lifecycle_observations(observation_id,status,model_invoked,created_at)
VALUES ('legacy-observation','ready',1,'2026-09-03 10:04:00');
INSERT INTO research_v160_decision_events(event_id,decision_type,reason,created_at)
VALUES ('legacy-event','hold','unchanged','2026-09-03 10:05:00');
INSERT INTO research_v160_simulated_accounts(id,initial_cash,cash,updated_at)
VALUES (1,500000,498567.09,'2026-09-03 10:06:00');
INSERT INTO research_v160_simulated_trades(id,trade_id,total_fees,net_cash_flow)
VALUES (1,'legacy-trade',12.34,-50012.34);
INSERT INTO research_v160_positions(id,recommendation_id,status,net_pnl,updated_at)
VALUES (1,'legacy-recommendation','open',-1361.41,'2026-09-03 10:07:00');`).Error; err != nil {
		t.Fatal(err)
	}

	financialBefore := snapshotSchema25FinancialRows(t, database)
	if err := applyResearch1DataChainV3Schema(database); err != nil {
		t.Fatalf("apply schema 25: %v", err)
	}
	if err := applyResearch1DataChainV3Schema(database); err != nil {
		t.Fatalf("repeat schema 25: %v", err)
	}

	var run struct {
		StrategyVersion        string
		DataProfileVersion     string
		EvidenceProfileVersion string
		FinalReport            string
		UpdatedAt              string
	}
	if err := database.Table("research_v160_analysis_runs").Where("run_id = ?", "legacy-run").Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.StrategyVersion != legacyUnversioned || run.DataProfileVersion != legacyUnversioned || run.EvidenceProfileVersion != "" || run.FinalReport != "unchanged" || !strings.HasPrefix(run.UpdatedAt, "2026-09-03T10:00:00") {
		t.Fatalf("legacy run=%+v", run)
	}

	var opportunities []struct {
		OpportunityID       string
		RequestedAction     string
		Action              string
		DecisionQuoteStatus string
		DataProfileVersion  string
		UpdatedAt           string
	}
	if err := database.Table("research_v270_buy_opportunities").Order("id ASC").Find(&opportunities).Error; err != nil {
		t.Fatal(err)
	}
	if len(opportunities) != 2 || opportunities[0].RequestedAction != "wait" || opportunities[0].Action != "wait" || opportunities[0].DecisionQuoteStatus != legacyRecordedQuoteStatus || opportunities[0].DataProfileVersion != legacyUnversioned || opportunities[1].RequestedAction != "reject" || opportunities[1].DecisionQuoteStatus != legacyUnavailableQuoteStatus {
		t.Fatalf("legacy opportunities=%+v", opportunities)
	}

	var observationVersion, policyVersion string
	if err := database.Table("research_v160_lifecycle_observations").Select("data_profile_version").Where("observation_id = ?", "legacy-observation").Scan(&observationVersion).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Table("research_v160_decision_events").Select("decision_policy_version").Where("event_id = ?", "legacy-event").Scan(&policyVersion).Error; err != nil {
		t.Fatal(err)
	}
	if observationVersion != legacyUnversioned || policyVersion != legacyUnversioned {
		t.Fatalf("observationVersion=%q policyVersion=%q", observationVersion, policyVersion)
	}
	if financialAfter := snapshotSchema25FinancialRows(t, database); financialAfter != financialBefore {
		t.Fatalf("schema 25 changed financial history\nbefore: %s\nafter:  %s", financialBefore, financialAfter)
	}
	if err := verifyMainSchema25Runtime(database); err != nil {
		t.Fatal(err)
	}
}

func TestSchema25PreservesExplicitV3ProvenanceOnRepeat(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research_v160_analysis_runs (id INTEGER PRIMARY KEY, strategy_version TEXT, data_profile_version TEXT);
CREATE TABLE research_v270_buy_opportunities (id INTEGER PRIMARY KEY, requested_action TEXT, action TEXT, quote_price REAL, quote_at DATETIME, decision_quote_status TEXT, reanalysis_at DATETIME, superseded_by_run_id TEXT, data_profile_version TEXT);
CREATE TABLE research_v160_lifecycle_observations (id INTEGER PRIMARY KEY, data_profile_version TEXT);
CREATE TABLE research_v160_decision_events (id INTEGER PRIMARY KEY, decision_policy_version TEXT);
INSERT INTO research_v160_analysis_runs VALUES (1,'research1-v3','research1-data-v3');
INSERT INTO research_v270_buy_opportunities VALUES (1,'buy_now','wait',10.5,'2026-09-03 10:00:00','stale','2026-09-03 10:30:00','next-run','research1-data-v3');
INSERT INTO research_v160_lifecycle_observations VALUES (1,'research1-data-v3');
INSERT INTO research_v160_decision_events VALUES (1,'research1-lifecycle-v3');`).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearch1DataChainV3Schema(database); err != nil {
		t.Fatal(err)
	}
	var row struct {
		RequestedAction     string
		Action              string
		DecisionQuoteStatus string
		DataProfileVersion  string
	}
	if err := database.Table("research_v270_buy_opportunities").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.RequestedAction != "buy_now" || row.Action != "wait" || row.DecisionQuoteStatus != "stale" || row.DataProfileVersion != "research1-data-v3" {
		t.Fatalf("explicit v3 provenance changed: %+v", row)
	}
}

func TestSchema25DefinitionAndVerifier(t *testing.T) {
	definition := mainMigrationV25Definition()
	for _, fragment := range []string{"data_profile_version", "requested_action", "decision_quote_status", "reanalysis_at", "superseded_by_run_id", "decision_policy_version", "no account, trade, position, cash, fee, P&L or return row is rewritten"} {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("schema 25 definition is missing %q: %s", fragment, definition)
		}
	}
}

func snapshotSchema25FinancialRows(t *testing.T, database *gorm.DB) string {
	t.Helper()
	var result string
	if err := database.Raw(`SELECT printf('%.2f|%.2f|%s|%.2f|%.2f|%s|%.2f',
  a.initial_cash,a.cash,t.trade_id,t.total_fees,t.net_cash_flow,p.status,p.net_pnl)
FROM research_v160_simulated_accounts a
JOIN research_v160_simulated_trades t ON t.id=1
JOIN research_v160_positions p ON p.id=1
WHERE a.id=1`).Scan(&result).Error; err != nil {
		t.Fatal(err)
	}
	return result
}
