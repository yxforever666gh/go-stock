package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"go-stock/backend/governance"
	"go-stock/internal/releaseinfo"
)

type fakeWebStatusProvider struct {
	version  releaseinfo.VersionStatus
	strategy governance.StrategyRuntimeStatus
}

func (p fakeWebStatusProvider) SystemVersion(context.Context) releaseinfo.VersionStatus {
	return p.version
}

func (p fakeWebStatusProvider) StrategyRuntime(context.Context) governance.StrategyRuntimeStatus {
	return p.strategy
}

func TestSpaFileServerServesExistingAssets(t *testing.T) {
	handler := spaFileServer(testStaticFS())
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "console.log('ok');" {
		t.Fatalf("unexpected asset body: %q", body)
	}
}

func TestCleanStaticRequestPathUsesURLRules(t *testing.T) {
	if got := cleanStaticRequestPath("/assets/app.js"); got != "assets/app.js" {
		t.Fatalf("unexpected cleaned asset path: %q", got)
	}
	if got := cleanStaticRequestPath("/research/../assets/app.js"); got != "assets/app.js" {
		t.Fatalf("unexpected normalized asset path: %q", got)
	}
}

func TestSpaFileServerReturnsNotFoundForMissingAssets(t *testing.T) {
	handler := spaFileServer(testStaticFS())
	req := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestSpaFileServerFallsBackToIndexForClientRoutes(t *testing.T) {
	handler := spaFileServer(testStaticFS())
	req := httptest.NewRequest(http.MethodGet, "/research", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("expected html content-type, got %q", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, `<div id="app"></div>`) {
		t.Fatalf("unexpected index body: %q", body)
	}
}

func TestSpaFileServerReturnsNotFoundForMissingTypedPath(t *testing.T) {
	handler := spaFileServer(testStaticFS())
	req := httptest.NewRequest(http.MethodGet, "/foo/bar.css", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestWebV1SystemAndStrategyStatusRoutes(t *testing.T) {
	changedAt := time.Date(2026, time.August, 6, 1, 2, 3, 0, time.UTC)
	provider := fakeWebStatusProvider{
		version: releaseinfo.VersionStatus{
			AppVersion:             "1.5.1",
			CurrentStrategyVersion: "1.5.0",
			Commit:                 "abc123",
			ArtifactSHA256:         "artifact-hash",
			StrategyMode:           governance.StrategyModePaused,
			Readiness: releaseinfo.ReadinessStatus{
				Migrations:   true,
				Database:     true,
				Services:     true,
				Scheduler:    true,
				StrategyMode: governance.StrategyModePaused,
				Ready:        true,
			},
		},
		strategy: governance.StrategyRuntimeStatus{
			Mode:                   governance.StrategyModePaused,
			CurrentStrategyVersion: "1.5.0",
			Reason:                 "refactor",
			ChangedAt:              changedAt,
			Ready:                  true,
		},
	}
	mux := http.NewServeMux()
	registerWebV1Routes(mux, nil, NewWebEventHub(), provider)

	versionRec := httptest.NewRecorder()
	mux.ServeHTTP(versionRec, httptest.NewRequest(http.MethodGet, "/api/v1/system/version", nil))
	if versionRec.Code != http.StatusOK {
		t.Fatalf("version status = %d, body=%s", versionRec.Code, versionRec.Body.String())
	}
	var version map[string]any
	if err := json.Unmarshal(versionRec.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if version["appVersion"] != "1.5.1" || version["currentStrategyVersion"] != "1.5.0" {
		t.Fatalf("unexpected version response: %#v", version)
	}
	readyRec := httptest.NewRecorder()
	mux.ServeHTTP(readyRec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readyRec.Code != http.StatusOK || !strings.Contains(readyRec.Body.String(), `"artifactSHA256":"artifact-hash"`) {
		t.Fatalf("unexpected readiness response: status=%d body=%s", readyRec.Code, readyRec.Body.String())
	}
	healthRec := httptest.NewRecorder()
	mux.ServeHTTP(healthRec, httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil))
	if healthRec.Code != http.StatusOK || !strings.Contains(healthRec.Body.String(), `"version":"1.5.1"`) {
		t.Fatalf("unexpected health response: status=%d body=%s", healthRec.Code, healthRec.Body.String())
	}

	strategyRec := httptest.NewRecorder()
	mux.ServeHTTP(strategyRec, httptest.NewRequest(http.MethodGet, "/api/v1/strategy/runtime", nil))
	if strategyRec.Code != http.StatusOK {
		t.Fatalf("strategy status = %d, body=%s", strategyRec.Code, strategyRec.Body.String())
	}
	var strategy map[string]any
	if err := json.Unmarshal(strategyRec.Body.Bytes(), &strategy); err != nil {
		t.Fatal(err)
	}
	if strategy["mode"] != "paused" || strategy["targetStrategyVersion"] != "1.5.0" {
		t.Fatalf("unexpected strategy response: %#v", strategy)
	}
}

func TestWebV1ReadinessUsesServiceUnavailableUntilReady(t *testing.T) {
	provider := fakeWebStatusProvider{
		version: releaseinfo.VersionStatus{
			AppVersion: "1.5.1",
			Readiness: releaseinfo.ReadinessStatus{
				StrategyMode: governance.StrategyModePaused,
			},
		},
	}
	mux := http.NewServeMux()
	registerWebV1Routes(mux, nil, NewWebEventHub(), provider)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", rec.Code)
	}
}

func TestLoopbackOnlyRejectsRemoteHostOriginAndForwarding(t *testing.T) {
	handler := loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name       string
		host       string
		remoteAddr string
		origin     string
		forwarded  string
		want       int
	}{
		{name: "loopback", host: "127.0.0.1:34115", remoteAddr: "127.0.0.1:50000", origin: "http://127.0.0.1:34115", want: http.StatusNoContent},
		{name: "ipv6 loopback", host: "[::1]:34115", remoteAddr: "[::1]:50000", origin: "http://[::1]:34115", want: http.StatusNoContent},
		{name: "remote host", host: "192.168.1.10:34115", remoteAddr: "127.0.0.1:50000", want: http.StatusForbidden},
		{name: "remote peer", host: "127.0.0.1:34115", remoteAddr: "192.168.1.10:50000", want: http.StatusForbidden},
		{name: "foreign origin", host: "127.0.0.1:34115", remoteAddr: "127.0.0.1:50000", origin: "http://example.com", want: http.StatusForbidden},
		{name: "different local origin", host: "127.0.0.1:34115", remoteAddr: "127.0.0.1:50000", origin: "http://localhost:34115", want: http.StatusForbidden},
		{name: "forwarded", host: "127.0.0.1:34115", remoteAddr: "127.0.0.1:50000", forwarded: "for=127.0.0.1", want: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/livez", nil)
			req.Host = tc.host
			req.RemoteAddr = tc.remoteAddr
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.forwarded != "" {
				req.Header.Set("Forwarded", tc.forwarded)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestAllowedOriginUsesHTTPSDefaultPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://localhost/livez", nil)
	req.Host = "localhost"
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Origin", "https://localhost:443")
	if !hasAllowedOrigin(req) {
		t.Fatal("expected equivalent HTTPS origins to be accepted")
	}
}

func TestValidateLoopbackListenAddr(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:34115", "localhost:34115", "[::1]:34115"} {
		if err := validateLoopbackListenAddr(addr); err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:34115", ":34115", "192.168.1.10:34115"} {
		if err := validateLoopbackListenAddr(addr); err == nil {
			t.Fatalf("expected %s to be rejected", addr)
		}
	}
}

func TestRPCCompatibilityUsesExplicitAllowlist(t *testing.T) {
	if !isRPCMethodAllowed("GetConfig") {
		t.Fatal("expected a compatibility method to be allowed")
	}
	for _, method := range []string{"", "AddCronTask", "startup", "SomeFutureExportedMethod"} {
		if isRPCMethodAllowed(method) {
			t.Fatalf("unexpected allowed RPC method: %q", method)
		}
	}
}

func testStaticFS() fs.FS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte(`<!DOCTYPE html><html><body><div id="app"></div></body></html>`)},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('ok');")},
	}
}
