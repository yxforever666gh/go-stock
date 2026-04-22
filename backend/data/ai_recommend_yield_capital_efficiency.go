package data

import (
	"time"

	"go-stock/backend/models"
)

const (
	moneyMarketAnnualRate = 2.5 // 货币基金年化收益率 2.5%
)

// calculateCapitalEfficiency 计算资金利用率
func calculateCapitalEfficiency(entries []yieldDailyOverviewEntry, tradingDays []time.Time) models.AiRecommendYieldCapitalEfficiency {
	result := models.AiRecommendYieldCapitalEfficiency{
		TotalDays:                    len(tradingDays),
		CapitalUtilizationText:       "--",
		AdjustedAnnualizedReturnText: "--",
	}

	if len(tradingDays) == 0 || len(entries) == 0 {
		return result
	}

	// 统计每天是否有持仓
	investedDaySet := make(map[string]bool)
	for _, day := range tradingDays {
		dayStr := day.Format("2006-01-02")
		hasPosition := false
		for _, entry := range entries {
			if day.Before(entry.BuyDay) {
				continue
			}
			if !entry.SellDay.IsZero() && day.After(entry.SellDay) {
				continue
			}
			hasPosition = true
			break
		}
		if hasPosition {
			investedDaySet[dayStr] = true
		}
	}

	result.InvestedDays = len(investedDaySet)
	result.IdleDays = result.TotalDays - result.InvestedDays

	// 计算资金利用率
	if result.TotalDays > 0 {
		result.CapitalUtilization = round2(float64(result.InvestedDays) / float64(result.TotalDays) * 100)
		result.CapitalUtilizationText = formatSignedPercent(result.CapitalUtilization)
	}

	return result
}

// calculateStrategyXirrWithIdleCash 计算包含空仓期货币基金收益的策略XIRR
func calculateStrategyXirrWithIdleCash(entries []yieldDailyOverviewEntry, moneyMarketRate float64) (float64, bool) {
	if len(entries) == 0 {
		return 0, false
	}

	if moneyMarketRate <= 0 {
		moneyMarketRate = moneyMarketAnnualRate
	}
	dailyRate := moneyMarketRate / 365.0 / 100.0

	cashflows := make([]xirrCashflow, 0, len(entries)*3)

	for i, entry := range entries {
		if entry.BuyCostNet <= 0 || entry.BuyTime.IsZero() {
			continue
		}

		// 买入现金流
		cashflows = append(cashflows, xirrCashflow{
			At:     entry.BuyTime,
			Amount: -entry.BuyCostNet,
		})

		// 卖出现金流
		endTime, endValue, ok := resolveStrategyExitCashflow(entry)
		if !ok || endValue <= 0 {
			continue
		}
		cashflows = append(cashflows, xirrCashflow{
			At:     endTime,
			Amount: endValue,
		})

		// 如果有下一笔投资，计算空仓期货币基金收益
		if i < len(entries)-1 {
			nextEntry := entries[i+1]
			if !nextEntry.BuyTime.IsZero() && nextEntry.BuyTime.After(endTime) {
				idleDays := int(nextEntry.BuyTime.Sub(endTime).Hours() / 24)
				if idleDays > 0 {
					// 空仓期按货币基金收益计算
					idleInterest := endValue * dailyRate * float64(idleDays)
					if idleInterest > 0 {
						cashflows = append(cashflows, xirrCashflow{
							At:     nextEntry.BuyTime,
							Amount: idleInterest,
						})
					}
				}
			}
		}
	}

	return calculateXirr(cashflows)
}

// calculateBenchmarkWithIdleCash 计算包含空仓期货币基金收益的基准收益
func calculateBenchmarkWithIdleCash(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeries *yieldDailyOverviewPriceSeries,
	moneyMarketRate float64,
) (float64, bool) {
	if len(entries) == 0 || len(tradingDays) == 0 || priceSeries == nil {
		return 0, false
	}

	if moneyMarketRate <= 0 {
		moneyMarketRate = moneyMarketAnnualRate
	}
	dailyRate := moneyMarketRate / 365.0 / 100.0

	positions := make([]benchmarkCashflowPosition, 0, len(entries))
	benchmarkCashflows := make([]xirrCashflow, 0, len(entries)*3)

	for i, entry := range entries {
		buyClose := priceSeries.CloseByDay[entry.BuyDay.Format("2006-01-02")]
		if buyClose <= 0 || entry.BuyCostNet <= 0 {
			continue
		}

		endDay := entry.CurrentDay
		endTime := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 15, 0, 0, 0, cnLocation())
		if !entry.SellDay.IsZero() {
			endDay = entry.SellDay
			if sellTime, ok := parseYieldOverviewDisplayTime(entry.SellTime); ok {
				endTime = sellTime
			} else {
				endTime = time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 15, 0, 0, 0, cnLocation())
			}
		}

		endPrice := priceSeries.CloseByDay[endDay.Format("2006-01-02")]
		if endPrice <= 0 {
			endPrice = buyClose
		}

		position := benchmarkCashflowPosition{
			BuyDay:        entry.BuyDay,
			EndDay:        endDay,
			EndTime:       endTime,
			InvestedNet:   entry.BuyCostNet,
			Shares:        entry.BuyCostNet / buyClose,
			SellAmount:    endPrice,
			HasSellAmount: entry.HasSellAmount,
		}
		positions = append(positions, position)

		// 买入现金流
		benchmarkCashflows = append(benchmarkCashflows, xirrCashflow{
			At:     entry.BuyTime,
			Amount: -entry.BuyCostNet,
		})

		// 卖出现金流
		benchmarkEndValue := round2(position.Shares * endPrice)
		benchmarkCashflows = append(benchmarkCashflows, xirrCashflow{
			At:     endTime,
			Amount: benchmarkEndValue,
		})

		// 如果有下一笔投资，计算空仓期货币基金收益
		if i < len(entries)-1 {
			nextEntry := entries[i+1]
			if !nextEntry.BuyTime.IsZero() && nextEntry.BuyTime.After(endTime) {
				idleDays := int(nextEntry.BuyTime.Sub(endTime).Hours() / 24)
				if idleDays > 0 {
					idleInterest := benchmarkEndValue * dailyRate * float64(idleDays)
					if idleInterest > 0 {
						benchmarkCashflows = append(benchmarkCashflows, xirrCashflow{
							At:     nextEntry.BuyTime,
							Amount: idleInterest,
						})
					}
				}
			}
		}
	}

	return calculateXirr(benchmarkCashflows)
}
