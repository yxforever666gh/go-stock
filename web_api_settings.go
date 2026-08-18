package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"go-stock/backend/models"
)

type aiConfigTestRequest struct {
	ID int `json:"id"`
}

func registerSettingsRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/settings", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Config.GetConfig())
	})
	mux.HandleFunc("PUT /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		var req models.SettingConfig
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeCommand(w, app.updateConfig(&req))
	})
	mux.HandleFunc("GET /api/v1/ai/configs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.AI.GetAiConfigs())
	})
	mux.HandleFunc("POST /api/v1/ai/configs/test", func(w http.ResponseWriter, r *http.Request) {
		var req aiConfigTestRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeJSON(w, http.StatusOK, app.services.AI.TestAIConfig(context.Background(), req.ID))
	})
	mux.HandleFunc("GET /api/v1/ai/prompts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.AI.GetPromptTemplates(r.URL.Query().Get("name"), r.URL.Query().Get("type")))
	})
	mux.HandleFunc("POST /api/v1/ai/prompts", func(w http.ResponseWriter, r *http.Request) {
		var req models.Prompt
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeCommand(w, app.addPrompt(req))
	})
	mux.HandleFunc("DELETE /api/v1/ai/prompts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(strings.TrimSpace(r.PathValue("id")), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid prompt id"})
			return
		}
		writeCommand(w, app.services.AI.DelPrompt(uint(id)))
	})
}
