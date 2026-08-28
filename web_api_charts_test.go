package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-stock/backend/data"
	"go-stock/backend/marketdata"
)

type chartRouteStub struct{ scopes []data.ChartDrawingScope }

func (*chartRouteStub) Chart(context.Context, data.ChartRequest) marketdata.DataEnvelope[data.ChartData] {
	return marketdata.DataEnvelope[data.ChartData]{}
}
func (s *chartRouteStub) GetDrawings(_ context.Context, scope data.ChartDrawingScope) (data.ChartDrawingDocument, error) {
	s.scopes = append(s.scopes, scope)
	return data.ChartDrawingDocument{}, nil
}
func (s *chartRouteStub) PutDrawings(_ context.Context, scope data.ChartDrawingScope, _ int64, _ []data.ChartDrawing) (data.ChartDrawingDocument, error) {
	s.scopes = append(s.scopes, scope)
	return data.ChartDrawingDocument{}, nil
}
func (s *chartRouteStub) DeleteDrawings(_ context.Context, scope data.ChartDrawingScope, _ int64) (data.ChartDrawingDocument, error) {
	s.scopes = append(s.scopes, scope)
	return data.ChartDrawingDocument{}, nil
}

func TestInstrumentDrawingRoutesUseSameExplicitETFScope(t *testing.T) {
	stub := &chartRouteStub{}
	original := instrumentChartServiceFactory
	instrumentChartServiceFactory = func() instrumentChartAPI { return stub }
	t.Cleanup(func() { instrumentChartServiceFactory = original })
	mux := http.NewServeMux()
	registerInstrumentChartRoutes(mux, nil)

	putBody := `{"expectedRevision":0,"assetType":"etf","market":"SZ","period":"5m","adjustment":"none","drawings":[]}`
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPut, "http://127.0.0.1/api/v1/instruments/159915/drawings", strings.NewReader(putBody)),
		httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/instruments/159915/drawings?assetType=etf&market=SZ&period=5m&adjustment=none", nil),
		httptest.NewRequest(http.MethodDelete, "http://127.0.0.1/api/v1/instruments/159915/drawings?assetType=etf&market=SZ&period=5m&adjustment=none&expectedRevision=1", nil),
	}
	for _, request := range requests {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", request.Method, recorder.Code, recorder.Body.String())
		}
	}
	if len(stub.scopes) != 3 {
		t.Fatalf("scopes=%+v", stub.scopes)
	}
	for _, scope := range stub.scopes {
		request := scope.Request
		if request.Instrument.AssetType != "etf" || request.Instrument.Market != "SZ" || request.Instrument.Code != "sz159915" || request.Period != "5m" || request.Adjustment != "none" {
			t.Fatalf("scope mismatch=%+v", scope)
		}
	}
}
