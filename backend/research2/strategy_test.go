package research2

import (
	"context"
	"strings"
	"testing"
	"time"

	"go-stock/backend/research"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type sequenceAI struct {
	responses []string
	calls     int
}

func (a *sequenceAI) Complete(_ context.Context, _ research.CompletionRequest) (research.CompletionResult, error) {
	a.calls++
	index := a.calls - 1
	if index >= len(a.responses) {
		index = len(a.responses) - 1
	}
	return research.CompletionResult{Content: a.responses[index], Model: "test-model"}, nil
}

type fixedEvidence struct{ value Evidence }

func (e fixedEvidence) Collect(context.Context, time.Time) (Evidence, error) { return e.value, nil }

type testCalendar struct{}

func (testCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	return value.Weekday() != time.Saturday && value.Weekday() != time.Sunday, nil
}

type testMarket struct{ price float64 }

func (m testMarket) PriceAt(_ context.Context, code string, target time.Time, _ bool) (PriceSnapshot, error) {
	return PriceSnapshot{Code: code, Price: m.price, At: target, Source: "test"}, nil
}
func (testMarket) Metrics(context.Context, Recommendation) (MetricSnapshot, error) {
	return MetricSnapshot{}, nil
}

type recordingMarket struct {
	prices       map[string]float64
	currentFlags []bool
}

func (m *recordingMarket) PriceAt(_ context.Context, code string, target time.Time, current bool) (PriceSnapshot, error) {
	m.currentFlags = append(m.currentFlags, current)
	return PriceSnapshot{Code: code, Price: m.prices[code], At: target, Source: "test"}, nil
}
func (*recordingMarket) Metrics(context.Context, Recommendation) (MetricSnapshot, error) {
	return MetricSnapshot{}, nil
}

func research2TestRepository(t *testing.T) *Repository {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&AnalysisRun{}, &Recommendation{}, &Trade{}, &Account{}, &AccountSnapshot{}); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(database)
	if err = repository.EnsureAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestParseModelOutputAcceptsJSONFence(t *testing.T) {
	value, err := ParseModelOutput("```json\n{\"tradingDay\":true,\"reportMarkdown\":\"ok\",\"recommendations\":[]}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if !value.TradingDay || value.ReportMarkdown != "ok" {
		t.Fatalf("unexpected output: %+v", value)
	}
}

func TestRunnerRetriesOnceWhenModelJSONIsInvalid(t *testing.T) {
	repository := research2TestRepository(t)
	ai := &sequenceAI{responses: []string{
		`{"tradingDay":true,"reportMarkdown":"损坏"æ}`,
		`{"tradingDay":true,"conclusion":"证据不足，空仓","reportMarkdown":"# 隔离报告\n\n证据不足，空仓。","recommendations":[]}`,
	}}
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	runner := NewRunner(repository, ai, fixedEvidence{value: Evidence{Prompt: "测试证据", SourceStatusJSON: "[]"}}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return scheduled.Add(7 * time.Minute) }, func(context.Context, time.Time) error { return nil })
	run, err := runner.Run(context.Background(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	if ai.calls != 2 || run.Status != "no_recommendation" || !strings.Contains(run.ReportMarkdown, "隔离报告") {
		t.Fatalf("calls=%d run=%+v", ai.calls, run)
	}
}

func TestValidateRecommendationsRejectsStocksOutsideFrozenEvidence(t *testing.T) {
	loc := shanghai()
	generated := time.Date(2026, 8, 27, 9, 58, 0, 0, loc)
	values := []modelRecommendation{
		{Rank: 1, Code: "sh600000", Name: "模型名称", MarketScore: 10, SectorScore: 15, StockScore: 20, CatalystScore: 15, FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11},
		{Rank: 2, Code: "sz000001", Name: "证据外股票", MarketScore: 10, SectorScore: 15, StockScore: 20, CatalystScore: 15, FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11},
	}
	items, warnings := validateRecommendations("run", generated, generated, []research.StockCandidate{{Code: "sh600000", Name: "证据名称"}}, values)
	if len(items) != 1 || items[0].StockCode != "sh600000" || items[0].StockName != "证据名称" {
		t.Fatalf("unexpected validated items: %+v", items)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one evidence warning, got %v", warnings)
	}
}

func TestValidateAllocationUsesEqualCashFractions(t *testing.T) {
	for _, test := range []struct {
		count int
		want  int64
	}{{1, 1100}, {2, 500}, {3, 300}} {
		prices := make([]float64, test.count)
		for index := range prices {
			prices[index] = 10
		}
		quantities, err := ValidateAllocation(prices, InitialCash)
		if err != nil {
			t.Fatal(err)
		}
		for _, quantity := range quantities {
			if quantity != test.want {
				t.Fatalf("count=%d quantities=%v want=%d", test.count, quantities, test.want)
			}
		}
	}
}

func TestTradingServiceBuysThreeRecommendationsAtAboutOneThirdEach(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 27, 10, 0, 5, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.Add(-10 * time.Minute), StartedAt: now.Add(-10 * time.Minute), EvidenceCutoffAt: now.Add(-5 * time.Minute), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 3, OnTime: true}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	items := make([]Recommendation, 0, 3)
	for index, code := range []string{"sh600000", "sz000001", "sz002594"} {
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, Rank: index + 1, StockCode: code, StockName: code, SignalAt: now.Add(-time.Minute), FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11, Status: "buy_pending", TargetBuyAt: now.Add(-5 * time.Second)})
	}
	if err := repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	service := NewTradingService(repository, testMarket{price: 10}, testCalendar{})
	if err := service.ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	bought, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range bought {
		if item.Status != "active" || item.Quantity != 300 {
			t.Fatalf("recommendation=%+v", item)
		}
	}
	overview, err := repository.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.Cash >= 3000 || overview.Cash <= 2900 {
		t.Fatalf("cash=%f; expected roughly 3000 after three equal one-third purchases", overview.Cash)
	}
}

func TestTradingServiceReallocatesAfterUnaffordableCandidate(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 27, 10, 0, 5, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.Add(-10 * time.Minute), StartedAt: now.Add(-10 * time.Minute), EvidenceCutoffAt: now.Add(-5 * time.Minute), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 3, OnTime: true}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	prices := map[string]float64{"sh600000": 10, "sz000001": 10, "sz002594": 60}
	items := make([]Recommendation, 0, 3)
	for index, code := range []string{"sh600000", "sz000001", "sz002594"} {
		price := prices[code]
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, Rank: index + 1, StockCode: code, StockName: code, SignalAt: now.Add(-time.Minute), FinalScore: 60, ReferencePrice: price, BuyLower: price * 0.9, BuyUpper: price * 1.1, Status: "buy_pending", TargetBuyAt: now.Add(-5 * time.Second)})
	}
	if err := repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	service := NewTradingService(repository, &recordingMarket{prices: prices}, testCalendar{})
	if err := service.ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result {
		if item.StockCode == "sz002594" {
			if item.Status != "missed_cash" {
				t.Fatalf("high-priced third candidate should be skipped: %+v", item)
			}
			continue
		}
		if item.Status != "active" || item.Quantity != 500 {
			t.Fatalf("remaining candidates should each receive about one-half: %+v", item)
		}
	}
}

func TestTradingServiceDoesNotTradeDuringLunch(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.Add(-2 * time.Hour), StartedAt: now.Add(-2 * time.Hour), EvidenceCutoffAt: now.Add(-2 * time.Hour), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 1, OnTime: true}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, Rank: 1, StockCode: "sh600000", StockName: "test", SignalAt: now.Add(-2 * time.Hour), FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11, Status: "buy_pending", TargetBuyAt: now.Add(-time.Hour)}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	market := &recordingMarket{prices: map[string]float64{"sh600000": 10}}
	if err := NewTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(market.currentFlags) != 0 || result[0].Status != "buy_pending" {
		t.Fatalf("lunch processing must not request a quote or trade: calls=%v item=%+v", market.currentFlags, result[0])
	}
}

func TestSellRetryUsesCurrentQuoteAfterTargetMinute(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 28, 10, 3, 0, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.AddDate(0, 0, -1), StartedAt: now.AddDate(0, 0, -1), EvidenceCutoffAt: now.AddDate(0, 0, -1), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 1, OnTime: true}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, Rank: 1, StockCode: "sh600000", StockName: "test", SignalAt: now.AddDate(0, 0, -1), FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11, Status: "buy_pending", TargetBuyAt: now.AddDate(0, 0, -1)}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	buyCost := research.CalculateBuyCost(10, 100)
	sellAt := now.Add(-3 * time.Minute)
	if err := repository.RecordBuy(context.Background(), item.RecommendationID, Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "buy", TradedAt: now.AddDate(0, 0, -1), MarketPrice: 10, ExecutionPrice: buyCost.ExecutionPrice, Quantity: 100, Commission: buyCost.Commission, TransferFee: buyCost.TransferFee, SlippageAmount: buyCost.SlippageAmount, NetCashFlow: buyCost.NetCashFlow}, sellAt); err != nil {
		t.Fatal(err)
	}
	market := &recordingMarket{prices: map[string]float64{"sh600000": 10.5}}
	if err := NewTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(market.currentFlags) != 1 || !market.currentFlags[0] {
		t.Fatalf("expected sell retry to use a current quote, got %v", market.currentFlags)
	}
}
