//go:build dev

package main

import "net/http"

const devFrontendURL = "http://127.0.0.1:5173"

func registerFrontendRoutes(mux *http.ServeMux) error {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, devFrontendURL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	})
	return nil
}
