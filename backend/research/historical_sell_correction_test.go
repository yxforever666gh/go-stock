package research

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func historicalSellCorrectionTestService(t *testing.T) (*Service, *gorm.DB, HistoricalSellCorrectionRequest) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "research.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrate(&Recommendation{}, &LifecycleMessage{}, &DecisionEvent{}, &LifecycleObservation{},
		&SimulatedAccount{}, &SimulatedTrade{}, &Position{}, &AccountValuationSnapshot{}); err != nil {
		t.Fatal(err)
	}
	recommendationID := "053e7c47-a538-4d6d-9dbd-61e9897d8285"
	observationID := "046ced9c-cc28-4471-b04b-ff8faf2d0055"
	decisionEventID := "3eab7cb7-f454-4b9b-8120-bacf1bf8ef24"
	quoteAt := time.Date(2026, 8, 21, 13, 30, 4, 0, shanghaiLocation)
	observedAt := time.Date(2026, 8, 21, 13, 30, 3, 845000, shanghaiLocation)
	decisionAt := time.Date(2026, 8, 21, 13, 30, 45, 509059300, shanghaiLocation)
	quote := Quote{Code: "sh601899", Name: "XD紫金矿", Market: "SH", Price: 34.14, PreviousClose: 33.83,
		Volume: 187965500, Amount: 6436552631, At: quoteAt}
	quoteJSON, _ := json.Marshal(quote)
	if err := database.Create(&SimulatedAccount{ID: 1, InitialCash: InitialCash, Cash: 100000}).Error; err != nil {
		t.Fatal(err)
	}
	recommendation := Recommendation{RecommendationID: recommendationID, AnalysisRunID: "run", StockCode: "sh601899",
		StockName: "紫金矿业", SignalAt: time.Date(2026, 8, 18, 14, 35, 40, 0, shanghaiLocation), Status: "sell_pending",
		LastDecision: "卖出", LastDecisionAt: &decisionAt}
	if err := database.Create(&recommendation).Error; err != nil {
		t.Fatal(err)
	}
	position := Position{RecommendationID: recommendationID, StockCode: recommendation.StockCode, StockName: recommendation.StockName,
		Market: "SH", Quantity: 1500, EntryAt: time.Date(2026, 8, 19, 9, 30, 19, 0, shanghaiLocation),
		EntryPrice: 32.49246, BuyFees: 15.1089939, CurrentPrice: quote.Price, CurrentPriceAt: &quoteAt, Status: "open"}
	if err := database.Create(&position).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&SimulatedTrade{TradeID: "buy", RecommendationID: recommendationID, StockCode: recommendation.StockCode,
		Side: "buy", TradedAt: position.EntryAt, MarketPrice: 32.46, ExecutionPrice: position.EntryPrice,
		Quantity: position.Quantity, TotalFees: position.BuyFees, NetCashFlow: -48753.7989939}).Error; err != nil {
		t.Fatal(err)
	}
	observation := LifecycleObservation{ObservationID: observationID, RecommendationID: recommendationID, Phase: "holding",
		WindowFrom: observedAt.Add(-15 * time.Minute), ObservedAt: observedAt, Status: "ready", QuoteJSON: string(quoteJSON),
		MinuteSummaryJSON: "{}", EvidenceJSON: "[]", SourceStatusJSON: "[]", ContentFingerprint: "fingerprint", ModelInvoked: true}
	if err := database.Create(&observation).Error; err != nil {
		t.Fatal(err)
	}
	refs, _ := json.Marshal([]string{LifecycleSourceID(observationID, LifecycleQuoteSourceSuffix), LifecycleSourceID(observationID, LifecycleMinuteSourceSuffix)})
	decision := DecisionEvent{EventID: decisionEventID, RecommendationID: recommendationID, DecisionType: "卖出",
		DecidedAt: decisionAt, Reason: "短线转弱，卖出保护利润", SourceRefs: string(refs), DataStatus: "ready"}
	if err := database.Create(&decision).Error; err != nil {
		t.Fatal(err)
	}
	request := HistoricalSellCorrectionRequest{RecommendationID: recommendationID, ObservationID: observationID,
		DecisionEventID: decisionEventID, AppliedAt: time.Date(2026, 8, 21, 15, 0, 0, 0, shanghaiLocation)}
	return NewService(NewRepository(database), nil, nil, WeekdayCalendar{}), database, request
}

func TestApplyHistoricalSellCorrectionIsAtomicAndIdempotent(t *testing.T) {
	service, database, request := historicalSellCorrectionTestService(t)
	receipt, err := service.ApplyHistoricalSellCorrection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	expected := CalculateSellCost(34.14, 1500)
	if receipt.Status != "applied" || receipt.QuoteName != "XD紫金矿" || receipt.Quantity != 1500 ||
		!receipt.QuoteAt.Equal(time.Date(2026, 8, 21, 13, 30, 4, 0, shanghaiLocation)) ||
		!receipt.TradedAt.Equal(receipt.DecisionAt) ||
		math.Abs(receipt.ExecutionPrice-expected.ExecutionPrice) > 1e-9 || math.Abs(receipt.NetCashFlow-expected.NetCashFlow) > 1e-6 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	var recommendation Recommendation
	var position Position
	var sellTrades, correctionEvents, messages int64
	_ = database.Where("recommendation_id = ?", request.RecommendationID).First(&recommendation).Error
	_ = database.Where("recommendation_id = ?", request.RecommendationID).First(&position).Error
	_ = database.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", request.RecommendationID, "sell").Count(&sellTrades).Error
	_ = database.Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", request.RecommendationID, HistoricalSellCorrectionDecision).Count(&correctionEvents).Error
	_ = database.Model(&LifecycleMessage{}).Where("recommendation_id = ? AND phase = ?", request.RecommendationID, "holding").Count(&messages).Error
	if recommendation.Status != "closed" || position.Status != "closed" || sellTrades != 1 || correctionEvents != 1 || messages != 1 {
		t.Fatalf("correction state recommendation=%s position=%s trades=%d events=%d messages=%d", recommendation.Status, position.Status, sellTrades, correctionEvents, messages)
	}
	if recommendation.LastDecision != "卖出" || recommendation.LastDecisionAt == nil || recommendation.NextCheckAt != nil {
		t.Fatalf("original sell decision was not preserved: %+v", recommendation)
	}
	if recommendation.ClosedAt == nil || !recommendation.ClosedAt.Equal(receipt.DecisionAt) || position.ExitAt == nil || !position.ExitAt.Equal(receipt.DecisionAt) {
		t.Fatalf("historical sell was not booked at the AI decision: recommendation=%v position=%v", recommendation.ClosedAt, position.ExitAt)
	}
	if math.Abs(recommendation.TotalFees-(position.BuyFees+expected.TotalFees)) > 1e-6 ||
		math.Abs(recommendation.NetPnL-receipt.NetPnL) > 1e-6 || math.Abs(position.SellFees-expected.TotalFees) > 1e-6 {
		t.Fatalf("fees or net PnL are inconsistent: recommendation=%+v position=%+v receipt=%+v", recommendation, position, receipt)
	}
	var account SimulatedAccount
	_ = database.First(&account, 1).Error
	if math.Abs(account.Cash-(100000+expected.NetCashFlow)) > 1e-6 || math.Abs(receipt.CashAfter-account.Cash) > 1e-6 {
		t.Fatalf("cash=%f receipt=%f", account.Cash, receipt.CashAfter)
	}

	duplicate, err := service.ApplyHistoricalSellCorrection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Status != "already_applied" || duplicate.Quantity != 1500 {
		t.Fatalf("unexpected duplicate receipt: %+v", duplicate)
	}
	_ = database.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", request.RecommendationID, "sell").Count(&sellTrades).Error
	if sellTrades != 1 {
		t.Fatalf("duplicate correction created %d sell trades", sellTrades)
	}
}

func TestApplyHistoricalSellCorrectionRejectsUnrelatedQuoteWithoutWrites(t *testing.T) {
	service, database, request := historicalSellCorrectionTestService(t)
	var observation LifecycleObservation
	if err := database.Where("observation_id = ?", request.ObservationID).First(&observation).Error; err != nil {
		t.Fatal(err)
	}
	var quote Quote
	_ = json.Unmarshal([]byte(observation.QuoteJSON), &quote)
	quote.Name = "XD错误股票"
	quoteJSON, _ := json.Marshal(quote)
	if err := database.Model(&LifecycleObservation{}).Where("id = ?", observation.ID).Update("quote_json", string(quoteJSON)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyHistoricalSellCorrection(context.Background(), request); err == nil {
		t.Fatal("expected mismatched quote to fail")
	}
	var position Position
	var account SimulatedAccount
	var recommendation Recommendation
	var sellTrades, correctionEvents, messages int64
	_ = database.Where("recommendation_id = ?", request.RecommendationID).First(&position).Error
	_ = database.Where("recommendation_id = ?", request.RecommendationID).First(&recommendation).Error
	_ = database.First(&account, 1).Error
	_ = database.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", request.RecommendationID, "sell").Count(&sellTrades).Error
	_ = database.Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", request.RecommendationID, HistoricalSellCorrectionDecision).Count(&correctionEvents).Error
	_ = database.Model(&LifecycleMessage{}).Where("recommendation_id = ?", request.RecommendationID).Count(&messages).Error
	if position.Status != "open" || recommendation.Status != "sell_pending" || account.Cash != 100000 || sellTrades != 0 || correctionEvents != 0 || messages != 0 {
		t.Fatalf("failed correction wrote state position=%s recommendation=%s cash=%f trades=%d events=%d messages=%d",
			position.Status, recommendation.Status, account.Cash, sellTrades, correctionEvents, messages)
	}
}

func TestApplyHistoricalSellCorrectionRejectsWrongCodeEvenWhenNameMatches(t *testing.T) {
	service, database, request := historicalSellCorrectionTestService(t)
	var observation LifecycleObservation
	if err := database.Where("observation_id = ?", request.ObservationID).First(&observation).Error; err != nil {
		t.Fatal(err)
	}
	var quote Quote
	_ = json.Unmarshal([]byte(observation.QuoteJSON), &quote)
	quote.Code = "sh600000"
	quoteJSON, _ := json.Marshal(quote)
	if err := database.Model(&LifecycleObservation{}).Where("id = ?", observation.ID).Update("quote_json", string(quoteJSON)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyHistoricalSellCorrection(context.Background(), request); err == nil {
		t.Fatal("expected wrong quote code to fail")
	}
	var sellTrades int64
	_ = database.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", request.RecommendationID, "sell").Count(&sellTrades).Error
	if sellTrades != 0 {
		t.Fatalf("wrong-code correction created %d sell trades", sellTrades)
	}
}

func TestApplyHistoricalSellCorrectionRejectsExistingDailyCloseSnapshot(t *testing.T) {
	service, database, request := historicalSellCorrectionTestService(t)
	if err := database.Create(&AccountValuationSnapshot{SnapshotID: "daily-close-2026-08-21", SnapshotType: "daily_close",
		TradingDate: "2026-08-21", ValuedAt: time.Date(2026, 8, 21, 15, 5, 0, 0, shanghaiLocation),
		Cash: 100000, NetAssetValue: 100000, CumulativeNetContribution: 100000, UnitValue: 1, ValuationStatus: "complete"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyHistoricalSellCorrection(context.Background(), request); err == nil {
		t.Fatal("expected existing daily-close snapshot to block correction")
	}
	var sellTrades int64
	_ = database.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", request.RecommendationID, "sell").Count(&sellTrades).Error
	if sellTrades != 0 {
		t.Fatalf("blocked correction created %d sell trades", sellTrades)
	}
}
