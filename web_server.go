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
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gorilla/websocket"
	"go-stock/backend/db"
	log "go-stock/backend/logger"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
)

var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

type rpcRequest struct {
	ID     any               `json:"id"`
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

type rpcResponse struct {
	ID     any    `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type exportRequest struct {
	Mode string `json:"mode"`
}

type markdownExportRequest struct {
	Mode      string `json:"mode"`
	StockCode string `json:"stockCode"`
	StockName string `json:"stockName"`
}

type imageExportRequest struct {
	Mode       string `json:"mode"`
	Name       string `json:"name"`
	Base64Data string `json:"base64Data"`
}

type wordExportRequest struct {
	Mode       string `json:"mode"`
	Filename   string `json:"filename"`
	Base64Data string `json:"base64Data"`
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
	mu       sync.RWMutex
	clients  map[*wsClient]struct{}
	upgrader websocket.Upgrader
}

func NewWebEventHub() *WebEventHub {
	return &WebEventHub{
		clients: make(map[*wsClient]struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
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
	// Send a one-shot done signal to avoid the UI blocking on loading state.
	client.mu.Lock()
	_ = client.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = client.conn.WriteJSON(map[string]any{
		"event":   "loadingMsg",
		"payload": "done",
	})
	client.mu.Unlock()

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
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"mode":    "web",
			"version": Version,
		})
	})

	mux.HandleFunc("/api/rpc", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, rpcResponse{OK: false, Error: "method not allowed"})
			return
		}

		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, rpcResponse{ID: req.ID, OK: false, Error: "invalid request"})
			return
		}

		result, err := invokeAppMethod(app, req.Method, req.Args)
		if err != nil {
			writeJSON(w, http.StatusOK, rpcResponse{ID: req.ID, OK: false, Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, rpcResponse{ID: req.ID, OK: true, Result: result})
	})

	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	})

	mux.HandleFunc("/api/market-summary/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		sinceSeconds := 0
		if raw := strings.TrimSpace(r.URL.Query().Get("sinceSeconds")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				sinceSeconds = v
			}
		}

		var latest models.AIResponseResult
		_ = db.Dao.Model(&models.AIResponseResult{}).
			Where("stock_name = ? OR stock_code = ?", "市场资讯", "市场资讯").
			Order("id desc").
			Limit(1).
			Find(&latest).Error

		ok := latest.ID != 0 && strings.TrimSpace(latest.Content) != ""
		if ok && sinceSeconds > 0 {
			ok = time.Since(latest.CreatedAt) <= time.Duration(sinceSeconds)*time.Second
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           ok,
			"id":           latest.ID,
			"createdAt":    latest.CreatedAt,
			"stockCode":    latest.StockCode,
			"stockName":    latest.StockName,
			"question":     latest.Question,
			"providerName": latest.ProviderName,
			"modelName":    latest.ModelName,
			"contentLen":   len(strings.TrimSpace(latest.Content)),
		})
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

	mux.HandleFunc("/api/export/markdown", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req markdownExportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
			return
		}

		res := app.services.AI.GetAIResponseResult(app.ctx, req.StockCode)
		if res == nil || len(res.Content) <= 100 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "分析结果异常,无法保存。"})
			return
		}
		analysisTime := res.CreatedAt.Format("2006-01-02_15_04_05")
		filename := sanitizeFilename(fmt.Sprintf("%s[%s]AI分析结果_%s.md", req.StockName, req.StockCode, analysisTime), ".md")
		writeExport(w, req.Mode, filename, "text/markdown; charset=utf-8", []byte(res.Content))
	})

	mux.HandleFunc("/api/export/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req exportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
			return
		}
		config := app.services.Config.ExportConfig()
		writeExport(w, req.Mode, "config.json", "application/json", []byte(config))
	})

	mux.HandleFunc("/api/export/image", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req imageExportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
			return
		}
		payload, err := base64.StdEncoding.DecodeString(req.Base64Data)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "文件内容异常,无法保存。"})
			return
		}
		filename := sanitizeFilename(req.Name+"AI分析.png", ".png")
		writeExport(w, req.Mode, filename, "image/png", payload)
	})

	mux.HandleFunc("/api/export/word", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req wordExportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
			return
		}
		payload, err := base64.StdEncoding.DecodeString(req.Base64Data)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "文件内容异常,无法保存。"})
			return
		}
		filename := sanitizeFilename(req.Filename, ".docx")
		writeExport(w, req.Mode, filename, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", payload)
	})

	staticFS, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return err
	}
	mux.Handle("/", spaFileServer(staticFS))

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.SugaredLogger.Infof("web mode listening on http://%s", addr)
	return server.ListenAndServe()
}

func invokeAppMethod(app *App, methodName string, args []json.RawMessage) (any, error) {
	if !isRPCMethodAllowed(methodName) {
		return nil, fmt.Errorf("method not allowed: %s", methodName)
	}

	appValue := reflect.ValueOf(app)
	method := appValue.MethodByName(methodName)
	if !method.IsValid() {
		return nil, fmt.Errorf("unknown method: %s", methodName)
	}

	methodType := method.Type()
	in := make([]reflect.Value, 0, methodType.NumIn())
	argIndex := 0
	for i := 0; i < methodType.NumIn(); i++ {
		paramType := methodType.In(i)
		if paramType == contextType {
			ctx := app.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			in = append(in, reflect.ValueOf(ctx))
			continue
		}

		if argIndex >= len(args) {
			in = append(in, reflect.Zero(paramType))
			continue
		}

		decoded, err := decodeArg(args[argIndex], paramType)
		if err != nil {
			return nil, fmt.Errorf("arg %d decode failed: %w", argIndex+1, err)
		}
		in = append(in, decoded)
		argIndex++
	}

	results := method.Call(in)
	if len(results) == 0 {
		return nil, nil
	}
	if len(results) == 1 {
		return normalizeResult(results[0]), nil
	}
	if len(results) == 2 {
		if errVal, ok := results[1].Interface().(error); ok {
			if errVal != nil {
				return nil, errVal
			}
			return normalizeResult(results[0]), nil
		}
	}
	out := make([]any, 0, len(results))
	for _, item := range results {
		out = append(out, normalizeResult(item))
	}
	return out, nil
}

func isRPCMethodAllowed(methodName string) bool {
	if methodName == "" {
		return false
	}
	if methodName == "startup" || methodName == "domReady" || methodName == "beforeClose" || methodName == "shutdown" || methodName == "AddCronTask" {
		return false
	}
	if strings.ToUpper(methodName[:1]) != methodName[:1] {
		return false
	}
	return true
}

func decodeArg(raw json.RawMessage, targetType reflect.Type) (reflect.Value, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return reflect.Zero(targetType), nil
	}

	v := reflect.New(targetType)
	if err := json.Unmarshal(raw, v.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return v.Elem(), nil
}

func normalizeResult(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Map || v.Kind() == reflect.Slice || v.Kind() == reflect.Interface || v.Kind() == reflect.Func {
		if v.IsNil() {
			return nil
		}
	}
	return v.Interface()
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func sanitizeFilename(name string, ext string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "export" + ext
	}
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			return '_'
		default:
			return r
		}
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "export" + ext
	}
	if ext != "" && !strings.HasSuffix(strings.ToLower(name), strings.ToLower(ext)) {
		name += ext
	}
	return name
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
		_, _ = w.Write(content)
	})
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
