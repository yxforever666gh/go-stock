package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var boundaryImports = map[string]map[string]bool{
	"backend/marketdata":     {},
	"backend/news":           {},
	"backend/marketintel":    {},
	"backend/recommendation": {"go-stock/backend/marketintel": true, "go-stock/backend/strategy/v150": true},
	"backend/execution":      {"go-stock/backend/marketdata": true, "go-stock/backend/strategy/v150": true},
	"backend/portfolio":      {},
	"backend/legacy":         {"go-stock/backend/marketdata": true},
}

var v150AllowedProductionImports = map[string]bool{
	"crypto/sha256": true,
	"encoding/hex":  true,
	"encoding/json": true,
	"errors":        true,
	"fmt":           true,
	"math":          true,
	"sort":          true,
	"strings":       true,
	"time":          true,
}

// This is an explicit migration debt ledger, not a permanent exemption. New
// direct imports fail the test; removing one requires removing its entry here
// in the same change, so the list can only shrink deliberately.
var deprecatedDataImportDebt = map[string]bool{
	"app_ai_api.go":              true,
	"app_config_api.go":          true,
	"app_summary_runtime.go":     true,
	"backend/agent/agent_api.go": true,
	"internal/bootstrap/dependencies.go":                     true,
	"internal/migrations/migrations.go":                      true,
	"main.go":                                                true,
	"runtime_bootstrap.go":                                   true,
}

var deprecatedDataCompatibilityAdapters = map[string]bool{
	"internal/bootstrap/agent_tools_compat.go":    true,
	"internal/bootstrap/service_compat_ai.go":     true,
	"internal/bootstrap/service_compat_market.go": true,
	"internal/bootstrap/service_compat_stock.go":  true,
	"internal/bootstrap/legacy_replay.go":         true,
}

var globalDBImportDebt = map[string]bool{
	"app_summary_runtime.go":                                true,
	"app_update_runtime.go":                                 true,
	"app_v150_execution_runtime.go":                         true,
	"internal/cli/bootstrap.go":                             true,
	"internal/cli/cmd_backfill_market_summary_recommend.go": true,
	"internal/cli/cmd_db.go":                                true,
	"internal/cli/cmd_release.go":                           true,
	"internal/cli/cmd_strategy.go":                          true,
	"internal/cli/cmd_strategy_backtest.go":                 true,
	"main.go":                                               true,
	"runtime_compat.go":                                     true,
}

func TestBoundaryPackagesHaveExplicitDependencyDirection(t *testing.T) {
	root := repositoryRoot(t)
	for relative, allowed := range boundaryImports {
		relative, allowed := relative, allowed
		t.Run(strings.ReplaceAll(relative, "/", "_"), func(t *testing.T) {
			files := parseProductionPackageFiles(t, filepath.Join(root, filepath.FromSlash(relative)))
			for name, file := range files {
				for _, spec := range file.Imports {
					path, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						t.Fatalf("%s: invalid import %s", name, spec.Path.Value)
					}
					if strings.HasPrefix(path, "go-stock/") && !allowed[path] {
						t.Errorf("%s imports %q outside its boundary", name, path)
					}
				}
				assertNoGlobalDatabaseSelector(t, name, file)
			}
		})
	}
}

func TestV150StrategyCoreImportsOnlyStandardLibrary(t *testing.T) {
	root := repositoryRoot(t)
	files := parseProductionPackageFiles(t, filepath.Join(root, "backend", "strategy", "v150"))
	for name, file := range files {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: invalid import %s", name, spec.Path.Value)
			}
			if !v150AllowedProductionImports[path] {
				t.Errorf("%s imports package %q outside the frozen core allowlist", name, path)
			}
			if spec.Name != nil {
				t.Errorf("%s uses import alias %q for %q", name, spec.Name.Name, path)
			}
		}
		assertNoGlobalDatabaseSelector(t, name, file)
		assertNoRuntimeClock(t, name, file)
		assertNoVersionRouting(t, name, file)
		assertNoReplaceableFunctionDependency(t, name, file)
	}
	assertSingleV150VersionLiteral(t, files)
}

func TestDeprecatedDataDirectImportsCannotIncrease(t *testing.T) {
	root := repositoryRoot(t)
	actual := findProductionImporters(t, root, "go-stock/backend/data")
	for filename := range actual {
		if deprecatedDataCompatibilityAdapters[filename] {
			continue
		}
		if !deprecatedDataImportDebt[filename] {
			t.Errorf("new direct backend/data import outside the migration ledger: %s", filename)
		}
	}
	for filename := range deprecatedDataImportDebt {
		if !actual[filename] {
			t.Errorf("stale backend/data debt entry %s; remove it after migrating the import", filename)
		}
	}
	for filename := range deprecatedDataCompatibilityAdapters {
		if !actual[filename] {
			t.Errorf("stale backend/data compatibility adapter %s", filename)
		}
	}
}

func TestServiceUseCasesDoNotImportCompatibilityOrGlobalDB(t *testing.T) {
	root := repositoryRoot(t)
	files := parseProductionPackageFiles(t, filepath.Join(root, "internal", "service"))
	for name, file := range files {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: invalid import %s", name, spec.Path.Value)
			}
			if path == "go-stock/backend/data" || path == "go-stock/backend/db" {
				t.Errorf("service use case %s imports forbidden compatibility package %q", name, path)
			}
		}
	}
}

func TestDeliveryAndUseCaseGlobalDBImportsCannotIncrease(t *testing.T) {
	root := repositoryRoot(t)
	actual := findProductionImporters(t, root, "go-stock/backend/db")
	for filename := range actual {
		if permittedGlobalDBOwner(filename) {
			continue
		}
		if !globalDBImportDebt[filename] {
			t.Errorf("new global backend/db import outside the migration ledger: %s", filename)
		}
	}
	for filename := range globalDBImportDebt {
		if !actual[filename] {
			t.Errorf("stale backend/db debt entry %s; remove it after injecting storage", filename)
		}
	}
}

func permittedGlobalDBOwner(filename string) bool {
	return strings.HasPrefix(filename, "backend/data/") ||
		strings.HasPrefix(filename, "backend/db/") ||
		strings.HasPrefix(filename, "internal/bootstrap/") ||
		strings.HasPrefix(filename, "internal/migrations/")
}

func TestDeprecatedDataMayImportNewBoundariesOnlyFromCompatibilityAdapters(t *testing.T) {
	root := repositoryRoot(t)
	files := parseProductionPackageFiles(t, filepath.Join(root, "backend", "data"))
	for name, file := range files {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: invalid import %s", name, spec.Path.Value)
			}
			if _, isBoundary := boundaryImports[strings.TrimPrefix(path, "go-stock/")]; isBoundary && !strings.Contains(strings.TrimSuffix(name, ".go"), "_compat") {
				t.Errorf("deprecated data file %s imports new boundary %q but is not an explicit compatibility adapter", name, path)
			}
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func parsePackageFiles(t *testing.T, directory string) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]*ast.File)
	set := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(set, filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		result[entry.Name()] = file
	}
	return result
}

func parseProductionPackageFiles(t *testing.T, directory string) map[string]*ast.File {
	t.Helper()
	files := parsePackageFiles(t, directory)
	for name := range files {
		if strings.HasSuffix(name, "_test.go") {
			delete(files, name)
		}
	}
	return files
}

func findProductionImporters(t *testing.T, root, importPath string) map[string]bool {
	t.Helper()
	result := make(map[string]bool)
	set := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			first := strings.Split(filepath.ToSlash(relative), "/")[0]
			if first == ".git" || first == "frontend" || first == "runtime" || first == "build" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(set, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			value, err := strconv.Unquote(spec.Path.Value)
			if err == nil && value == importPath {
				relative, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				result[filepath.ToSlash(relative)] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertNoRuntimeClock(t *testing.T, filename string, file *ast.File) {
	t.Helper()
	timeAliases := importedAliases(file, "time", "time")
	forbidden := map[string]bool{
		"Now": true, "Since": true, "Until": true, "After": true, "AfterFunc": true,
		"Sleep": true, "Tick": true, "NewTicker": true, "NewTimer": true, "Local": true,
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || !forbidden[selector.Sel.Name] {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok && timeAliases[identifier.Name] {
			t.Errorf("%s reads runtime clock through %s.%s", filename, identifier.Name, selector.Sel.Name)
		}
		return true
	})
}

func assertNoVersionRouting(t *testing.T, filename string, file *ast.File) {
	t.Helper()
	inspectCondition := func(kind string, expression ast.Expr) {
		if expression != nil && containsVersionIdentifier(expression) {
			t.Errorf("%s routes strategy behavior by version in %s", filename, kind)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.IfStmt:
			inspectCondition("if condition", value.Cond)
		case *ast.SwitchStmt:
			inspectCondition("switch expression", value.Tag)
		case *ast.TypeSwitchStmt:
			if value.Assign != nil && containsVersionIdentifier(value.Assign) {
				t.Errorf("%s routes strategy behavior by version in type switch", filename)
			}
		case *ast.ForStmt:
			inspectCondition("for condition", value.Cond)
		case *ast.CaseClause:
			for _, expression := range value.List {
				inspectCondition("case expression", expression)
			}
		}
		return true
	})
}

func containsVersionIdentifier(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(inner ast.Node) bool {
		identifier, ok := inner.(*ast.Ident)
		if !ok {
			return true
		}
		name := strings.ToLower(identifier.Name)
		if name == "strategyversion" || name == "summaryversion" || name == "currentstrategyversion" {
			found = true
			return false
		}
		return true
	})
	return found
}

func assertNoReplaceableFunctionDependency(t *testing.T, filename string, file *ast.File) {
	t.Helper()
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range value.Names {
				if strings.HasSuffix(name.Name, "Fn") {
					t.Errorf("%s declares replaceable package dependency %s", filename, name.Name)
				}
			}
		}
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name.Name == "init" {
			t.Errorf("%s declares init(), which hides strategy-core initialization", filename)
		}
	}
}

func assertSingleV150VersionLiteral(t *testing.T, files map[string]*ast.File) {
	t.Helper()
	semver := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	allowed := make(map[*ast.BasicLit]bool)
	for _, declaration := range files["config.go"].Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range value.Names {
				if name.Name != "StrategyVersion" || index >= len(value.Values) {
					continue
				}
				literal, ok := value.Values[index].(*ast.BasicLit)
				if ok && literal.Kind == token.STRING {
					allowed[literal] = true
					version, _ := strconv.Unquote(literal.Value)
					if version != "1.5.0" {
						t.Errorf("StrategyVersion = %q, want immutable 1.5.0", version)
					}
				}
			}
		}
	}
	if len(allowed) != 1 {
		t.Fatalf("expected exactly one StrategyVersion string declaration, found %d", len(allowed))
	}
	for filename, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING || allowed[literal] {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && semver.MatchString(value) {
				t.Errorf("%s contains version literal %q outside StrategyVersion declaration", filename, value)
			}
			return true
		})
	}
}

func importedAliases(file *ast.File, importPath, defaultName string) map[string]bool {
	aliases := make(map[string]bool)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		name := defaultName
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = true
	}
	return aliases
}

func assertNoGlobalDatabaseSelector(t *testing.T, filename string, file *ast.File) {
	t.Helper()
	dbAliases := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "go-stock/backend/db" {
			continue
		}
		name := "db"
		if spec.Name != nil {
			name = spec.Name.Name
		}
		dbAliases[name] = true
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Dao" && selector.Sel.Name != "MinuteDao") {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok && dbAliases[identifier.Name] {
			t.Errorf("%s directly references global database %s.%s", filename, identifier.Name, selector.Sel.Name)
		}
		return true
	})
}
