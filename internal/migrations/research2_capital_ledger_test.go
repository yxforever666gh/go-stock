package migrations

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"go-stock/backend/research2"
	"go-stock/internal/trading"
)

func TestSchema31RebuildsSlotCapitalAndSameDayFixedAllocation(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(
		&research2.Account{}, &research2.AnalysisRun{}, &research2.ExecutionChain{}, &research2.Recommendation{},
		&research2.Trade{}, &research2.AccountSnapshot{},
	); err != nil {
		t.Fatal(err)
	}
	baseline := time.Date(2026, 9, 18, 16, 0, 0, 0, research2Shanghai())
	for _, slot := range research2.Slots() {
		if err := database.Create(&research2.Account{
			Slot: slot, InitialCash: 10038.76, Cash: 10038.76, SeedCash: 38.76,
			BaselineAt: &baseline, BaselineNetAssetValue: 10038.76,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	historyAt := time.Date(2026, 9, 18, 10, 50, 0, 0, research2Shanghai())
	historyRun := research2.AnalysisRun{RunID: "history-run", TradingDate: "2026-09-18", ScheduledSlot: "10:50", Slot: "10:50", ScheduledFor: historyAt, StartedAt: historyAt, EvidenceCutoffAt: historyAt, Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	if err := database.Create(&historyRun).Error; err != nil {
		t.Fatal(err)
	}
	history := research2.Recommendation{RecommendationID: "history-rec", AnalysisRunID: historyRun.RunID, Slot: "10:50", StockCode: "sz000001", StockName: "历史大额买入", SignalAt: historyAt, TargetBuyAt: historyAt, Status: "closed", FinalScore: 80, BuyPrice: 138, BuyMarketPrice: 137.86, Quantity: 100, BuyFees: 5.14, SellAt: pointerTime(historyAt.AddDate(0, 0, 1)), SellPrice: 140, SellMarketPrice: 140.14, SellFees: 12, NetPnL: 180}
	if err := database.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	buyCost := trading.CalculateBuyCost(137.86, 100)
	sellCost := trading.CalculateSellCost(140.14, 100)
	if err := database.Create(&[]research2.Trade{
		{TradeID: "history-buy", Slot: "10:50", RecommendationID: history.RecommendationID, Side: "buy", TradedAt: historyAt, MarketPrice: 137.86, ExecutionPrice: buyCost.ExecutionPrice, Quantity: 100, Commission: buyCost.Commission, TransferFee: buyCost.TransferFee, NetCashFlow: buyCost.NetCashFlow},
		{TradeID: "history-sell", Slot: "10:50", RecommendationID: history.RecommendationID, Side: "sell", TradedAt: historyAt.AddDate(0, 0, 1), MarketPrice: 140.14, ExecutionPrice: sellCost.ExecutionPrice, Quantity: 100, Commission: sellCost.Commission, StampDuty: sellCost.StampDuty, TransferFee: sellCost.TransferFee, NetCashFlow: sellCost.NetCashFlow},
	}).Error; err != nil {
		t.Fatal(err)
	}
	raw := research2.AccountSnapshot{SnapshotID: "raw-history-snapshot", Slot: "09:50", ValuedAt: historyAt, TradingDate: "2026-09-18", SnapshotType: "daily_close", Cash: 10038.76, NetAssetValue: 10038.76}
	if err := database.Create(&raw).Error; err != nil {
		t.Fatal(err)
	}

	dayAt := time.Date(2026, 9, 21, 10, 10, 5, 0, research2Shanghai())
	run := research2.AnalysisRun{RunID: "day-run", ChainID: "day-chain", TradingDate: research2CapitalRebaseDate, ScheduledSlot: "10:10", Slot: "10:10", Published: true, ScheduledFor: dayAt, StartedAt: dayAt, EvidenceCutoffAt: dayAt, GeneratedAt: &dayAt, Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	chain := research2.ExecutionChain{ChainID: "day-chain", Slot: "10:10", WinnerRunID: run.RunID, TradingDate: research2CapitalRebaseDate, ScheduledFor: dayAt, StartedAt: dayAt, Status: "completed", TargetSlots: 5}
	if err := database.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&chain).Error; err != nil {
		t.Fatal(err)
	}
	quoteAt := dayAt.Add(time.Second)
	// The current execution roster is persisted by status, not by a score
	// threshold; this selected 40-point row must remain tradeable in replay.
	day := research2.Recommendation{RecommendationID: "day-rec", AnalysisRunID: run.RunID, Slot: "10:10", StockCode: "sh600000", StockName: "当日标的", SignalAt: dayAt, TargetBuyAt: dayAt, Status: "active", FinalScore: 40, ExecutionQuotePrice: 10, ExecutionQuoteAt: &quoteAt, BuyMarketPrice: 10, BuyPrice: 10.01, Quantity: 100}
	if err := database.Create(&day).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&research2.Trade{TradeID: "old-day-buy", Slot: "10:10", RecommendationID: day.RecommendationID, Side: "buy", TradedAt: quoteAt, MarketPrice: 10, ExecutionPrice: 10.01, Quantity: 100, NetCashFlow: -1007}).Error; err != nil {
		t.Fatal(err)
	}

	if err := database.Transaction(applyResearch2CapitalLedger); err != nil {
		t.Fatal(err)
	}
	if err := database.Transaction(applyResearch2CapitalLedger); err != nil {
		t.Fatal(err)
	}
	var transfer research2.AccountCapitalEvent
	if err := database.Where("slot = ? AND event_type = ?", "10:50", research2.CapitalEventLegacyPoolTransfer).First(&transfer).Error; err != nil || transfer.Amount <= 0 || transfer.External {
		t.Fatalf("transfer=%+v err=%v", transfer, err)
	}
	var account research2.Account
	if err := database.Where("slot = ?", "10:10").First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.InitialCash != research2CapitalInitialAmount || account.Cash < 0 {
		t.Fatalf("account=%+v", account)
	}
	var stored research2.Recommendation
	if err := database.Where("recommendation_id = ?", day.RecommendationID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "active" || stored.Quantity <= 0 || stored.BuyAt == nil {
		t.Fatalf("stored=%+v", stored)
	}
	var storedChain research2.ExecutionChain
	if err := database.Where("chain_id = ?", chain.ChainID).First(&storedChain).Error; err != nil {
		t.Fatal(err)
	}
	if storedChain.AllocationBaseCash == nil || *storedChain.AllocationBaseCash < 20000 || storedChain.FilledSlots != 1 {
		t.Fatalf("chain=%+v", storedChain)
	}
	var rawCount, ledgerCount int64
	_ = database.Model(&research2.AccountSnapshot{}).Where("snapshot_id = ?", raw.SnapshotID).Count(&rawCount).Error
	_ = database.Model(&research2.AccountLedgerSnapshot{}).Where("slot = ?", "10:10").Count(&ledgerCount).Error
	if rawCount != 1 || ledgerCount < 3 {
		t.Fatalf("raw=%d ledger=%d", rawCount, ledgerCount)
	}
	overview, err := research2.NewRepository(database).WithSlot("10:50").Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.CumulativeExternalCapital != 20000 || overview.NetInternalTransfer <= 0 || math.Abs(overview.ReturnRate-overview.CumulativeCapitalReturn) > 1e-12 {
		t.Fatalf("overview=%+v", overview)
	}
}

func pointerTime(value time.Time) *time.Time { return &value }

func TestSchema31RefusesToRewriteSoldSameDayBuy(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&research2.Account{}, &research2.AnalysisRun{}, &research2.ExecutionChain{}, &research2.Recommendation{}, &research2.Trade{}, &research2.AccountSnapshot{}); err != nil {
		t.Fatal(err)
	}
	baseline := time.Date(2026, 9, 18, 16, 0, 0, 0, research2Shanghai())
	for _, slot := range research2.Slots() {
		if err := database.Create(&research2.Account{Slot: slot, InitialCash: 10038.76, Cash: 10038.76, BaselineAt: &baseline, BaselineNetAssetValue: 10038.76}).Error; err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 9, 21, 10, 0, 0, 0, research2Shanghai())
	run := research2.AnalysisRun{RunID: uuid.NewString(), ChainID: "guard-chain", TradingDate: research2CapitalRebaseDate, Slot: "10:00", ScheduledSlot: "10:00", ScheduledFor: at, StartedAt: at, EvidenceCutoffAt: at, GeneratedAt: &at, Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	chain := research2.ExecutionChain{ChainID: run.ChainID, Slot: run.Slot, WinnerRunID: run.RunID, TradingDate: research2CapitalRebaseDate, ScheduledFor: at, StartedAt: at, Status: "completed", TargetSlots: 5}
	rec := research2.Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, Slot: run.Slot, StockCode: "sh600000", StockName: "guard", SignalAt: at, TargetBuyAt: at, Status: "closed", FinalScore: 80, ExecutionQuotePrice: 10}
	if err := database.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&chain).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&research2.Trade{TradeID: uuid.NewString(), Slot: rec.Slot, RecommendationID: rec.RecommendationID, Side: "sell", TradedAt: at.AddDate(0, 0, 1), NetCashFlow: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Transaction(applyResearch2CapitalLedger); err == nil {
		t.Fatal("expected dependent-sell guard")
	}
}

func TestSchema31PreservesScorePriorityWhenQuotesArriveOutOfOrder(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := db.AutoMigrate(&research2.Account{}, &research2.AnalysisRun{}, &research2.ExecutionChain{}, &research2.Recommendation{}, &research2.Trade{}, &research2.AccountSnapshot{}); err != nil {
		t.Fatal(err)
	}
	at := research2CapitalTopUpAt.Add(40 * time.Minute)
	for _, slot := range research2.Slots() {
		if err := db.Create(&research2.Account{Slot: slot, InitialCash: 12000, Cash: 12000}).Error; err != nil {
			t.Fatal(err)
		}
	}
	run := research2.AnalysisRun{RunID: "rank-run", ChainID: "rank-chain", Slot: "10:05", ScheduledSlot: "10:05", TradingDate: research2CapitalRebaseDate, Published: true, Status: "success", GeneratedAt: &at}
	chain := research2.ExecutionChain{ChainID: run.ChainID, Slot: run.Slot, TradingDate: run.TradingDate, WinnerRunID: run.RunID, Status: "completed", TargetSlots: 5}
	for _, model := range []any{&run, &chain} {
		if err := db.Create(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	for rank := 1; rank <= 6; rank++ {
		quotedAt := at.Add(time.Duration(7-rank) * time.Second)
		item := research2.Recommendation{RecommendationID: fmt.Sprintf("rank-%d", rank), AnalysisRunID: run.RunID, Slot: run.Slot, StockCode: fmt.Sprintf("sh60000%d", rank), StockName: "ranked", SelectionRank: rank, FinalScore: float64(40 - rank), Status: "missed_cash", ExecutionQuotePrice: 10, ExecutionQuoteAt: &quotedAt}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Transaction(applyResearch2CapitalLedger); err != nil {
		t.Fatal(err)
	}
	var items []research2.Recommendation
	if err := db.Order("selection_rank ASC").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.SelectionRank <= 5 && (item.Status != "active" || item.Quantity != 300) {
			t.Fatalf("higher-priority candidate lost its allocation: %+v", item)
		}
		if item.SelectionRank == 6 && (item.Quantity != 0 || item.BuyAt != nil) {
			t.Fatalf("earlier low-priority quote stole a seat: %+v", item)
		}
	}
	var trades []research2.Trade
	if err := db.Find(&trades).Error; err != nil {
		t.Fatal(err)
	}
	for _, trade := range trades {
		if trade.QuoteAt == nil || trade.TradedAt.Before(*trade.QuoteAt) || trade.TradedAt.Before(at) {
			t.Fatalf("execution predates evidence: %+v", trade)
		}
	}
}
