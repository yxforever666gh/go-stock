package main

import (
	"errors"
	"net/http"
	"time"

	"go-stock/backend/research2"
	"go-stock/internal/recommendationchart"
)

func registerResearch2Routes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("POST /api/v1/research2/analysis-runs/{id}/rerun", func(w http.ResponseWriter, r *http.Request) {
		if !app.research2RunMu.TryLock() {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "研究中心2正在运行"})
			return
		}
		defer app.research2RunMu.Unlock()
		setting := app.loadResearch2Settings()
		if setting == nil || !setting.Research2AutoEnabled || !withinResearch2RecoveryWindow(time.Now()) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "自动研究未开启或不在允许启动窗口"})
			return
		}
		runtime, err := app.ensureResearch2Runtime(setting)
		if err != nil {
			writeResearchResult(w, nil, err)
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(20 * time.Minute))
		run, err := runtime.Runner.Rerun(app.ctx, research2ScheduledRoot(time.Now()), r.PathValue("id"))
		if errors.Is(err, research2.ErrExecutionChainClosed) || errors.Is(err, research2.ErrDailyBuyLimitReached) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		if err == nil {
			app.processResearch2Trades(time.Now())
		}
		if run.ChainID != "" {
			if chain, chainErr := runtime.Repository.ExecutionChain(app.ctx, run.ChainID); chainErr == nil {
				app.queueResearch2FinalEmail(runtime, chain)
			}
		}
		writeResearchResult(w, run, err)
	})
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
