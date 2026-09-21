package research2

import (
	"context"
	"math"
	"testing"
	"time"

	"go-stock/internal/trading"
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
	oldPeriodPnL := -50.0
	closed.PeriodPnL = &oldPeriodPnL
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
	if performance.ClosedTrades != 1 || performance.WinningTrades != 1 || performance.Curve[len(performance.Curve)-1].ValuationBasis != CapitalValuationBasisLedger {
		t.Fatalf("performance=%+v", performance)
	}
	if _, err = repository.SaveSnapshot(ctx, "trade_cycle", topUpAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var derived int64
	if err = repository.DB().Model(&AccountLedgerSnapshot{}).Where("slot = ?", DefaultSlot).Count(&derived).Error; err != nil || derived != 1 {
		t.Fatalf("derived=%d err=%v", derived, err)
	}
	buyAt := topUpAt.Add(time.Hour)
	pending := Recommendation{RecommendationID: "ledger-buy", Slot: DefaultSlot, AnalysisRunID: "ledger-run", StockCode: "sh600001", StockName: "new", SignalAt: buyAt, TargetBuyAt: buyAt, Status: "buy_pending"}
	if err = repository.DB().Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	cost := trading.CalculateBuyCost(10, 100)
	buy := Trade{TradeID: "new-ledger-buy", RecommendationID: pending.RecommendationID, Side: "buy", TradedAt: buyAt, Quantity: 100, MarketPrice: 10, ExecutionPrice: cost.ExecutionPrice, Commission: cost.Commission, TransferFee: cost.TransferFee, NetCashFlow: cost.NetCashFlow}
	if err = repository.DB().Exec("CREATE TRIGGER reject_ledger_snapshot BEFORE INSERT ON research2_account_ledger_snapshots BEGIN SELECT RAISE(ABORT,'fixture'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err = repository.RecordBuy(ctx, pending.RecommendationID, buy, buyAt.AddDate(0, 0, 1)); err == nil {
		t.Fatal("trade must roll back when the ledger snapshot fails")
	}
	afterFailure, err := repository.Overview(ctx)
	if err != nil || afterFailure.Cash != 20700 || afterFailure.OpenPositions != 0 {
		t.Fatalf("non-atomic failed buy: %+v %v", afterFailure, err)
	}
	if err = repository.DB().Exec("DROP TRIGGER reject_ledger_snapshot").Error; err != nil {
		t.Fatal(err)
	}
	if err = repository.RecordBuy(ctx, pending.RecommendationID, buy, buyAt.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	sellCost := trading.CalculateSellCost(11, 100)
	sell := Trade{TradeID: "new-ledger-sell", RecommendationID: pending.RecommendationID, Side: "sell", TradedAt: buyAt.AddDate(0, 0, 1), Quantity: 100, MarketPrice: 11, ExecutionPrice: sellCost.ExecutionPrice, Commission: sellCost.Commission, TransferFee: sellCost.TransferFee, StampDuty: sellCost.StampDuty, NetCashFlow: sellCost.NetCashFlow}
	if err = repository.RecordSell(ctx, pending.RecommendationID, sell); err != nil {
		t.Fatal(err)
	}
	var tradeEvents []AccountLedgerSnapshot
	if err = repository.DB().Where("snapshot_type = ?", "trade").Order("valued_at ASC").Find(&tradeEvents).Error; err != nil || len(tradeEvents) != 2 {
		t.Fatalf("trade events=%+v err=%v", tradeEvents, err)
	}
	if math.Abs(tradeEvents[1].NetProfit-(200+cost.NetCashFlow+sellCost.NetCashFlow)) > 1e-8 {
		t.Fatalf("trade ledger lost original profit: %+v", tradeEvents[1])
	}
}

func pointerResearch2Time(value time.Time) *time.Time { return &value }

func TestCapitalEventDrawdownIgnoresContributionsAndTransfers(t *testing.T) {
	curve := []AccountLedgerSnapshot{
		{NetAssetValue: 10000, CumulativeExternalCapital: 10000},
		{NetAssetValue: 12000, CumulativeExternalCapital: 10000},
		{NetAssetValue: 22000, CumulativeExternalCapital: 20000},
		{NetAssetValue: 25000, CumulativeExternalCapital: 20000, NetInternalTransfer: 3000},
	}
	if got := capitalEventDrawdown(curve); got != 0 {
		t.Fatalf("non-trading cash flow created a drawdown: %v", got)
	}
	curve = append(curve, AccountLedgerSnapshot{NetAssetValue: 22500, CumulativeExternalCapital: 20000, NetInternalTransfer: 3000})
	if got := capitalEventDrawdown(curve); math.Abs(got-0.1) > 1e-12 {
		t.Fatalf("actual 10%% trading drawdown=%v", got)
	}
}
