package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"go-stock/backend/data"
	"go-stock/backend/models"
	"go-stock/backend/researchconfig"
)

type researchSettingsPayload struct {
	Revision  int64              `json:"revision"`
	Config    json.RawMessage    `json:"config"`
	AIConfigs []*models.AIConfig `json:"aiConfigs"`
}

func writeResearchSettings(w http.ResponseWriter, snapshot researchconfig.Snapshot, err error) {
	if err == nil {
		var raw []byte
		raw, err = researchconfig.ConfigJSON(snapshot.Center, snapshot.Settings)
		if err == nil {
			writeJSON(w, http.StatusOK, researchSettingsPayload{snapshot.Revision, raw, snapshot.Settings.AiConfigs})
			return
		}
	}
	writeResearchConfigError(w, err)
}

func writeResearchConfigError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, researchconfig.ErrConflict) {
		status = http.StatusConflict
	} else if errors.Is(err, researchconfig.ErrInvalidCenter) || errors.Is(err, researchconfig.ErrInvalidConfig) || errors.Is(err, researchconfig.ErrModelOwnership) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func registerResearchSettingsRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/research-centers/{center}/settings", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := app.researchConfiguration(r.Context(), r.PathValue("center"))
		writeResearchSettings(w, snapshot, err)
	})
	mux.HandleFunc("PUT /api/v1/research-centers/{center}/settings", func(w http.ResponseWriter, r *http.Request) {
		var request researchSettingsPayload
		if !decodeAPIRequest(w, r, &request) {
			return
		}
		if request.AIConfigs == nil {
			writeResearchConfigError(w, researchconfig.ErrInvalidConfig)
			return
		}
		center := r.PathValue("center")
		cfg, err := researchconfig.DecodeConfig(center, request.Config)
		if err != nil {
			writeResearchConfigError(w, err)
			return
		}
		cfg.AiConfigs = request.AIConfigs
		snapshot, err := app.saveResearchConfiguration(r.Context(), center, request.Revision, cfg)
		writeResearchSettings(w, snapshot, err)
	})
}

func (a *App) scopedAIModels(w http.ResponseWriter, r *http.Request) ([]*models.AIConfig, bool) {
	center := r.URL.Query().Get("center")
	if center == "" || center == researchconfig.Global {
		return a.services.AI.GetAIConfigs(), true
	}
	snapshot, err := a.researchConfiguration(r.Context(), center)
	if err != nil {
		writeResearchConfigError(w, err)
		return nil, false
	}
	return data.EnabledAIConfigs(snapshot.Settings.AiConfigs), true
}
