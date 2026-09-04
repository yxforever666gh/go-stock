package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerateTypeScriptIsDeterministic(t *testing.T) {
	spec := &document{
		OpenAPI: "3.1.0",
		Paths: map[string]pathItem{
			"/livez": {Get: &operation{OperationID: "getLiveness"}},
		},
		Components: components{Schemas: map[string]*schema{
			"Status": {
				Type:     "object",
				Required: []string{"ok"},
				Properties: map[string]*schema{
					"ok":      {Type: "boolean"},
					"message": {Type: "string"},
				},
			},
		}},
	}
	first, err := generateTypeScript(spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateTypeScript(spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("generated output is not deterministic")
	}
	if !strings.Contains(string(first), "message?: string") || !strings.Contains(string(first), "ok: boolean") {
		t.Fatalf("unexpected generated output:\n%s", first)
	}
}

func TestGenerateTypeScriptSupportsOpenAPI31NullUnion(t *testing.T) {
	value, err := tsType(&schema{OneOf: []*schema{{Ref: "#/components/schemas/ThemeSnapshot"}, {Type: "null"}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if value != "ThemeSnapshot | null" {
		t.Fatalf("null union = %q, want ThemeSnapshot | null", value)
	}
}

func TestDiscoverGoRouteFilesExcludesTestsAndSorts(t *testing.T) {
	tempDir := t.TempDir()
	for _, name := range []string{"web_api_z.go", "web_api_a.go", "web_api_a_test.go", "other.go"} {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("package sample\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := discoverGoRouteFiles(filepath.Join(tempDir, "web_api*.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(tempDir, "web_api_a.go"), filepath.Join(tempDir, "web_api_z.go")}
	if strings.Join(files, "|") != strings.Join(want, "|") {
		t.Fatalf("route files = %v, want %v", files, want)
	}
}

func TestValidateGoRoutesRejectsContractDrift(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "routes.go")
	source := `package sample
import "net/http"
func register(mux *http.ServeMux) {
	mux.HandleFunc("/livez", methodHandler(http.MethodGet, nil))
}`
	if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := &document{
		Paths: map[string]pathItem{
			"/livez":  {Get: &operation{OperationID: "getLiveness"}},
			"/readyz": {Get: &operation{OperationID: "getReadiness"}},
		},
	}
	err := validateGoRoutes(spec, []string{filename})
	if err == nil || !strings.Contains(err.Error(), "GET /readyz") {
		t.Fatalf("expected missing readiness route, got %v", err)
	}
}

func TestValidateGoRoutesAcceptsGo122MethodPatterns(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "routes.go")
	source := `package sample
import "net/http"
func register(mux *http.ServeMux) {
	mux.HandleFunc("GET /livez", nil)
	mux.HandleFunc("POST /api/v1/items", nil)
	mux.HandleFunc("DELETE /api/v1/items/{id}", nil)
}`
	if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := &document{
		Paths: map[string]pathItem{
			"/livez":             {Get: &operation{OperationID: "getLiveness"}},
			"/api/v1/items":      {Post: &operation{OperationID: "createItem"}},
			"/api/v1/items/{id}": {Delete: &operation{OperationID: "deleteItem"}},
		},
	}
	if err := validateGoRoutes(spec, []string{filename}); err != nil {
		t.Fatalf("Go 1.22 route patterns were rejected: %v", err)
	}
}

func TestValidateGoRoutesRejectsUndocumentedGo122Route(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "routes.go")
	source := `package sample
import "net/http"
func register(mux *http.ServeMux) {
	mux.HandleFunc("GET /livez", nil)
	mux.HandleFunc("POST /api/v1/undocumented", nil)
}`
	if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := &document{Paths: map[string]pathItem{
		"/livez": {Get: &operation{OperationID: "getLiveness"}},
	}}
	err := validateGoRoutes(spec, []string{filename})
	if err == nil || !strings.Contains(err.Error(), "POST /api/v1/undocumented") {
		t.Fatalf("expected undocumented route failure, got %v", err)
	}
}

func TestSplitRoutePattern(t *testing.T) {
	tests := []struct {
		pattern    string
		wantMethod string
		wantPath   string
	}{
		{pattern: "GET /livez", wantMethod: "get", wantPath: "/livez"},
		{pattern: " delete   /api/v1/items/{id} ", wantMethod: "delete", wantPath: "/api/v1/items/{id}"},
		{pattern: "/readyz", wantPath: "/readyz"},
		{pattern: "CONNECT /api/v1/events/ws", wantPath: "CONNECT /api/v1/events/ws"},
	}
	for _, test := range tests {
		method, path := splitRoutePattern(test.pattern)
		if method != test.wantMethod || path != test.wantPath {
			t.Errorf("splitRoutePattern(%q) = (%q, %q), want (%q, %q)", test.pattern, method, path, test.wantMethod, test.wantPath)
		}
	}
}

func TestValidateReferencesRejectsMissingTarget(t *testing.T) {
	var source yaml.Node
	if err := yaml.Unmarshal([]byte("openapi: 3.1.0\nvalue:\n  $ref: '#/components/schemas/Missing'\n"), &source); err != nil {
		t.Fatal(err)
	}
	if err := validateReferences(&source); err == nil {
		t.Fatal("expected unresolved reference to fail")
	}
}

func TestValidateInstrumentChartErrorResponsesAcceptsActualRouteShapes(t *testing.T) {
	if err := validateInstrumentChartErrorResponses(instrumentChartContractFixture()); err != nil {
		t.Fatalf("valid instrument chart error contract rejected: %v", err)
	}
}

func TestValidateInstrumentChartErrorResponsesRejectsWrongSchemaAndMissingDeleteNotFound(t *testing.T) {
	wrongSchema := instrumentChartContractFixture()
	wrongSchema.Paths["/api/v1/instruments/{code}/chart"].Get.Responses["400"] = jsonResponse("#/components/schemas/DataEnvelope")
	if err := validateInstrumentChartErrorResponses(wrongSchema); err == nil || !strings.Contains(err.Error(), "getInstrumentChart response 400") {
		t.Fatalf("wrong chart error schema was not rejected: %v", err)
	}

	missingNotFound := instrumentChartContractFixture()
	delete(missingNotFound.Paths["/api/v1/instruments/{code}/drawings"].Delete.Responses, "404")
	if err := validateInstrumentChartErrorResponses(missingNotFound); err == nil || !strings.Contains(err.Error(), "deleteInstrumentDrawings is missing 404") {
		t.Fatalf("missing DELETE 404 was not rejected: %v", err)
	}

	optionalError := instrumentChartContractFixture()
	optionalError.Components.Schemas["ErrorResponse"].Required = nil
	if err := validateInstrumentChartErrorResponses(optionalError); err == nil || !strings.Contains(err.Error(), "error string property must be required") {
		t.Fatalf("optional error field was not rejected: %v", err)
	}
}

func instrumentChartContractFixture() *document {
	errorRef := func(name string) *response { return &response{Ref: "#/components/responses/" + name} }
	return &document{
		Paths: map[string]pathItem{
			"/api/v1/instruments/{code}/chart": {Get: &operation{OperationID: "getInstrumentChart", Responses: map[string]*response{
				"400": errorRef("BadRequest"),
			}}},
			"/api/v1/instruments/{code}/drawings": {
				Get: &operation{OperationID: "getInstrumentDrawings", Responses: map[string]*response{
					"400": errorRef("BadRequest"), "500": errorRef("InternalServerError"),
				}},
				Put: &operation{OperationID: "putInstrumentDrawings", Responses: map[string]*response{
					"400": errorRef("BadRequest"), "409": errorRef("Conflict"), "500": errorRef("InternalServerError"),
				}},
				Delete: &operation{OperationID: "deleteInstrumentDrawings", Responses: map[string]*response{
					"400": errorRef("BadRequest"), "404": errorRef("NotFound"), "409": errorRef("Conflict"), "500": errorRef("InternalServerError"),
				}},
			},
		},
		Components: components{
			Responses: map[string]*response{
				"BadRequest":          jsonResponse("#/components/schemas/ErrorResponse"),
				"NotFound":            jsonResponse("#/components/schemas/ErrorResponse"),
				"Conflict":            jsonResponse("#/components/schemas/ErrorResponse"),
				"InternalServerError": jsonResponse("#/components/schemas/ErrorResponse"),
			},
			Schemas: map[string]*schema{
				"ErrorResponse": {Type: "object", Required: []string{"error"}, Properties: map[string]*schema{"error": {Type: "string"}}},
			},
		},
	}
}

func jsonResponse(ref string) *response {
	return &response{Content: map[string]mediaType{"application/json": {Schema: &schema{Ref: ref}}}}
}

func TestNormalizeLineEndings(t *testing.T) {
	if got := string(normalizeLineEndings([]byte("a\r\nb\n"))); got != "a\nb\n" {
		t.Fatalf("unexpected normalized content %q", got)
	}
}
