package data

import (
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"strings"
	"time"
)

const (
	v132MinRewardRiskRatio  = 1.3
	v132MaxDownsideRiskPct  = 5.0
	v132CooldownTradeDays   = 5
	v136HardRewardRiskRatio = 0.8
	v136VWAPConfirmMinBars  = 20
)

type v132ActivationGateResult struct {
	Allowed bool
	Reason  string
	Kind    string
}

func isV132Recommend(rec models.AiRecommendStocks) bool {
	return strings.TrimSpace(rec.SummaryVersion) == marketSummaryVersionV132
}

func isV136Recommend(rec models.AiRecommendStocks) bool {
	return strings.TrimSpace(rec.SummaryVersion) == marketSummaryVersion136
}

func evaluateV132ActivationGate(rec models.AiRecommendStocks, activationTime time.Time, activationPrice float64, bars []minuteBar) v132ActivationGateResult {
	if isV136Recommend(rec) {
		return evaluateV136ActivationGate(rec, activationTime, activationPrice, bars)
	}
	if !isV132Recommend(rec) {
		return v132ActivationGateResult{Allowed: true}
	}
	if activationTime.IsZero() || activationPrice <= 0 {
		return v132ActivationGateResult{Allowed: true}
	}
	if ok, reason := passesV132RewardRiskGate(rec, activationPrice); !ok {
		return v132ActivationGateResult{Allowed: false, Kind: "reward_risk", Reason: reason}
	}
	if ok, reason := passesV132StrengthGate(rec, activationTime, activationPrice, bars); !ok {
		return v132ActivationGateResult{Allowed: false, Kind: "strength", Reason: reason}
	}
	if ok, reason := passesV132CooldownGate(rec, activationTime); !ok {
		return v132ActivationGateResult{Allowed: false, Kind: "cooldown", Reason: reason}
	}
	return v132ActivationGateResult{Allowed: true}
}

func evaluateV136ActivationGate(rec models.AiRecommendStocks, activationTime time.Time, activationPrice float64, bars []minuteBar) v132ActivationGateResult {
	if activationTime.IsZero() || activationPrice <= 0 {
		return v132ActivationGateResult{Allowed: true}
	}
	if ok, reason := passesV136RewardRiskGate(rec, activationPrice); !ok {
		return v132ActivationGateResult{Allowed: false, Kind: "reward_risk", Reason: reason}
	}
	if ok, reason := passesV136StrengthGate(activationTime, activationPrice, bars); !ok {
		return v132ActivationGateResult{Allowed: false, Kind: "strength", Reason: reason}
	}
	if ok, reason := passesV136CooldownGate(rec, activationTime); !ok {
		return v132ActivationGateResult{Allowed: false, Kind: "cooldown", Reason: reason}
	}
	return v132ActivationGateResult{Allowed: true}
}

func passesV132RewardRiskGate(rec models.AiRecommendStocks, activationPrice float64) (bool, string) {
	stopProfit, profitOK := parseStopProfitPrice(rec)
	stopLoss, lossOK := parseStopLossPrice(rec)
	if !profitOK || !lossOK || stopProfit <= activationPrice || stopLoss <= 0 || stopLoss >= activationPrice {
		return false, "V1.3.2盈亏比准入未通过：缺少有效止盈止损或止盈止损位置无效"
	}
	upside := stopProfit - activationPrice
	downside := activationPrice - stopLoss
	if downside <= 0 {
		return false, "V1.3.2盈亏比准入未通过：下行风险无效"
	}
	ratio := upside / downside
	downsidePct := downside / activationPrice * 100
	if ratio < v132MinRewardRiskRatio {
		return false, fmt.Sprintf("V1.3.2盈亏比准入未通过：盈亏比 %.2f 低于 %.2f", round2(ratio), v132MinRewardRiskRatio)
	}
	if downsidePct > v132MaxDownsideRiskPct {
		return false, fmt.Sprintf("V1.3.2盈亏比准入未通过：止损空间 %.2f%% 超过 %.2f%%", round2(downsidePct), v132MaxDownsideRiskPct)
	}
	return true, ""
}

func passesV136RewardRiskGate(rec models.AiRecommendStocks, activationPrice float64) (bool, string) {
	stopProfit, profitOK := parseStopProfitPrice(rec)
	stopLoss, lossOK := parseStopLossPrice(rec)
	if !profitOK || !lossOK || stopProfit <= activationPrice || stopLoss <= 0 || stopLoss >= activationPrice {
		return false, "V1.3.6源头质量门槛未通过：缺少有效止盈止损或止盈止损位置无效"
	}
	downside := activationPrice - stopLoss
	if downside <= 0 {
		return false, "V1.3.6源头质量门槛未通过：下行风险无效"
	}
	ratio := (stopProfit - activationPrice) / downside
	downsidePct := downside / activationPrice * 100
	if ratio < v136HardRewardRiskRatio {
		return false, fmt.Sprintf("V1.3.6盈亏比硬底线未通过：盈亏比 %.2f 低于 %.2f", round2(ratio), v136HardRewardRiskRatio)
	}
	if downsidePct > v132MaxDownsideRiskPct {
		return false, fmt.Sprintf("V1.3.6源头质量门槛未通过：止损空间 %.2f%% 超过 %.2f%%", round2(downsidePct), v132MaxDownsideRiskPct)
	}
	if ratio < v132MinRewardRiskRatio {
		return true, fmt.Sprintf("V1.3.6盈亏比灰区：盈亏比 %.2f 低于 %.2f，已进入二次确认", round2(ratio), v132MinRewardRiskRatio)
	}
	return true, ""
}

func passesV132StrengthGate(rec models.AiRecommendStocks, activationTime time.Time, activationPrice float64, bars []minuteBar) (bool, string) {
	sameDayBars := v132BarsUntilActivation(bars, activationTime)
	if len(sameDayBars) == 0 {
		return false, "V1.3.2强弱过滤未通过：缺少激活前分钟线"
	}
	vwap := v132VWAP(sameDayBars)
	if vwap > 0 && activationPrice < vwap {
		return false, fmt.Sprintf("V1.3.2强弱过滤未通过：激活价 %.2f 低于 VWAP %.2f", round2(activationPrice), round2(vwap))
	}
	if len(sameDayBars) >= 5 && vwap > 0 {
		recent := sameDayBars
		if len(recent) > 30 {
			recent = recent[len(recent)-30:]
		}
		above := 0
		total := 0
		for _, bar := range recent {
			price := firstPositiveFloat64(bar.Close, bar.Open)
			if price <= 0 {
				continue
			}
			total++
			if price >= vwap {
				above++
			}
		}
		if total > 0 && above*2 < total {
			return false, fmt.Sprintf("V1.3.2强弱过滤未通过：最近%d根分钟线仅%d根站上 VWAP", total, above)
		}
	}
	anchor := resolveV132AnchorPrice(rec, sameDayBars)
	if anchor > 0 {
		changePct := (activationPrice - anchor) / anchor * 100
		if changePct < 0 {
			return false, fmt.Sprintf("V1.3.2强弱过滤未通过：激活点较锚点下跌 %.2f%%", round2(changePct))
		}
	}
	return true, ""
}

func passesV136StrengthGate(activationTime time.Time, activationPrice float64, bars []minuteBar) (bool, string) {
	sameDayBars := v132BarsUntilActivation(bars, activationTime)
	if len(sameDayBars) < v136VWAPConfirmMinBars {
		return false, fmt.Sprintf("V1.3.6强弱二次确认未通过：当日激活前分钟线仅%d根，少于%d根", len(sameDayBars), v136VWAPConfirmMinBars)
	}
	vwap := v132VWAP(sameDayBars)
	if vwap > 0 && activationPrice < vwap {
		return false, fmt.Sprintf("V1.3.6强弱二次确认未通过：激活价 %.2f 低于 VWAP %.2f", round2(activationPrice), round2(vwap))
	}
	recent := sameDayBars
	if len(recent) > 30 {
		recent = recent[len(recent)-30:]
	}
	above := 0
	total := 0
	for _, bar := range recent {
		price := firstPositiveFloat64(bar.Close, bar.Open)
		if price <= 0 {
			continue
		}
		total++
		if vwap <= 0 || price >= vwap {
			above++
		}
	}
	if total > 0 && above*2 < total {
		return false, fmt.Sprintf("V1.3.6强弱二次确认未通过：最近%d根分钟线仅%d根站上 VWAP", total, above)
	}
	return true, ""
}

func passesV132CooldownGate(rec models.AiRecommendStocks, activationTime time.Time) (bool, string) {
	recentCount := countRecentStopLossRecordsForGate(rec, activationTime)
	if recentCount >= 2 {
		return false, "V1.3.2重复止损冷却：同股连续两次止损，本阶段降级为仅分析"
	}
	if recentCount >= 1 {
		return false, fmt.Sprintf("V1.3.2重复止损冷却：同股最近%d个交易日内已有止损记录", v132CooldownTradeDays)
	}
	return true, ""
}

func passesV136CooldownGate(rec models.AiRecommendStocks, activationTime time.Time) (bool, string) {
	recentCount := countRecentStopLossRecordsForGate(rec, activationTime)
	if recentCount >= 2 {
		return false, "V1.3.6重复止损冷却：同股最近5个交易日内连续两次止损，继续跳过"
	}
	return true, ""
}

func countRecentStopLossRecordsForGate(rec models.AiRecommendStocks, activationTime time.Time) int {
	code := normalizeRecommendStockCode(rec.StockCode)
	if code == "" {
		return 0
	}
	cutoff := activationTime.AddDate(0, 0, -14)
	rows := make([]models.AiRecommendYieldRecordState, 0)
	err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).
		Where("stock_code = ? AND position_status = ? AND sell_time IS NOT NULL AND sell_time >= ? AND recommend_id <> ?", code, "已止损", cutoff, rec.ID).
		Order("sell_time DESC").
		Limit(2).
		Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return 0
	}
	recentCount := 0
	cursor := activationTime
	for i := 0; i < v132CooldownTradeDays; i++ {
		cursor = shiftToPrevCNOpenTradeDay(cursor.AddDate(0, 0, -1))
	}
	for _, row := range rows {
		if row.SellTime == nil || row.SellTime.IsZero() {
			continue
		}
		if !row.SellTime.Before(cursor) && row.SellTime.Before(activationTime) {
			recentCount++
		}
	}
	return recentCount
}

func v132BarsUntilActivation(bars []minuteBar, activationTime time.Time) []minuteBar {
	out := make([]minuteBar, 0, len(bars))
	for _, bar := range bars {
		if bar.TradeTime.IsZero() || !isSameCNTradeDate(bar.TradeTime, activationTime) || bar.TradeTime.After(activationTime) {
			continue
		}
		out = append(out, bar)
	}
	return out
}

func v132VWAP(bars []minuteBar) float64 {
	totalAmount := 0.0
	totalVolume := 0.0
	fallbackSum := 0.0
	fallbackCount := 0
	for _, bar := range bars {
		if bar.Amount > 0 && bar.Volume > 0 {
			totalAmount += bar.Amount
			totalVolume += bar.Volume
		}
		if price := firstPositiveFloat64(bar.Close, bar.Open); price > 0 {
			fallbackSum += price
			fallbackCount++
		}
	}
	reference := 0.0
	if totalAmount > 0 && totalVolume > 0 {
		if fallbackCount > 0 {
			reference = fallbackSum / float64(fallbackCount)
		}
		return normalizeV132VWAP(totalAmount/totalVolume, reference)
	}
	if fallbackCount > 0 {
		return fallbackSum / float64(fallbackCount)
	}
	return 0
}

func normalizeV132VWAP(raw float64, reference float64) float64 {
	if raw <= 0 {
		return 0
	}
	if reference <= 0 {
		return raw
	}
	value := raw
	if value > reference*20 {
		value = value / 100
	}
	if value > reference*20 || value < reference/20 {
		return reference
	}
	return value
}

func resolveV132AnchorPrice(rec models.AiRecommendStocks, bars []minuteBar) float64 {
	for _, raw := range []string{rec.StockCurrentPrice, rec.ObservePrice, rec.StockPrice, rec.StockClosePrice} {
		if price, ok := parseBuyPrice(raw); ok && price > 0 {
			return price
		}
	}
	if len(bars) > 0 {
		return firstPositiveFloat64(bars[0].Open, bars[0].Close)
	}
	return 0
}

func firstPositiveFloat64(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
