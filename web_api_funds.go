package main

import "net/http"

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
		writeCommand(w, app.services.Fund.FollowFund(req.FundCode))
	})
	mux.HandleFunc("DELETE /api/v1/watchlist/funds/{code}", func(w http.ResponseWriter, r *http.Request) {
		writeCommand(w, app.services.Fund.UnFollowFund(r.PathValue("code")))
	})
}
