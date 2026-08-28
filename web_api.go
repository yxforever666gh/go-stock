package main

import (
	"context"
	"errors"
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
	registerMarketEvidenceRoutes(mux, app)
	registerInstrumentEvidenceRoutes(mux, app)
	registerResearchRoutes(mux, app)
	registerResearch2Routes(mux, app)
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

func writeCommandResult(w http.ResponseWriter, message string, err error) {
	if err == nil {
		writeCommand(w, message)
		return
	}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		status = http.StatusBadRequest
	case errors.Is(err, service.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, service.ErrConflict):
		status = http.StatusConflict
	}
	if strings.TrimSpace(message) == "" {
		message = err.Error()
	}
	writeJSON(w, status, map[string]any{"error": message})
}

func writeBooleanCommand(w http.ResponseWriter, ok bool, successMessage, failureMessage string, failureStatus int) {
	if ok {
		writeCommand(w, successMessage)
		return
	}
	writeJSON(w, failureStatus, map[string]any{"error": failureMessage})
}

func writeResearchResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func registerExportRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("POST /api/v1/exports/config", func(w http.ResponseWriter, r *http.Request) { handleConfigExport(app, w, r) })
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
