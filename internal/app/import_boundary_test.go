package app

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionImportsOnlyControllerV2Facade(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s) error = %v", file, err)
		}
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("Unquote(%s) error = %v", spec.Path.Value, err)
			}
			if strings.HasPrefix(path, "github.com/WuKongIM/WuKongIM/pkg/controller/") {
				t.Fatalf("%s imports Controller implementation package %q; app production code should import root pkg/controller", file, path)
			}
		}
	}
}
