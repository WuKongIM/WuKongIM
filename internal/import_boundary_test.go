package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestInternalDoesNotImportLegacySharedContracts(t *testing.T) {
	forbidden := []string{
		"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/channelid",
		"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/plugin",
		"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/plugin/pluginproto",
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "bench", "legacy":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			for _, blocked := range forbidden {
				if importPath == blocked || strings.HasPrefix(importPath, blocked+"/") {
					t.Fatalf("%s imports old shared contract %q; use pkg/protocol/channelid, pkg/plugin/pluginproto, or pkg/plugin/pluginhost", path, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
}
