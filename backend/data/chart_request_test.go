package data

import (
	"testing"
	"time"

	"go-stock/backend/instruments"
)

func TestNormalizeChartRequestDefaultsAndInstrumentRules(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, cnLocation())
	stock, err := instruments.ParseInstrumentID("600000", "stock", "SH")
	if err != nil {
		t.Fatal(err)
	}
	request, err := NormalizeChartRequest(ChartRequest{Instrument: stock}, now)
	if err != nil {
		t.Fatal(err)
	}
	if request.Period != ChartPeriodDay || request.Adjustment != ChartAdjustmentQFQ || request.Instrument.Code != "sh600000" {
		t.Fatalf("stock defaults=%+v", request)
	}
	etf, err := instruments.ParseInstrumentID("159915", "etf", "SZ")
	if err != nil {
		t.Fatal(err)
	}
	request, err = NormalizeChartRequest(ChartRequest{Instrument: etf}, now)
	if err != nil || request.Adjustment != ChartAdjustmentNone {
		t.Fatalf("ETF defaults=%+v err=%v", request, err)
	}
	index, err := instruments.ParseInstrumentID("sh000001", "index", "SH")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeChartRequest(ChartRequest{Instrument: index, Adjustment: ChartAdjustmentQFQ}, now); err == nil {
		t.Fatal("index accepted an adjusted request")
	}
	if _, err := instruments.ParseInstrumentID("sh600000", "stock", "SZ"); err == nil {
		t.Fatal("mismatched market was accepted")
	}
}

func TestChartBaseBarLimitExpandsTargetPeriodsWithinBoundedRawLimits(t *testing.T) {
	tests := []struct {
		period string
		limit  int
		want   int
	}{
		{period: ChartPeriod1Minute, limit: 100, want: 100},
		{period: ChartPeriod5Minute, limit: 100, want: 500},
		{period: ChartPeriod60Minute, limit: 5000, want: 300000},
		{period: ChartPeriodWeek, limit: 100, want: 600},
		{period: ChartPeriodMonth, limit: 100, want: 2300},
		{period: ChartPeriodQuarter, limit: 100, want: 6600},
		{period: ChartPeriodYear, limit: 100, want: maxChartBaseDailyBars},
		{period: ChartPeriodMonth, limit: maxChartBars, want: maxChartBaseDailyBars},
	}
	for _, test := range tests {
		if got := chartBaseBarLimit(test.period, test.limit); got != test.want {
			t.Errorf("chartBaseBarLimit(%s,%d)=%d want %d", test.period, test.limit, got, test.want)
		}
	}
}
