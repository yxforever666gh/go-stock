package main

import (
	"net/http"
	"strings"
)

type fundCodeRequest struct {
	FundCode string `json:"fundCode"`
}

func registerFundRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/funds/search", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Fund.GetFundList(r.URL.Query().Get("key")))
	})
	mux.HandleFunc("GET /api/v1/watchlist/funds", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Fund.GetFollowedFund())
	})
	mux.HandleFunc("POST /api/v1/watchlist/funds", func(w http.ResponseWriter, r *http.Request) {
		var req fundCodeRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.FundCode) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "fundCode is required"})
			return
		}
		message, err := app.services.Fund.FollowFund(strings.TrimSpace(req.FundCode))
		writeCommandResult(w, message, err)
	})
	mux.HandleFunc("DELETE /api/v1/watchlist/funds/{code}", func(w http.ResponseWriter, r *http.Request) {
		message, err := app.services.Fund.UnFollowFund(r.PathValue("code"))
		writeCommandResult(w, message, err)
	})
}
