package main

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestResearch2PortfolioRequestNormalizesSlotsAndDates(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/research2/portfolio/performance?slots=10:00,09:30,10:00&from=2026-09-01&to=2026-09-22", nil)
	recorder := httptest.NewRecorder()
	query, ok := research2PortfolioRequest(recorder, request)
	if !ok || !reflect.DeepEqual(query.Slots, []string{"09:30", "10:00"}) || query.From != "2026-09-01" || query.To != "2026-09-22" {
		t.Fatalf("query=%+v ok=%v body=%s", query, ok, recorder.Body.String())
	}
}

func TestResearch2PortfolioRequestRejectsInvalidRangeAndSlot(t *testing.T) {
	for _, path := range []string{
		"/api/v1/research2/portfolio/performance?slots=09:31",
		"/api/v1/research2/portfolio/performance?from=2026-09-22&to=2026-09-01",
	} {
		recorder := httptest.NewRecorder()
		if _, ok := research2PortfolioRequest(recorder, httptest.NewRequest("GET", path, nil)); ok || recorder.Code != 400 {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}
