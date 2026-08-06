package service

import (
	"context"
	"errors"
	"reflect"
	"time"

	"go-stock/backend/models"
)

var ErrMarketSummaryV150ProducerUnavailable = errors.New("market summary V1.5 producer is not configured")

// MarketSummaryV150ProductionRequest is the consumer-owned command for one
// run. Provider resolution and all market-dependent assembly remain behind the
// producer port.
type MarketSummaryV150ProductionRequest struct {
	AIConfigID  int       `json:"aiConfigId"`
	Question    string    `json:"question"`
	SysPromptID *int      `json:"sysPromptId,omitempty"`
	Think       bool      `json:"think"`
	StartedAt   time.Time `json:"startedAt"`
}

// MarketSummaryV150ProductionResult is the typed delivery result after the
// frozen decision has crossed the atomic publisher boundary. It deliberately
// contains no opaque decision JSON.
type MarketSummaryV150ProductionResult struct {
	RunID                  string                                   `json:"runId"`
	StrategyVersion        string                                   `json:"strategyVersion"`
	ReportText             string                                   `json:"reportText"`
	ProviderName           string                                   `json:"providerName"`
	ModelName              string                                   `json:"modelName"`
	CandidateCount         int                                      `json:"candidateCount"`
	VerifiedCandidateCount int                                      `json:"verifiedCandidateCount"`
	ProductionCount        int                                      `json:"productionCount"`
	NoTradeReason          string                                   `json:"noTradeReason,omitempty"`
	RouteLog               *MarketSummaryRouteLog                   `json:"routeLog,omitempty"`
	SaveResult             *models.MarketSummaryRecommendSaveResult `json:"saveResult,omitempty"`
}

// MarketSummaryV150Producer owns the complete build-and-publish production
// attempt. RecommendService only gates access to and delegates this typed port.
type MarketSummaryV150Producer interface {
	Produce(context.Context, MarketSummaryV150ProductionRequest) (*MarketSummaryV150ProductionResult, error)
}

func isNilMarketSummaryV150Producer(producer MarketSummaryV150Producer) bool {
	if producer == nil {
		return true
	}
	value := reflect.ValueOf(producer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
