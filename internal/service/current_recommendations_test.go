package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/portfolio"
)

type currentRecommendationTestClock struct{ now time.Time }

func (clock currentRecommendationTestClock) Now() time.Time { return clock.now }

type recordingCurrentRecommendationReader struct {
	rows  []portfolio.CurrentRecommendation
	err   error
	calls int
	query portfolio.RecommendationQuery
}

func (reader *recordingCurrentRecommendationReader) List(_ context.Context, query portfolio.RecommendationQuery) ([]portfolio.CurrentRecommendation, error) {
	reader.calls++
	reader.query = query
	return append([]portfolio.CurrentRecommendation(nil), reader.rows...), reader.err
}

type recordingRecommendationListOperations struct {
	RecommendOperations
	calls int
	query *models.AiRecommendStocksQuery
	page  *models.AiRecommendStocksPageData
	err   error
}

func (operations *recordingRecommendationListOperations) GetAiRecommendStocksList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksPageData, error) {
	operations.calls++
	operations.query = query
	return operations.page, operations.err
}

func TestRecommendServiceCurrentListUsesFrozenReaderAndBuildsCompatibilityPage(t *testing.T) {
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 8, 7, 14, 30, 0, 0, zone)
	reader := &recordingCurrentRecommendationReader{rows: []portfolio.CurrentRecommendation{
		currentRecommendationServiceFixture("rule-old", "000001.SZ", "older", "bank", now.Add(-2*time.Hour), portfolio.RecommendationHolding, 11),
		currentRecommendationServiceFixture("rule-new", "600000.SH", "newer", "technology", now.Add(-time.Hour), portfolio.RecommendationPending, 22),
		currentRecommendationServiceFixture("rule-middle", "000002.SZ", "middle", "bank", now.Add(-90*time.Minute), portfolio.RecommendationExpired, 0),
	}}
	legacyOperations := &recordingRecommendationListOperations{}
	service := NewRecommendService(legacyOperations, nil, reader, currentRecommendationTestClock{now: now}, "1.5.0")

	page, err := service.GetAiRecommendStocksList(&models.AiRecommendStocksQuery{
		StrategyCohort: "current", StartDate: "2026-08-01", EndDate: "2026-08-07", Page: 1, PageSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacyOperations.calls != 0 || reader.calls != 1 {
		t.Fatalf("legacy/current calls = %d/%d, want 0/1", legacyOperations.calls, reader.calls)
	}
	if reader.query.StrategyVersion != "1.5.0" || !reader.query.AsOf.Equal(now) ||
		reader.query.Start.Format(time.DateOnly) != "2026-08-01" || reader.query.End.Format(time.DateOnly) != "2026-08-07" {
		t.Fatalf("reader query = %+v", reader.query)
	}
	if page.Total != 3 || page.Page != 1 || page.PageSize != 2 || page.TotalPages != 2 || page.StrategyCohort != "1.5.0" || len(page.List) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.List[0].StrategyRuleID != "rule-new" || page.List[1].StrategyRuleID != "rule-middle" {
		t.Fatalf("page order = %s, %s", page.List[0].StrategyRuleID, page.List[1].StrategyRuleID)
	}
	first := page.List[0]
	if first.ID != 22 || first.StockCode != "600000.SH" || first.StockName != "newer" || first.BkName != "technology" ||
		first.SummaryVersion != "1.5.0" || first.StrategyRunID != "run-rule-new" ||
		first.ActivationStatus != "pending" || first.ExecutionState != "pending" || first.RecommendStatus != "pending" ||
		first.ProviderName != "provider" || first.ModelName != "model" || !first.CreatedAt.Equal(now.Add(-time.Hour)) ||
		first.DataTime == nil || !first.DataTime.Equal(now.Add(-time.Hour)) {
		t.Fatalf("mapped current row = %+v", first)
	}
	if first.StockCurrentPrice != "" || first.RecommendBuyPrice != "" || first.RecommendStopProfitPrice != "" || first.RecommendStopLossPrice != "" {
		t.Fatalf("current DTO manufactured mutable prices: %+v", first)
	}
}

func TestRecommendServiceExactCurrentVersionFiltersBeforePagination(t *testing.T) {
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 8, 7, 11, 0, 0, 0, zone)
	reader := &recordingCurrentRecommendationReader{rows: []portfolio.CurrentRecommendation{
		currentRecommendationServiceFixture("rule-a", "000001.SZ", "alpha", "bank", now.Add(-time.Hour), portfolio.RecommendationPending, 1),
		currentRecommendationServiceFixture("rule-b", "600000.SH", "beta", "energy", now.Add(-2*time.Hour), portfolio.RecommendationHolding, 2),
	}}
	service := NewRecommendService(&recordingRecommendationListOperations{}, nil, reader, currentRecommendationTestClock{now: now}, "1.5.0")

	page, err := service.GetAiRecommendStocksList(&models.AiRecommendStocksQuery{
		StrategyCohort: "1.5.0", StockCode: "600000", StockName: "BETA", Page: 1, PageSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.List) != 1 || page.List[0].StrategyRuleID != "rule-b" {
		t.Fatalf("filtered page = %+v", page)
	}
	if reader.query.Start.Format(time.DateOnly) != "1970-01-01" || reader.query.End.Format(time.DateOnly) != "2026-08-07" {
		t.Fatalf("default date window = %s..%s", reader.query.Start, reader.query.End)
	}
}

func TestRecommendServiceCurrentListRequiresEveryFilterToMatch(t *testing.T) {
	now := time.Date(2026, 8, 7, 11, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	reader := &recordingCurrentRecommendationReader{rows: []portfolio.CurrentRecommendation{
		currentRecommendationServiceFixture("rule-a", "000001.SZ", "alpha", "bank", now.Add(-time.Hour), portfolio.RecommendationPending, 1),
	}}
	service := NewRecommendService(&recordingRecommendationListOperations{}, nil, reader, currentRecommendationTestClock{now: now}, "1.5.0")

	for _, query := range []models.AiRecommendStocksQuery{
		{StrategyCohort: "current", StockCode: "000001", StockName: "missing"},
		{StrategyCohort: "current", BkCode: "BK0001"},
		{StrategyCohort: "current", ModelName: "missing"},
	} {
		page, err := service.GetAiRecommendStocksList(&query)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 0 || len(page.List) != 0 {
			t.Fatalf("query %+v unexpectedly matched %+v", query, page.List)
		}
	}
}

func TestRecommendServiceCurrentVersionAcceptsCanonicalAliases(t *testing.T) {
	for _, cohort := range []string{"1.5.0", "v1.5.0", " V1.5.0 "} {
		t.Run(cohort, func(t *testing.T) {
			reader := &recordingCurrentRecommendationReader{}
			service := NewRecommendService(&recordingRecommendationListOperations{}, nil, reader, currentRecommendationTestClock{now: time.Now()}, "1.5.0")
			if _, err := service.GetAiRecommendStocksList(&models.AiRecommendStocksQuery{StrategyCohort: cohort}); err != nil {
				t.Fatal(err)
			}
			if reader.calls != 1 {
				t.Fatalf("reader calls = %d, want 1", reader.calls)
			}
		})
	}
}

func TestRecommendServiceLegacyListContinuesThroughOperations(t *testing.T) {
	want := &models.AiRecommendStocksPageData{Total: 7}
	for _, cohort := range []string{"legacy", "1.4.2", "v1.4.1", "phase3-v4"} {
		t.Run(cohort, func(t *testing.T) {
			reader := &recordingCurrentRecommendationReader{}
			operations := &recordingRecommendationListOperations{page: want}
			service := NewRecommendService(operations, nil, reader, currentRecommendationTestClock{now: time.Now()}, "1.5.0")
			query := &models.AiRecommendStocksQuery{StrategyCohort: cohort}
			got, err := service.GetAiRecommendStocksList(query)
			if err != nil {
				t.Fatal(err)
			}
			if got != want || operations.calls != 1 || operations.query != query || reader.calls != 0 {
				t.Fatalf("got=%p operations=%d reader=%d", got, operations.calls, reader.calls)
			}
		})
	}
}

func TestRecommendServiceRecommendationListRejectsMixedAndUnknownCohorts(t *testing.T) {
	for _, cohort := range []string{"", "all", "unknown", "1.5.1"} {
		t.Run(cohort, func(t *testing.T) {
			reader := &recordingCurrentRecommendationReader{}
			operations := &recordingRecommendationListOperations{}
			service := NewRecommendService(operations, nil, reader, currentRecommendationTestClock{now: time.Now()}, "1.5.0")
			_, err := service.GetAiRecommendStocksList(&models.AiRecommendStocksQuery{StrategyCohort: cohort})
			if !errors.Is(err, ErrInvalidStrategyCohort) {
				t.Fatalf("error = %v, want ErrInvalidStrategyCohort", err)
			}
			if operations.calls != 0 || reader.calls != 0 {
				t.Fatalf("invalid cohort reached a source: operations=%d reader=%d", operations.calls, reader.calls)
			}
		})
	}
	service := NewRecommendService(&recordingRecommendationListOperations{}, nil, &recordingCurrentRecommendationReader{}, currentRecommendationTestClock{now: time.Now()}, "1.5.0")
	if _, err := service.GetAiRecommendStocksList(nil); !errors.Is(err, ErrInvalidRecommendationListQuery) {
		t.Fatalf("nil query error = %v, want ErrInvalidRecommendationListQuery", err)
	}
}

func TestRecommendServiceCurrentListPropagatesFrozenReaderErrorAndKeepsNoSnapshotEmpty(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	sealedErr := errors.New("snapshot seal mismatch")
	reader := &recordingCurrentRecommendationReader{err: sealedErr}
	service := NewRecommendService(&recordingRecommendationListOperations{}, nil, reader, currentRecommendationTestClock{now: now}, "1.5.0")
	_, err := service.GetAiRecommendStocksList(&models.AiRecommendStocksQuery{StrategyCohort: "current"})
	if err != sealedErr {
		t.Fatalf("error = %v, want frozen reader error", err)
	}

	reader.err = nil
	page, err := service.GetAiRecommendStocksList(&models.AiRecommendStocksQuery{StrategyCohort: "current"})
	if err != nil {
		t.Fatal(err)
	}
	if page.List == nil || len(page.List) != 0 || page.Total != 0 || page.TotalPages != 0 || page.Page != 1 || page.PageSize != 10 {
		t.Fatalf("empty page = %+v", page)
	}
}

func TestRecommendServiceCurrentListRejectsInvalidDateWindowBeforeRead(t *testing.T) {
	reader := &recordingCurrentRecommendationReader{}
	service := NewRecommendService(&recordingRecommendationListOperations{}, nil, reader, currentRecommendationTestClock{now: time.Now()}, "1.5.0")
	for _, query := range []*models.AiRecommendStocksQuery{
		{StrategyCohort: "current", StartDate: "invalid"},
		{StrategyCohort: "current", StartDate: "2026-08-08", EndDate: "2026-08-07"},
	} {
		if _, err := service.GetAiRecommendStocksList(query); !errors.Is(err, ErrInvalidRecommendationListQuery) {
			t.Fatalf("query=%+v error=%v, want ErrInvalidRecommendationListQuery", query, err)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("invalid date reached reader %d times", reader.calls)
	}
}

func currentRecommendationServiceFixture(
	ruleID, symbol, name, sector string,
	decisionAt time.Time,
	status portfolio.RecommendationLifecycleStatus,
	recommendID uint,
) portfolio.CurrentRecommendation {
	row := portfolio.CurrentRecommendation{
		Frozen: portfolio.FrozenRecommendation{
			RunID: "run-" + ruleID, RuleID: ruleID, CandidateID: "candidate-" + ruleID,
			StrategyVersion: "1.5.0", Symbol: symbol, Name: name, Sector: sector,
			DecisionAt: decisionAt, ValidFromAt: decisionAt.Add(time.Minute),
		},
		Lifecycle: portfolio.RecommendationLifecycle{Status: status, Reason: "sealed-reason"},
	}
	if recommendID != 0 {
		row.Display = &portfolio.DisplayMetadata{RecommendID: recommendID, Provider: "provider", Model: "model"}
	}
	return row
}
