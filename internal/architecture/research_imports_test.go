package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestResearchImportBoundaries(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(currentFile)))
	for _, boundary := range []struct {
		paths, forbidden []string
		shared           bool
	}{
		{[]string{"backend/research", "backend/researchapp"}, []string{"backend/research2", "backend/research2app", "backend/data"}, false},
		{[]string{"backend/research2", "backend/research2app"}, []string{"backend/research", "backend/researchapp", "backend/data"}, false},
		{[]string{"backend/ai", "backend/researchconfig", "internal/trading", "internal/marketquote", "internal/researchevidence", "internal/recommendationchart", "internal/sqlitedb"},
			nil, true},
	} {
		for _, path := range boundary.paths {
			err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(path)), func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() && entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
				if err != nil {
					return err
				}
				for _, spec := range file.Imports {
					importPath, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						return err
					}
					if boundary.shared && strings.HasPrefix(importPath, "go-stock/backend/") {
						switch importPath {
						case "go-stock/backend/models", "go-stock/backend/ai", "go-stock/backend/researchconfig":
						default:
							t.Errorf("%s imports business infrastructure %s from a shared primitive", filepath.Base(path), importPath)
						}
					}
					for _, forbidden := range boundary.forbidden {
						prefix := "go-stock/" + forbidden
						if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
							relative, _ := filepath.Rel(root, path)
							t.Errorf("%s imports forbidden research dependency %s", filepath.ToSlash(relative), importPath)
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
