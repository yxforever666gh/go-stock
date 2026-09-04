package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "go-stock/backend/logger"
	appconfig "go-stock/internal/config"
	"go-stock/internal/releaseinfo"
)

const (
	maxExportRequestBodyBytes int64 = 32 << 20
	maxAPIRequestBodyBytes    int64 = 4 << 20
)

type exportRequest struct {
	Mode string `json:"mode"`
}

type downloadPayload struct {
	Mode          string `json:"mode"`
	Filename      string `json:"filename"`
	MIME          string `json:"mime"`
	ContentBase64 string `json:"contentBase64"`
}

type serverFilePayload struct {
	Mode     string `json:"mode"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
}

type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type WebEventHub struct {
	mu             sync.RWMutex
	clients        map[*wsClient]struct{}
	upgrader       websocket.Upgrader
	readinessCheck func() bool
}

func NewWebEventHub() *WebEventHub {
	return &WebEventHub{
		clients: make(map[*wsClient]struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     hasAllowedOrigin,
		},
		readinessCheck: func() bool { return releaseinfo.Readiness().Ready },
	}
}

func (h *WebEventHub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.SugaredLogger.Warnf("websocket upgrade failed: %v", err)
		return
	}

	client := &wsClient{conn: conn}
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	// Web mode may emit startup events before frontend websocket is connected.
	// Only replay completion after the process has actually become ready.
	if h.isReady() {
		client.mu.Lock()
		_ = client.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = client.conn.WriteJSON(map[string]any{
			"event":   "loadingMsg",
			"payload": "done",
		})
		client.mu.Unlock()
	}

	defer func() {
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
		_ = conn.Close()
	}()

	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Time{})
	for {
		if _, _, readErr := conn.ReadMessage(); readErr != nil {
			return
		}
	}
}

func (h *WebEventHub) isReady() bool {
	return h != nil && h.readinessCheck != nil && h.readinessCheck()
}

func (h *WebEventHub) Emit(event string, payload any) {
	evt := map[string]any{
		"event":   event,
		"payload": payload,
	}

	h.mu.RLock()
	clients := make([]*wsClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		c.mu.Lock()
		_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := c.conn.WriteJSON(evt)
		c.mu.Unlock()
		if err != nil {
			h.mu.Lock()
			delete(h.clients, c)
			h.mu.Unlock()
			_ = c.conn.Close()
		}
	}
}

func runWebMode(app *App, addr string, hub *WebEventHub) error {
	if err := validateLoopbackListenAddr(addr); err != nil {
		return err
	}
	mux := http.NewServeMux()
	var server *http.Server
	status := defaultWebStatusProvider{runtime: app.services.Runtime}
	registerWebV1Routes(mux, app, hub, status, func() {
		go func() {
			time.Sleep(300 * time.Millisecond)
			if app.cron != nil {
				app.cron.Stop()
			}
			if !app.runtime.Shutdown(5 * time.Second) {
				log.SugaredLogger.Warn("background tasks did not finish before shutdown timeout")
			}
			if server != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := server.Shutdown(ctx); err != nil {
					log.SugaredLogger.Warnf("web shutdown failed: %v", err)
				}
			}
		}()
	})

	mux.HandleFunc("/build/appicon.png", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if len(icon) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(icon)
	})

	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if len(icon2) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(icon2)
	})

	staticFS, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return err
	}
	mux.Handle("/", spaFileServer(staticFS))

	server = &http.Server{
		Addr:              addr,
		Handler:           loopbackOnly(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	log.SugaredLogger.Infof("web mode listening on http://%s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func isLocalRequest(r *http.Request) bool {
	return isLoopbackRequest(r) && hasAllowedOrigin(r)
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, target any, limit int64, allowEmpty bool) error {
	if r == nil || r.Body == nil {
		if allowEmpty {
			return nil
		}
		return errors.New("request body is required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func writeRequestError(w http.ResponseWriter, err error) {
	status, message := requestErrorResponse(err)
	writeJSON(w, status, map[string]any{"error": message})
}

func requestErrorResponse(err error) (int, string) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge, "request body is too large"
	}
	return http.StatusBadRequest, "invalid request"
}

func normalizeExportMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return "download", nil
	}
	if mode != "download" && mode != "server" {
		return "", fmt.Errorf("invalid export mode %q", raw)
	}
	return mode, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeExport(w http.ResponseWriter, mode string, filename string, mime string, content []byte) {
	if strings.EqualFold(mode, "server") {
		relPath, err := saveExportFile(filename, content)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, serverFilePayload{Mode: "server", Path: relPath, Filename: filename})
		return
	}

	writeJSON(w, http.StatusOK, downloadPayload{
		Mode:          "download",
		Filename:      filename,
		MIME:          mime,
		ContentBase64: base64.StdEncoding.EncodeToString(content),
	})
}

func saveExportFile(filename string, content []byte) (string, error) {
	dateDir := time.Now().Format("20060102")
	baseDir := filepath.Join(appconfig.Load().ExportBaseDir(), dateDir)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}

	target := filepath.Join(baseDir, filename)
	target = uniquePath(target)
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(target), nil
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}

	ext := filepath.Ext(path)
	name := strings.TrimSuffix(filepath.Base(path), ext)
	dir := filepath.Dir(path)
	for i := 1; i < 1000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", name, i, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%d%s", name, time.Now().UnixNano(), ext))
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
