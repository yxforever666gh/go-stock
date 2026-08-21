package main

import (
	"net/http"
	"strings"
)

type stockCodeRequest struct {
	StockCode string `json:"stockCode"`
}

type stockQueryRequest struct {
	Words string `json:"words"`
}

type stockPositionRequest struct {
	Price  float64 `json:"price"`
	Volume int64   `json:"volume"`
}

type stockAlarmRequest struct {
	Value      float64 `json:"value"`
	AlarmPrice float64 `json:"alarmPrice"`
}

type stockSortRequest struct {
	Sort int64 `json:"sort"`
}

func registerStockRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/stocks/search", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Stock.GetStockList(r.URL.Query().Get("key")))
	})
	mux.HandleFunc("POST /api/v1/stocks/query", func(w http.ResponseWriter, r *http.Request) {
		var req stockQueryRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeJSON(w, http.StatusOK, app.services.Stock.SearchStock(req.Words))
	})
	mux.HandleFunc("GET /api/v1/stocks/{code}/snapshot", func(w http.ResponseWriter, r *http.Request) {
		item := app.stockSnapshot(r.PathValue("code"))
		if item == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "followed stock not found"})
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
	mux.HandleFunc("GET /api/v1/stocks/{code}/kline", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Stock.GetStockKLine(r.PathValue("code"), queryInt64(r, "days", 120)))
	})
	mux.HandleFunc("GET /api/v1/stocks/{code}/minute-line", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Stock.GetStockMinutePriceLineData(r.PathValue("code"), r.URL.Query().Get("name")))
	})
	mux.HandleFunc("GET /api/v1/watchlist/stocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Stock.GetFollowList(queryInt(r, "groupId", 0)))
	})
	mux.HandleFunc("POST /api/v1/watchlist/stocks", func(w http.ResponseWriter, r *http.Request) {
		var req stockCodeRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.StockCode) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "stockCode is required"})
			return
		}
		message, err := app.services.Stock.Follow(strings.TrimSpace(req.StockCode))
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("DELETE /api/v1/watchlist/stocks/{code}", func(w http.ResponseWriter, r *http.Request) {
		message, err := app.services.Stock.UnFollow(r.PathValue("code"))
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("PUT /api/v1/watchlist/stocks/{code}/position", func(w http.ResponseWriter, r *http.Request) {
		var req stockPositionRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		message, err := app.services.Stock.SetCostPriceAndVolume(r.PathValue("code"), req.Price, req.Volume)
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("PUT /api/v1/watchlist/stocks/{code}/alarm", func(w http.ResponseWriter, r *http.Request) {
		var req stockAlarmRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		message, err := app.services.Stock.SetAlarmChangePercent(req.Value, req.AlarmPrice, r.PathValue("code"))
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("PUT /api/v1/watchlist/stocks/{code}/sort", func(w http.ResponseWriter, r *http.Request) {
		var req stockSortRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		app.services.Stock.SetStockSort(req.Sort, r.PathValue("code"))
		writeCommand(w, "saved")
	})
}
