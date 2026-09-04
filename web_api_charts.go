package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/instruments"
	"go-stock/backend/marketdata"
)

type instrumentChartAPI interface {
	Chart(context.Context, data.ChartRequest) marketdata.DataEnvelope[data.ChartData]
	GetDrawings(context.Context, data.ChartDrawingScope) (data.ChartDrawingDocument, error)
	PutDrawings(context.Context, data.ChartDrawingScope, int64, []data.ChartDrawing) (data.ChartDrawingDocument, error)
	DeleteDrawings(context.Context, data.ChartDrawingScope, int64) (data.ChartDrawingDocument, error)
}

var instrumentChartServiceFactory = func() instrumentChartAPI { return data.NewChartService() }

type putChartDrawingsRequest struct {
	ExpectedRevision *int64              `json:"expectedRevision"`
	AssetType        string              `json:"assetType"`
	Market           string              `json:"market"`
	Period           string              `json:"period"`
	Adjustment       string              `json:"adjustment"`
	Drawings         []data.ChartDrawing `json:"drawings"`
}

// registerInstrumentChartRoutes is mounted centrally by web_api.go. Keeping
// all three drawing verbs on the exact same pattern also makes the generated
// route inventory deterministic.
func registerInstrumentChartRoutes(mux *http.ServeMux, _ *App) {
	service := instrumentChartServiceFactory()
	mux.HandleFunc("GET /api/v1/instruments/{code}/chart", func(w http.ResponseWriter, r *http.Request) {
		request, ok := instrumentChartRequest(w, r, true)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, service.Chart(r.Context(), request))
	})
	mux.HandleFunc("GET /api/v1/instruments/{code}/drawings", func(w http.ResponseWriter, r *http.Request) {
		scope, ok := instrumentDrawingScope(w, r)
		if !ok {
			return
		}
		item, err := service.GetDrawings(r.Context(), scope)
		writeChartDrawingResult(w, item, err)
	})
	mux.HandleFunc("PUT /api/v1/instruments/{code}/drawings", func(w http.ResponseWriter, r *http.Request) {
		var body putChartDrawingsRequest
		if !decodeAPIRequest(w, r, &body) {
			return
		}
		if body.ExpectedRevision == nil || body.Drawings == nil || strings.TrimSpace(body.AssetType) == "" || strings.TrimSpace(body.Market) == "" || strings.TrimSpace(body.Period) == "" || strings.TrimSpace(body.Adjustment) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "assetType, market, period, adjustment, expectedRevision and drawings are required"})
			return
		}
		scope, ok := instrumentDrawingScopeValues(w, r.PathValue("code"), body.AssetType, body.Market, body.Period, body.Adjustment)
		if !ok {
			return
		}
		item, err := service.PutDrawings(r.Context(), scope, *body.ExpectedRevision, body.Drawings)
		writeChartDrawingResult(w, item, err)
	})
	mux.HandleFunc("DELETE /api/v1/instruments/{code}/drawings", func(w http.ResponseWriter, r *http.Request) {
		scope, ok := instrumentDrawingScope(w, r)
		if !ok {
			return
		}
		rawRevision := strings.TrimSpace(r.URL.Query().Get("expectedRevision"))
		if rawRevision == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "expectedRevision is required"})
			return
		}
		revision, err := strconv.ParseInt(rawRevision, 10, 64)
		if err != nil || revision < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "expectedRevision must be a non-negative integer"})
			return
		}
		item, err := service.DeleteDrawings(r.Context(), scope, revision)
		writeChartDrawingResult(w, item, err)
	})
}

func instrumentDrawingScope(w http.ResponseWriter, r *http.Request) (data.ChartDrawingScope, bool) {
	return instrumentDrawingScopeValues(w, r.PathValue("code"), r.URL.Query().Get("assetType"), r.URL.Query().Get("market"),
		r.URL.Query().Get("period"), r.URL.Query().Get("adjustment"))
}

func instrumentDrawingScopeValues(w http.ResponseWriter, code, assetType, market, period, adjustment string) (data.ChartDrawingScope, bool) {
	if strings.TrimSpace(period) == "" || strings.TrimSpace(adjustment) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "period and adjustment are required"})
		return data.ChartDrawingScope{}, false
	}
	if strings.TrimSpace(assetType) == "" {
		assetType = "stock"
	}
	instrument, err := instruments.ParseInstrumentID(code, assetType, market)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return data.ChartDrawingScope{}, false
	}
	request, err := data.NormalizeChartRequest(data.ChartRequest{Instrument: instrument, Period: period, Adjustment: adjustment, Limit: 500}, time.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return data.ChartDrawingScope{}, false
	}
	return data.ChartDrawingScope{ScopeType: "user", ScopeID: "local", Request: request}, true
}

func instrumentChartRequest(w http.ResponseWriter, r *http.Request, includeRange bool) (data.ChartRequest, bool) {
	assetType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("assetType")))
	if assetType == "" {
		assetType = "stock"
	}
	instrument, err := instruments.ParseInstrumentID(r.PathValue("code"), assetType, r.URL.Query().Get("market"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return data.ChartRequest{}, false
	}
	limit, err := queryBoundedInt(r, "limit", 500, 1, 5000)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return data.ChartRequest{}, false
	}
	request := data.ChartRequest{Instrument: instrument, Period: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period"))),
		Adjustment: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("adjustment"))), Limit: limit}
	if includeRange {
		request.From, err = parseOptionalChartTime(r.URL.Query().Get("from"), false)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid from: " + err.Error()})
			return data.ChartRequest{}, false
		}
		request.To, err = parseOptionalChartTime(r.URL.Query().Get("to"), true)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid to: " + err.Error()})
			return data.ChartRequest{}, false
		}
	}
	normalized, err := data.NormalizeChartRequest(request, time.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return data.ChartRequest{}, false
	}
	return normalized, true
}

func parseOptionalChartTime(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}
	location, _ := time.LoadLocation("Asia/Shanghai")
	if location == nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	parsed, err := time.ParseInLocation(time.DateOnly, raw, location)
	if err != nil {
		return time.Time{}, errors.New("must be RFC3339 or YYYY-MM-DD")
	}
	if endOfDay {
		parsed = parsed.Add(24*time.Hour - time.Nanosecond)
	}
	return parsed, nil
}

func writeChartDrawingResult(w http.ResponseWriter, item data.ChartDrawingDocument, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, item)
	case errors.Is(err, data.ErrDrawingRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
	case errors.Is(err, data.ErrDrawingNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
	case strings.Contains(strings.ToLower(err.Error()), "required"), strings.Contains(strings.ToLower(err.Error()), "invalid"), strings.Contains(strings.ToLower(err.Error()), "unsupported"), strings.Contains(strings.ToLower(err.Error()), "must"):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
}
