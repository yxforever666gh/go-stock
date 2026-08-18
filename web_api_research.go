package main

import "net/http"

func registerResearchRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/research/analysis-runs", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := webPage(r)
		items, err := app.listAIAnalysisReports(limit, offset)
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
		item, err := app.getAIAnalysisReport(r.PathValue("id"))
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research/recommendations", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := webPage(r)
		items, err := app.listAIRecommendations(limit, offset)
		writeResearchResult(w, items, err)
	})
	mux.HandleFunc("GET /api/v1/research/recommendations/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getAIRecommendation(r.PathValue("id"))
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research/account", func(w http.ResponseWriter, _ *http.Request) {
		item, err := app.getAISimulatedAccount()
		writeResearchResult(w, item, err)
	})
}
