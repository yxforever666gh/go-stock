package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"go-stock/internal/releaseinfo"
	"go-stock/internal/service"
)

type webStatusProvider interface {
	SystemVersion(context.Context) releaseinfo.VersionStatus
}

type defaultWebStatusProvider struct {
	runtime service.RuntimeService
}

func (p defaultWebStatusProvider) SystemVersion(context.Context) releaseinfo.VersionStatus {
	return releaseinfo.SystemVersion()
}

func registerWebV1Routes(mux *http.ServeMux, app *App, hub *WebEventHub, status webStatusProvider) {
	mux.HandleFunc("/livez", methodHandler(http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/readyz", methodHandler(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		version := status.SystemVersion(r.Context())
		code := http.StatusOK
		if !version.Readiness.Ready {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, version)
	}))
	mux.HandleFunc("/api/v1/system/version", methodHandler(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, status.SystemVersion(r.Context()))
	}))
	mux.HandleFunc("/api/v1/system/health", methodHandler(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		version := status.SystemVersion(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "web", "version": version.AppVersion})
	}))
	mux.HandleFunc("/api/v1/events/ws", methodHandler(http.MethodGet, hub.HandleWS))
	mux.HandleFunc("/api/v1/research/analysis-runs", methodHandler(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		limit, offset := webPage(r)
		items, err := app.ListAIAnalysisReports(limit, offset)
		writeResearchResult(w, items, err)
	}))
	mux.HandleFunc("/api/v1/research/analysis-run", methodHandler(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		item, err := app.GetAIAnalysisReport(r.URL.Query().Get("id"))
		writeResearchResult(w, item, err)
	}))
	mux.HandleFunc("/api/v1/research/recommendations", methodHandler(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		limit, offset := webPage(r)
		items, err := app.ListAIRecommendations(limit, offset)
		writeResearchResult(w, items, err)
	}))
	mux.HandleFunc("/api/v1/research/recommendation", methodHandler(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		item, err := app.GetAIRecommendation(r.URL.Query().Get("id"))
		writeResearchResult(w, item, err)
	}))
	mux.HandleFunc("/api/v1/research/account", methodHandler(http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		item, err := app.GetAISimulatedAccount()
		writeResearchResult(w, item, err)
	}))
	mux.HandleFunc("/api/v1/exports/markdown", methodHandler(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		handleMarkdownExport(app, w, r)
	}))
	mux.HandleFunc("/api/v1/exports/config", methodHandler(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		handleConfigExport(app, w, r)
	}))
	mux.HandleFunc("/api/v1/exports/image", methodHandler(http.MethodPost, handleImageExport))
	mux.HandleFunc("/api/v1/exports/word", methodHandler(http.MethodPost, handleWordExport))
}

func methodHandler(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		next(w, r)
	}
}

func webPage(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	return normalizedPage(limit, offset)
}

func writeResearchResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, value)
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
	config := app.services.Config.ExportConfig()
	writeExport(w, mode, "config.json", "application/json", []byte(config))
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
	filename := sanitizeFilename(req.Name+"AI-analysis.png", ".png")
	writeExport(w, mode, filename, "image/png", payload)
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
	filename := sanitizeFilename(req.Filename, ".docx")
	writeExport(w, mode, filename, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", payload)
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRequest(r) || !hasAllowedOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "local requests only"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackRequest(r *http.Request) bool {
	if r == nil || hasForwardingHeaders(r.Header) || !isLoopbackHost(r.Host) {
		return false
	}
	remoteHost, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		remoteHost = strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
	}
	ip := net.ParseIP(remoteHost)
	return strings.EqualFold(remoteHost, "localhost") || (ip != nil && ip.IsLoopback())
}

func isLoopbackHost(authority string) bool {
	host := hostname(authority)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostname(authority string) string {
	authority = strings.TrimSpace(authority)
	if host, _, err := net.SplitHostPort(authority); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(authority, "[]")
}

func hasForwardingHeaders(header http.Header) bool {
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if strings.TrimSpace(header.Get(name)) != "" {
			return true
		}
	}
	return false
}

func hasAllowedOrigin(r *http.Request) bool {
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" {
		return true
	}
	origin, err := url.Parse(raw)
	if err != nil || origin == nil || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	if !isLoopbackHost(origin.Host) {
		return false
	}
	requestScheme := "http"
	if r.TLS != nil {
		requestScheme = "https"
	}
	return canonicalAuthority(origin.Host, origin.Scheme) == canonicalAuthority(r.Host, requestScheme)
}

func canonicalAuthority(authority, scheme string) string {
	host := strings.ToLower(hostname(authority))
	port := ""
	if _, parsedPort, err := net.SplitHostPort(strings.TrimSpace(authority)); err == nil {
		port = parsedPort
	}
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port)
}

func validateLoopbackListenAddr(addr string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return fmt.Errorf("invalid web listen address %q: %w", addr, err)
	}
	host = strings.TrimSpace(host)
	if strings.TrimSpace(port) == "" || (host != "127.0.0.1" && host != "::1") {
		return fmt.Errorf("web listen address must use literal 127.0.0.1 or ::1: %q", addr)
	}
	return nil
}
