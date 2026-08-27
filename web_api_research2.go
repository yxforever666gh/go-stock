package main

import "net/http"

func registerResearch2Routes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/research2/analysis-runs", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := webPage(r)
		items, err := app.listResearch2Runs(r.Context(), limit, offset)
		writeResearchResult(w, items, err)
	})
	mux.HandleFunc("GET /api/v1/research2/analysis-runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getResearch2Run(r.Context(), r.PathValue("id"))
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research2/recommendations", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := webPage(r)
		items, err := app.listResearch2Recommendations(r.Context(), limit, offset)
		writeResearchResult(w, items, err)
	})
	mux.HandleFunc("GET /api/v1/research2/recommendations/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getResearch2Recommendation(r.Context(), r.PathValue("id"))
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research2/account", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getResearch2Account(r.Context())
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research2/account/performance", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getResearch2Performance(r.Context())
		writeResearchResult(w, item, err)
	})
}
