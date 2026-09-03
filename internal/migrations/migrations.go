package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/research"
	"go-stock/internal/releaseinfo"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const minuteBarTableSQL = `CREATE TABLE IF NOT EXISTS minute_bar (
  stock_code TEXT NOT NULL,
  trade_time INTEGER NOT NULL,
  open REAL NOT NULL,
  high REAL NOT NULL,
  low REAL NOT NULL,
  close REAL NOT NULL,
  volume REAL,
  amount REAL,
  source TEXT,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (stock_code, trade_time)
) WITHOUT ROWID`

const minuteBarIndexSQL = `CREATE INDEX IF NOT EXISTS idx_minute_bar_trade_time
ON minute_bar(trade_time)`

const minuteBarSchemaSQL = minuteBarTableSQL + ";\n\n" + minuteBarIndexSQL + ";\n"

type MigrationRecord struct {
	ID         int       `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Name       string    `gorm:"not null" json:"name"`
	Checksum   string    `gorm:"size:64;not null" json:"checksum"`
	AppliedAt  time.Time `gorm:"not null" json:"appliedAt"`
	AppVersion string    `gorm:"not null" json:"appVersion"`
}

func (MigrationRecord) TableName() string { return "schema_migrations" }

type DatabaseStatus struct {
	Database        string            `json:"database"`
	CurrentVersion  int               `json:"currentVersion"`
	ExpectedVersion int               `json:"expectedVersion"`
	Pending         []int             `json:"pending"`
	Records         []MigrationRecord `json:"records"`
	QuickCheck      string            `json:"quickCheck,omitempty"`
}

type migration struct {
	id                int
	name              string
	description       string
	definition        func() string
	publishedChecksum string
	apply             func(*gorm.DB) error
}

func (m migration) checksum() string {
	if m.publishedChecksum != "" {
		return m.publishedChecksum
	}
	definition := ""
	if m.definition != nil {
		definition = m.definition()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%06d\n%s\n%s\n%s", m.id, m.name, m.description, definition)))
	return hex.EncodeToString(sum[:])
}

var mainMigrations = []migration{
	{
		id: 1, name: "baseline_app_schema",
		description:       "Published App 1.5.1 primary-schema baseline, retained only so existing databases can upgrade without rewriting history.",
		publishedChecksum: "41df05f8dbf7b1c56fe959ee8893d97938ddfe35425e98110333e47e2ee40ba6",
		apply:             applyMainSchema,
	},
	{
		id: 2, name: "lock_app_schema_definition",
		description:       "Published App 1.5.1 schema definition retained as an inert historical baseline.",
		definition:        func() string { return mainMigrationV2Definition },
		publishedChecksum: "616fac7d92781aa3c88470d13f7a34df4ec2d35772978167a03b570385b8e9b3",
		apply:             applyMainSchemaV2,
	},
	{
		id: 3, name: "research_v160_clean_schema",
		description: "App 1.6.0 creates isolated AI-analysis, lifecycle, account, trade and position tables and removes all legacy strategy guard triggers without deleting history.",
		definition:  mainMigrationV3Definition,
		apply:       applyResearchV160Schema,
	},
	{
		id: 4, name: "ai_config_model_switch_fallback_order",
		description: "App 1.6.0 adds a per-model call switch while retaining ai_config.sort as the single fallback order.",
		definition:  func() string { return "ai_config.disabled NOT NULL DEFAULT 0\nai_config.sort is fallback order" },
		apply:       applyAIConfigModelSwitchFallbackOrder,
	},
	{
		id: 5, name: "research_model_attempt_diagnostics",
		description: "App 1.6.2 persists sanitized, structured model-attempt diagnostics for each research analysis run.",
		definition:  func() string { return "research_v160_analysis_runs.model_attempt_log_json TEXT NOT NULL DEFAULT '[]'" },
		apply:       applyResearchModelAttemptDiagnostics,
	},
	{
		id: 6, name: "research_four_hour_activation_recovery",
		description: "App 1.6.3 restores the two after-close recommendations that were invalidated by the former same-day rule while preserving their decision history.",
		definition: func() string {
			return "restore c49ade23-12f4-4aa0-8203-b985bfd9d7e4 and 699640bc-861e-4330-8023-4182173b3e9e as pending at 2026-08-18 09:30 Asia/Shanghai; append deterministic recovery events"
		},
		apply: applyResearchFourHourActivationRecovery,
	},
	{
		id: 7, name: "research_lifecycle_observation_evidence",
		description: "App 1.6.4 adds bounded lifecycle evidence snapshots, source citations and a capped critical-data pause budget.",
		definition: func() string {
			return strings.Join([]string{
				"research_v160_lifecycle_observations",
				"research_v160_recommendations.data_pause_seconds INTEGER NOT NULL DEFAULT 0",
				"research_v160_decision_events.source_refs TEXT",
				"research_v160_decision_events.data_status VARCHAR(32)",
			}, "\n")
		},
		apply: applyResearchLifecycleObservationEvidence,
	},
	{
		id: 8, name: "research_direct_buy_and_fixed_sell_schedule",
		description: "App 1.6.5 replaces activation with one-shot direct buying and anchors sell reviews to fixed T+1 trading slots.",
		definition: func() string {
			return strings.Join([]string{
				"research recommendation status: buy_pending, active, sell_pending, missed_cash, missed_untradable, closed",
				"migrate legacy pending recommendations to buy_pending",
				"restore the four approved 2026-08-17/18 recommendations when they have no buy or position",
				"preserve historical activation fields, messages, observations and decisions",
			}, "\n")
		},
		apply: applyResearchDirectBuyStrategy,
	},
	{
		id: 9, name: "archive_legacy_strategy_runtime",
		description: "App 1.6.6 removes archived pre-1.6 strategy and yield tables after an externally verified permanent database archive has been created.",
		definition:  func() string { return strings.Join(legacyStrategyTables, "\n") },
		apply:       applyLegacyStrategyArchiveCleanup,
	},
	{
		id: 10, name: "research_multi_position_funding_performance",
		description: "App 1.7.0 adds external contribution flows, a four-deposit funding plan, unitized account valuation snapshots and queued-buy cash reservations without rewriting account cash or trading history.",
		definition: func() string {
			return strings.Join([]string{
				"research_v170_account_cash_flows",
				"research_v170_funding_plans",
				"research_v170_account_snapshots",
				"research_v160_recommendations.reserved_cash REAL NOT NULL DEFAULT 0",
				"initial contribution sequence 0 amount 100000; four future scheduled deposits of 100000; target 500000",
			}, "\n")
		},
		apply: applyResearchMultiPositionFunding,
	},
	{
		id: 11, name: "settings_provider_order_and_review_schedule",
		description: "Adds an explicit minute-provider fallback order and configurable AI holding-review schedule without deleting legacy settings or AI history.",
		definition: func() string {
			return strings.Join([]string{
				"settings.minute_provider_order TEXT NOT NULL DEFAULT 'tencent,sina,akshare,private'",
				"settings.ai_review_start_time TEXT NOT NULL DEFAULT '09:50'",
				"settings.ai_review_interval_minutes INTEGER NOT NULL DEFAULT 15",
			}, "\n")
		},
		apply: applySettingsProviderOrderAndReviewSchedule,
	},
	{
		id: 12, name: "research_fixed_capital_unlimited_positions",
		description: "App 1.7.7 rebases the simulated account to one fixed 500000 contribution, retires scheduled funding and restores the approved 2026-08-24 China Ping An recommendation when its exact historical run is present.",
		definition: func() string {
			return strings.Join([]string{
				"fixed initial contribution 500000; no scheduled deposits",
				"remove pre_deposit and post_deposit snapshots; rebase remaining snapshots to fixed capital",
				"restore analysis run 4bf3e4d9-959a-48c6-95b7-56104167c6cd at 2026-08-24 13:00 from Tencent first-minute open 55.17",
				"deterministic recommendation, trade, lifecycle messages and correction decision",
			}, "\n")
		},
		apply: applyResearchFixedCapitalAndHistoricalBuy,
	},
	{
		id: 13, name: "research2_overnight_strength_strategy",
		description: "App 1.8.4 adds the isolated 12000-yuan overnight-strength research account, reports, recommendations, trades, metrics and automatic-run switch.",
		definition: func() string {
			return strings.Join([]string{
				"settings.research2_auto_enabled BOOLEAN NOT NULL DEFAULT 1",
				"research2_analysis_runs",
				"research2_recommendations",
				"research2_trades",
				"research2_accounts initial_cash=12000 cash=12000",
				"research2_account_snapshots",
			}, "\n")
		},
		apply: applyResearch2OvernightStrengthStrategy,
	},
	{
		id: 14, name: "research2_report_email",
		description: "App 1.8.6 adds isolated Research Center 2 SMTP settings and durable report-email delivery state.",
		definition: func() string {
			return strings.Join([]string{
				"settings.research2_email_* copied once from complete legacy SMTP settings",
				"research2_email_deliveries unique analysis_run_id",
				"eligible statuses: success, no_recommendation, missed_window",
			}, "\n")
		},
		apply: applyResearch2ReportEmail,
	},
	{
		id: 15, name: "research_evidence_and_market_cache",
		description: "App 2.0.0 adds immutable research evidence sets, nullable future-run version links and an opt-in evidence switch without rewriting historical research or account state.",
		definition:  mainMigrationV15Definition,
		apply:       applyResearchEvidenceSchema,
	},
	{
		id: 16, name: "chart_drawing_documents",
		description: "App 2.1.0 adds isolated current chart-drawing documents and immutable revision history without rewriting any research, account or trading data.",
		definition:  mainMigrationV16Definition,
		apply:       applyChartDrawingSchema,
	},
	{
		id: 17, name: "market_themes_and_catalysts",
		description: "App 2.2.0 adds normalized market themes, immutable daily snapshots, catalyst claims, constituents and evidence links without rewriting historical research, account or trading data.",
		definition:  mainMigrationV17Definition,
		apply:       applyThemeCatalystSchema,
	},
	{
		id: 18, name: "research_audit_and_replay",
		description: "App 2.3.0 adds immutable prompt and payload audit archives plus controlled replay state without rewriting historical research, account, trading, drawing or theme data.",
		definition:  mainMigrationV18Definition,
		apply:       applyResearchAuditSchema,
	},
	{
		id: 19, name: "controlled_knowledge_and_memory",
		description: "App 2.4.0 adds immutable knowledge versions, explicit user approval state, FTS5 retrieval and auditable memory candidates without rewriting historical research, account or trading data.",
		definition:  mainMigrationV19Definition,
		apply:       applyKnowledgeSchema,
	},
	{
		id: 20, name: "fund_rankings_and_exchange_traded_funds",
		description: "App 2.5.0 adds fund and ETF ranking caches, ETF detail snapshots and an isolated ETF watchlist without rewriting historical research, recommendation, account or trading data.",
		definition:  mainMigrationV20Definition,
		apply:       applyFundETFSchema,
	},
	{
		id: 21, name: "event_driven_capital_deployment",
		description: "App 2.7.0 replaces fixed-time research with durable event-driven capital deployment, structured buy opportunities and database leases without rewriting historical research, recommendation, account or trading rows.",
		definition:  mainMigrationV21Definition,
		apply:       applyEventDrivenCapitalDeploymentSchema,
	},
	{
		id: 22, name: "research2_live_valuation",
		description: "Removes Research Center 2 recommendation ranks and stores the latest valid quote for live position valuation without rewriting realized returns or trading history.",
		definition:  mainMigrationV22Definition,
		apply:       applyResearch2LiveValuationSchema,
	},
	{
		id: 23, name: "research2_trailing5_evidence_and_execution",
		description: "Adds Research Center 2 trailing-five-minute evidence quality and execution provenance without rewriting schema 22 analysis, trade, account or return history.",
		definition:  mainMigrationV23Definition,
		apply:       applyResearch2Trailing5Schema,
	},
	{
		id: 24, name: "research2_analysis_attempts",
		description: "Allows multiple immutable Research Center 2 analysis attempts per trading date while preserving every historical run as attempt 1.",
		definition:  mainMigrationV24Definition,
		apply:       applyResearch2AnalysisAttemptSchema,
	},
}

var legacyStrategyTables = []string{
	"ai_recommend_daily_bar",
	"ai_recommend_minute_bar",
	"ai_recommend_opening_review",
	"ai_recommend_stocks",
	"ai_recommend_yield_dirty_code",
	"ai_recommend_yield_meta",
	"ai_recommend_yield_override",
	"ai_recommend_yield_record_state",
	"ai_recommend_yield_state",
	"strategy_backtest_metric",
	"strategy_backtest_run",
	"strategy_backtest_trade",
	"strategy_candidate_snapshot",
	"strategy_order_event",
	"strategy_rule_snapshot",
	"strategy_run_snapshot",
	"strategy_runtime_control",
}

func LegacyStrategyRowCounts(database *gorm.DB) (map[string]int64, error) {
	if database == nil {
		return nil, errors.New("main database is unavailable")
	}
	counts := make(map[string]int64, len(legacyStrategyTables))
	for _, table := range legacyStrategyTables {
		if !database.Migrator().HasTable(table) {
			counts[table] = 0
			continue
		}
		var count int64
		if err := database.Table(table).Count(&count).Error; err != nil {
			return nil, fmt.Errorf("count archived legacy strategy table %s: %w", table, err)
		}
		counts[table] = count
	}
	return counts, nil
}

var minuteMigrations = []migration{
	{
		id:                1,
		name:              "baseline_minute_bar_schema",
		description:       "App 1.5.1 minute_bar WITHOUT ROWID baseline and trade_time index.",
		publishedChecksum: "e838c98300ecee89806e5da10fc424bacff60754e212b449066feadecf59c8ec",
		apply:             func(tx *gorm.DB) error { return tx.Exec(minuteBarSchemaSQL).Error },
	},
	{id: 2, name: "lock_minute_bar_schema_definition", description: "App 1.5.1 locks the complete minute_bar DDL without rewriting the published baseline migration.", publishedChecksum: "f479775a220b2f4816aaa254c0193f49861fb8d61181634607b76e338debbde0", definition: func() string { return minuteBarSchemaSQL }, apply: func(tx *gorm.DB) error {
		if err := tx.Exec(minuteBarSchemaSQL).Error; err != nil {
			return err
		}
		return verifyMinuteSchema(tx)
	}},
	{
		id: 3, name: "market_evidence_cache",
		description: "App 2.0.0 adds typed multi-period bars, auction snapshots and trade ticks to the isolated minute cache.",
		definition:  minuteMigrationV3Definition,
		apply:       applyMarketEvidenceCacheSchema,
	},
}

func applyMainSchema(tx *gorm.DB) error   { return applyFrozenMainSchemaV2(tx) }
func applyMainSchemaV2(tx *gorm.DB) error { return applyFrozenMainSchemaV2(tx) }

func mainMigrationV3Definition() string {
	return strings.Join([]string{
		"research_v160_analysis_runs", "research_v160_recommendations", "research_v160_lifecycle_messages",
		"research_v160_decision_events", "research_v160_simulated_accounts", "research_v160_simulated_trades",
		"research_v160_positions", "settings.ai_analysis_enabled", "settings.ai_analysis_config_id",
		"settings.ai_analysis_times", "drop legacy guard_strategy/guard_legacy/immutable_strategy/immutable_corporate_action triggers",
	}, "\n")
}

func applyResearchV160Schema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := dropLegacyStrategyTriggers(tx); err != nil {
		return err
	}
	if err := tx.AutoMigrate(
		&research.AnalysisRun{}, &research.Recommendation{}, &research.LifecycleMessage{},
		&research.DecisionEvent{}, &research.SimulatedAccount{}, &research.SimulatedTrade{}, &research.Position{},
		&models.Settings{},
	); err != nil {
		return fmt.Errorf("create 1.6.0 research schema: %w", err)
	}
	account := research.SimulatedAccount{ID: 1, InitialCash: research.LegacyInitialCash, Cash: research.LegacyInitialCash}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error; err != nil {
		return fmt.Errorf("initialize 1.6.0 simulated account: %w", err)
	}
	return nil
}

func applyResearchMultiPositionFunding(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := tx.AutoMigrate(&research.AccountCashFlow{}, &research.FundingPlan{}, &research.AccountValuationSnapshot{}, &research.Recommendation{}); err != nil {
		return fmt.Errorf("create 1.7.0 funding and performance schema: %w", err)
	}
	var account research.SimulatedAccount
	if err := tx.First(&account, 1).Error; err != nil {
		return fmt.Errorf("load simulated account for funding migration: %w", err)
	}
	now := time.Now()
	effectiveAt := account.CreatedAt
	if effectiveAt.IsZero() {
		effectiveAt = now
	}
	localEffective := research.ShanghaiTime(effectiveAt)
	initialFlow := research.AccountCashFlow{
		FlowID:   uuid.NewSHA1(uuid.NameSpaceOID, []byte("go-stock-1.7.0-initial-contribution")).String(),
		Sequence: 0, Type: "initial_deposit", Amount: research.LegacyInitialCash, EffectiveAt: effectiveAt,
		TradingDate: localEffective.Format("2006-01-02"), NetAssetValueBefore: 0,
		NetAssetValueAfter: research.LegacyInitialCash, UnitValueBefore: 1, UnitsIssued: research.LegacyInitialCash,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&initialFlow).Error; err != nil {
		return fmt.Errorf("register initial contribution: %w", err)
	}
	plan := research.FundingPlan{
		ID: 1, InitialContribution: research.LegacyInitialCash, TargetContribution: research.TargetContribution,
		DepositAmount: research.ScheduledDepositAmount, PlannedDeposits: research.ScheduledDepositCount,
		StartAfterTradingDate: research.ShanghaiTime(now).Format("2006-01-02"),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&plan).Error; err != nil {
		return fmt.Errorf("initialize funding plan: %w", err)
	}
	baseline := research.AccountValuationSnapshot{
		SnapshotID: "initial-deposit-baseline", SnapshotType: "initial_deposit",
		TradingDate: localEffective.Format("2006-01-02"), ValuedAt: effectiveAt,
		Cash: research.LegacyInitialCash, PositionValue: 0, NetAssetValue: research.LegacyInitialCash,
		CumulativeNetContribution: research.LegacyInitialCash, UnitValue: 1, TimeWeightedReturn: 0,
		ValuationStatus: "baseline",
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&baseline).Error; err != nil {
		return fmt.Errorf("initialize account valuation baseline: %w", err)
	}
	// Raw SQL deliberately avoids GORM's UpdatedAt callback: schema migration
	// must not rewrite the historical modification time of queued records, and
	// the zero guard makes a repeated migration a true no-op.
	if err := tx.Exec(`UPDATE research_v160_recommendations
		SET reserved_cash = ?
		WHERE status IN ('buy_pending', 'pending') AND reserved_cash = 0`, research.MaxCashPerTrade).Error; err != nil {
		return fmt.Errorf("backfill queued-buy reservations: %w", err)
	}
	return verifyMainSchema10Runtime(tx)
}

func applySettingsProviderOrderAndReviewSchedule(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&models.Settings{}) {
		return errors.New("settings table is unavailable")
	}
	if !tx.Migrator().HasColumn(&models.Settings{}, "MinuteProviderOrder") {
		if err := tx.Exec("ALTER TABLE settings ADD COLUMN minute_provider_order TEXT NOT NULL DEFAULT 'tencent,sina,akshare,private'").Error; err != nil {
			return fmt.Errorf("add settings.minute_provider_order: %w", err)
		}
	}
	if !tx.Migrator().HasColumn(&models.Settings{}, "AIReviewStartTime") {
		if err := tx.Exec("ALTER TABLE settings ADD COLUMN ai_review_start_time TEXT NOT NULL DEFAULT '09:50'").Error; err != nil {
			return fmt.Errorf("add settings.ai_review_start_time: %w", err)
		}
	}
	if !tx.Migrator().HasColumn(&models.Settings{}, "AIReviewIntervalMinutes") {
		if err := tx.Exec("ALTER TABLE settings ADD COLUMN ai_review_interval_minutes INTEGER NOT NULL DEFAULT 15").Error; err != nil {
			return fmt.Errorf("add settings.ai_review_interval_minutes: %w", err)
		}
	}
	if err := tx.Exec(`UPDATE settings
		SET minute_provider_order = CASE
			WHEN LOWER(TRIM(COALESCE(minute_provider_mode, ''))) = 'private' THEN 'private,tencent,sina,akshare'
			ELSE 'tencent,sina,akshare,private'
		END
		WHERE minute_provider_order IS NULL OR TRIM(minute_provider_order) = ''
			OR minute_provider_order = 'tencent,sina,akshare,private' AND LOWER(TRIM(COALESCE(minute_provider_mode, ''))) = 'private'`).Error; err != nil {
		return fmt.Errorf("backfill minute provider order: %w", err)
	}
	if err := tx.Exec("UPDATE settings SET ai_review_start_time = '09:50' WHERE ai_review_start_time IS NULL OR TRIM(ai_review_start_time) = ''").Error; err != nil {
		return fmt.Errorf("backfill AI review start time: %w", err)
	}
	if err := tx.Exec("UPDATE settings SET ai_review_interval_minutes = 15 WHERE ai_review_interval_minutes IS NULL OR ai_review_interval_minutes < 5 OR ai_review_interval_minutes > 120").Error; err != nil {
		return fmt.Errorf("backfill AI review interval: %w", err)
	}
	return verifyMainSchema11Runtime(tx)
}

func applyAIConfigModelSwitchFallbackOrder(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&models.AIConfig{}) {
		return errors.New("ai_config table is unavailable")
	}
	if !tx.Migrator().HasColumn(&models.AIConfig{}, "Disabled") {
		if err := tx.Exec("ALTER TABLE ai_config ADD COLUMN disabled numeric NOT NULL DEFAULT 0").Error; err != nil {
			return fmt.Errorf("add ai_config.disabled: %w", err)
		}
	}
	if err := tx.Exec("UPDATE ai_config SET disabled = 0 WHERE disabled IS NULL").Error; err != nil {
		return fmt.Errorf("backfill ai_config.disabled: %w", err)
	}
	return nil
}

func applyResearchModelAttemptDiagnostics(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&research.AnalysisRun{}) {
		return errors.New("research analysis table is unavailable")
	}
	if !tx.Migrator().HasColumn(&research.AnalysisRun{}, "ModelAttemptLogJSON") {
		if err := tx.Exec("ALTER TABLE research_v160_analysis_runs ADD COLUMN model_attempt_log_json TEXT NOT NULL DEFAULT '[]'").Error; err != nil {
			return fmt.Errorf("add research model attempt log: %w", err)
		}
	}
	if err := tx.Exec("UPDATE research_v160_analysis_runs SET model_attempt_log_json = '[]' WHERE model_attempt_log_json IS NULL OR TRIM(model_attempt_log_json) = ''").Error; err != nil {
		return fmt.Errorf("backfill research model attempt log: %w", err)
	}
	return nil
}

func applyResearchFourHourActivationRecovery(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&research.Recommendation{}) || !tx.Migrator().HasTable(&research.DecisionEvent{}) {
		return errors.New("research lifecycle tables are unavailable")
	}
	nextCheck := time.Date(2026, 8, 18, 9, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	recoveries := []struct {
		recommendationID string
		eventID          string
	}{
		{recommendationID: "c49ade23-12f4-4aa0-8203-b985bfd9d7e4", eventID: "16300000-0000-4000-8000-000000000001"},
		{recommendationID: "699640bc-861e-4330-8023-4182173b3e9e", eventID: "16300000-0000-4000-8000-000000000002"},
	}
	for _, recovery := range recoveries {
		result := tx.Model(&research.Recommendation{}).
			Where("recommendation_id = ? AND status = ?", recovery.recommendationID, "invalidated").
			Updates(map[string]any{
				"status": "pending", "next_check_at": nextCheck, "last_decision": "", "last_decision_at": nil,
			})
		if result.Error != nil {
			return fmt.Errorf("restore recommendation %s: %w", recovery.recommendationID, result.Error)
		}
		if result.RowsAffected == 0 {
			continue
		}
		event := research.DecisionEvent{
			EventID: recovery.eventID, RecommendationID: recovery.recommendationID,
			DecisionType: "人工恢复", DecidedAt: time.Now(),
			Reason: "1.6.3 启用累计4小时开盘交易时长规则，恢复为未激活并从下一交易日继续判断",
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error; err != nil {
			return fmt.Errorf("record recovery event %s: %w", recovery.recommendationID, err)
		}
	}
	return nil
}

func applyResearchLifecycleObservationEvidence(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&research.Recommendation{}) || !tx.Migrator().HasTable(&research.DecisionEvent{}) {
		return errors.New("research lifecycle tables are unavailable")
	}
	if err := tx.AutoMigrate(&research.Recommendation{}, &research.DecisionEvent{}, &research.LifecycleObservation{}); err != nil {
		return fmt.Errorf("add lifecycle observation evidence schema: %w", err)
	}
	if err := tx.Exec("UPDATE research_v160_recommendations SET data_pause_seconds = 0 WHERE data_pause_seconds IS NULL").Error; err != nil {
		return fmt.Errorf("backfill lifecycle data pause budget: %w", err)
	}
	return nil
}

func applyResearchDirectBuyStrategy(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&research.Recommendation{}) || !tx.Migrator().HasTable(&research.DecisionEvent{}) ||
		!tx.Migrator().HasTable(&research.SimulatedTrade{}) || !tx.Migrator().HasTable(&research.Position{}) {
		return errors.New("research direct-buy tables are unavailable")
	}
	targets := []string{
		"c49ade23-12f4-4aa0-8203-b985bfd9d7e4",
		"699640bc-861e-4330-8023-4182173b3e9e",
		"3bf68fd1-d97f-4426-aa2c-cb63236be808",
		"053e7c47-a538-4d6d-9dbd-61e9897d8285",
	}
	var rows []research.Recommendation
	if err := tx.Where("status = ? OR (recommendation_id IN ? AND status = ?)", "pending", targets, "invalidated").
		Order("signal_at ASC, id ASC").Find(&rows).Error; err != nil {
		return fmt.Errorf("list legacy recommendations for direct buy: %w", err)
	}
	now := time.Now()
	for _, row := range rows {
		var bought, positioned int64
		if err := tx.Model(&research.SimulatedTrade{}).
			Where("recommendation_id = ? AND side = ?", row.RecommendationID, "buy").Count(&bought).Error; err != nil {
			return err
		}
		if err := tx.Model(&research.Position{}).Where("recommendation_id = ?", row.RecommendationID).Count(&positioned).Error; err != nil {
			return err
		}
		if bought > 0 || positioned > 0 {
			continue
		}
		if err := tx.Model(&research.Recommendation{}).Where("recommendation_id = ?", row.RecommendationID).Updates(map[string]any{
			"status": "buy_pending", "next_check_at": now, "last_decision": "待买入", "last_decision_at": now,
		}).Error; err != nil {
			return fmt.Errorf("queue legacy recommendation %s for direct buy: %w", row.RecommendationID, err)
		}
		event := research.DecisionEvent{
			EventID:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("go-stock-1.6.5-direct-buy:"+row.RecommendationID)).String(),
			RecommendationID: row.RecommendationID, DecisionType: "策略升级待买入", DecidedAt: now,
			Reason: "1.6.5 删除激活流程；保留历史记录并按原信号顺序进入一次性直接买入队列",
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error; err != nil {
			return fmt.Errorf("append direct-buy migration event %s: %w", row.RecommendationID, err)
		}
	}
	var openPositions []research.Position
	if err := tx.Where("status = ?", "open").Order("entry_at ASC, id ASC").Find(&openPositions).Error; err != nil {
		return err
	}
	for _, position := range openPositions {
		entry := research.ShanghaiTime(position.EntryAt).AddDate(0, 0, 1)
		y, m, d := entry.Date()
		nominalNext := time.Date(y, m, d, 9, 50, 0, 0, entry.Location())
		if err := tx.Model(&research.Recommendation{}).
			Where("recommendation_id = ? AND status IN ?", position.RecommendationID, []string{"active", "sell_pending"}).
			Update("next_check_at", nominalNext).Error; err != nil {
			return fmt.Errorf("re-anchor open position %s: %w", position.RecommendationID, err)
		}
	}
	return nil
}

func applyLegacyStrategyArchiveCleanup(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, table := range legacyStrategyTables {
		if err := tx.Exec("DROP TABLE IF EXISTS " + quoteSQLiteIdentifier(table)).Error; err != nil {
			return fmt.Errorf("drop archived legacy strategy table %s: %w", table, err)
		}
	}
	return verifyMainSchema9Runtime(tx)
}

func dropLegacyStrategyTriggers(database *gorm.DB) error {
	var names []string
	if err := database.Raw(`SELECT name FROM sqlite_master
WHERE type = 'trigger' AND (
  name LIKE 'guard_strategy_%' OR
  name LIKE 'guard_legacy_%' OR
  name LIKE 'immutable_strategy_%' OR
  name LIKE 'immutable_corporate_action_%'
) ORDER BY name`).Scan(&names).Error; err != nil {
		return fmt.Errorf("list legacy strategy triggers: %w", err)
	}
	for _, name := range names {
		if err := database.Exec("DROP TRIGGER IF EXISTS " + quoteSQLiteIdentifier(name)).Error; err != nil {
			return fmt.Errorf("drop legacy strategy trigger %s: %w", name, err)
		}
	}
	return nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func verifyMinuteSchema(database *gorm.DB) error {
	if err := verifySQLiteSchemaObject(database, "table", "minute_bar", minuteBarTableSQL); err != nil {
		return fmt.Errorf("verify minute_bar: %w", err)
	}
	if err := verifySQLiteSchemaObject(database, "index", "idx_minute_bar_trade_time", minuteBarIndexSQL); err != nil {
		return fmt.Errorf("verify idx_minute_bar_trade_time: %w", err)
	}
	return nil
}

func verifySQLiteSchemaObject(database *gorm.DB, objectType, name, expectedSQL string) error {
	var actualSQL string
	result := database.Raw("SELECT sql FROM sqlite_master WHERE type = ? AND name = ?", objectType, name).Scan(&actualSQL)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 || strings.TrimSpace(actualSQL) == "" {
		return fmt.Errorf("%s %q is missing", objectType, name)
	}
	if normalizeSQLiteSQL(actualSQL) != normalizeSQLiteSQL(expectedSQL) {
		return fmt.Errorf("%s %q definition conflict: database=%q code=%q", objectType, name, actualSQL, expectedSQL)
	}
	return nil
}

func normalizeSQLiteSQL(value string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(value), ";")), " ")
	for _, prefix := range []string{"CREATE TRIGGER", "CREATE TABLE", "CREATE INDEX"} {
		normalized = strings.Replace(normalized, prefix+" IF NOT EXISTS", prefix, 1)
	}
	return normalized
}

func MigrateMain(database *gorm.DB) error {
	return migrate(database, "main", mainMigrations, releaseinfo.Manifest().MainSchemaVersion)
}
func MigrateMinute(database *gorm.DB) error {
	return migrate(database, "minute", minuteMigrations, releaseinfo.Manifest().MinuteSchemaVersion)
}
func MigrateAll(mainDB, minuteDB *gorm.DB) error {
	if err := MigrateMain(mainDB); err != nil {
		return err
	}
	return MigrateMinute(minuteDB)
}

func migrate(database *gorm.DB, databaseName string, migrations []migration, expected int) error {
	if database == nil {
		return fmt.Errorf("%s database is not initialized", databaseName)
	}
	if expected != latestVersion(migrations) {
		return fmt.Errorf("%s manifest schema version %d does not match latest migration %d", databaseName, expected, latestVersion(migrations))
	}
	if err := database.AutoMigrate(&MigrationRecord{}); err != nil {
		return fmt.Errorf("initialize %s migration ledger: %w", databaseName, err)
	}
	if _, err := records(database, migrations); err != nil {
		return fmt.Errorf("verify %s migration ledger: %w", databaseName, err)
	}
	if err := ensureNoMigrationGaps(database, migrations); err != nil {
		return fmt.Errorf("verify %s migration ledger: %w", databaseName, err)
	}
	for _, item := range migrations {
		var count int64
		if err := database.Model(&MigrationRecord{}).Where("id = ?", item.id).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			continue
		}
		record := MigrationRecord{ID: item.id, Name: item.name, Checksum: item.checksum(), AppliedAt: time.Now().UTC(), AppVersion: releaseinfo.Manifest().AppVersion}
		if err := database.Transaction(func(tx *gorm.DB) error {
			if err := item.apply(tx); err != nil {
				return err
			}
			return tx.Create(&record).Error
		}); err != nil {
			return fmt.Errorf("apply %s migration %06d_%s: %w", databaseName, item.id, item.name, err)
		}
	}
	return Verify(database, databaseName, migrations, expected)
}

func ensureNoMigrationGaps(database *gorm.DB, migrations []migration) error {
	firstMissing := -1
	for index, item := range migrations {
		var count int64
		if err := database.Model(&MigrationRecord{}).Where("id = ?", item.id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 && firstMissing < 0 {
			firstMissing = index
			continue
		}
		if count != 0 && firstMissing >= 0 {
			return fmt.Errorf("migration %d is applied while prior migration %d is missing", item.id, migrations[firstMissing].id)
		}
	}
	return nil
}

func StatusMain(database *gorm.DB) (DatabaseStatus, error) {
	return status(database, "main", mainMigrations, releaseinfo.Manifest().MainSchemaVersion, false)
}
func StatusMinute(database *gorm.DB) (DatabaseStatus, error) {
	return status(database, "minute", minuteMigrations, releaseinfo.Manifest().MinuteSchemaVersion, false)
}
func VerifyMain(database *gorm.DB) (DatabaseStatus, error) {
	return verifiedStatus(database, "main", mainMigrations, releaseinfo.Manifest().MainSchemaVersion)
}
func VerifyMinute(database *gorm.DB) (DatabaseStatus, error) {
	return verifiedStatus(database, "minute", minuteMigrations, releaseinfo.Manifest().MinuteSchemaVersion)
}

func verifiedStatus(database *gorm.DB, name string, migrations []migration, expected int) (DatabaseStatus, error) {
	result, err := status(database, name, migrations, expected, true)
	if err != nil {
		return result, err
	}
	if result.CurrentVersion != expected || len(result.Pending) != 0 {
		return result, fmt.Errorf("%s schema version is %d, expected %d", name, result.CurrentVersion, expected)
	}
	if !strings.EqualFold(strings.TrimSpace(result.QuickCheck), "ok") {
		return result, fmt.Errorf("%s quick_check returned %q", name, result.QuickCheck)
	}
	if name == "main" && expected >= 3 {
		if err := verifyMainSchema3Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 4 {
		if err := verifyMainSchema4Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 5 {
		if err := verifyMainSchema5Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 7 {
		if err := verifyMainSchema7Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 8 {
		if err := verifyMainSchema8Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 9 {
		if err := verifyMainSchema9Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 10 {
		if err := verifyMainSchema10Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 11 {
		if err := verifyMainSchema11Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 12 {
		if err := verifyMainSchema12Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 13 {
		if err := verifyMainSchema13Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 14 {
		if err := verifyMainSchema14Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 15 {
		if err := verifyMainSchema15Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 16 {
		if err := verifyMainSchema16Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 17 {
		if err := verifyMainSchema17Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 18 {
		if err := verifyMainSchema18Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 19 {
		if err := verifyMainSchema19Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 20 {
		if err := verifyMainSchema20Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 21 {
		if err := verifyMainSchema21Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 22 {
		if err := verifyMainSchema22Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 23 {
		if err := verifyMainSchema23Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 24 {
		if err := verifyMainSchema24Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "minute" && expected >= 2 {
		if err := verifyMinuteSchema(database); err != nil {
			return result, err
		}
	}
	if name == "minute" && expected >= 3 {
		if err := verifyMinuteSchema3Runtime(database); err != nil {
			return result, err
		}
	}
	return result, nil
}

func verifyMainSchema3Runtime(database *gorm.DB) error {
	modelsToCheck := []any{&research.AnalysisRun{}, &research.Recommendation{}, &research.LifecycleMessage{}, &research.DecisionEvent{}, &research.SimulatedAccount{}, &research.SimulatedTrade{}, &research.Position{}}
	for _, model := range modelsToCheck {
		if !database.Migrator().HasTable(model) {
			return fmt.Errorf("main schema 3 table for %T is missing", model)
		}
	}
	var account research.SimulatedAccount
	if err := database.First(&account, 1).Error; err != nil {
		return fmt.Errorf("main schema 3 simulated account is unavailable: %w", err)
	}
	if account.InitialCash <= 0 {
		return fmt.Errorf("main schema 3 initial cash is invalid: %.2f", account.InitialCash)
	}
	var triggerCount int64
	if err := database.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND (name LIKE 'guard_strategy_%' OR name LIKE 'guard_legacy_%' OR name LIKE 'immutable_strategy_%' OR name LIKE 'immutable_corporate_action_%')`).Scan(&triggerCount).Error; err != nil {
		return err
	}
	if triggerCount != 0 {
		return fmt.Errorf("main schema 3 still has %d legacy strategy guard triggers", triggerCount)
	}
	return nil
}

func verifyMainSchema4Runtime(database *gorm.DB) error {
	if !database.Migrator().HasColumn(&models.AIConfig{}, "Disabled") {
		return errors.New("main schema 4 ai_config.disabled is missing")
	}
	var nullCount int64
	if err := database.Raw("SELECT COUNT(*) FROM ai_config WHERE disabled IS NULL").Scan(&nullCount).Error; err != nil {
		return err
	}
	if nullCount != 0 {
		return fmt.Errorf("main schema 4 has %d ai_config rows without call-switch state", nullCount)
	}
	return nil
}

func verifyMainSchema5Runtime(database *gorm.DB) error {
	if !database.Migrator().HasColumn(&research.AnalysisRun{}, "ModelAttemptLogJSON") {
		return errors.New("main schema 5 research model attempt log is missing")
	}
	var invalidCount int64
	if err := database.Raw("SELECT COUNT(*) FROM research_v160_analysis_runs WHERE model_attempt_log_json IS NULL OR TRIM(model_attempt_log_json) = ''").Scan(&invalidCount).Error; err != nil {
		return err
	}
	if invalidCount != 0 {
		return fmt.Errorf("main schema 5 has %d analysis rows without model attempt diagnostics", invalidCount)
	}
	return nil
}

func verifyMainSchema7Runtime(database *gorm.DB) error {
	if !database.Migrator().HasTable(&research.LifecycleObservation{}) {
		return errors.New("main schema 7 lifecycle observation table is missing")
	}
	checks := []struct {
		model any
		field string
	}{
		{model: &research.Recommendation{}, field: "DataPauseSeconds"},
		{model: &research.DecisionEvent{}, field: "SourceRefs"},
		{model: &research.DecisionEvent{}, field: "DataStatus"},
	}
	for _, check := range checks {
		if !database.Migrator().HasColumn(check.model, check.field) {
			return fmt.Errorf("main schema 7 field %T.%s is missing", check.model, check.field)
		}
	}
	var nullCount int64
	if err := database.Raw("SELECT COUNT(*) FROM research_v160_recommendations WHERE data_pause_seconds IS NULL").Scan(&nullCount).Error; err != nil {
		return err
	}
	if nullCount != 0 {
		return fmt.Errorf("main schema 7 has %d recommendations without data pause state", nullCount)
	}
	return nil
}

func verifyMainSchema8Runtime(database *gorm.DB) error {
	var pending int64
	if err := database.Model(&research.Recommendation{}).Where("status = ?", "pending").Count(&pending).Error; err != nil {
		return err
	}
	if pending != 0 {
		return fmt.Errorf("main schema 8 has %d legacy pending recommendations", pending)
	}
	return nil
}

func verifyMainSchema9Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	for _, table := range legacyStrategyTables {
		if database.Migrator().HasTable(table) {
			return fmt.Errorf("main schema 9 still has archived legacy strategy table %s", table)
		}
		var indexCount int64
		if err := database.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = ?", table).Scan(&indexCount).Error; err != nil {
			return fmt.Errorf("inspect archived legacy strategy indexes for %s: %w", table, err)
		}
		if indexCount != 0 {
			return fmt.Errorf("main schema 9 still has %d indexes for archived legacy strategy table %s", indexCount, table)
		}
	}
	return nil
}

func verifyMainSchema10Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	for _, model := range []any{&research.AccountCashFlow{}, &research.FundingPlan{}, &research.AccountValuationSnapshot{}} {
		if !database.Migrator().HasTable(model) {
			return fmt.Errorf("main schema 10 table for %T is missing", model)
		}
	}
	if !database.Migrator().HasColumn(&research.Recommendation{}, "ReservedCash") {
		return errors.New("main schema 10 recommendation reserved_cash is missing")
	}
	var initialCount int64
	if err := database.Model(&research.AccountCashFlow{}).
		Where("sequence = ? AND type = ?", 0, "initial_deposit").
		Count(&initialCount).Error; err != nil {
		return err
	}
	if initialCount != 1 {
		return fmt.Errorf("main schema 10 has %d valid initial contribution rows, expected 1", initialCount)
	}
	var plan research.FundingPlan
	if err := database.First(&plan, 1).Error; err != nil {
		return fmt.Errorf("main schema 10 funding plan is unavailable: %w", err)
	}
	if plan.TargetContribution <= 0 || plan.InitialContribution <= 0 {
		return fmt.Errorf("main schema 10 funding plan is structurally invalid: %+v", plan)
	}
	return nil
}

func verifyMainSchema11Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	for _, field := range []string{"MinuteProviderOrder", "AIReviewStartTime", "AIReviewIntervalMinutes"} {
		if !database.Migrator().HasColumn(&models.Settings{}, field) {
			return fmt.Errorf("main schema 11 settings.%s is missing", field)
		}
	}
	var invalid int64
	if err := database.Model(&models.Settings{}).Where(
		"minute_provider_order IS NULL OR TRIM(minute_provider_order) = '' OR ai_review_start_time IS NULL OR TRIM(ai_review_start_time) = '' OR ai_review_interval_minutes < 5 OR ai_review_interval_minutes > 120",
	).Count(&invalid).Error; err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("main schema 11 has %d invalid settings rows", invalid)
	}
	return nil
}

func Verify(database *gorm.DB, name string, migrations []migration, expected int) error {
	_, err := verifiedStatus(database, name, migrations, expected)
	return err
}

func status(database *gorm.DB, name string, migrations []migration, expected int, runQuickCheck bool) (DatabaseStatus, error) {
	result := DatabaseStatus{Database: name, ExpectedVersion: expected}
	if database == nil {
		return result, fmt.Errorf("%s database is not initialized", name)
	}
	if !database.Migrator().HasTable(&MigrationRecord{}) {
		for _, item := range migrations {
			result.Pending = append(result.Pending, item.id)
		}
		if runQuickCheck {
			result.QuickCheck = quickCheck(database)
		}
		return result, nil
	}
	rows, err := records(database, migrations)
	if err != nil {
		return result, err
	}
	result.Records = rows
	seen := make(map[int]struct{}, len(rows))
	for _, row := range rows {
		seen[row.ID] = struct{}{}
		if row.ID > result.CurrentVersion {
			result.CurrentVersion = row.ID
		}
	}
	for _, item := range migrations {
		if _, ok := seen[item.id]; !ok {
			result.Pending = append(result.Pending, item.id)
		}
	}
	if runQuickCheck {
		result.QuickCheck = quickCheck(database)
	}
	return result, nil
}

func records(database *gorm.DB, migrations []migration) ([]MigrationRecord, error) {
	var rows []MigrationRecord
	if err := database.Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	expected := make(map[int]migration, len(migrations))
	for _, item := range migrations {
		expected[item.id] = item
	}
	for _, row := range rows {
		item, ok := expected[row.ID]
		if !ok {
			return nil, fmt.Errorf("unknown applied migration id %d", row.ID)
		}
		if row.Name != item.name {
			return nil, fmt.Errorf("migration %d name conflict: database=%q code=%q", row.ID, row.Name, item.name)
		}
		if !strings.EqualFold(row.Checksum, item.checksum()) {
			return nil, fmt.Errorf("migration %d checksum conflict: database=%s code=%s", row.ID, row.Checksum, item.checksum())
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func quickCheck(database *gorm.DB) string {
	var result string
	if err := database.Raw("PRAGMA quick_check").Scan(&result).Error; err != nil {
		return "error: " + err.Error()
	}
	return strings.TrimSpace(result)
}
func latestVersion(items []migration) int {
	if len(items) == 0 {
		return 0
	}
	latest := items[0].id
	for _, item := range items[1:] {
		if item.id <= latest {
			panic(errors.New("migrations must have strictly increasing ids"))
		}
		latest = item.id
	}
	return latest
}
