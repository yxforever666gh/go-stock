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

func TestValidateReferencesRejectsMissingTarget(t *testing.T) {
	var source yaml.Node
	if err := yaml.Unmarshal([]byte("openapi: 3.1.0\nvalue:\n  $ref: '#/components/schemas/Missing'\n"), &source); err != nil {
		t.Fatal(err)
	}
	if err := validateReferences(&source); err == nil {
		t.Fatal("expected unresolved reference to fail")
	}
}

func TestNormalizeLineEndings(t *testing.T) {
	if got := string(normalizeLineEndings([]byte("a\r\nb\n"))); got != "a\nb\n" {
		t.Fatalf("unexpected normalized content %q", got)
	}
}
