package migrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/research"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	fixedCapitalAnalysisRunID = "4bf3e4d9-959a-48c6-95b7-56104167c6cd"
	fixedCapitalDecisionType  = "1.7.7 历史重复持仓补买纠正"
	fixedCapitalStockCode     = "sh601318"
	fixedCapitalStockName     = "中国平安"
	fixedCapitalMarketPrice   = 55.17
	fixedCapitalClosePrice    = 54.93
)

var (
	fixedCapitalSignalAt = time.Date(2026, 8, 24, 12, 15, 5, 857285700, time.FixedZone("Asia/Shanghai", 8*60*60))
	fixedCapitalTradeAt  = time.Date(2026, 8, 24, 13, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	fixedCapitalQuoteAt  = time.Date(2026, 8, 24, 13, 1, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	fixedCapitalCloseAt  = time.Date(2026, 8, 24, 15, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
)

func fixedCapitalID(kind string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("go-stock-1.7.7-"+kind)).String()
}

func applyResearchFixedCapitalAndHistoricalBuy(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := rebaseResearchAccountToFixedCapital(tx); err != nil {
		return err
	}
	if err := restoreFixedCapitalHistoricalBuy(tx); err != nil {
		return err
	}
	return verifyMainSchema12Runtime(tx)
}

func rebaseResearchAccountToFixedCapital(tx *gorm.DB) error {
	var account research.SimulatedAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, 1).Error; err != nil {
		return fmt.Errorf("load simulated account for fixed-capital migration: %w", err)
	}
	var flows []research.AccountCashFlow
	if err := tx.Order("sequence ASC, id ASC").Find(&flows).Error; err != nil {
		return fmt.Errorf("load contribution ledger: %w", err)
	}
	if len(flows) == 0 {
		return errors.New("fixed-capital migration requires the initial contribution ledger")
	}
	initialIndex := -1
	contribution := 0.0
	for index, flow := range flows {
		switch flow.Type {
		case "initial_deposit":
			if flow.Sequence != 0 || initialIndex >= 0 {
				return errors.New("fixed-capital migration found an invalid initial contribution ledger")
			}
			initialIndex = index
		case "scheduled_deposit":
			if flow.Sequence <= 0 {
				return errors.New("fixed-capital migration found an invalid scheduled contribution sequence")
			}
		default:
			return fmt.Errorf("fixed-capital migration found unsupported contribution type %q", flow.Type)
		}
		if flow.Amount <= 0 {
			return errors.New("fixed-capital migration found a non-positive contribution")
		}
		contribution += flow.Amount
	}
	if initialIndex < 0 || contribution > research.InitialCash+1e-6 {
		return fmt.Errorf("fixed-capital migration cannot rebase contribution %.6f", contribution)
	}
	gap := research.InitialCash - contribution
	if account.InitialCash != research.LegacyInitialCash && account.InitialCash != research.InitialCash {
		return fmt.Errorf("fixed-capital migration found unexpected initial cash %.6f", account.InitialCash)
	}

	if err := tx.Model(&research.SimulatedAccount{}).Where("id = ?", account.ID).Updates(map[string]any{
		"initial_cash": research.InitialCash,
		"cash":         gorm.Expr("cash + ?", gap),
	}).Error; err != nil {
		return fmt.Errorf("rebase simulated account cash: %w", err)
	}
	initial := flows[initialIndex]
	if err := tx.Model(&research.AccountCashFlow{}).Where("id = ?", initial.ID).Updates(map[string]any{
		"amount": research.InitialCash, "net_asset_value_before": 0, "net_asset_value_after": research.InitialCash,
		"unit_value_before": 1, "units_issued": research.InitialCash,
	}).Error; err != nil {
		return fmt.Errorf("consolidate initial contribution: %w", err)
	}
	if err := tx.Where("type = ?", "scheduled_deposit").Delete(&research.AccountCashFlow{}).Error; err != nil {
		return fmt.Errorf("remove scheduled contributions: %w", err)
	}
	if err := tx.Where("snapshot_type IN ?", []string{"pre_deposit", "post_deposit"}).Delete(&research.AccountValuationSnapshot{}).Error; err != nil {
		return fmt.Errorf("remove scheduled-contribution snapshots: %w", err)
	}
	var snapshots []research.AccountValuationSnapshot
	if err := tx.Order("valued_at ASC, id ASC").Find(&snapshots).Error; err != nil {
		return fmt.Errorf("load account snapshots for fixed-capital rebase: %w", err)
	}
	for _, snapshot := range snapshots {
		if snapshot.CumulativeNetContribution <= 0 || snapshot.CumulativeNetContribution > research.InitialCash+1e-6 {
			return fmt.Errorf("snapshot %s has invalid contribution %.6f", snapshot.SnapshotID, snapshot.CumulativeNetContribution)
		}
		snapshotGap := research.InitialCash - snapshot.CumulativeNetContribution
		cash := snapshot.Cash + snapshotGap
		nav := snapshot.NetAssetValue + snapshotGap
		unit := nav / research.InitialCash
		if err := tx.Model(&research.AccountValuationSnapshot{}).Where("id = ?", snapshot.ID).Updates(map[string]any{
			"cash": cash, "net_asset_value": nav, "cumulative_net_contribution": research.InitialCash,
			"unit_value": unit, "time_weighted_return": unit - 1,
		}).Error; err != nil {
			return fmt.Errorf("rebase account snapshot %s: %w", snapshot.SnapshotID, err)
		}
	}
	plan := research.FundingPlan{ID: 1, InitialContribution: research.InitialCash, TargetContribution: research.InitialCash}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&plan).Error; err != nil {
		return fmt.Errorf("ensure frozen funding compatibility row: %w", err)
	}
	if err := tx.Model(&research.FundingPlan{}).Where("id = ?", 1).Updates(map[string]any{
		"initial_contribution": research.InitialCash, "target_contribution": research.InitialCash,
		"deposit_amount": 0, "planned_deposits": 0, "completed_deposits": 0,
		"start_after_trading_date": "", "last_deposit_trading_date": "",
	}).Error; err != nil {
		return fmt.Errorf("freeze retired funding plan: %w", err)
	}
	return nil
}

func restoreFixedCapitalHistoricalBuy(tx *gorm.DB) error {
	var runCount int64
	if err := tx.Model(&research.AnalysisRun{}).Where("run_id = ?", fixedCapitalAnalysisRunID).Count(&runCount).Error; err != nil {
		return err
	}
	if runCount == 0 {
		return nil
	}
	if runCount != 1 {
		return errors.New("approved historical analysis run is not unique")
	}
	recommendationID := fixedCapitalID("china-ping-an-recommendation")
	tradeID := fixedCapitalID("china-ping-an-buy-trade")
	eventID := fixedCapitalID("china-ping-an-correction-event")
	var recommendationCount, tradeCount, positionCount, eventCount, messageCount int64
	checks := []struct {
		model any
		query string
		args  []any
		count *int64
	}{
		{&research.Recommendation{}, "recommendation_id = ?", []any{recommendationID}, &recommendationCount},
		{&research.SimulatedTrade{}, "trade_id = ?", []any{tradeID}, &tradeCount},
		{&research.Position{}, "recommendation_id = ?", []any{recommendationID}, &positionCount},
		{&research.DecisionEvent{}, "event_id = ?", []any{eventID}, &eventCount},
		{&research.LifecycleMessage{}, "recommendation_id = ?", []any{recommendationID}, &messageCount},
	}
	for _, check := range checks {
		if err := tx.Model(check.model).Where(check.query, check.args...).Count(check.count).Error; err != nil {
			return err
		}
	}
	if recommendationCount == 1 && tradeCount == 1 && positionCount == 1 && eventCount == 1 && messageCount == 3 {
		return verifyFixedCapitalHistoricalBuy(tx)
	}
	if recommendationCount+tradeCount+positionCount+eventCount+messageCount != 0 {
		return errors.New("1.7.7 historical buy correction is partially applied")
	}

	var run research.AnalysisRun
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("run_id = ?", fixedCapitalAnalysisRunID).First(&run).Error; err != nil {
		return err
	}
	if run.Status != "no_recommendation" || run.RecommendationCount != 0 || run.CompletedAt == nil || !run.CompletedAt.Equal(fixedCapitalSignalAt) ||
		!strings.Contains(run.FinalReport, fixedCapitalStockName) || !strings.Contains(run.FinalReport, "| 股票名称 | 股票代码 | AI分析摘要 | 主要风险 | 来源编号 |") {
		return errors.New("approved 2026-08-24 analysis run does not match its immutable report evidence")
	}
	var linkedRecommendations int64
	if err := tx.Model(&research.Recommendation{}).Where("analysis_run_id = ?", run.RunID).Count(&linkedRecommendations).Error; err != nil {
		return err
	}
	if linkedRecommendations != 0 {
		return errors.New("approved historical analysis run already has unrelated recommendations")
	}
	quantity, cost, err := research.SizeBuy(fixedCapitalStockCode, fixedCapitalMarketPrice, research.InitialCash)
	if err != nil {
		return err
	}
	if quantity != 1000 || math.Abs(cost.ExecutionPrice-55.22517) > 1e-8 || math.Abs(-cost.NetCashFlow-55242.2898027) > 1e-6 {
		return errors.New("1.7.7 historical buy cost constants no longer match the approved correction")
	}
	var account research.SimulatedAccount
	if err := tx.First(&account, 1).Error; err != nil {
		return err
	}
	if account.Cash+1e-8 < -cost.NetCashFlow {
		return research.ErrInsufficientCash
	}
	var closeSnapshot research.AccountValuationSnapshot
	if err := tx.Where("snapshot_id = ?", "daily-close-2026-08-24").First(&closeSnapshot).Error; err != nil {
		return errors.New("2026-08-24 daily close snapshot is required for the approved historical buy")
	}
	nextCheckAt := fixedCapitalNextCheckAt(tx)
	markPrice, markAt := fixedCapitalCurrentMark(tx)
	summary := "金融防御属性突出，板块资金改善，个股资金连续流入，并在弱市中保持相对强势"
	risk := "日内涨幅和换手放大后存在获利回吐风险；财务与估值数据不完整"
	refs := "S067,S068,S069,S070,S071,S072,S073,S074,S075"
	recommendation := research.Recommendation{
		RecommendationID: recommendationID, AnalysisRunID: run.RunID, StockCode: fixedCapitalStockCode, StockName: fixedCapitalStockName,
		SignalAt: fixedCapitalSignalAt, AISummary: summary, MainRisk: risk, SourceRefs: refs, Status: "active",
		NextCheckAt: &nextCheckAt, ActivatedAt: &fixedCapitalTradeAt, ActivationPrice: cost.ExecutionPrice, Quantity: quantity,
		TotalFees: cost.TotalFees, LastDecision: fixedCapitalDecisionType, LastDecisionAt: &fixedCapitalTradeAt,
		CreatedAt: fixedCapitalSignalAt, UpdatedAt: fixedCapitalTradeAt,
	}
	if err := tx.Create(&recommendation).Error; err != nil {
		return err
	}
	position := research.Position{RecommendationID: recommendationID, StockCode: fixedCapitalStockCode, StockName: fixedCapitalStockName, Market: "SH",
		Quantity: quantity, EntryAt: fixedCapitalTradeAt, EntryPrice: cost.ExecutionPrice, BuyFees: cost.TotalFees,
		CurrentPrice: markPrice, CurrentPriceAt: &markAt, Status: "open", CreatedAt: fixedCapitalTradeAt, UpdatedAt: markAt}
	if err := tx.Create(&position).Error; err != nil {
		return err
	}
	trade := research.SimulatedTrade{TradeID: tradeID, RecommendationID: recommendationID, StockCode: fixedCapitalStockCode, Side: "buy", TradedAt: fixedCapitalTradeAt,
		MarketPrice: fixedCapitalMarketPrice, ExecutionPrice: cost.ExecutionPrice, Quantity: quantity, Notional: cost.Notional,
		Commission: cost.Commission, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, TotalFees: cost.TotalFees,
		NetCashFlow: cost.NetCashFlow, CreatedAt: fixedCapitalTradeAt}
	if err := tx.Create(&trade).Error; err != nil {
		return err
	}
	row := fmt.Sprintf("| %s | %s | %s | %s | %s |", fixedCapitalStockName, fixedCapitalStockCode, summary, risk, refs)
	messages := []research.LifecycleMessage{
		{RecommendationID: recommendationID, Sequence: 1, Role: "system", Phase: "initial", Content: "该推荐独立于同股既有仓位，按报告完成时点建立信号并单独管理后续卖出。", Model: run.ModelName, CreatedAt: fixedCapitalSignalAt},
		{RecommendationID: recommendationID, Sequence: 2, Role: "assistant", Phase: "initial", Content: row, Model: run.ModelName, CreatedAt: fixedCapitalSignalAt},
		{RecommendationID: recommendationID, Sequence: 3, Role: "system", Phase: "holding", Content: "1.7.7 历史纠正：报告于午休期间完成，模拟成交顺延至 13:00；价格证据为腾讯 13:01 首分钟K线开盘价 55.17 元。", CreatedAt: fixedCapitalTradeAt},
	}
	if err := tx.Create(&messages).Error; err != nil {
		return err
	}
	quoteAt := fixedCapitalQuoteAt
	sourceRefs, _ := json.Marshal([]string{"S068", "S070", "tencent:m1:202608241301:open=55.17"})
	event := research.DecisionEvent{EventID: eventID, RecommendationID: recommendationID, DecisionType: fixedCapitalDecisionType,
		DecidedAt: fixedCapitalTradeAt, Reason: "移除重复股票限制后，按原报告完成时间和下一交易时段开盘证据补记独立模拟买入。",
		QuotePrice: fixedCapitalMarketPrice, QuoteAt: &quoteAt, SourceRefs: string(sourceRefs), DataStatus: "complete", CreatedAt: fixedCapitalTradeAt}
	if err := tx.Create(&event).Error; err != nil {
		return err
	}
	result := tx.Model(&research.SimulatedAccount{}).Where("id = ? AND cash >= ?", 1, -cost.NetCashFlow).
		Update("cash", gorm.Expr("cash - ?", -cost.NetCashFlow))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return research.ErrInsufficientCash
	}
	closeValue := research.CalculateSellCost(fixedCapitalClosePrice, quantity).NetCashFlow
	closeCash := closeSnapshot.Cash + cost.NetCashFlow
	closePositionValue := closeSnapshot.PositionValue + closeValue
	closeNAV := closeCash + closePositionValue
	closeUnit := closeNAV / research.InitialCash
	if err := tx.Model(&research.AccountValuationSnapshot{}).Where("id = ?", closeSnapshot.ID).Updates(map[string]any{
		"cash": closeCash, "position_value": closePositionValue, "net_asset_value": closeNAV,
		"cumulative_net_contribution": research.InitialCash, "unit_value": closeUnit, "time_weighted_return": closeUnit - 1,
	}).Error; err != nil {
		return err
	}
	finalReport, err := fixedCapitalRestoreReportRow(run.FinalReport, row)
	if err != nil {
		return err
	}
	if err := tx.Model(&research.AnalysisRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"status": "success", "recommendation_count": 1, "failure_reason": "", "final_report": finalReport,
	}).Error; err != nil {
		return err
	}
	return verifyFixedCapitalHistoricalBuy(tx)
}

func fixedCapitalNextCheckAt(tx *gorm.DB) time.Time {
	hour, minute := 9, 50
	var setting models.Settings
	if err := tx.Order("id ASC").First(&setting).Error; err == nil {
		if parsed, parseErr := time.Parse("15:04", strings.TrimSpace(setting.AIReviewStartTime)); parseErr == nil {
			hour, minute = parsed.Hour(), parsed.Minute()
		}
	}
	return time.Date(2026, 8, 25, hour, minute, 0, 0, fixedCapitalTradeAt.Location())
}

func fixedCapitalCurrentMark(tx *gorm.DB) (float64, time.Time) {
	var existing research.Position
	if err := tx.Where("stock_code = ? AND status = ? AND current_price > 0 AND current_price_at IS NOT NULL", fixedCapitalStockCode, "open").
		Order("current_price_at DESC, id DESC").First(&existing).Error; err == nil && existing.CurrentPriceAt != nil {
		return existing.CurrentPrice, *existing.CurrentPriceAt
	}
	return fixedCapitalClosePrice, fixedCapitalCloseAt
}

func fixedCapitalRestoreReportRow(report, row string) (string, error) {
	const header = "| 股票名称 | 股票代码 | AI分析摘要 | 主要风险 | 来源编号 |"
	lines := strings.Split(strings.TrimSpace(report), "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != header {
			continue
		}
		if index+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[index+1]), "|---") {
			return "", errors.New("approved historical report table separator is invalid")
		}
		return strings.Join(append(lines[:index+2], row), "\n"), nil
	}
	return "", errors.New("approved historical report table header is missing")
}

func verifyFixedCapitalHistoricalBuy(tx *gorm.DB) error {
	recommendationID := fixedCapitalID("china-ping-an-recommendation")
	var recommendation research.Recommendation
	if err := tx.Where("recommendation_id = ?", recommendationID).First(&recommendation).Error; err != nil {
		return err
	}
	lifecycleValid := recommendation.Status == "active" && recommendation.ClosedAt == nil
	if recommendation.Status == "closed" && recommendation.ActivatedAt != nil && recommendation.ClosedAt != nil {
		lifecycleValid = !recommendation.ClosedAt.Before(*recommendation.ActivatedAt)
	}
	if recommendation.AnalysisRunID != fixedCapitalAnalysisRunID || recommendation.StockCode != fixedCapitalStockCode || !lifecycleValid ||
		recommendation.Quantity != 1000 || recommendation.ActivatedAt == nil || !recommendation.SignalAt.Equal(fixedCapitalSignalAt) || !recommendation.ActivatedAt.Equal(fixedCapitalTradeAt) {
		return errors.New("1.7.7 historical recommendation verification failed")
	}
	var trade research.SimulatedTrade
	if err := tx.Where("trade_id = ?", fixedCapitalID("china-ping-an-buy-trade")).First(&trade).Error; err != nil {
		return err
	}
	if trade.Quantity != 1000 || !trade.TradedAt.Equal(fixedCapitalTradeAt) || math.Abs(trade.MarketPrice-fixedCapitalMarketPrice) > 1e-8 || math.Abs(trade.ExecutionPrice-55.22517) > 1e-8 {
		return errors.New("1.7.7 historical trade verification failed")
	}
	return nil
}

func verifyMainSchema12Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	var account research.SimulatedAccount
	if err := database.First(&account, 1).Error; err != nil {
		return err
	}
	if math.Abs(account.InitialCash-research.InitialCash) > 1e-8 {
		return fmt.Errorf("main schema 12 initial cash is %.6f, expected %.6f", account.InitialCash, research.InitialCash)
	}
	var flows []research.AccountCashFlow
	if err := database.Order("sequence ASC").Find(&flows).Error; err != nil {
		return err
	}
	if len(flows) != 1 || flows[0].Sequence != 0 || flows[0].Type != "initial_deposit" || math.Abs(flows[0].Amount-research.InitialCash) > 1e-8 {
		return fmt.Errorf("main schema 12 contribution ledger is invalid: %+v", flows)
	}
	var plan research.FundingPlan
	if err := database.First(&plan, 1).Error; err != nil {
		return err
	}
	if plan.InitialContribution != research.InitialCash || plan.TargetContribution != research.InitialCash || plan.DepositAmount != 0 || plan.PlannedDeposits != 0 || plan.CompletedDeposits != 0 {
		return fmt.Errorf("main schema 12 frozen funding plan is invalid: %+v", plan)
	}
	var obsoleteSnapshots int64
	if err := database.Model(&research.AccountValuationSnapshot{}).Where("snapshot_type IN ?", []string{"pre_deposit", "post_deposit"}).Count(&obsoleteSnapshots).Error; err != nil {
		return err
	}
	if obsoleteSnapshots != 0 {
		return fmt.Errorf("main schema 12 retained %d obsolete funding snapshots", obsoleteSnapshots)
	}
	var invalidSnapshots int64
	if err := database.Model(&research.AccountValuationSnapshot{}).Where("ABS(cumulative_net_contribution - ?) > ?", research.InitialCash, 1e-6).Count(&invalidSnapshots).Error; err != nil {
		return err
	}
	if invalidSnapshots != 0 {
		return fmt.Errorf("main schema 12 has %d snapshots outside the fixed-capital basis", invalidSnapshots)
	}
	var runCount int64
	if err := database.Model(&research.AnalysisRun{}).Where("run_id = ?", fixedCapitalAnalysisRunID).Count(&runCount).Error; err != nil {
		return err
	}
	if runCount == 1 {
		return verifyFixedCapitalHistoricalBuy(database)
	}
	return nil
}
