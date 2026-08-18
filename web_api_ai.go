package main

import "net/http"

type chatRunRequest struct {
	Stock       string `json:"stock"`
	StockCode   string `json:"stockCode"`
	Question    string `json:"question"`
	AIConfigID  int    `json:"aiConfigId"`
	SysPromptID *int   `json:"sysPromptId"`
	EnableTools bool   `json:"enableTools"`
	Think       bool   `json:"think"`
}

type aiResponseSaveRequest struct {
	StockCode  string `json:"stockCode"`
	StockName  string `json:"stockName"`
	Result     string `json:"result"`
	ChatID     string `json:"chatId"`
	Question   string `json:"question"`
	AIConfigID int    `json:"aiConfigId"`
}

type shareAnalysisRequest struct {
	StockName string `json:"stockName"`
}

func registerAIRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("POST /api/v1/ai/chat-runs", func(w http.ResponseWriter, r *http.Request) {
		var req chatRunRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		go app.runChatStream(req.Stock, req.StockCode, req.Question, req.AIConfigID, req.SysPromptID, req.EnableTools, req.Think)
		writeJSON(w, http.StatusAccepted, acceptedResponse{Accepted: true})
	})
	mux.HandleFunc("GET /api/v1/ai/responses/{stockCode}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.AI.GetAIResponseResult(app.ctx, r.PathValue("stockCode")))
	})
	mux.HandleFunc("POST /api/v1/ai/responses", func(w http.ResponseWriter, r *http.Request) {
		var req aiResponseSaveRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		app.services.AI.SaveAIResponseResult(app.ctx, req.StockCode, req.StockName, req.Result, req.ChatID, req.Question, req.AIConfigID)
		writeCommand(w, "saved")
	})
	mux.HandleFunc("POST /api/v1/ai/responses/{stockCode}/share", func(w http.ResponseWriter, r *http.Request) {
		var req shareAnalysisRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeCommand(w, app.shareAnalysis(r.PathValue("stockCode"), req.StockName))
	})
}
