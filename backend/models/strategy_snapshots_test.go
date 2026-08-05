package models

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestStrategyPersistenceModelTableNames(t *testing.T) {
	tests := []struct {
		model interface{ TableName() string }
		want  string
	}{
		{StrategyRunSnapshot{}, "strategy_run_snapshot"},
		{CandidateSnapshot{}, "strategy_candidate_snapshot"},
		{RuleSnapshot{}, "strategy_rule_snapshot"},
		{OrderEvent{}, "strategy_order_event"},
		{BacktestRun{}, "strategy_backtest_run"},
		{Trade{}, "strategy_backtest_trade"},
		{Metric{}, "strategy_backtest_metric"},
		{SecurityMasterHistory{}, "security_master_history"},
		{CorporateActionEvent{}, "corporate_action_event"},
	}
	for _, tt := range tests {
		if got := tt.model.TableName(); got != tt.want {
			t.Errorf("table name = %q, want %q", got, tt.want)
		}
	}
}

func TestStrategyPersistenceModelsAutoMigrate(t *testing.T) {
	dsn := fmt.Sprintf("file:strategy-models-%s?mode=memory&cache=shared", t.Name())
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(StrategyPersistenceModels()...); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	tables := []string{
		"strategy_run_snapshot",
		"strategy_candidate_snapshot",
		"strategy_rule_snapshot",
		"strategy_order_event",
		"strategy_backtest_run",
		"strategy_backtest_trade",
		"strategy_backtest_metric",
		"security_master_history",
		"corporate_action_event",
	}
	for _, table := range tables {
		if !database.Migrator().HasTable(table) {
			t.Errorf("missing table %s", table)
		}
	}

	indexes := []struct {
		model any
		name  string
	}{
		{&StrategyRunSnapshot{}, "idx_strategy_run_version_date"},
		{&CandidateSnapshot{}, "idx_strategy_candidate_run_symbol"},
		{&RuleSnapshot{}, "idx_strategy_rule_run_symbol_path"},
		{&OrderEvent{}, "idx_strategy_order_run_rule_sequence"},
		{&BacktestRun{}, "idx_strategy_backtest_version_range"},
		{&Trade{}, "idx_strategy_backtest_trade_seq"},
		{&Metric{}, "idx_strategy_backtest_metric_key"},
		{&SecurityMasterHistory{}, "idx_security_master_run_symbol_effective"},
		{&CorporateActionEvent{}, "idx_corporate_action_version_date"},
	}
	for _, index := range indexes {
		if !database.Migrator().HasIndex(index.model, index.name) {
			t.Errorf("missing index %s", index.name)
		}
	}
}
