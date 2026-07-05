package proxy

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionCodeDoesNotImportLegacyPackages(t *testing.T) {
	const legacyPrefix = "github.com/WuKongIM/WuKongIM/pkg/legacy"
	var violations []string
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if importPath == legacyPrefix || strings.HasPrefix(importPath, legacyPrefix+"/") {
				violations = append(violations, path+" imports "+importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk proxy imports: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("slot proxy production code must not import legacy packages:\n%s", strings.Join(violations, "\n"))
	}
}
