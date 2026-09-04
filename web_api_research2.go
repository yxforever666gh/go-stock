package main

import (
	"errors"
	"net/http"
	"time"

	"go-stock/internal/recommendationchart"
)

func registerResearch2Routes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("POST /api/v1/research2/email/test", func(w http.ResponseWriter, r *http.Request) {
		writeCommandResult(w, "研究中心2测试邮件发送成功", app.testResearch2Email(r.Context()))
	})
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
	mux.HandleFunc("GET /api/v1/research2/recommendations/{id}/chart", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getResearch2RecommendationChart(r.Context(), r.PathValue("id"), false)
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("POST /api/v1/research2/recommendations/{id}/chart/refresh", func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Minute))
		item, err := app.getResearch2RecommendationChart(r.Context(), r.PathValue("id"), true)
		if errors.Is(err, recommendationchart.ErrRefreshInProgress) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
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
