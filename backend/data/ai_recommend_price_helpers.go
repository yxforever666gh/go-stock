package data

import (
	"fmt"
	"go-stock/backend/models"
	"math"
	"regexp"
	"strconv"
	"strings"
)

func parseBuyPrice(price string) (float64, bool) {
	values := parsePriceValues(price)
	if len(values) == 0 {
		return 0, false
	}
	return values[0], true
}

func resolveRecommendBuyRange(rec models.AiRecommendStocks) (string, float64, float64, bool) {
	rawText := strings.TrimSpace(rec.RecommendBuyPrice)
	storedText, storedMin, storedMax := normalizePriceRangeText(rawText, rec.RecommendBuyPriceMin, rec.RecommendBuyPriceMax)
	if shouldPreferTextResolvedBuyRange(rawText, storedMin, storedMax) {
		textMin, _ := parsePriceMinFromText(rawText)
		textMax, _ := parsePriceMaxFromText(rawText)
		return rawText, textMin, textMax, true
	}
	if explicitText, explicitMin, explicitMax, ok := parseExplicitSignalDrivenBuyRange(rec); ok {
		if shouldPreferSignalResolvedBuyRange(rec, storedMin, storedMax, explicitMin, explicitMax, true) {
			return explicitText, explicitMin, explicitMax, true
		}
	}
	if signalText, signalMin, signalMax, ok := parseThresholdSignalDrivenBuyRange(rec); ok {
		if shouldPreferSignalResolvedBuyRange(rec, storedMin, storedMax, signalMin, signalMax, false) {
			return signalText, signalMin, signalMax, true
		}
	}
	if storedMin > 0 && storedMax > 0 {
		if strings.TrimSpace(storedText) == "" {
			if round2(storedMin) == round2(storedMax) {
				storedText = formatRecommendPrice(storedMin)
			} else {
				storedText = formatRecommendPrice(storedMin) + "-" + formatRecommendPrice(storedMax)
			}
		}
		return storedText, storedMin, storedMax, true
	}
	if explicitText, explicitMin, explicitMax, ok := parseExplicitSignalDrivenBuyRange(rec); ok {
		return explicitText, explicitMin, explicitMax, true
	}
	if signalText, signalMin, signalMax, ok := parseThresholdSignalDrivenBuyRange(rec); ok {
		return signalText, signalMin, signalMax, true
	}
	return strings.TrimSpace(rec.RecommendBuyPrice), storedMin, storedMax, false
}

func shouldPreferTextResolvedBuyRange(raw string, storedMin, storedMax float64) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	textMin, okMin := parsePriceMinFromText(raw)
	textMax, okMax := parsePriceMaxFromText(raw)
	if !okMin || !okMax || textMin <= 0 || textMax <= 0 {
		return false
	}
	if textMin > textMax {
		textMin, textMax = textMax, textMin
	}
	if storedMin <= 0 || storedMax <= 0 {
		return true
	}
	if storedMin > storedMax {
		storedMin, storedMax = storedMax, storedMin
	}
	return round2(storedMin) == round2(storedMax) && round2(textMin) != round2(textMax)
}

func resolveRecommendBuyRangeDisplay(rec models.AiRecommendStocks) string {
	text, min, max, ok := resolveRecommendBuyRange(rec)
	if ok {
		if strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		if round2(min) == round2(max) {
			return formatRecommendPrice(min)
		}
		return formatRecommendPrice(min) + "-" + formatRecommendPrice(max)
	}
	return strings.TrimSpace(rec.RecommendBuyPrice)
}

func repairRecommendBuyRangeFromSignals(recommend *models.AiRecommendStocks) {
	if recommend == nil {
		return
	}
	text, min, max, ok := resolveRecommendBuyRange(*recommend)
	if !ok || min <= 0 || max <= 0 {
		return
	}
	recommend.RecommendBuyPrice = text
	recommend.RecommendBuyPriceMin = min
	recommend.RecommendBuyPriceMax = max
}

func parseExplicitSignalDrivenBuyRange(rec models.AiRecommendStocks) (string, float64, float64, bool) {
	texts := []string{rec.BuySignalDetail, rec.BuySignal}
	for _, text := range texts {
		matches := signalDrivenExplicitBuyRangeRegexp.FindStringSubmatch(strings.TrimSpace(text))
		if len(matches) != 3 {
			continue
		}
		min, errMin := strconv.ParseFloat(matches[1], 64)
		max, errMax := strconv.ParseFloat(matches[2], 64)
		if errMin != nil || errMax != nil || min <= 0 || max <= 0 {
			continue
		}
		if min > max {
			min, max = max, min
		}
		return formatRecommendPrice(min) + "-" + formatRecommendPrice(max), min, max, true
	}
	return "", 0, 0, false
}

func parseThresholdSignalDrivenBuyRange(rec models.AiRecommendStocks) (string, float64, float64, bool) {
	minTexts := []string{rec.BuySignal, rec.BuySignalDetail}
	maxTexts := []string{rec.BuySignalDetail, rec.BuySignal}
	minPrice := firstSignalDrivenPriceMatch(minTexts, signalDrivenMinTriggerPriceRegexp)
	maxPrice := firstSignalDrivenPriceMatch(maxTexts, signalDrivenMaxChasePriceRegexp)
	if minPrice <= 0 || maxPrice <= 0 {
		return "", 0, 0, false
	}
	if minPrice > maxPrice {
		minPrice, maxPrice = maxPrice, minPrice
	}
	return formatRecommendPrice(minPrice) + "-" + formatRecommendPrice(maxPrice), minPrice, maxPrice, true
}

func firstSignalDrivenPriceMatch(texts []string, pattern *regexp.Regexp) float64 {
	for _, text := range texts {
		matches := pattern.FindStringSubmatch(strings.TrimSpace(text))
		if len(matches) < 2 {
			continue
		}
		price, err := strconv.ParseFloat(matches[1], 64)
		if err != nil || price <= 0 {
			continue
		}
		return price
	}
	return 0
}

func shouldPreferSignalResolvedBuyRange(rec models.AiRecommendStocks, storedMin, storedMax, candidateMin, candidateMax float64, explicit bool) bool {
	if candidateMin <= 0 || candidateMax <= 0 {
		return false
	}
	if storedMin <= 0 || storedMax <= 0 {
		return true
	}
	if explicit {
		return round2(storedMin) != round2(candidateMin) || round2(storedMax) != round2(candidateMax)
	}
	return isRecommendBuyRangeSuspicious(rec, storedMin, storedMax) && !isRecommendBuyRangeSuspicious(rec, candidateMin, candidateMax)
}

func isRecommendBuyRangeSuspicious(rec models.AiRecommendStocks, minPrice, maxPrice float64) bool {
	if minPrice <= 0 || maxPrice <= 0 {
		return true
	}
	refPrice := resolveRecommendReferencePrice(rec)
	if refPrice <= 0 {
		return false
	}
	if minPrice < refPrice*0.7 && maxPrice >= refPrice*0.9 {
		return true
	}
	if minPrice < refPrice*0.75 && (maxPrice-minPrice) > refPrice*0.2 {
		return true
	}
	return false
}

func resolveRecommendReferencePrice(rec models.AiRecommendStocks) float64 {
	candidates := []string{
		rec.StockCurrentPrice,
		rec.ObservePrice,
		rec.StockPrice,
		rec.StockClosePrice,
		rec.StockPrePrice,
	}
	for _, raw := range candidates {
		if price, ok := parseBuyPrice(raw); ok && price > 0 {
			return price
		}
	}
	return 0
}

func parseStopProfitPrice(item models.AiRecommendStocks) (float64, bool) {
	min := item.RecommendStopProfitPriceMin
	max := item.RecommendStopProfitPriceMax
	if min > 0 && max > 0 {
		if min > max {
			min = max
		}
		// Use the lower bound for stop-profit, not the average.
		return min, true
	}
	if min > 0 {
		return min, true
	}
	if max > 0 {
		return max, true
	}
	return parsePriceMinFromText(item.RecommendStopProfitPrice)
}

func parsePriceAverageFromText(priceText string) (float64, bool) {
	values := parsePriceValues(priceText)
	if len(values) == 0 {
		return 0, false
	}
	if len(values) == 1 {
		return values[0], true
	}
	return (values[0] + values[1]) / 2, true
}

func parsePriceMinFromText(priceText string) (float64, bool) {
	values := parsePriceValues(priceText)
	if len(values) == 0 {
		return 0, false
	}
	if len(values) == 1 {
		return values[0], true
	}
	a, b := values[0], values[1]
	if a <= b {
		return a, true
	}
	return b, true
}

func parsePriceMaxFromText(priceText string) (float64, bool) {
	values := parsePriceValues(priceText)
	if len(values) == 0 {
		return 0, false
	}
	if len(values) == 1 {
		return values[0], true
	}
	a, b := values[0], values[1]
	if a >= b {
		return a, true
	}
	return b, true
}

func parseStopLossPrice(item models.AiRecommendStocks) (float64, bool) {
	// Use the upper bound for stop-loss, not the average.
	return parsePriceMaxFromText(item.RecommendStopLossPrice)
}

func parsePriceValues(priceText string) []float64 {
	matches := priceNumberRegexp.FindAllString(priceText, -1)
	if len(matches) == 0 {
		return nil
	}
	prices := make([]float64, 0, len(matches))
	for _, match := range matches {
		price, err := strconv.ParseFloat(match, 64)
		if err != nil || price <= 0 {
			continue
		}
		prices = append(prices, price)
	}
	return prices
}

func copyFloatPointer(v *float64) *float64 {
	if v == nil {
		return nil
	}
	copied := *v
	return &copied
}

func isSQLiteNoSuchTable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "no such table")
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func formatSignedPercent(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.2f%%", v)
	}
	return fmt.Sprintf("%.2f%%", v)
}
