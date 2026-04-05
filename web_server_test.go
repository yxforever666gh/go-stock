package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

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

func testStaticFS() fs.FS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte(`<!DOCTYPE html><html><body><div id="app"></div></body></html>`)},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('ok');")},
	}
}
