package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/marketdata"
)

var marketEvidenceServiceFactory = data.NewMarketEvidenceService
var marketHotWordsServiceFactory = data.NewMarketHotWordsService

// registerMarketEvidenceRoutes is intentionally isolated so the 2.0 route
// surface can be mounted in one line from the central router.
func registerMarketEvidenceRoutes(mux *http.ServeMux, _ *App) {
	service := marketEvidenceServiceFactory()
	hotWordsService := marketHotWordsServiceFactory()
	mux.HandleFunc("GET /api/v1/market/hot/words", func(w http.ResponseWriter, r *http.Request) {
		hours, err := queryBoundedInt(r, "hours", 24, 1, 72)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence(data.HotWordsData{Items: []data.HotWordItem{}}, "validation", "invalid_hours", err.Error()))
			return
		}
		baselineDays, err := queryBoundedInt(r, "baselineDays", 7, 3, 30)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence(data.HotWordsData{Items: []data.HotWordItem{}}, "validation", "invalid_baseline_days", err.Error()))
			return
		}
		limit, err := queryBoundedInt(r, "limit", 30, 1, 100)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence(data.HotWordsData{Items: []data.HotWordItem{}}, "validation", "invalid_limit", err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, hotWordsService.HotWords(r.Context(), data.HotWordsQuery{Hours: hours, BaselineDays: baselineDays, Limit: limit}))
	})
	mux.HandleFunc("GET /api/v1/market/breadth", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, service.Breadth(r.Context()))
	})
	mux.HandleFunc("GET /api/v1/market/fund-flows", func(w http.ResponseWriter, r *http.Request) {
		scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
		if scope != "sector" && scope != "concept" {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence([]data.FundFlowRow{}, "validation", "invalid_scope", "scope 必须是 sector 或 concept"))
			return
		}
		date, ok := optionalEvidenceDate(w, r, []data.FundFlowRow{})
		if !ok {
			return
		}
		sortBy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))
		if sortBy == "" {
			sortBy = "netamount"
		}
		allowedSort := map[string]bool{"netamount": true, "inamount": true, "outamount": true, "avg_changeratio": true}
		if !allowedSort[sortBy] {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence([]data.FundFlowRow{}, "validation", "invalid_sort", "sort 不受支持"))
			return
		}
		limit, err := queryBoundedInt(r, "limit", 20, 1, 100)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence([]data.FundFlowRow{}, "validation", "invalid_limit", err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, service.FundFlows(r.Context(), marketdata.ProviderRequest{Scope: scope, Date: date, Sort: sortBy, Limit: limit}))
	})
	mux.HandleFunc("GET /api/v1/market/futures/positions", func(w http.ResponseWriter, r *http.Request) {
		symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
		if !map[string]bool{"IF": true, "IH": true, "IC": true, "IM": true}[symbol] {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence(data.FuturesPositionsData{Rows: []data.FuturesPositionRow{}}, "validation", "invalid_symbol", "symbol 必须是 IF、IH、IC 或 IM"))
			return
		}
		date, ok := optionalEvidenceDate(w, r, data.FuturesPositionsData{Variety: symbol, Rows: []data.FuturesPositionRow{}})
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, service.FuturesPositions(r.Context(), marketdata.ProviderRequest{Symbol: symbol, Date: date}))
	})
	mux.HandleFunc("GET /api/v1/market/margin", func(w http.ResponseWriter, r *http.Request) {
		scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
		if scope == "" {
			scope = "market"
		}
		if scope != "market" && scope != "security" {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence(data.MarginData{Scope: scope, Rows: []data.MarginRow{}}, "validation", "invalid_scope", "scope 必须是 market 或 security"))
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if scope == "security" && code == "" {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence(data.MarginData{Scope: scope, Rows: []data.MarginRow{}}, "validation", "missing_code", "security scope 必须提供 code"))
			return
		}
		if scope == "security" {
			normalized, valid := data.NormalizeInstrumentID(code, "stock")
			if !valid {
				normalized, valid = data.NormalizeInstrumentID(code, "etf")
			}
			if !valid {
				writeJSON(w, http.StatusBadRequest, badMarketEvidence(data.MarginData{Scope: scope, Rows: []data.MarginRow{}}, "validation", "invalid_code", "code 必须是沪深股票或场内 ETF 代码"))
				return
			}
			code = normalized
		}
		date, ok := optionalEvidenceDate(w, r, data.MarginData{Scope: scope, Rows: []data.MarginRow{}})
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, service.Margin(r.Context(), marketdata.ProviderRequest{Scope: scope, Code: code, Date: date}))
	})
}

func badMarketEvidence[T any](dataValue T, provider, code, message string) marketdata.DataEnvelope[T] {
	return marketdata.DataEnvelope[T]{Data: dataValue, Source: provider, AsOf: time.Time{}, FetchedAt: time.Now(), Status: marketdata.StatusUnavailable, Errors: []marketdata.DataError{{Provider: provider, Code: code, Message: message}}}
}

func optionalEvidenceDate[T any](w http.ResponseWriter, r *http.Request, empty T) (string, bool) {
	value := strings.TrimSpace(r.URL.Query().Get("date"))
	if value == "" {
		return "", true
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		writeJSON(w, http.StatusBadRequest, badMarketEvidence(empty, "validation", "invalid_date", "date 必须是 YYYY-MM-DD"))
		return "", false
	}
	return value, true
}

func queryBoundedInt(r *http.Request, name string, fallback, minimum, maximum int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, &evidenceQueryError{name: name, minimum: minimum, maximum: maximum}
	}
	return value, nil
}

type evidenceQueryError struct {
	name             string
	minimum, maximum int
}

func (e *evidenceQueryError) Error() string {
	return e.name + " 必须介于 " + strconv.Itoa(e.minimum) + " 和 " + strconv.Itoa(e.maximum) + " 之间"
}
