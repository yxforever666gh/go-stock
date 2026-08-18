package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"go-stock/internal/releaseinfo"
	"go-stock/internal/service"
)

type webStatusProvider interface {
	SystemVersion(context.Context) releaseinfo.VersionStatus
}

type defaultWebStatusProvider struct{ runtime service.RuntimeService }

func (p defaultWebStatusProvider) SystemVersion(context.Context) releaseinfo.VersionStatus {
	return releaseinfo.SystemVersion()
}

type commandResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type acceptedResponse struct {
	Accepted bool `json:"accepted"`
}

func registerWebV1Routes(mux *http.ServeMux, app *App, hub *WebEventHub, status webStatusProvider, shutdown func()) {
	registerSystemRoutes(mux, app, hub, status, shutdown)
	registerSettingsRoutes(mux, app)
	registerGroupRoutes(mux, app)
	registerStockRoutes(mux, app)
	registerFundRoutes(mux, app)
	registerMarketRoutes(mux, app)
	registerAIRoutes(mux, app)
	registerResearchRoutes(mux, app)
	registerExportRoutes(mux, app)
}

func decodeAPIRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := decodeJSONRequest(w, r, target, maxAPIRequestBodyBytes, false); err != nil {
		writeRequestError(w, err)
		return false
	}
	return true
}

func queryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(key)))
	if err != nil {
		return fallback
	}
	return value
}

func queryInt64(r *http.Request, key string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get(key)), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func webPage(r *http.Request) (int, int) {
	return normalizedPage(queryInt(r, "limit", 50), queryInt(r, "offset", 0))
}

func writeCommand(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusOK, commandResponse{OK: true, Message: message})
}

func writeResearchResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func registerExportRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("POST /api/v1/exports/markdown", func(w http.ResponseWriter, r *http.Request) { handleMarkdownExport(app, w, r) })
	mux.HandleFunc("POST /api/v1/exports/config", func(w http.ResponseWriter, r *http.Request) { handleConfigExport(app, w, r) })
	mux.HandleFunc("POST /api/v1/exports/image", handleImageExport)
	mux.HandleFunc("POST /api/v1/exports/word", handleWordExport)
}

func handleMarkdownExport(app *App, w http.ResponseWriter, r *http.Request) {
	var req markdownExportRequest
	if err := decodeJSONRequest(w, r, &req, maxExportRequestBodyBytes, false); err != nil {
		writeRequestError(w, err)
		return
	}
	req.StockCode = strings.TrimSpace(req.StockCode)
	req.StockName = strings.TrimSpace(req.StockName)
	if req.StockCode == "" || req.StockName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "stockCode and stockName are required"})
		return
	}
	mode, err := normalizeExportMode(req.Mode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	res := app.services.AI.GetAIResponseResult(app.ctx, req.StockCode)
	if res == nil || len(res.Content) <= 100 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "analysis result is unavailable"})
		return
	}
	analysisTime := res.CreatedAt.Format("2006-01-02_15_04_05")
	filename := sanitizeFilename(fmt.Sprintf("%s[%s]AI-analysis_%s.md", req.StockName, req.StockCode, analysisTime), ".md")
	writeExport(w, mode, filename, "text/markdown; charset=utf-8", []byte(res.Content))
}

func handleConfigExport(app *App, w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := decodeJSONRequest(w, r, &req, maxExportRequestBodyBytes, true); err != nil {
		writeRequestError(w, err)
		return
	}
	mode, err := normalizeExportMode(req.Mode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeExport(w, mode, "config.json", "application/json", []byte(app.services.Config.ExportConfig()))
}

func handleImageExport(w http.ResponseWriter, r *http.Request) {
	var req imageExportRequest
	if err := decodeJSONRequest(w, r, &req, maxExportRequestBodyBytes, false); err != nil {
		writeRequestError(w, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Base64Data = strings.TrimSpace(req.Base64Data)
	if req.Name == "" || req.Base64Data == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name and base64Data are required"})
		return
	}
	mode, err := normalizeExportMode(req.Mode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	payload, err := base64.StdEncoding.DecodeString(req.Base64Data)
	if err != nil || len(payload) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid file content"})
		return
	}
	writeExport(w, mode, sanitizeFilename(req.Name+"AI-analysis.png", ".png"), "image/png", payload)
}

func handleWordExport(w http.ResponseWriter, r *http.Request) {
	var req wordExportRequest
	if err := decodeJSONRequest(w, r, &req, maxExportRequestBodyBytes, false); err != nil {
		writeRequestError(w, err)
		return
	}
	req.Filename = strings.TrimSpace(req.Filename)
	req.Base64Data = strings.TrimSpace(req.Base64Data)
	if req.Filename == "" || req.Base64Data == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "filename and base64Data are required"})
		return
	}
	mode, err := normalizeExportMode(req.Mode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	payload, err := base64.StdEncoding.DecodeString(req.Base64Data)
	if err != nil || len(payload) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid file content"})
		return
	}
	writeExport(w, mode, sanitizeFilename(req.Filename, ".docx"), "application/vnd.openxmlformats-officedocument.wordprocessingml.document", payload)
}
