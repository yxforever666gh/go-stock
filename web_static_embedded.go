//go:build !dev

package main

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:frontend/dist
var assets embed.FS

func registerFrontendRoutes(mux *http.ServeMux) error {
	staticFS, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return err
	}
	mux.Handle("/", spaFileServer(staticFS))
	return nil
}

func spaFileServer(staticFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/healthz") {
			http.NotFound(w, r)
			return
		}

		cleanPath := cleanStaticRequestPath(r.URL.Path)
		if cleanPath == "." {
			cleanPath = "index.html"
		}
		if _, err := fs.Stat(staticFS, cleanPath); err == nil {
			setSpaCacheHeaders(w, cleanPath)
			fileServer.ServeHTTP(w, r)
			return
		}

		if shouldServeNotFound(cleanPath) {
			http.NotFound(w, r)
			return
		}

		content, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			http.Error(w, "index not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		setSpaCacheHeaders(w, "index.html")
		_, _ = w.Write(content)
	})
}

func setSpaCacheHeaders(w http.ResponseWriter, cleanPath string) {
	if cleanPath == "" || cleanPath == "." || cleanPath == "index.html" || filepath.Ext(cleanPath) == ".html" {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		return
	}

	if strings.HasPrefix(cleanPath, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
}

func cleanStaticRequestPath(requestPath string) string {
	return strings.TrimPrefix(path.Clean(requestPath), "/")
}

func shouldServeNotFound(cleanPath string) bool {
	if cleanPath == "" || cleanPath == "." || cleanPath == "index.html" {
		return false
	}

	if strings.HasPrefix(cleanPath, "assets/") {
		return true
	}

	return filepath.Ext(cleanPath) != ""
}
