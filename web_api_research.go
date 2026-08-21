package main

import (
	"errors"
	"net/http"
	"time"

	"go-stock/backend/research"
)

func registerResearchRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/research/analysis-runs", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := webPage(r)
		items, err := app.listAIAnalysisReports(r.Context(), limit, offset)
		writeResearchResult(w, items, err)
	})
	mux.HandleFunc("POST /api/v1/research/analysis-runs", func(w http.ResponseWriter, _ *http.Request) {
		ok, err := app.startManualAIAnalysis()
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, acceptedResponse{Accepted: ok})
	})
	mux.HandleFunc("GET /api/v1/research/analysis-runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getAIAnalysisReport(r.Context(), r.PathValue("id"))
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research/recommendations", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := webPage(r)
		items, err := app.listAIRecommendations(r.Context(), limit, offset)
		writeResearchResult(w, items, err)
	})
	mux.HandleFunc("GET /api/v1/research/recommendations/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getAIRecommendation(r.Context(), r.PathValue("id"))
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research/recommendations/{id}/chart", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getAIRecommendationChart(r.Context(), r.PathValue("id"), false)
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("POST /api/v1/research/recommendations/{id}/chart/refresh", func(w http.ResponseWriter, r *http.Request) {
		// Historical minute providers can need longer than the server-wide write
		// timeout. This is an explicit user refresh and remains cancellable when
		// the browser closes or the application shuts down.
		controller := http.NewResponseController(w)
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Minute))
		item, err := app.getAIRecommendationChart(r.Context(), r.PathValue("id"), true)
		if errors.Is(err, research.ErrChartRefreshInProgress) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research/account", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getAISimulatedAccountContext(r.Context())
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research/account/cash-flows", func(w http.ResponseWriter, r *http.Request) {
		items, err := app.getAIAccountCashFlowsContext(r.Context())
		writeResearchResult(w, items, err)
	})
	mux.HandleFunc("GET /api/v1/research/account/performance", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getAIAccountPerformanceContext(r.Context())
		writeResearchResult(w, item, err)
	})
}
