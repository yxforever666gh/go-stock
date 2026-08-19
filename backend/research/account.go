package research

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	fundingWindowHour        = 9
	fundingWindowMinute      = 20
	fundingWindowCloseMinute = 30
	dailyCloseSnapshotHour   = 15
	dailyCloseSnapshotMinute = 5
)

// ProcessScheduledFunding applies at most one scheduled contribution on an
// eligible trading day. The lifecycle serial lock makes the account update
// deterministic with buys and sells; the sequence unique key is the database
// level idempotency guard.
func (s *Service) ProcessScheduledFunding(ctx context.Context, now time.Time) (FundingProcessResult, error) {
	s.serial.Lock()
	defer s.serial.Unlock()

	local := ShanghaiTime(now)
	result := FundingProcessResult{Reason: "outside_funding_window"}
	if local.Hour() != fundingWindowHour || local.Minute() < fundingWindowMinute || local.Minute() >= fundingWindowCloseMinute {
		result.NextContributionAt = s.nextContributionAt(ctx, local)
		return result, nil
	}
	trading, err := s.calendar.IsTradingDay(ctx, local)
	if err != nil {
		return result, err
	}
	if !trading {
		result.Reason = "not_trading_day"
		result.NextContributionAt = s.nextContributionAt(ctx, local)
		return result, nil
	}
	date := local.Format("2006-01-02")
	var currentPlan FundingPlan
	if err := s.repository.db.WithContext(ctx).First(&currentPlan, 1).Error; err != nil {
		return result, err
	}
	result.CompletedDeposits = currentPlan.CompletedDeposits
	result.RemainingDeposits = maxInt(0, currentPlan.PlannedDeposits-currentPlan.CompletedDeposits)
	if currentPlan.CompletedDeposits >= currentPlan.PlannedDeposits {
		result.Reason = "funding_complete"
		return result, nil
	}
	if date <= currentPlan.StartAfterTradingDate || date == currentPlan.LastDepositTradingDate {
		result.Reason = "not_yet_eligible"
		result.NextContributionAt = s.nextContributionAt(ctx, local)
		return result, nil
	}

	// Refresh what is available, but a single quote failure must not prevent an
	// external contribution. The transaction below values missing quotes using
	// the last persisted price and records the status as partial.
	allFresh := s.refreshAccountQuotes(ctx, local, true)
	err = s.repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		var plan FundingPlan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&plan, 1).Error; err != nil {
			return err
		}
		result.CompletedDeposits = plan.CompletedDeposits
		result.RemainingDeposits = maxInt(0, plan.PlannedDeposits-plan.CompletedDeposits)
		if plan.CompletedDeposits >= plan.PlannedDeposits {
			result.Reason = "funding_complete"
			return nil
		}
		if date <= plan.StartAfterTradingDate || date == plan.LastDepositTradingDate {
			result.Reason = "not_yet_eligible"
			return nil
		}

		sequence := plan.CompletedDeposits + 1
		var existing int64
		if err := tx.Model(&AccountCashFlow{}).Where("sequence = ?", sequence).Count(&existing).Error; err != nil {
			return err
		}
		if existing != 0 {
			result.Reason = "already_applied"
			return nil
		}

		account, positionValue, valuationStatus, err := storedAccountValuation(tx)
		if err != nil {
			return err
		}
		if !allFresh && valuationStatus == "complete" {
			valuationStatus = "partial"
		}
		beforeNAV := account.Cash + positionValue
		contribution, units, err := fundingLedger(tx)
		if err != nil {
			return err
		}
		unitValue := 1.0
		if units > 0 && beforeNAV > 0 {
			unitValue = beforeNAV / units
		}
		if unitValue <= 0 || math.IsNaN(unitValue) || math.IsInf(unitValue, 0) {
			return errors.New("account unit value is invalid")
		}
		amount := plan.DepositAmount
		issued := amount / unitValue
		effectiveAt := local
		flow := AccountCashFlow{
			FlowID: newID(), Sequence: sequence, Type: "scheduled_deposit", Amount: amount,
			EffectiveAt: effectiveAt, TradingDate: date, NetAssetValueBefore: beforeNAV,
			NetAssetValueAfter: beforeNAV + amount, UnitValueBefore: unitValue, UnitsIssued: issued,
		}
		createFlow := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&flow)
		if createFlow.Error != nil {
			return createFlow.Error
		}
		if createFlow.RowsAffected == 0 {
			result.Reason = "already_applied"
			return nil
		}
		if err := tx.Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", gorm.Expr("cash + ?", amount)).Error; err != nil {
			return err
		}
		if err := tx.Model(&FundingPlan{}).Where("id = ?", plan.ID).Updates(map[string]any{
			"completed_deposits": plan.CompletedDeposits + 1, "last_deposit_trading_date": date,
		}).Error; err != nil {
			return err
		}
		newContribution, newUnits := contribution+amount, units+issued
		for _, snapshot := range []AccountValuationSnapshot{
			newAccountSnapshot("pre_deposit", date, effectiveAt, account.Cash, positionValue, beforeNAV, contribution, unitValue, valuationStatus, "pre-deposit-"+date),
			newAccountSnapshot("post_deposit", date, effectiveAt, account.Cash+amount, positionValue, beforeNAV+amount, newContribution, (beforeNAV+amount)/newUnits, valuationStatus, "post-deposit-"+date),
		} {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot).Error; err != nil {
				return err
			}
		}
		result.Applied = true
		result.Reason = "applied"
		result.CashFlow = &flow
		result.CompletedDeposits = plan.CompletedDeposits + 1
		result.RemainingDeposits = maxInt(0, plan.PlannedDeposits-result.CompletedDeposits)
		return nil
	})
	if err != nil {
		return result, err
	}
	result.NextContributionAt = s.nextContributionAt(ctx, local.Add(time.Minute))
	return result, nil
}

// ProcessScheduledSnapshot persists one close snapshot per strict trading day.
// It is safe to invoke repeatedly after 15:05; the deterministic snapshot ID
// makes retries after a transient calendar or quote failure idempotent.
func (s *Service) ProcessScheduledSnapshot(ctx context.Context, now time.Time) (bool, error) {
	s.serial.Lock()
	defer s.serial.Unlock()
	local := ShanghaiTime(now)
	if local.Hour() < dailyCloseSnapshotHour || (local.Hour() == dailyCloseSnapshotHour && local.Minute() < dailyCloseSnapshotMinute) {
		return false, nil
	}
	trading, err := s.calendar.IsTradingDay(ctx, local)
	if err != nil || !trading {
		return false, err
	}
	date := local.Format("2006-01-02")
	var existing int64
	if err := s.repository.db.WithContext(ctx).Model(&AccountValuationSnapshot{}).Where("snapshot_id = ?", "daily-close-"+date).Count(&existing).Error; err != nil {
		return false, err
	}
	if existing != 0 {
		return false, nil
	}
	allFresh := s.refreshAccountQuotes(ctx, local, false)
	applied := false
	err = s.repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		existing = 0
		if err := tx.Model(&AccountValuationSnapshot{}).Where("snapshot_id = ?", "daily-close-"+date).Count(&existing).Error; err != nil {
			return err
		}
		if existing != 0 {
			return nil
		}
		account, positionValue, status, err := storedAccountValuation(tx)
		if err != nil {
			return err
		}
		if !allFresh && status == "complete" {
			status = "partial"
		}
		contribution, units, err := fundingLedger(tx)
		if err != nil {
			return err
		}
		nav := account.Cash + positionValue
		unitValue := safeUnitValue(nav, units)
		snapshot := newAccountSnapshot("daily_close", date, local, account.Cash, positionValue, nav, contribution, unitValue, status, "daily-close-"+date)
		if err := tx.Create(&snapshot).Error; err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

func (s *Service) CashFlows(ctx context.Context) ([]AccountCashFlow, error) {
	if !s.repository.db.Migrator().HasTable(&AccountCashFlow{}) {
		return []AccountCashFlow{}, nil
	}
	var result []AccountCashFlow
	err := s.repository.db.WithContext(ctx).Order("sequence ASC, id ASC").Find(&result).Error
	return result, err
}

func (s *Service) AccountPerformance(ctx context.Context) (AccountPerformance, error) {
	// The account endpoint performs the explicit live quote refresh. Performance
	// reads the persisted valuation so opening the yield page does not issue a
	// second set of upstream quote requests for every open position.
	overview, err := s.accountOverview(ctx, false)
	if err != nil {
		return AccountPerformance{}, err
	}
	result := AccountPerformance{
		ValuedAt: overview.ValuedAt, UnitValue: overview.TimeWeightedReturn + 1,
		TimeWeightedReturn: overview.TimeWeightedReturn, CumulativeCapitalReturn: overview.CumulativeCapitalReturn,
		NetProfit: overview.NetProfit, NetAssetValue: overview.NetAssetValue,
		CumulativeNetContribution: overview.CumulativeNetContribution,
	}
	if s.repository.db.Migrator().HasTable(&AccountValuationSnapshot{}) {
		var snapshots []AccountValuationSnapshot
		if err := s.repository.db.WithContext(ctx).Order("valued_at ASC, id ASC").Find(&snapshots).Error; err != nil {
			return result, err
		}
		result.Curve = make([]AccountPerformancePoint, 0, len(snapshots)+1)
		for _, row := range snapshots {
			result.Curve = append(result.Curve, performancePoint(row))
		}
	}
	current := AccountPerformancePoint{ValuedAt: overview.ValuedAt, TradingDate: ShanghaiTime(overview.ValuedAt).Format("2006-01-02"),
		SnapshotType: "current", Cash: overview.Cash, PositionValue: overview.PositionValue, NetAssetValue: overview.NetAssetValue,
		CumulativeNetContribution: overview.CumulativeNetContribution, UnitValue: result.UnitValue,
		TimeWeightedReturn: overview.TimeWeightedReturn, ValuationStatus: "live"}
	result.Curve = appendOrReplaceCurrentPoint(result.Curve, current)
	result.Metrics, err = s.performanceMetrics(ctx, overview, result.Curve)
	return result, err
}

func (s *Service) accountOverview(ctx context.Context, refreshQuotes bool) (AccountOverview, error) {
	account, err := s.repository.Account(ctx)
	if err != nil {
		return AccountOverview{}, err
	}
	positions, err := s.repository.OpenPositions(ctx)
	if err != nil {
		return AccountOverview{}, err
	}
	value := 0.0
	now := s.now()
	if refreshQuotes && s.quotes != nil {
		// Ten holdings should not turn one account request into ten serial
		// upstream waits. Five workers match lifecycle concurrency while every
		// request remains cancellable through the caller's context.
		semaphore := make(chan struct{}, 5)
		var wait sync.WaitGroup
		for index := range positions {
			index := index
			wait.Add(1)
			go func() {
				defer wait.Done()
				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
				case <-ctx.Done():
					return
				}
				quote, quoteErr := s.quotes.CurrentQuote(ctx, positions[index].StockCode)
				if quoteErr == nil && quote.Price > 0 {
					positions[index].CurrentPrice, positions[index].CurrentPriceAt = quote.Price, &quote.At
					_ = s.repository.UpdatePositionQuote(ctx, positions[index].ID, quote)
				}
			}()
		}
		wait.Wait()
	}
	for index := range positions {
		if positions[index].CurrentPrice <= 0 {
			positions[index].CurrentPrice = positions[index].EntryPrice
		}
		enrichPositionValue(&positions[index])
		value += positions[index].NetSellValue
	}
	nav := account.Cash + value
	contribution, units := account.InitialCash, account.InitialCash
	plan := FundingPlan{InitialContribution: account.InitialCash, TargetContribution: TargetContribution, DepositAmount: ScheduledDepositAmount, PlannedDeposits: ScheduledDepositCount}
	if s.repository.db.Migrator().HasTable(&AccountCashFlow{}) {
		if contribution, units, err = fundingLedger(s.repository.db.WithContext(ctx)); err != nil {
			return AccountOverview{}, err
		}
	}
	if s.repository.db.Migrator().HasTable(&FundingPlan{}) {
		if err := s.repository.db.WithContext(ctx).First(&plan, 1).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return AccountOverview{}, err
		}
	}
	unitValue := safeUnitValue(nav, units)
	twr := unitValue - 1
	netProfit := nav - contribution
	capitalReturn := safeRate(netProfit, contribution)
	var pending int64
	if err := s.repository.db.WithContext(ctx).Model(&Recommendation{}).Where("status IN ?", []string{"buy_pending", "pending"}).Count(&pending).Error; err != nil {
		return AccountOverview{}, err
	}
	remainingPositions := MaxPortfolioExposures - len(positions) - int(pending)
	if remainingPositions < 0 {
		remainingPositions = 0
	}
	remainingDeposits := maxInt(0, plan.PlannedDeposits-plan.CompletedDeposits)
	return AccountOverview{
		InitialCash: account.InitialCash, Cash: account.Cash, PositionValue: value, NetAssetValue: nav,
		CumulativeNetContribution: contribution, TargetContribution: plan.TargetContribution, DepositAmount: plan.DepositAmount,
		PlannedDeposits: plan.PlannedDeposits, CompletedDeposits: plan.CompletedDeposits, RemainingDeposits: remainingDeposits,
		NextContributionAt: s.nextContributionAt(ctx, now), CurrentPositions: len(positions), PendingBuys: int(pending),
		MaxPositions: MaxPortfolioExposures, RemainingPositions: remainingPositions,
		NetProfit: netProfit, NetYieldRate: twr, TimeWeightedReturn: twr, CumulativeCapitalReturn: capitalReturn,
		ValuedAt: now, Positions: positions,
	}, nil
}

func (s *Service) nextContributionAt(ctx context.Context, after time.Time) *time.Time {
	if !s.repository.db.Migrator().HasTable(&FundingPlan{}) {
		return nil
	}
	var plan FundingPlan
	if err := s.repository.db.WithContext(ctx).First(&plan, 1).Error; err != nil || plan.CompletedDeposits >= plan.PlannedDeposits {
		return nil
	}
	local := ShanghaiTime(after)
	for offset := 0; offset < 370; offset++ {
		day := time.Date(local.Year(), local.Month(), local.Day(), fundingWindowHour, fundingWindowMinute, 0, 0, local.Location()).AddDate(0, 0, offset)
		date := day.Format("2006-01-02")
		if date <= plan.StartAfterTradingDate || date == plan.LastDepositTradingDate {
			continue
		}
		trading, err := s.calendar.IsTradingDay(ctx, day)
		if err != nil {
			return nil
		}
		if !trading {
			continue
		}
		if offset == 0 && local.Hour() == fundingWindowHour && local.Minute() >= fundingWindowMinute && local.Minute() < fundingWindowCloseMinute {
			retry := local.Truncate(time.Minute).Add(time.Minute)
			windowClose := time.Date(local.Year(), local.Month(), local.Day(), fundingWindowHour, fundingWindowCloseMinute, 0, 0, local.Location())
			if retry.Before(windowClose) {
				return &retry
			}
		}
		if day.After(local) {
			return &day
		}
	}
	return nil
}

// refreshAccountQuotes updates persisted marks only when their timestamps can
// support a durable TWR valuation. A 09:20 contribution accepts either a
// current pre-open quote or the immediately preceding trading day's close;
// the daily close snapshot requires a same-day closing-session quote.
func (s *Service) refreshAccountQuotes(ctx context.Context, valuedAt time.Time, allowPreviousClose bool) bool {
	positions, err := s.repository.OpenPositions(ctx)
	if err != nil {
		return false
	}
	allFresh := true
	for _, position := range positions {
		if s.quotes == nil {
			allFresh = false
			continue
		}
		quote, quoteErr := s.quotes.CurrentQuote(ctx, position.StockCode)
		if quoteErr != nil || quote.Price <= 0 || !s.validValuationQuote(ctx, quote.At, valuedAt, allowPreviousClose) {
			allFresh = false
			continue
		}
		if err := s.repository.UpdatePositionQuote(ctx, position.ID, quote); err != nil {
			allFresh = false
		}
	}
	return allFresh
}

func (s *Service) validValuationQuote(ctx context.Context, quoteAt, valuedAt time.Time, allowPreviousClose bool) bool {
	if quoteAt.IsZero() {
		return false
	}
	quoteLocal, valueLocal := ShanghaiTime(quoteAt), ShanghaiTime(valuedAt)
	if quoteLocal.After(valueLocal.Add(5 * time.Minute)) {
		return false
	}
	quoteDate, valueDate := quoteLocal.Format("2006-01-02"), valueLocal.Format("2006-01-02")
	if quoteDate == valueDate {
		if allowPreviousClose {
			return valueLocal.Sub(quoteLocal) <= 45*time.Minute
		}
		closeWindow := time.Date(valueLocal.Year(), valueLocal.Month(), valueLocal.Day(), 14, 30, 0, 0, valueLocal.Location())
		return !quoteLocal.Before(closeWindow)
	}
	previousCloseWindow := time.Date(quoteLocal.Year(), quoteLocal.Month(), quoteLocal.Day(), 14, 30, 0, 0, quoteLocal.Location())
	if !allowPreviousClose || quoteLocal.Before(previousCloseWindow) {
		return false
	}
	for offset := 1; offset <= 15; offset++ {
		candidate := valueLocal.AddDate(0, 0, -offset)
		trading, err := s.calendar.IsTradingDay(ctx, candidate)
		if err != nil {
			return false
		}
		if trading {
			return quoteDate == candidate.Format("2006-01-02")
		}
	}
	return false
}

func storedAccountValuation(tx *gorm.DB) (SimulatedAccount, float64, string, error) {
	var account SimulatedAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, 1).Error; err != nil {
		return account, 0, "failed", err
	}
	var positions []Position
	if err := tx.Where("status = ?", "open").Find(&positions).Error; err != nil {
		return account, 0, "failed", err
	}
	status := "complete"
	value := 0.0
	for _, position := range positions {
		price := position.CurrentPrice
		if price <= 0 {
			price = position.EntryPrice
			status = "partial"
		}
		if price <= 0 || position.Quantity <= 0 {
			status = "partial"
			continue
		}
		value += CalculateSellCost(price, position.Quantity).NetCashFlow
	}
	return account, value, status, nil
}

func fundingLedger(tx *gorm.DB) (float64, float64, error) {
	var row struct{ Contribution, Units float64 }
	err := tx.Model(&AccountCashFlow{}).Select("COALESCE(SUM(amount), 0) AS contribution, COALESCE(SUM(units_issued), 0) AS units").Scan(&row).Error
	return row.Contribution, row.Units, err
}

func newAccountSnapshot(kind, date string, valuedAt time.Time, cash, positionValue, nav, contribution, unitValue float64, status, id string) AccountValuationSnapshot {
	return AccountValuationSnapshot{SnapshotID: id, SnapshotType: kind, TradingDate: date, ValuedAt: valuedAt,
		Cash: cash, PositionValue: positionValue, NetAssetValue: nav, CumulativeNetContribution: contribution,
		UnitValue: unitValue, TimeWeightedReturn: unitValue - 1, ValuationStatus: status}
}

func performancePoint(row AccountValuationSnapshot) AccountPerformancePoint {
	return AccountPerformancePoint{ValuedAt: row.ValuedAt, TradingDate: row.TradingDate, SnapshotType: row.SnapshotType,
		Cash: row.Cash, PositionValue: row.PositionValue, NetAssetValue: row.NetAssetValue,
		CumulativeNetContribution: row.CumulativeNetContribution, UnitValue: row.UnitValue,
		TimeWeightedReturn: row.TimeWeightedReturn, ValuationStatus: row.ValuationStatus}
}

func appendOrReplaceCurrentPoint(points []AccountPerformancePoint, current AccountPerformancePoint) []AccountPerformancePoint {
	if len(points) > 0 && points[len(points)-1].SnapshotType == "current" {
		points[len(points)-1] = current
		return points
	}
	return append(points, current)
}

func (s *Service) performanceMetrics(ctx context.Context, overview AccountOverview, curve []AccountPerformancePoint) (StrategyPerformanceMetrics, error) {
	metrics := StrategyPerformanceMetrics{IndustryConcentrationStatus: "unavailable_no_structured_industry_data", SampleLevel: "样本不足"}
	var closed []Position
	if err := s.repository.db.WithContext(ctx).Where("status = ?", "closed").Order("exit_at ASC, id ASC").Find(&closed).Error; err != nil {
		return metrics, err
	}
	metrics.ClosedTrades = len(closed)
	if len(closed) >= 100 {
		metrics.SampleLevel = "可进行阶段性评价"
	} else if len(closed) >= 30 {
		metrics.SampleLevel = "初步观察"
	}
	var gains, losses []float64
	totalHoldingMinutes := 0.0
	for _, position := range closed {
		invested := position.EntryPrice*float64(position.Quantity) + position.BuyFees
		rate := safeRate(position.NetPnL, invested)
		if position.NetPnL > 0 {
			gains = append(gains, rate)
		} else if position.NetPnL < 0 {
			losses = append(losses, rate)
		}
		if position.ExitAt != nil && position.ExitAt.After(position.EntryAt) {
			totalHoldingMinutes += position.ExitAt.Sub(position.EntryAt).Minutes()
		}
	}
	metrics.WinRate = safeRate(float64(len(gains)), float64(len(closed)))
	metrics.AverageGainRate = average(gains)
	metrics.AverageLossRate = average(losses)
	if metrics.AverageLossRate < 0 {
		metrics.PayoffRatio = metrics.AverageGainRate / math.Abs(metrics.AverageLossRate)
	}
	metrics.AverageHoldingMinutes = safeRate(totalHoldingMinutes, float64(len(closed)))
	metrics.MaxDrawdown = maximumDrawdown(curve)
	var tradeTotals struct{ Fees, Notional float64 }
	if err := s.repository.db.WithContext(ctx).Model(&SimulatedTrade{}).
		Select("COALESCE(SUM(total_fees), 0) AS fees, COALESCE(SUM(notional), 0) AS notional").Scan(&tradeTotals).Error; err != nil {
		return metrics, err
	}
	metrics.TotalFees = tradeTotals.Fees
	averageNAV := overview.NetAssetValue
	if len(curve) > 0 {
		values := make([]float64, 0, len(curve))
		for _, point := range curve {
			if point.NetAssetValue > 0 {
				values = append(values, point.NetAssetValue)
			}
		}
		if len(values) > 0 {
			averageNAV = average(values)
		}
	}
	metrics.TurnoverRate = safeRate(tradeTotals.Notional, averageNAV)
	metrics.CapitalUtilization = timeWeightedUtilization(curve, safeRate(overview.PositionValue, overview.NetAssetValue))
	var missed, executed int64
	if err := s.repository.db.WithContext(ctx).Model(&Recommendation{}).
		Where("status IN ?", []string{"missed_cash", "missed_untradable"}).Count(&missed).Error; err != nil {
		return metrics, err
	}
	if err := s.repository.db.WithContext(ctx).Model(&Recommendation{}).
		Where("status IN ?", []string{"active", "sell_pending", "closed"}).Count(&executed).Error; err != nil {
		return metrics, err
	}
	metrics.MissedExecutionRate = safeRate(float64(missed), float64(missed+executed))
	return metrics, nil
}

func timeWeightedUtilization(curve []AccountPerformancePoint, fallback float64) float64 {
	weighted, duration := 0.0, 0.0
	for index := 0; index+1 < len(curve); index++ {
		seconds := curve[index+1].ValuedAt.Sub(curve[index].ValuedAt).Seconds()
		if seconds <= 0 || curve[index].NetAssetValue <= 0 {
			continue
		}
		weighted += safeRate(curve[index].PositionValue, curve[index].NetAssetValue) * seconds
		duration += seconds
	}
	if duration > 0 {
		return weighted / duration
	}
	values := make([]float64, 0, len(curve))
	for _, point := range curve {
		if point.NetAssetValue > 0 {
			values = append(values, safeRate(point.PositionValue, point.NetAssetValue))
		}
	}
	if len(values) > 0 {
		return average(values)
	}
	return fallback
}

func maximumDrawdown(curve []AccountPerformancePoint) float64 {
	peak, maxDrawdown := 0.0, 0.0
	for _, point := range curve {
		if point.UnitValue <= 0 {
			continue
		}
		if point.UnitValue > peak {
			peak = point.UnitValue
		}
		if peak > 0 {
			drawdown := (peak - point.UnitValue) / peak
			if drawdown > maxDrawdown {
				maxDrawdown = drawdown
			}
		}
	}
	return maxDrawdown
}

func safeUnitValue(nav, units float64) float64 {
	if units <= 0 {
		return 1
	}
	return nav / units
}

func safeRate(numerator, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
