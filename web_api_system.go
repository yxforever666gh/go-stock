package main

import "net/http"

func registerSystemRoutes(mux *http.ServeMux, app *App, hub *WebEventHub, status webStatusProvider, shutdown func()) {
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		version := status.SystemVersion(r.Context())
		code := http.StatusOK
		if !version.Readiness.Ready {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, version)
	})
	mux.HandleFunc("GET /api/v1/system/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.versionInfo())
	})
	mux.HandleFunc("POST /api/v1/system/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if !isLocalRequest(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "shutdown is only allowed from localhost"})
			return
		}
		writeCommand(w, "shutdown requested")
		shutdown()
	})
	mux.HandleFunc("GET /api/v1/events/ws", hub.HandleWS)
}
