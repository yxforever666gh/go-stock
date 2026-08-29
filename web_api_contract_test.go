package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-stock/internal/releaseinfo"
	"go-stock/internal/service"
)

type stubWebStatusProvider struct{}

func (stubWebStatusProvider) SystemVersion(context.Context) releaseinfo.VersionStatus {
	return releaseinfo.VersionStatus{AppVersion: "test"}
}

func testWebV1Mux() *http.ServeMux {
	mux := http.NewServeMux()
	registerWebV1Routes(mux, nil, NewWebEventHub(), stubWebStatusProvider{}, func() {})
	return mux
}

func TestWebV1RoutesRegisteredAndLegacyRoutesRemoved(t *testing.T) {
	mux := testWebV1Mux()

	typed := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/livez"},
		{http.MethodGet, "/readyz"},
		{http.MethodGet, "/api/v1/market/hot/stocks"},
		{http.MethodGet, "/api/v1/market/hot/words"},
		{http.MethodGet, "/api/v1/market/breadth"},
		{http.MethodGet, "/api/v1/market/fund-flows"},
		{http.MethodGet, "/api/v1/market/futures/positions"},
		{http.MethodGet, "/api/v1/market/margin"},
		{http.MethodGet, "/api/v1/instruments/sh600000/auction"},
		{http.MethodGet, "/api/v1/instruments/sh600000/trades"},
		{http.MethodGet, "/api/v1/funds/rankings"},
		{http.MethodGet, "/api/v1/etfs/rankings"},
		{http.MethodGet, "/api/v1/etfs/search"},
		{http.MethodGet, "/api/v1/etfs/510300"},
		{http.MethodGet, "/api/v1/watchlist/etfs"},
		{http.MethodPost, "/api/v1/watchlist/etfs"},
		{http.MethodDelete, "/api/v1/watchlist/etfs/510300"},
		{http.MethodGet, "/api/v1/research/analysis-runs"},
		{http.MethodGet, "/api/v1/research/capital-deployment/status"},
		{http.MethodGet, "/api/v1/research/buy-opportunities"},
		{http.MethodGet, "/api/v1/research/recommendations/example"},
		{http.MethodPost, "/api/v1/exports/config"},
	}
	for _, item := range typed {
		req := httptest.NewRequest(item.method, "http://127.0.0.1:34115"+item.path, nil)
		_, pattern := mux.Handler(req)
		if pattern == "" {
			t.Errorf("%s %s is not registered", item.method, item.path)
		}
	}

	legacy := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/rpc"},
		{http.MethodGet, "/api/ws"},
		{http.MethodPost, "/api/export/markdown"},
		{http.MethodPost, "/api/shutdown"},
		{http.MethodGet, "/healthz"},
		{http.MethodPost, "/api/v1/ai/chat-runs"},
		{http.MethodGet, "/api/v1/ai/prompts"},
		{http.MethodPut, "/api/v1/watchlist/stocks/example/ai-cron"},
		{http.MethodPost, "/api/v1/exports/markdown"},
		{http.MethodPost, "/api/v1/exports/image"},
		{http.MethodPost, "/api/v1/exports/word"},
		{http.MethodGet, "/api/v1/system/version"},
		{http.MethodGet, "/api/v1/system/health"},
	}
	for _, item := range legacy {
		req := httptest.NewRequest(item.method, "http://127.0.0.1:34115"+item.path, nil)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", item.method, item.path, recorder.Code)
		}
	}
}

func TestManualResearchAnalysisRouteRemoved(t *testing.T) {
	mux := testWebV1Mux()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:34115/api/v1/research/analysis-runs", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/v1/research/analysis-runs status = %d, want 405", recorder.Code)
	}
}

func TestDecodeAPIRequestRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"flag":1,"legacy":true}`},
		{name: "trailing value", body: `{"flag":1} {"flag":2}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:34115/api/v1/system/update-check", strings.NewReader(test.body))
			recorder := httptest.NewRecorder()
			var target updateCheckRequest
			if decodeAPIRequest(recorder, req, &target) {
				t.Fatal("invalid JSON request was accepted")
			}
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", recorder.Code)
			}
			if !strings.Contains(recorder.Body.String(), `"error":"invalid request"`) {
				t.Fatalf("unexpected error body %q", recorder.Body.String())
			}
		})
	}
}

func TestLoopbackOnlyRejectsRemoteForwardedAndCrossOriginRequests(t *testing.T) {
	handler := loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	tests := []struct {
		name       string
		host       string
		remoteAddr string
		origin     string
		forwarded  string
		want       int
	}{
		{name: "loopback", host: "127.0.0.1:34115", remoteAddr: "127.0.0.1:53100", want: http.StatusOK},
		{name: "matching localhost origin", host: "localhost:34115", remoteAddr: "[::1]:53100", origin: "http://localhost:34115", want: http.StatusOK},
		{name: "remote peer", host: "127.0.0.1:34115", remoteAddr: "192.0.2.10:53100", want: http.StatusForbidden},
		{name: "nonloopback host", host: "192.0.2.10:34115", remoteAddr: "127.0.0.1:53100", want: http.StatusForbidden},
		{name: "forwarding header", host: "127.0.0.1:34115", remoteAddr: "127.0.0.1:53100", forwarded: "for=127.0.0.1", want: http.StatusForbidden},
		{name: "cross origin", host: "127.0.0.1:34115", remoteAddr: "127.0.0.1:53100", origin: "http://localhost:34115", want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+test.host+"/livez", nil)
			req.Host = test.host
			req.RemoteAddr = test.remoteAddr
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			if test.forwarded != "" {
				req.Header.Set("Forwarded", test.forwarded)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%q", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestValidateLoopbackListenAddr(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:34115", "localhost:0", "[::1]:34115"} {
		if err := validateLoopbackListenAddr(addr); err != nil {
			t.Errorf("validateLoopbackListenAddr(%q): %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:34115", "192.0.2.1:34115", ":34115", "127.0.0.1"} {
		if err := validateLoopbackListenAddr(addr); err == nil {
			t.Errorf("validateLoopbackListenAddr(%q) unexpectedly succeeded", addr)
		}
	}
}

func TestWriteCommandResultMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "success", status: http.StatusOK},
		{name: "invalid", err: service.ErrInvalidInput, status: http.StatusBadRequest},
		{name: "missing", err: service.ErrNotFound, status: http.StatusNotFound},
		{name: "conflict", err: service.ErrConflict, status: http.StatusConflict},
		{name: "failed", err: service.ErrOperationFailed, status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeCommandResult(recorder, "result", test.err)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%q", recorder.Code, test.status, recorder.Body.String())
			}
			if test.err == nil && !strings.Contains(recorder.Body.String(), `"ok":true`) {
				t.Fatalf("unexpected success body %q", recorder.Body.String())
			}
			if test.err != nil && !strings.Contains(recorder.Body.String(), `"error":"result"`) {
				t.Fatalf("unexpected error body %q", recorder.Body.String())
			}
		})
	}
}

func TestFundAndETFMarketRoutesRejectInvalidQueriesBeforeProviderWork(t *testing.T) {
	mux := testWebV1Mux()
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/v1/funds/rankings?category=invalid"},
		{method: http.MethodGet, path: "/api/v1/etfs/rankings?pageSize=101"},
		{method: http.MethodGet, path: "/api/v1/etfs/search"},
		{method: http.MethodGet, path: "/api/v1/etfs/not-an-etf"},
		{method: http.MethodPost, path: "/api/v1/watchlist/etfs", body: `{"code":"510300","name":"沪深300ETF","market":"SZ","category":"broad"}`},
	}
	for _, item := range tests {
		req := httptest.NewRequest(item.method, "http://127.0.0.1:34115"+item.path, strings.NewReader(item.body))
		if item.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s %s status=%d body=%q", item.method, item.path, recorder.Code, recorder.Body.String())
		}
	}
}
