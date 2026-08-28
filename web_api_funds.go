package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/funds"
	"go-stock/backend/models"
)

type fundCodeRequest struct {
	FundCode string `json:"fundCode"`
}

type etfWatchlistRequest struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Market   string `json:"market"`
	Category string `json:"category"`
}

func registerFundRoutes(mux *http.ServeMux, app *App) {
	fundMarket := funds.NewProductionService()
	mux.HandleFunc("GET /api/v1/funds/search", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Fund.GetFundList(r.URL.Query().Get("key")))
	})
	mux.HandleFunc("GET /api/v1/funds/rankings", func(w http.ResponseWriter, r *http.Request) {
		query, err := funds.NormalizeFundRankingQuery(funds.FundRankingQuery{
			Category: funds.FundCategory(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category")))),
			Period:   funds.FundPeriod(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period")))),
			Q:        r.URL.Query().Get("q"), SortDirection: funds.SortDirection(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sortDirection")))),
			Page: queryInt(r, "page", 0), PageSize: queryInt(r, "pageSize", 0),
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		writeJSON(w, http.StatusOK, fundMarket.FundRankings(ctx, query))
	})
	mux.HandleFunc("GET /api/v1/etfs/rankings", func(w http.ResponseWriter, r *http.Request) {
		query, err := funds.NormalizeETFQuery(funds.ETFQuery{
			Category: funds.ETFCategory(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category")))),
			Q:        r.URL.Query().Get("q"), Sort: funds.ETFSort(strings.TrimSpace(r.URL.Query().Get("sort"))),
			SortDirection: funds.SortDirection(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sortDirection")))),
			Page:          queryInt(r, "page", 0), PageSize: queryInt(r, "pageSize", 0),
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		writeJSON(w, http.StatusOK, fundMarket.ETFRankings(ctx, query))
	})
	mux.HandleFunc("GET /api/v1/etfs/search", func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		limit := queryInt(r, "limit", 20)
		if q == "" || limit < 1 || limit > 50 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "q is required and limit must be between 1 and 50"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		writeJSON(w, http.StatusOK, fundMarket.ETFSearch(ctx, q, limit))
	})
	mux.HandleFunc("GET /api/v1/etfs/{code}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		result := fundMarket.ETFDetail(ctx, r.PathValue("code"))
		for _, item := range result.Errors {
			switch item.Code {
			case "validation":
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": item.Message})
				return
			case "not_found":
				writeJSON(w, http.StatusNotFound, map[string]any{"error": item.Message})
				return
			}
		}
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("GET /api/v1/watchlist/funds", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Fund.GetFollowedFund())
	})
	mux.HandleFunc("POST /api/v1/watchlist/funds", func(w http.ResponseWriter, r *http.Request) {
		var req fundCodeRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.FundCode) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "fundCode is required"})
			return
		}
		message, err := app.services.Fund.FollowFund(strings.TrimSpace(req.FundCode))
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("DELETE /api/v1/watchlist/funds/{code}", func(w http.ResponseWriter, r *http.Request) {
		message, err := app.services.Fund.UnFollowFund(r.PathValue("code"))
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("GET /api/v1/watchlist/etfs", func(w http.ResponseWriter, _ *http.Request) {
		items, err := app.services.Fund.GetFollowedETFs()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, items)
	})
	mux.HandleFunc("POST /api/v1/watchlist/etfs", func(w http.ResponseWriter, r *http.Request) {
		var req etfWatchlistRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.Market = strings.ToUpper(strings.TrimSpace(req.Market))
		req.Category = strings.ToLower(strings.TrimSpace(req.Category))
		instrument, instrumentErr := data.ParseInstrumentID(req.Code, "etf", req.Market)
		if instrumentErr != nil || req.Name == "" || !validETFCategory(req.Category) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "code, name, SH/SZ market and a valid ETF category are required"})
			return
		}
		message, err := app.services.Fund.FollowETF(models.ETFWatchlistItem{Code: instrument.Code, Name: req.Name, Market: instrument.Market, Category: req.Category})
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("DELETE /api/v1/watchlist/etfs/{code}", func(w http.ResponseWriter, r *http.Request) {
		code, ok := data.NormalizeETFCode(r.PathValue("code"))
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid ETF code"})
			return
		}
		message, err := app.services.Fund.UnFollowETF(code)
		writeCommandResult(w, message, err)
	})
}

func validETFCategory(category string) bool {
	switch category {
	case "broad", "industry", "cross_border", "bond", "commodity", "money":
		return true
	default:
		return false
	}
}
