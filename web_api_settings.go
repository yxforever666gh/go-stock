package main

import (
	"net/http"

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
		message, err := app.updateConfig(&req)
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("GET /api/v1/ai/configs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.AI.GetAIConfigs())
	})
	mux.HandleFunc("POST /api/v1/ai/configs/test", func(w http.ResponseWriter, r *http.Request) {
		var req aiConfigTestRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeJSON(w, http.StatusOK, app.services.AI.TestAIConfig(r.Context(), req.ID))
	})
}
