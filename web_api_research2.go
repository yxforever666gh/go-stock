package main

import (
	"errors"
	"net/http"
	"time"

	"go-stock/backend/research2"
	"go-stock/internal/recommendationchart"
)

func registerResearch2Routes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/research2/slots", func(w http.ResponseWriter, r *http.Request) {
		repository, err := app.research2Repository()
		if err != nil {
			writeResearchResult(w, nil, err)
			return
		}
		states, err := repository.SlotStatuses(r.Context(), time.Now())
		writeResearchResult(w, states, err)
	})
	mux.HandleFunc("POST /api/v1/research2/analysis-runs/{id}/rerun", func(w http.ResponseWriter, r *http.Request) {
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
		parent, parentErr := runtime.Repository.GetRun(r.Context(), r.PathValue("id"))
		if parentErr != nil {
			writeResearchResult(w, nil, parentErr)
			return
		}
		run, err := runtime.Runner.Rerun(app.ctx, parent.ScheduledFor, r.PathValue("id"))
		if errors.Is(err, research2.ErrExecutionChainClosed) || errors.Is(err, research2.ErrDailyBuyLimitReached) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		if err == nil {
			app.processResearch2Trades(time.Now())
		}

		writeResearchResult(w, run, err)
	})
	mux.HandleFunc("POST /api/v1/research2/email/test", func(w http.ResponseWriter, r *http.Request) {
		writeCommandResult(w, "研究中心2测试邮件发送成功", app.testResearch2Email(r.Context()))
	})
	mux.HandleFunc("GET /api/v1/research2/analysis-runs", func(w http.ResponseWriter, r *http.Request) {
		slot, ok := research2RequestSlot(w, r)
		if !ok {
			return
		}
		limit, offset := webPage(r)
		items, err := app.listResearch2Runs(r.Context(), limit, offset, slot)
		writeResearchResult(w, items, err)
	})
	mux.HandleFunc("GET /api/v1/research2/analysis-runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := app.getResearch2Run(r.Context(), r.PathValue("id"))
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research2/recommendations", func(w http.ResponseWriter, r *http.Request) {
		slot, ok := research2RequestSlot(w, r)
		if !ok {
			return
		}
		limit, offset := webPage(r)
		items, err := app.listResearch2Recommendations(r.Context(), limit, offset, slot)
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
		slot, ok := research2RequestSlot(w, r)
		if !ok {
			return
		}
		item, err := app.getResearch2Account(r.Context(), slot)
		writeResearchResult(w, item, err)
	})
	mux.HandleFunc("GET /api/v1/research2/account/performance", func(w http.ResponseWriter, r *http.Request) {
		slot, ok := research2RequestSlot(w, r)
		if !ok {
			return
		}
		item, err := app.getResearch2Performance(r.Context(), slot)
		writeResearchResult(w, item, err)
	})
}

func research2RequestSlot(w http.ResponseWriter, r *http.Request) (string, bool) {
	slot := r.URL.Query().Get("slot")
	if slot != "" && !research2.ValidSlot(slot) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效五分钟区间"})
		return "", false
	}
	return slot, true
}
