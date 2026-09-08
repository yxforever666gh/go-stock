package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/models"
	"go-stock/backend/researchconfig"
)

type aiConfigTestRequest struct {
	ID int `json:"id"`
}

func registerSettingsRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/settings", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Config.GetConfig())
	})
	mux.HandleFunc("PUT /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]json.RawMessage
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		current, err := mergeGlobalSettings(app.services.Config.GetConfig(), req)
		if err != nil {
			writeResearchConfigError(w, err)
			return
		}
		message, err := app.updateConfig(current)
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("GET /api/v1/ai/configs", func(w http.ResponseWriter, r *http.Request) {
		if models, ok := app.scopedAIModels(w, r); ok {
			writeJSON(w, http.StatusOK, models)
		}
	})
	mux.HandleFunc("POST /api/v1/ai/configs/test", func(w http.ResponseWriter, r *http.Request) {
		var req aiConfigTestRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		center := r.URL.Query().Get("center")
		if center == "" || center == researchconfig.Global {
			writeJSON(w, http.StatusOK, app.services.AI.TestAIConfig(r.Context(), req.ID))
			return
		}
		snapshot, err := app.researchConfiguration(r.Context(), center)
		if err != nil {
			writeResearchConfigError(w, err)
			return
		}
		model, err := researchModel(snapshot, req.ID)
		if err != nil {
			writeResearchConfigError(w, err)
			return
		}
		provider := data.NewOpenAiWithSettings(r.Context(), model, snapshot.Settings)
		started := time.Now()
		content, _, name, callErr := provider.CompleteChat([]map[string]any{{"role": "user", "content": "请只回复 OK"}}, false)
		result := &models.AIModelTestResult{Success: callErr == nil && strings.TrimSpace(content) != "", Protocol: model.ApiProtocol, Model: name}
		result.LatencyMs = time.Since(started).Milliseconds()
		if callErr != nil {
			result.Message = callErr.Error()
		} else if !result.Success {
			result.Message = "模型返回内容为空"
		} else {
			result.Message = "测试成功"
			runes := []rune(content)
			result.ContentPreview = string(runes[:min(len(runes), 120)])
		}
		writeJSON(w, http.StatusOK, result)
	})
}

func mergeGlobalSettings(current *models.SettingConfig, changes map[string]json.RawMessage) (*models.SettingConfig, error) {
	if current == nil || current.Settings == nil || changes == nil {
		return nil, researchconfig.ErrInvalidConfig
	}
	forbidden := func(key string) bool {
		return strings.HasPrefix(key, "research2") || strings.HasPrefix(key, "aiCapital") || strings.HasPrefix(key, "aiTarget") || strings.HasPrefix(key, "aiMaxImmediate") || strings.HasPrefix(key, "aiReanalysis") || strings.HasPrefix(key, "aiReview") || strings.HasPrefix(key, "aiAnalysis") || key == "experimentalEvidenceEnabled"
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	var merged map[string]json.RawMessage
	if err = json.Unmarshal(raw, &merged); err != nil {
		return nil, err
	}
	for key, value := range changes {
		if forbidden(key) {
			return nil, fmt.Errorf("%w: 研究配置已独立保存，请刷新页面", researchconfig.ErrInvalidConfig)
		}
		if _, ok := merged[key]; !ok || string(value) == "null" {
			return nil, fmt.Errorf("%w: field %s", researchconfig.ErrInvalidConfig, key)
		}
		merged[key] = value
	}
	raw, err = json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	var result models.SettingConfig
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%w: %v", researchconfig.ErrInvalidConfig, err)
	}
	// A UI-only save must not rewrite model metadata or archive any rows.
	if _, hasModels := changes["aiConfigs"]; !hasModels {
		result.AiConfigs = nil
	}
	return &result, nil
}
