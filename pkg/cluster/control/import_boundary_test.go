package control

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionImportsOnlyControllerV2Facade(t *testing.T) {
	disallowed := []string{
		"github.com/WuKongIM/WuKongIM/pkg/controller/command",
		"github.com/WuKongIM/WuKongIM/pkg/controller/fsm",
		"github.com/WuKongIM/WuKongIM/pkg/controller/raft",
		"github.com/WuKongIM/WuKongIM/pkg/controller/server",
		"github.com/WuKongIM/WuKongIM/pkg/controller/state",
		"github.com/WuKongIM/WuKongIM/pkg/controller/statefile",
		"github.com/WuKongIM/WuKongIM/pkg/controller/sync",
	}
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
			for _, blocked := range disallowed {
				if path == blocked {
					wd, _ := os.Getwd()
					t.Fatalf("%s imports Controller implementation package %q; production code should import only root pkg/controller from %s", file, path, wd)
				}
			}
		}
	}
}
