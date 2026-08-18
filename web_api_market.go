package main

import (
	"net/http"

	"github.com/duke-git/lancet/v2/slice"
)

type telegraphRefreshRequest struct {
	Source string `json:"source"`
}

type sentimentRequest struct {
	Text string `json:"text"`
}

func registerMarketRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/market/telegraphs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.GetTelegraphList(r.URL.Query().Get("source")))
	})
	mux.HandleFunc("POST /api/v1/market/telegraphs/refresh", func(w http.ResponseWriter, r *http.Request) {
		var req telegraphRefreshRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeJSON(w, http.StatusOK, app.services.Market.RefreshTelegraphList(req.Source))
	})
	mux.HandleFunc("GET /api/v1/market/indexes/global", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.GlobalStockIndexes())
	})
	mux.HandleFunc("GET /api/v1/market/industries/rank", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.GetIndustryRank(r.URL.Query().Get("sort"), queryInt(r, "count", 20)))
	})
	mux.HandleFunc("GET /api/v1/market/industries/money-rank", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.GetIndustryMoneyRankSina(r.URL.Query().Get("category"), r.URL.Query().Get("sort")))
	})
	mux.HandleFunc("GET /api/v1/market/stocks/money-rank", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.GetMoneyRankSina(r.URL.Query().Get("sort")))
	})
	mux.HandleFunc("GET /api/v1/market/stocks/{code}/money-trend", func(w http.ResponseWriter, r *http.Request) {
		rows := app.services.Market.GetStockMoneyTrendByDay(r.PathValue("code"), queryInt(r, "days", 10))
		slice.Reverse(rows)
		writeJSON(w, http.StatusOK, rows)
	})
	mux.HandleFunc("GET /api/v1/market/long-tiger", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.LongTigerRank(r.URL.Query().Get("date")))
	})
	mux.HandleFunc("GET /api/v1/market/stocks/research-reports", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.StockResearchReport("", 7))
	})
	mux.HandleFunc("GET /api/v1/market/stocks/{code}/research-reports", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.StockResearchReport(r.PathValue("code"), 7))
	})
	mux.HandleFunc("GET /api/v1/market/stocks/notices", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.StockNotice(""))
	})
	mux.HandleFunc("GET /api/v1/market/stocks/{code}/notices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.StockNotice(r.PathValue("code")))
	})
	mux.HandleFunc("GET /api/v1/market/industries/research-reports", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.IndustryResearchReport("", 7))
	})
	mux.HandleFunc("GET /api/v1/market/industries/{code}/research-reports", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.IndustryResearchReport(r.PathValue("code"), 7))
	})
	mux.HandleFunc("GET /api/v1/market/dictionary", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.EMDictCode(r.URL.Query().Get("code"), app.cache))
	})
	mux.HandleFunc("POST /api/v1/market/sentiment/weighted", func(w http.ResponseWriter, r *http.Request) {
		var req sentimentRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeJSON(w, http.StatusOK, app.services.AI.AnalyzeSentimentWithFreqWeight(req.Text))
	})
	mux.HandleFunc("GET /api/v1/market/hot/stocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.HotStock(r.URL.Query().Get("marketType"), 100))
	})
	mux.HandleFunc("GET /api/v1/market/hot/events", func(w http.ResponseWriter, r *http.Request) {
		size := queryInt(r, "size", 10)
		if size <= 0 {
			size = 10
		}
		writeJSON(w, http.StatusOK, app.services.Market.HotEvent(size))
	})
	mux.HandleFunc("GET /api/v1/market/hot/topics", func(w http.ResponseWriter, r *http.Request) {
		size := queryInt(r, "size", 10)
		if size <= 0 {
			size = 10
		}
		writeJSON(w, http.StatusOK, app.services.Market.HotTopic(size))
	})
	mux.HandleFunc("GET /api/v1/market/calendars/investment", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.InvestCalendar(r.URL.Query().Get("yearMonth")))
	})
	mux.HandleFunc("GET /api/v1/market/calendars/cls", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Market.ClsCalendar())
	})
}
