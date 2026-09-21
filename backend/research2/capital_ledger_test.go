package research2

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestCapitalLedgerUsesHistoricSlotReturnsAndNeutralTransfers(t *testing.T) {
	repository := research2TestRepository(t)
	if err := repository.DB().AutoMigrate(&AccountCapitalEvent{}, &AccountLedgerSnapshot{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialAt := time.Date(2026, 8, 27, 9, 30, 0, 0, shanghai())
	topUpAt := time.Date(2026, 9, 21, 9, 25, 0, 0, shanghai())
	events := []AccountCapitalEvent{
		{EventID: "ledger-initial", Slot: DefaultSlot, EventType: CapitalEventInitial, Amount: 10000, External: true, Source: "user_initial", EffectiveAt: initialAt, TradingDate: "2026-08-27"},
		{EventID: "ledger-transfer", Slot: DefaultSlot, EventType: CapitalEventLegacyPoolTransfer, Amount: 500, External: false, Source: "legacy_shared_pool", EffectiveAt: initialAt.Add(time.Minute), TradingDate: "2026-08-27"},
		{EventID: "ledger-topup", Slot: DefaultSlot, EventType: CapitalEventTopUp, Amount: 10000, External: true, Source: "user_top_up", EffectiveAt: topUpAt, TradingDate: "2026-09-21"},
	}
	if err := repository.DB().Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	// 20,000 external + 500 neutral transfer + 200 realized profit.
	if err := repository.DB().Model(&Account{}).Where("slot = ?", DefaultSlot).Update("cash", 20700).Error; err != nil {
		t.Fatal(err)
	}
	soldAt := time.Date(2026, 9, 1, 10, 0, 0, 0, shanghai())
	closed := Recommendation{RecommendationID: "historic-closed", AnalysisRunID: "historic-run", Slot: DefaultSlot, StockCode: "sh600000", StockName: "历史成交", SignalAt: soldAt.AddDate(0, 0, -1), TargetBuyAt: soldAt.AddDate(0, 0, -1), Status: "closed", BuyAt: pointerResearch2Time(soldAt.AddDate(0, 0, -1)), SellAt: &soldAt, BuyPrice: 10, Quantity: 100, NetPnL: 200}
	if err := repository.DB().Create(&closed).Error; err != nil {
		t.Fatal(err)
	}
	overview, err := repository.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if overview.ValuationBasis != CapitalValuationBasisLedger || overview.InitialContribution != 10000 || overview.TopUpContribution != 10000 || overview.CumulativeExternalCapital != 20000 || overview.NetInternalTransfer != 500 {
		t.Fatalf("overview=%+v", overview)
	}
	if math.Abs(overview.NetProfit-200) > 1e-8 || math.Abs(overview.ReturnRate-0.01) > 1e-8 || overview.CumulativeCapitalReturn != overview.ReturnRate {
		t.Fatalf("overview=%+v", overview)
	}
	performance, err := repository.Performance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if performance.ClosedTrades != 1 || performance.Curve[len(performance.Curve)-1].ValuationBasis != CapitalValuationBasisLedger {
		t.Fatalf("performance=%+v", performance)
	}
	if _, err = repository.SaveSnapshot(ctx, "trade_cycle", topUpAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var derived int64
	if err = repository.DB().Model(&AccountLedgerSnapshot{}).Where("slot = ?", DefaultSlot).Count(&derived).Error; err != nil || derived != 1 {
		t.Fatalf("derived=%d err=%v", derived, err)
	}
}

func pointerResearch2Time(value time.Time) *time.Time { return &value }
