package data

import (
	"go-stock/backend/models"
	"testing"
)

func TestResolveRecommendBuyRange_PrefersExplicitActivationRangeFromBuySignals(t *testing.T) {
	rec := models.AiRecommendStocks{
		StockCurrentPrice:    "9.36",
		RecommendBuyPrice:    "1.2-9.42",
		RecommendBuyPriceMin: 1.2,
		RecommendBuyPriceMax: 9.42,
		BuySignal:            "价格触发：未来3-5个交易日内股价进入9.42-9.56主买入区；量能触发：在9.42以上观察5分钟成交量，相对近5个5分钟均量至少1.2倍；逻辑触发：核心催化未证伪且板块未转弱",
		BuySignalDetail:      "激活买入区间：9.42-9.56；仅限激活买入，不可盘后视作直接下单",
	}

	text, minPrice, maxPrice, ok := resolveRecommendBuyRange(rec)
	if !ok {
		t.Fatalf("expected buy range resolved")
	}
	if text != "9.42-9.56" {
		t.Fatalf("unexpected buy range text: %s", text)
	}
	if round2(minPrice) != 9.42 || round2(maxPrice) != 9.56 {
		t.Fatalf("unexpected buy range %.2f-%.2f", minPrice, maxPrice)
	}
}

func TestResolveRecommendBuyRange_UsesSignalThresholdRangeWhenStoredRangeMalformed(t *testing.T) {
	rec := models.AiRecommendStocks{
		StockCurrentPrice:    "13.06",
		RecommendBuyPrice:    "1.15-13.1",
		RecommendBuyPriceMin: 1.15,
		RecommendBuyPriceMax: 13.1,
		BuySignal:            "价格位置：以13.06为锚点，必须站稳已突破的近5日/20日高点附近，避免假突破；量能确认：13.10以上观察5分钟量能，相对近5个5分钟均量至少1.15倍，且不能低于上一交易日同价位活跃度；催化仍成立：龙虎榜净流入+短线突破结构仍在",
		BuySignalDetail:      "只做强势续强，不做回落抄底；若开盘直接大幅高开脱离13.35，则放弃",
	}

	text, minPrice, maxPrice, ok := resolveRecommendBuyRange(rec)
	if !ok {
		t.Fatalf("expected buy range resolved")
	}
	if text != "13.1-13.35" {
		t.Fatalf("unexpected buy range text: %s", text)
	}
	if round2(minPrice) != 13.1 || round2(maxPrice) != 13.35 {
		t.Fatalf("unexpected buy range %.2f-%.2f", minPrice, maxPrice)
	}
}
